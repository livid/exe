// Desktop apps and the shared workspace.
//
// ~/.exe/apps/<Name>/ is an app bundle: app.json (metadata) plus index.html,
// served straight from disk at /apps/<Name>/ and opened by the web UI in a
// desktop window — editing an app is live on the next reload, no rebuild.
// Each app's private state lives OUTSIDE the served tree in
// ~/.exe/appdata/<Name>/, reachable only through the token-guarded
// /v1/apps/{app}/data API, so no static-serving path can ever leak it and the
// daemon can jail every app to its own directory. ~/.exe/workspace/ is the
// explicitly-shared area (agents and apps exchange files there) behind
// /v1/workspace.
package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"exe/internal/peer"
)

// System apps ship inside the binary and appear on every desktop without
// any install step; a same-named bundle in ~/.exe/apps or an apps_dir
// overrides them (disk roots win name collisions, embedded is last).
//
//go:embed all:sysapps
var sysAppsFS embed.FS

// Only these historical system-app names are aliases. User app names retain
// their spelling; display titles are independent of the stable bundle ID.
func canonicalAppName(name string) string {
	switch name {
	case "Mac OS 9":
		return "macos9"
	case "Hub":
		return "hub"
	case "BluePencil":
		return "bluepencil"
	}
	return name
}

// Keep the existing storage and peer-sync namespace, so renamed bundles do
// not strand data or fork it when talking to a node running an older exe.
func appDataName(name string) string {
	switch canonicalAppName(name) {
	case "macos9":
		return "Mac OS 9"
	case "hub":
		return "Hub"
	case "bluepencil":
		return "BluePencil"
	}
	return name
}

func sysAppExists(name string) bool {
	name = canonicalAppName(name)
	if !validAppName(name) {
		return false
	}
	_, err := fs.Stat(sysAppsFS, "sysapps/"+name+"/index.html")
	return err == nil
}

func loadSysAppMeta(name string) (*appMeta, error) {
	name = canonicalAppName(name)
	b, err := fs.ReadFile(sysAppsFS, "sysapps/"+name+"/app.json")
	if err != nil {
		return nil, err
	}
	m := &appMeta{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	if !sysAppExists(name) {
		return nil, errors.New("no index.html")
	}
	m.Name = name
	m.Offline = cleanOffline(m.Name, m.Offline)
	if m.Title == "" {
		m.Title = name
	}
	if m.Icon != "" && !filepath.IsLocal(m.Icon) {
		m.Icon = ""
	}
	m.SystemIcon = ""
	if strings.EqualFold(filepath.Ext(m.Icon), ".svg") {
		if svg, err := fs.ReadFile(sysAppsFS, "sysapps/"+name+"/"+filepath.ToSlash(m.Icon)); err == nil {
			m.SystemIcon = string(svg)
		}
	}
	return m, nil
}

func (s *Server) appsDir() string      { return filepath.Join(s.StateDir, "apps") }
func (s *Server) workspaceDir() string { return filepath.Join(s.StateDir, "workspace") }

// appRoots returns every directory scanned for app bundles: the writable
// ~/.exe/apps plus any apps_dirs from config — e.g. a separate git repo of
// experimental apps that should still appear on this desktop. Earlier roots
// win name collisions.
func (s *Server) appRoots() []string {
	roots := []string{s.appsDir()}
	for _, d := range s.Config().AppsDirs {
		if d = expandHome(d); d != "" {
			roots = append(roots, d)
		}
	}
	return roots
}

// expandHome resolves a leading ~ so apps_dirs entries stay portable in
// config.json.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// findAppRoot prefers a canonical folder over its legacy alias within each
// root. Root priority still wins, including an old-named disk override.
// Skip incomplete bundles just as app discovery does.
func (s *Server) findAppRoot(name string) (root, folder string, found bool) {
	if !validAppName(name) {
		return "", "", false
	}
	name = canonicalAppName(name)
	names := []string{name}
	if legacy := appDataName(name); legacy != name {
		names = append(names, legacy)
	}
	for _, root := range s.appRoots() {
		for _, folder := range names {
			if _, err := s.loadAppMeta(root, folder); err == nil {
				return root, folder, true
			}
		}
	}
	return "", "", false
}
func (s *Server) appDataDir(app string) string {
	return filepath.Join(s.StateDir, "appdata", appDataName(app))
}

// ensureStateDirs creates the writable state layout on startup.
func (s *Server) ensureStateDirs() {
	for _, d := range []string{s.appsDir(), s.workspaceDir(), filepath.Join(s.StateDir, "appdata")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Printf("state dir %s: %v", d, err)
		}
	}
}

var appNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

func validAppName(name string) bool {
	return appNameRE.MatchString(name) && !strings.Contains(name, "..")
}

type appWindow struct {
	Width  int  `json:"width,omitempty"`
	Height int  `json:"height,omitempty"`
	Grow   bool `json:"grow,omitempty"` // app draws an OS 9 grow box in its corner
}

type appMeta struct {
	Name       string     `json:"name"`
	Title      string     `json:"title"`
	Icon       string     `json:"icon,omitempty"`
	SystemIcon string     `json:"system_icon,omitempty"` // trusted embedded SVG, never supplied by a disk bundle
	Window     *appWindow `json:"window,omitempty"`
	// Hidden keeps an app off the desktop's icons and its lists of apps: it
	// is opened from a menu (Welcome, under Help).
	Hidden bool `json:"hidden,omitempty"`
	// Version fingerprints the code the app's window runs (appcode.go): the
	// desk reloads a window left on an older one.
	Version string `json:"version,omitempty"`
	// Offline is what the app keeps on a device, to be read while exe does
	// not answer (appOffline).
	Offline *appOffline `json:"offline,omitempty"`
}

// appOffline is an app that can be read while exe does not answer: its
// app.json says "offline": true, or names what it keeps beside its own
// folder. The service worker (ui/sw.js) does the keeping, on a device that
// has had the desk open, and the alert it shows when the daemon is away
// (ui/offline.html) offers the apps it has a copy of.
//
// Keep is whole addresses, asked of exe every time and their last answer
// kept: when exe does not answer, the copy is the answer, and says when it
// was kept in X-Exe-Kept (milliseconds). Fixed is the starts of addresses
// under which nothing ever changes, a file named by its hash: what passes
// is kept once and answered from the copy after. Wanted is one more kept
// address, whose answer is a JSON list of fixed addresses, written
// relative to itself, to be had though nobody has asked for them yet: an
// issue's pictures, say. An address is the app's own folder or data, a
// service's (/v1/svc/), or one of the shared Platinum blocks; one that is
// not is left out.
type appOffline struct {
	Keep   []string `json:"keep,omitempty"`
	Fixed  []string `json:"fixed,omitempty"`
	Wanted string   `json:"wanted,omitempty"`
}

// UnmarshalJSON reads "offline": true as an app that keeps its folder and
// nothing more.
func (o *appOffline) UnmarshalJSON(b []byte) error {
	var on bool
	if json.Unmarshal(b, &on) == nil {
		o.Keep, o.Fixed = nil, nil
		if !on {
			o.Keep = []string{offlineOff}
		}
		return nil
	}
	type plain appOffline
	return json.Unmarshal(b, (*plain)(o))
}

// offlineOff marks "offline": false between the reading of an app.json and
// the cleaning of what it read.
const offlineOff = "\x00off"

const offlineMost = 32 // addresses of each kind

