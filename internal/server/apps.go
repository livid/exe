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
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
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

func sysAppExists(name string) bool {
	if !validAppName(name) {
		return false
	}
	_, err := fs.Stat(sysAppsFS, "sysapps/"+name+"/index.html")
	return err == nil
}

func loadSysAppMeta(name string) (*appMeta, error) {
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
	if m.Title == "" {
		m.Title = name
	}
	if m.Icon != "" && !filepath.IsLocal(m.Icon) {
		m.Icon = ""
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

// findAppRoot returns the first root holding an app folder of this name.
func (s *Server) findAppRoot(name string) (string, bool) {
	if !validAppName(name) {
		return "", false
	}
	for _, root := range s.appRoots() {
		if fi, err := os.Stat(filepath.Join(root, name)); err == nil && fi.IsDir() {
			return root, true
		}
	}
	return "", false
}
func (s *Server) appDataDir(app string) string {
	return filepath.Join(s.StateDir, "appdata", app)
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
	Name   string     `json:"name"`
	Title  string     `json:"title"`
	Icon   string     `json:"icon,omitempty"`
	Window *appWindow `json:"window,omitempty"`
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
	m.Name = name // the folder is the identity; app.json cannot claim another
	if strings.TrimSpace(m.Title) == "" {
		m.Title = name
	}
	if m.Icon != "" && !filepath.IsLocal(m.Icon) {
		m.Icon = ""
	}
	return m, nil
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	apps := []*appMeta{}
	seen := map[string]bool{}
	for _, root := range s.appRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("apps: reading %s: %v", root, err)
			}
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !validAppName(e.Name()) || seen[e.Name()] {
				continue
			}
			m, err := s.loadAppMeta(root, e.Name())
			if err != nil {
				log.Printf("apps: skipping %s: %v", e.Name(), err)
				continue
			}
			seen[e.Name()] = true
			apps = append(apps, m)
		}
	}
	if entries, err := fs.ReadDir(sysAppsFS, "sysapps"); err == nil {
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			m, err := loadSysAppMeta(e.Name())
			if err != nil {
				log.Printf("apps: skipping embedded %s: %v", e.Name(), err)
				continue
			}
			seen[e.Name()] = true
			apps = append(apps, m)
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Title < apps[j].Title })
	writeJSON(w, http.StatusOK, apps)
}

// appStatic serves app bundles from disk at /apps/<name>/, resolving each
// app to whichever configured root holds it. os.DirFS is rooted, so requests
// cannot escape that root; app data lives in a separate tree this handler
// can never reach.
func (s *Server) appStatic() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/apps/"), "/")
		root, ok := s.findAppRoot(name)
		if !ok {
			if sysAppExists(name) {
				sub, _ := fs.Sub(sysAppsFS, "sysapps")
				w.Header().Set("Cache-Control", "no-cache")
				http.StripPrefix("/apps/", http.FileServerFS(sub)).ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		// Apps are edited live on disk; without this, browsers apply
		// heuristic freshness to the bare Last-Modified and can serve a
		// stale bundle for minutes after an edit. no-cache still allows
		// 304 revalidation, so unchanged files stay cheap.
		w.Header().Set("Cache-Control", "no-cache")
		http.StripPrefix("/apps/", http.FileServerFS(os.DirFS(root))).ServeHTTP(w, r)
	})
}

// ---- scoped file stores (per-app data + shared workspace) ----

// fileMax caps a single stored file.
const fileMax = 10 << 20

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
		if _, ok := s.findAppRoot(name); !ok && !sysAppExists(name) {
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

func handleFileGet(w http.ResponseWriter, root, rel string) {
	p, err := scopedPath(root, rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, errors.New("not found"))
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(p))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// Data is app/agent-written content: keep browsers from rendering it as
	// a page on this origin (fetch() by apps is unaffected).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	w.Write(b)
}

// handleFilePut reports success so app-data callers can notify the sync
// engine only for writes that actually landed.
func handleFilePut(w http.ResponseWriter, r *http.Request, root, rel string) bool {
	p, err := scopedPath(root, rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, fileMax+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	if len(body) > fileMax {
		writeErr(w, http.StatusRequestEntityTooLarge, errors.New("file exceeds 10 MB"))
		return false
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return false
	}
	_, statErr := os.Lstat(p)
	created := os.IsNotExist(statErr)
	// Atomic-ish: temp file in the same directory, then rename over.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return false
	}
	if _, err := tmp.Write(body); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
		writeErr(w, http.StatusInternalServerError, err)
		return false
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "path": rel, "size": len(body), "created": created})
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

func (s *Server) handleAppDataList(w http.ResponseWriter, r *http.Request) {
	s.withAppData(w, r, func(root string) { handleFileList(w, root) })
}
func (s *Server) handleAppDataGet(w http.ResponseWriter, r *http.Request) {
	s.withAppData(w, r, func(root string) { handleFileGet(w, root, r.PathValue("path")) })
}
func (s *Server) handleAppDataPut(w http.ResponseWriter, r *http.Request) {
	s.withAppData(w, r, func(root string) {
		app, rel := r.PathValue("app"), r.PathValue("path")
		// X-Exe-Seq is an optional monotonic content timestamp the app stamps
		// on each save; it lets us reject a PUT whose content is older than
		// one already stored, closing the window where two of an app's own
		// saves race on unload and the older rename lands last.
		seq, _ := strconv.ParseInt(r.Header.Get("X-Exe-Seq"), 10, 64)
		wrote := false
		// The file write and its versioning run under the sync engine's file
		// lock so a concurrent ApplyRemote can't clobber a write the API is
		// about to acknowledge (last-writer-loses race).
		s.withFileLock(func() {
			key := app + "/" + rel
			if seq > 0 && !s.seqNewer(key, seq) {
				writeJSON(w, http.StatusOK, map[string]any{"status": "stale", "path": rel})
				return
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
		// ignores its own echo via the client tag.
		if wrote {
			s.BroadcastAppData(app, rel, false, r.Header.Get("X-Exe-Client"))
		}
	})
}

// seqNewer reports whether seq is newer than the last accepted content
// timestamp for key (does not record it).
func (s *Server) seqNewer(key string, seq int64) bool {
	s.appSeqMu.Lock()
	defer s.appSeqMu.Unlock()
	return seq > s.appSeq[key]
}

// recordSeq stores seq as the newest accepted content timestamp for key.
func (s *Server) recordSeq(key string, seq int64) {
	s.appSeqMu.Lock()
	defer s.appSeqMu.Unlock()
	if s.appSeq == nil {
		s.appSeq = map[string]int64{}
	}
	if seq > s.appSeq[key] {
		s.appSeq[key] = seq
	}
}
func (s *Server) handleAppDataDelete(w http.ResponseWriter, r *http.Request) {
	s.withAppData(w, r, func(root string) {
		app, rel := r.PathValue("app"), r.PathValue("path")
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
	handleFileGet(w, s.workspaceDir(), r.PathValue("path"))
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