// cleanOffline is what an app named name may keep, of what its app.json
// asked for: nil for an app that keeps nothing.
func cleanOffline(name string, o *appOffline) *appOffline {
	if o == nil || (len(o.Keep) == 1 && o.Keep[0] == offlineOff) {
		return nil
	}
	own := []string{"/platinum/", "/v1/svc/", "/apps/" + name + "/", "/v1/apps/" + name + "/data/"}
	allowed := func(a string, prefix bool) bool {
		u, err := url.Parse(a)
		if err != nil || u.Scheme != "" || u.Host != "" || u.Fragment != "" || strings.ContainsAny(a, "\\#") {
			return false
		}
		// a path as it is asked for: rooted, with nothing to climb or fold
		clean := path.Clean(u.Path)
		if strings.HasSuffix(u.Path, "/") && clean != "/" {
			clean += "/"
		}
		if u.Path != clean || u.Query().Has("token") {
			return false
		}
		if prefix && (u.RawQuery != "" || !strings.HasSuffix(u.Path, "/")) {
			return false
		}
		for _, p := range own {
			if strings.HasPrefix(u.Path, p) && len(u.Path) > len(p) {
				return true
			}
		}
		return false
	}
	out := &appOffline{}
	for _, a := range o.Keep {
		if len(out.Keep) < offlineMost && allowed(a, false) {
			out.Keep = append(out.Keep, a)
		}
	}
	for _, a := range o.Fixed {
		if len(out.Fixed) < offlineMost && allowed(a, true) {
			out.Fixed = append(out.Fixed, a)
		}
	}
	if allowed(o.Wanted, false) {
		out.Wanted = o.Wanted
	}
	return out
}

// loadAppMeta reads one bundle's app.json; a folder only counts as an app
// when the metadata parses and index.html exists.
func (s *Server) loadAppMeta(root, name string) (*appMeta, error) {
	dir := filepath.Join(root, name)
	b, err := os.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		return nil, err
	}
	m := &appMeta{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return nil, errors.New("no index.html")
	}
	m.SystemIcon = ""               // disk bundles must remain isolated as image resources
	m.Name = canonicalAppName(name) // app.json cannot claim another identity
	m.Offline = cleanOffline(m.Name, m.Offline)
	if strings.TrimSpace(m.Title) == "" {
		m.Title = name
	}
	if m.Icon != "" && !filepath.IsLocal(m.Icon) {
		m.Icon = ""
	}
	return m, nil
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.listApps(true))
}

// listApps is every app the desk can open, with its code's version; quiet
// leaves a broken bundle out without a word (the code watch asks often).
func (s *Server) listApps(logSkips ...bool) []*appMeta {
	loud := len(logSkips) > 0 && logSkips[0]
	names := map[string]bool{}
	for _, root := range s.appRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			if loud && !os.IsNotExist(err) {
				log.Printf("apps: reading %s: %v", root, err)
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() && validAppName(e.Name()) {
				names[canonicalAppName(e.Name())] = true
			}
		}
	}
	if entries, err := fs.ReadDir(sysAppsFS, "sysapps"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				names[e.Name()] = true
			}
		}
	}
	apps := []*appMeta{}
	for name := range names {
		var m *appMeta
		var err error
		// Use the same resolution as static serving, so both spellings open
		// the bundle described here and never produce duplicate app entries.
		if root, folder, ok := s.findAppRoot(name); ok {
			if m, err = s.loadAppMeta(root, folder); err == nil {
				m.Version = appCodeVersion(filepath.Join(root, folder))
			}
		} else if m, err = loadSysAppMeta(name); err == nil {
			m.Version = sysAppVersion(name)
		}
		if err != nil {
			if loud {
				log.Printf("apps: skipping %s: %v", name, err)
			}
			continue
		}
		apps = append(apps, m)
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Title == apps[j].Title {
			return apps[i].Name < apps[j].Name
		}
		return apps[i].Title < apps[j].Title
	})
	return apps
}

// appStatic serves app bundles from disk at /apps/<name>/, resolving each
// app to whichever configured root holds it. os.DirFS is rooted, so requests
// cannot escape that root; app data lives in a separate tree this handler
// can never reach. /apps/ itself is the page that lists them (ui/apps.html).
func (s *Server) appStatic() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps/" {
			handleAppsIndex(w, r)
			return
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/apps/"), "/")
		root, folder, ok := s.findAppRoot(name)
		var files fs.FS
		if ok {
			files = os.DirFS(root)
		} else if sysAppExists(name) {
			folder = canonicalAppName(name)
			files, _ = fs.Sub(sysAppsFS, "sysapps")
		} else {
			http.NotFound(w, r)
			return
		}
		// Rewrite aliases internally, including nested assets. Old bookmarks
		// and already-open iframes continue using their original URL.
		r = r.Clone(r.Context())
		r.URL.Path = "/apps/" + folder + strings.TrimPrefix(r.URL.Path, "/apps/"+name)
		r.URL.RawPath = ""
		// Revalidate live disk edits and embedded assets after a rebuild.
		w.Header().Set("Cache-Control", "no-cache")
		http.StripPrefix("/apps/", http.FileServerFS(files)).ServeHTTP(w, r)
	})
}

// ---- scoped file stores (per-app data + shared workspace) ----

// fileMax caps a single stored file, the same 100 MB sync carries.
const fileMax = 100 << 20

// scopedPath resolves rel inside root, rejecting absolute paths and any
// traversal outside root.
func scopedPath(root, rel string) (string, error) {
	rel = filepath.FromSlash(rel)
	if rel == "" || !filepath.IsLocal(rel) {
		return "", errors.New("invalid path")
	}
	return filepath.Join(root, rel), nil
}

// appDataRoot validates the app name against an installed bundle and returns
// the app's private data directory (always under ~/.exe/appdata, whichever
// root the bundle itself lives in). "System" is reserved for the built-in
// desktop's own state (the Icon Editor's custom icons live there) and needs
// no bundle — it rides the same store so it persists, broadcasts and syncs
// to joined nodes like any app data.
func (s *Server) appDataRoot(name string) (string, error) {
	if name != "System" {
		if _, _, ok := s.findAppRoot(name); !ok && !sysAppExists(name) {
			return "", errors.New("no such app")
		}
	}
	return s.appDataDir(name), nil
}

type storedFile struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func handleFileList(w http.ResponseWriter, root string) {
	files := []storedFile{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, storedFile{Path: filepath.ToSlash(rel), Size: info.Size(), Modified: info.ModTime()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// mediaTypes pins the types the desktop's movie window plays; the host's
// /etc/mime.types may be absent or disagree, and a media element fed
// application/octet-stream under nosniff is a download, not a movie.
var mediaTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime",
	".webm": "video/webm", ".m4a": "audio/mp4", ".mp3": "audio/mpeg",
	".wav": "audio/wav", ".aac": "audio/aac", ".ogg": "audio/ogg",
	".oga": "audio/ogg", ".flac": "audio/flac",
}

func handleFileGet(w http.ResponseWriter, r *http.Request, root, rel string) {
	p, err := scopedPath(root, rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, errors.New("not found"))
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusInternalServerError, errors.New("not a file"))
		return
	}
	ext := strings.ToLower(filepath.Ext(p))
	ct := mediaTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// Data is app/agent-written content: keep browsers from rendering it as
	// a page on this origin (fetch() by apps is unaffected).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	// ServeContent answers Range with 206 and the requested bytes, so a
	// movie streams and scrubs (Safari refuses media served whole). The
	// name stays empty: the type is already set, and the modtime carries
	// the caching.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// handleFilePut reports success so app-data callers can notify the sync
// engine only for writes that actually landed.
func handleFilePut(w http.ResponseWriter, r *http.Request, root, rel string) bool {
	p, err := scopedPath(root, rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	tooBig := errors.New("file exceeds 100 MB")
	if r.ContentLength > fileMax {
		writeErr(w, http.StatusRequestEntityTooLarge, tooBig)
		return false
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return false
	}
	_, statErr := os.Lstat(p)
	created := os.IsNotExist(statErr)
	// Atomic-ish: temp file in the same directory, then rename over. The
	// body goes there as it arrives, not through memory: it can be 100 MB.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return false
	}
	size, err := io.Copy(tmp, io.LimitReader(r.Body, fileMax+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil || size > fileMax {
		os.Remove(tmp.Name())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
		} else {
			writeErr(w, http.StatusRequestEntityTooLarge, tooBig)
		}
		return false
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		writeErr(w, http.StatusInternalServerError, err)
		return false
	}
	// abs is where the file sits on this machine, for a caller that hands
	// the path to a program running here (a file dropped on an agent window)
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "path": rel, "abs": p, "size": size, "created": created})
	return true
}

func handleFileDelete(w http.ResponseWriter, root, rel string) bool {
	p, err := scopedPath(root, rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, errors.New("not found"))
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return false
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	return true
}

// ---- route handlers ----

func (s *Server) withAppData(w http.ResponseWriter, r *http.Request, fn func(root string)) {
	root, err := s.appDataRoot(r.PathValue("app"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	fn(root)
}

// App data is live state that other windows, desks and peers rewrite, so a
// browser must never answer an app's read out of its own cache. With only
// a Last-Modified to go on, Chrome kept a file that had sat idle for hours
// "fresh" for a tenth of that age, unasked: a desk that had not saved since
// went on reading its old copy of Blue Pencil's drafts after every change
// event, and typing there took the words written on another desk.
func (s *Server) handleAppDataList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.withAppData(w, r, func(root string) { handleFileList(w, root) })
}
func (s *Server) handleAppDataGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.withAppData(w, r, func(root string) { handleFileGet(w, r, root, r.PathValue("path")) })
}
func (s *Server) handleAppDataPut(w http.ResponseWriter, r *http.Request) {
	s.withAppData(w, r, func(root string) {
		app, rel := appDataName(r.PathValue("app")), r.PathValue("path")
		// X-Exe-Seq is an optional monotonic content timestamp the app stamps
		// on each save; it lets us reject a PUT whose content is older than
		// one already stored, closing the window where two of an app's own
		// saves race on unload and the older rename lands last. It is judged
		// among one writer's saves only (seqKey).
		seq, _ := strconv.ParseInt(r.Header.Get("X-Exe-Seq"), 10, 64)
		wrote, guarded := false, false
		// The file write and its versioning run under the sync engine's file
		// lock so a concurrent ApplyRemote can't clobber a write the API is
		// about to acknowledge (last-writer-loses race).
		s.withFileLock(func() {
			key := seqKey(app, rel, r.Header.Get("X-Exe-Client"))
			if seq > 0 && !s.seqNewer(key, seq) {
				writeJSON(w, http.StatusOK, map[string]any{"status": "stale", "path": rel})
				return
			}
			// A record-bearing document is judged record by record against
			// the copy on disk (peer.GuardLocalWrite), so a window running
			// older code cannot take back a newer record or a field it never
			// knew: an iPad still on the Notes from before colours wrote every
			// note back without its colour.
			if p, err := scopedPath(root, rel); err == nil && peer.Mergeable(app+"/"+rel) {
				body, err := io.ReadAll(io.LimitReader(r.Body, fileMax+1))
				if err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
				if disk, err := os.ReadFile(p); err == nil && len(body) <= fileMax {
					body, guarded = peer.GuardLocalWrite(app+"/"+rel, disk, body)
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			if handleFilePut(w, r, root, rel) {
				wrote = true
				if seq > 0 {
					s.recordSeq(key, seq)
				}
				if s.Peers != nil {
					s.Peers.LocalWrite(app, rel)
				}
			}
		})
		// Notify any OTHER open window on this node; the writing window
		// ignores its own echo via the client tag — unless the guard kept
		// something of disk's, when the writer must read the file again too.
		if wrote {
			client := r.Header.Get("X-Exe-Client")
			if guarded {
				client = ""
				log.Printf("app data: %s/%s kept records or fields a save left out", app, rel)
			}
			s.BroadcastAppData(app, rel, false, client)
		}
	})
}

// seqMark is the newest content timestamp accepted under one seqKey, and
// when it was.
type seqMark struct {
	seq int64
	at  time.Time
}

const (
	// a mark guards the seconds in which a window's last saves race each
	// other; past appSeqMax of them, those untouched for appSeqKeep go
	appSeqKeep = 10 * time.Minute
	appSeqMax  = 256
)

// seqKey names whose saves a seq is judged against: one file's, from one
// writer — the window behind the X-Exe-Client tag. The stamp is the
// writer's own clock, so it orders that writer's saves and nothing else:
// held against another desk's, a save stamped off the slower clock is
// dropped as "older" though it is the newest thing written, and the app is
// never told. Which desk's content wins is the apps' business (they reload
// on the change event and compare their records), not the clocks'. A PUT
// without the tag is judged file-wide.
func seqKey(app, rel, client string) string {
	key := app + "/" + rel
	if client == "" {
		return key
	}
	if len(client) > 64 {
		client = client[:64]
	}
	return key + "\x00" + client
}

// seqNewer reports whether seq is newer than the last accepted content
// timestamp for key (does not record it).
func (s *Server) seqNewer(key string, seq int64) bool {
	s.appSeqMu.Lock()
	defer s.appSeqMu.Unlock()
	return seq > s.appSeq[key].seq
}

// recordSeq stores seq as the newest accepted content timestamp for key.
func (s *Server) recordSeq(key string, seq int64) {
	s.appSeqMu.Lock()
	defer s.appSeqMu.Unlock()
	if s.appSeq == nil {
		s.appSeq = map[string]seqMark{}
	}
	now := time.Now()
	if seq > s.appSeq[key].seq {
		s.appSeq[key] = seqMark{seq: seq, at: now}
	}
	if len(s.appSeq) > appSeqMax { // every page load is a new writer
		for k, m := range s.appSeq {
			if k != key && now.Sub(m.at) > appSeqKeep {
				delete(s.appSeq, k)
			}
		}
	}
}
func (s *Server) handleAppDataDelete(w http.ResponseWriter, r *http.Request) {
	s.withAppData(w, r, func(root string) {
		app, rel := appDataName(r.PathValue("app")), r.PathValue("path")
		deleted := false
		s.withFileLock(func() {
			if handleFileDelete(w, root, rel) {
				deleted = true
				if s.Peers != nil {
					s.Peers.LocalDelete(app, rel)
				}
			}
		})
		if deleted {
			s.BroadcastAppData(app, rel, true, r.Header.Get("X-Exe-Client"))
		}
	})
}

// withFileLock runs fn under the sync engine's file lock when sync is active,
// or directly otherwise.
func (s *Server) withFileLock(fn func()) {
	if s.Peers != nil {
		s.Peers.LockFiles(fn)
		return
	}
	fn()
}

func (s *Server) handleWorkspaceList(w http.ResponseWriter, r *http.Request) {
	// ?dir= switches to a single-directory listing (with folders) for the
	// desktop's Finder-style Workspace window; the bare form stays the flat
	// recursive file list that apps already rely on.
	if r.URL.Query().Has("dir") {
		handleDirList(w, s.workspaceDir(), r.URL.Query().Get("dir"))
		return
	}
	handleFileList(w, s.workspaceDir())
}

// dirEntry is one row of a Finder-style folder listing.
type dirEntry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified"`
}

// handleDirList lists one directory level under root (rel "" is the root
// itself), hiding dotfiles like the classic Finder hides invisibles, plus
// the volume's free space for the "N items, X available" strip.
func handleDirList(w http.ResponseWriter, root, rel string) {
	p := root
	if rel != "" {
		var err error
		if p, err = scopedPath(root, rel); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("no such folder"))
		return
	}
	list := []dirEntry{}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, dirEntry{Name: e.Name(), Dir: e.IsDir(), Size: info.Size(), Modified: info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"entries": list, "free": diskFree(p)})
}
func (s *Server) handleWorkspaceGet(w http.ResponseWriter, r *http.Request) {
	handleFileGet(w, r, s.workspaceDir(), r.PathValue("path"))
}

// Workspace writes version + push like app-data writes, under the reserved
// @workspace namespace (the engine itself skips hidden paths like .Trash).
// The broadcast rides the same SSE stream app data uses; the desktop
// refreshes open Workspace windows on it.
func (s *Server) handleWorkspacePut(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	wrote := false
	s.withFileLock(func() {
		if handleFilePut(w, r, s.workspaceDir(), rel) {
			wrote = true
			if s.Peers != nil {
				s.Peers.LocalWrite(peer.WorkspaceNS, rel)
			}
		}
	})
	if wrote {
		s.BroadcastAppData(peer.WorkspaceNS, rel, false, r.Header.Get("X-Exe-Client"))
	}
}
func (s *Server) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	deleted := false
	s.withFileLock(func() {
		if handleFileDelete(w, s.workspaceDir(), rel) {
			deleted = true
			if s.Peers != nil {
				s.Peers.LocalDelete(peer.WorkspaceNS, rel)
			}
		}
	})
	if deleted {
		s.BroadcastAppData(peer.WorkspaceNS, rel, true, r.Header.Get("X-Exe-Client"))
	}
}

// handleWorkspaceMkdir creates a folder (POST …?mkdir=1) for the Finder's
// New Folder. Empty folders don't sync to peers — the engine's inventory is
// file-based — so there's no LocalWrite; the broadcast still refreshes other
// open desktops' windows on this node.
func (s *Server) handleWorkspaceMkdir(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	p, err := scopedPath(s.workspaceDir(), rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	made := false
	s.withFileLock(func() {
		if _, err := os.Lstat(p); err == nil {
			writeErr(w, http.StatusConflict, errors.New("name already taken"))
			return
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		made = true
	})
	if !made {
		return
	}
	s.BroadcastAppData(peer.WorkspaceNS, rel, false, r.Header.Get("X-Exe-Client"))
	writeJSON(w, http.StatusOK, map[string]any{"created": filepath.ToSlash(rel)})
}

// handleWorkspaceMove renames/moves a file or folder inside the workspace
// (body: {"to": "rel/path"}). The Finder's Move To Trash uses it to park
// items under the dot-hidden .Trash folder instead of hard-deleting.
func (s *Server) handleWorkspaceMove(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("mkdir") {
		s.handleWorkspaceMkdir(w, r)
		return
	}
	root := s.workspaceDir()
	src, err := scopedPath(root, r.PathValue("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	dst, err := scopedPath(root, body.To)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	moved := false
	s.withFileLock(func() {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if err := os.Rename(src, dst); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		moved = true
		// A file move syncs as delete-at-src + write-at-dst; the engine drops
		// the hidden half of a trash move or restore on its own. A folder move
		// is many files — leave it to the scanner, kicked to run now.
		if s.Peers != nil {
			if fi, serr := os.Stat(dst); serr == nil && !fi.IsDir() {
				s.Peers.LocalDelete(peer.WorkspaceNS, r.PathValue("path"))
				s.Peers.LocalWrite(peer.WorkspaceNS, body.To)
			} else {
				s.Peers.ReconcileNow()
			}
		}
	})
	if !moved {
		return
	}
	client := r.Header.Get("X-Exe-Client")
	s.BroadcastAppData(peer.WorkspaceNS, r.PathValue("path"), true, client)
	s.BroadcastAppData(peer.WorkspaceNS, body.To, false, client)
	writeJSON(w, http.StatusOK, map[string]any{"moved": filepath.ToSlash(body.To)})
}
