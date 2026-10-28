package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"exe/internal/config"
	"exe/internal/release"
)

// Extras: what adds to exe and is not part of it, and the desktop's way
// to turn each on and off (the Extras window, from Welcome's Apps card).
//
//	GET  /v1/extras          how each stands on this machine
//	POST /v1/extras/{name}   {"on": true|false, "dir": "…", "check": true}
//
// There are four. The extra apps (exe-apps) are bundles a release brings
// as one tarball; exe-planet, exe-easel and exe-projects are programs of
// their own that run beside exe, each with a folder of apps for the
// desktop and a daemon the desktop reaches as a service.
//
// On means the same for all of them: the extra's apps show on the desktop,
// and its service, where it has one, is named in the configuration. How it
// gets there is one of two roads:
//
//   - A folder. The extra's own checkout holds the bundles, and turning it
//     on names that folder in apps_dirs and its daemon in services. Off
//     takes both names out again and writes down where they pointed
//     (extras.json), so on is one step the next time. Nothing of the
//     extra's is touched: its files stay, and its daemon is its own.
//   - The release. For the extra apps of a released exe: on fetches the
//     tarball of the release that is running, checks it against the
//     release's sums and places the bundles in the apps folder, exactly as
//     the installer would have (release.InstallApps), so an update follows
//     them from then on. Off removes the ones that are still what was
//     placed (release.RemoveApps); one that was edited is its owner's.
//
// A copy that is on the machine and not turned on is looked for where it
// was last, then beside what exe already knows. One that is nowhere to be
// found is "missing": the window asks for its folder, and says how to get
// the program.
//
// The configuration is changed the way PUT /v1/config changes it and takes
// effect at once: the app list and the service relay read it live. Every
// desk hears that the list changed.

// extraDef is one extra, as exe knows it.
type extraDef struct {
	Name    string   // "apps", "planet", "easel", "projects"
	Repo    string   // the checkout's name
	Bundles []string // the app folders it brings
	Service string   // its name under services, "" for none
	URL     string   // where its daemon usually listens
	Health  string   // a path that answers 200 while the daemon runs
}

var extraDefs = []extraDef{
	{Name: "apps", Repo: "exe-apps", Bundles: []string{"Notes", "Paint", "Ping", "Tides", "Todo", "Weather", "World Clock"}},
	{Name: "planet", Repo: "exe-planet", Bundles: []string{"Planet", "Writer"},
		Service: "planet", URL: "http://127.0.0.1:7799", Health: "/v1/status"},
	{Name: "easel", Repo: "exe-easel", Bundles: []string{"easel"},
		Service: "easel", URL: "http://127.0.0.1:7794", Health: "/v1/health"},
	{Name: "projects", Repo: "exe-projects", Bundles: []string{"projects"},
		Service: "projects", URL: "http://127.0.0.1:7789", Health: "/v1/status"},
}

func extraNamed(name string) *extraDef {
	for i := range extraDefs {
		if extraDefs[i].Name == name {
			return &extraDefs[i]
		}
	}
	return nil
}

// extraInfo is how one extra stands.
type extraInfo struct {
	Name    string   `json:"name"`
	Repo    string   `json:"repo"`
	Bundles []string `json:"bundles"`
	// State is "on", "off" (a copy is here, or the release has one) or
	// "missing" (nothing to turn on).
	State string `json:"state"`
	// From is the road: "folder" or "release"; "apps" for bundles someone
	// put in the apps folder by hand, which no switch here removes.
	From string `json:"from,omitempty"`
	// Dir is the folder the bundles are in, or would be taken from.
	Dir string `json:"dir,omitempty"`
	// Have is the bundles on the desktop now.
	Have []string `json:"have,omitempty"`
	// Service is the daemon's address: the configured one, or the one
	// turning it on would set. Running says whether it answers.
	Service string `json:"service,omitempty"`
	Running *bool  `json:"running,omitempty"`
	// Release is the version the extra apps would be fetched from.
	Release string `json:"release,omitempty"`
	// Kept is the bundles a turn-off left in place because they are
	// their owner's: edited since they were placed, or never ours.
	Kept []string `json:"kept,omitempty"`
}

// extraMemory is where an extra was when it was turned off.
type extraMemory struct {
	Dir     string `json:"dir,omitempty"`
	Service string `json:"service,omitempty"`
}

type extrasState struct {
	mu sync.Mutex // one change at a time

	// seams, set by tests
	version func() string
	client  *release.Client
	store   func(nc *config.Config) error // write the configuration and make it live
	near    func() []string               // folders a checkout may stand in
	probe   func(url string) bool         // a daemon answers there
}

func (s *Server) extrasInit() *extrasState {
	e := &s.ext
	if e.version == nil {
		e.version = func() string { return release.Version }
	}
	if e.client == nil {
		e.client = release.NewClient()
	}
	if e.store == nil {
		e.store = s.storeConfig
	}
	if e.near == nil {
		e.near = s.extrasNear
	}
	if e.probe == nil {
		e.probe = func(url string) bool {
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return false
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return false
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}
	}
	return e
}

// storeConfig writes the configuration to disk and makes it the live one.
func (s *Server) storeConfig(nc *config.Config) error {
	b, err := json.MarshalIndent(nc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(config.Path(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	s.cfg.Store(nc)
	return nil
}

func (s *Server) extrasFile() string       { return filepath.Join(s.StateDir, "extras.json") }
func (s *Server) appsManifestFile() string { return filepath.Join(s.StateDir, "release", "apps.json") }

func (s *Server) extrasMemory() map[string]extraMemory {
	m := map[string]extraMemory{}
	if b, err := os.ReadFile(s.extrasFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (s *Server) extrasRemember(name string, mem extraMemory) error {
	m := s.extrasMemory()
	m[name] = mem
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := s.extrasFile() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.extrasFile())
}

// isBundle reports whether dir/name is an app's folder.
func isBundle(dir, name string) bool {
	st, err := os.Stat(filepath.Join(dir, name, "app.json"))
	return err == nil && st.Mode().IsRegular()
}

// extraHolds names the extra's bundles that stand in dir.
func extraHolds(dir string, def *extraDef) []string {
	var have []string
	for _, b := range def.Bundles {
		if isBundle(dir, b) {
			have = append(have, b)
		}
	}
	return have
}

// extraOwns reports whether dir is the extra's folder: it holds some of
// the extra's bundles and no app of anyone else's. A folder where
// somebody keeps their own apps beside a copy of Notes is theirs: naming
// it, or taking its name away, would move their apps with ours.
func extraOwns(dir string, def *extraDef) bool {
	if len(extraHolds(dir, def)) == 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && isBundle(dir, e.Name()) && !slices.Contains(def.Bundles, e.Name()) {
			return false
		}
	}
	return true
}

// extraLocate finds the extra's folder in what someone typed: the folder
// itself, or the checkout it is the apps folder of.
func extraLocate(typed string, def *extraDef) string {
	dir := expandHome(strings.TrimSpace(typed))
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	dir = filepath.Clean(dir)
	for _, d := range []string{dir, filepath.Join(dir, "apps")} {
		if extraOwns(d, def) {
			return d
		}
	}
	return ""
}

// extrasNear is the folders a checkout may stand in: beside the folders
// apps_dirs already names, beside this binary's own checkout, and the
// home folder.
func (s *Server) extrasNear() []string {
	var out []string
	add := func(d string) {
		if d != "" && d != "." && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	for _, d := range s.Config().AppsDirs {
		d = filepath.Clean(expandHome(d))
		add(filepath.Dir(d))
		add(filepath.Dir(filepath.Dir(d)))
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Dir(filepath.Dir(exe)))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	return out
}

// extraFind looks for a copy that is not turned on: where it was when it
// was turned off, then by its checkout's name in the folders nearby.
func (s *Server) extraFind(def *extraDef, mem extraMemory) string {
	if mem.Dir != "" && extraOwns(mem.Dir, def) {
		return mem.Dir
	}
	for _, near := range s.extrasInit().near() {
		if dir := extraLocate(filepath.Join(near, def.Repo), def); dir != "" {
			return dir
		}
	}
	return ""
}

// extraStand says how def stands now. probe asks its daemon whether it
// runs; a caller that only needs the state leaves that out.
func (s *Server) extraStand(def *extraDef, probe bool) extraInfo {
	e := s.extrasInit()
	cfg := s.Config()
	mem := s.extrasMemory()[def.Name]
	info := extraInfo{Name: def.Name, Repo: def.Repo, Bundles: def.Bundles}
	own := s.appsDir()
	// where each bundle shows from: the first root that has it, as the
	// desk takes it. wired is the extra's own folder among them; shown is
	// wherever the first bundle was found.
	wired, shown := "", ""
	for _, b := range def.Bundles {
		for _, root := range s.appRoots() {
			if !isBundle(root, b) {
				continue
			}
			info.Have = append(info.Have, b)
			if shown == "" {
				shown = root
			}
			if wired == "" && root != own && extraOwns(root, def) {
				wired = root
			}
			break
		}
	}
	svc := ""
	if def.Service != "" {
		svc = cfg.Services[def.Service]
	}
	tracked := def.Name == "apps" && release.AppsTracked(s.appsManifestFile())
	switch {
	case len(info.Have) > 0 && (def.Service == "" || svc != ""):
		info.State = "on"
		switch {
		case wired != "":
			info.From, info.Dir = "folder", wired
		case tracked:
			info.From, info.Dir = "release", own
		default:
			info.From, info.Dir = "apps", shown
		}
	default:
		// what turning it on would use: a folder first, then the release
		dir := wired
		if dir == "" {
			dir = s.extraFind(def, mem)
		}
		switch {
		case dir != "":
			info.State, info.From, info.Dir = "off", "folder", dir
		case def.Name == "apps" && e.version() != "":
			info.State, info.From, info.Release = "off", "release", e.version()
		default:
			info.State = "missing"
		}
	}
	if def.Service != "" {
		info.Service = svc
		if info.Service == "" {
			info.Service = mem.Service
		}
		if info.Service == "" {
			info.Service = def.URL
		}
		if probe && info.State != "missing" {
			up := e.probe(strings.TrimRight(info.Service, "/") + def.Health)
			info.Running = &up
		}
	}
	return info
}

// said is an extra as it is told: a folder under the home folder is
// written with "~", as the manual and config.json write it (expandHome
// reads it back).
func (x extraInfo) said() extraInfo {
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != string(filepath.Separator) {
		if rel, err := filepath.Rel(home, x.Dir); err == nil && x.Dir != "" && filepath.IsLocal(rel) {
			x.Dir = "~"
			if rel != "." {
				x.Dir = "~/" + filepath.ToSlash(rel)
			}
		}
	}
	return x
}

func (s *Server) handleExtras(w http.ResponseWriter, r *http.Request) {
	out := make([]extraInfo, len(extraDefs))
	var wg sync.WaitGroup
	for i := range extraDefs {
		wg.Add(1)
		go func() { // each asks its daemon; none waits for another's
			defer wg.Done()
			out[i] = s.extraStand(&extraDefs[i], true).said()
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"extras": out})
}

func (s *Server) handleExtraSet(w http.ResponseWriter, r *http.Request) {
	def := extraNamed(r.PathValue("name"))
	if def == nil {
		writeErr(w, http.StatusNotFound, errors.New("no such extra"))
		return
	}
	var in struct {
		On    bool   `json:"on"`
		Dir   string `json:"dir"`
		Check bool   `json:"check"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	e := s.extrasInit()
	e.mu.Lock()
	defer e.mu.Unlock()

	dir := ""
	if strings.TrimSpace(in.Dir) != "" {
		if dir = extraLocate(in.Dir, def); dir == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s is not in that folder: it would hold %s",
				def.Repo, filepath.Join(def.Bundles[0], "app.json")))
			return
		}
	}
	if in.Check { // is a copy there: nothing changes
		info := s.extraStand(def, true)
		if dir != "" && info.State != "on" {
			info.State, info.From, info.Dir, info.Release = "off", "folder", dir, ""
			if def.Service != "" { // a copy that is here may well be running
				up := e.probe(strings.TrimRight(info.Service, "/") + def.Health)
				info.Running = &up
			}
		}
		writeJSON(w, http.StatusOK, info.said())
		return
	}

	var err error
	if in.On {
		err = s.extraOn(r.Context(), def, dir)
	} else {
		err = s.extraOff(def)
	}
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errExtraMissing) {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	// every desk's list of apps is out of date now
	s.broadcastAppEvent(map[string]any{"app": "@apps", "path": ""})
	info := s.extraStand(def, true)
	if !in.On {
		// what is still on the desktop after a turn-off is its owner's:
		// edited since it was placed, or never ours to remove
		info.Kept = info.Have
	}
	writeJSON(w, http.StatusOK, info.said())
}

var errExtraMissing = errors.New("it is not on this machine")

// extraOn turns def on: from dir when one is given, else by the road
// extraStand found.
func (s *Server) extraOn(ctx context.Context, def *extraDef, dir string) error {
	now := s.extraStand(def, false)
	if dir == "" {
		if now.State == "on" {
			return nil
		}
		if now.From == "folder" {
			dir = now.Dir
		}
	}
	switch {
	case dir != "":
		return s.extraWire(def, dir)
	case now.From == "release":
		return s.extraFetchApps(ctx)
	}
	return fmt.Errorf("%s: %w", def.Repo, errExtraMissing)
}

// extraWire names dir in apps_dirs and the extra's daemon in services.
func (s *Server) extraWire(def *extraDef, dir string) error {
	dir = filepath.Clean(dir)
	old := s.Config()
	nc := *old
	nc.AppsDirs = slices.Clone(old.AppsDirs)
	nc.Services = maps.Clone(old.Services)
	named := false
	for _, d := range nc.AppsDirs {
		if filepath.Clean(expandHome(d)) == dir {
			named = true
		}
	}
	if !named {
		nc.AppsDirs = append(nc.AppsDirs, dir)
	}
	if def.Service != "" && nc.Services[def.Service] == "" {
		if nc.Services == nil {
			nc.Services = map[string]string{}
		}
		url := s.extrasMemory()[def.Name].Service
		if url == "" {
			url = def.URL
		}
		nc.Services[def.Service] = url
	}
	nc.Normalize()
	return s.extrasInit().store(&nc)
}

// extraFetchApps brings the extra apps of the release that is running:
// the installer's own steps, for an exe whose installer was told no.
func (s *Server) extraFetchApps(ctx context.Context) error {
	e := s.extrasInit()
	version := e.version()
	if version == "" {
		return errors.New("an exe built from a checkout takes its extra apps from a checkout of exe-apps")
	}
	scratch, err := os.MkdirTemp("", "exe-extras-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := e.client.Fetch(ctx, version, scratch, release.AppsAsset); err != nil {
		return fmt.Errorf("fetching the apps of exe %s: %w", version, err)
	}
	src := filepath.Join(scratch, "apps")
	if err := release.Extract(filepath.Join(scratch, release.AppsAsset), src); err != nil {
		return err
	}
	_, err = release.InstallApps(src, s.appsDir(), filepath.Join(s.StateDir, "release", "staging"), s.appsManifestFile())
	return err
}

// extraOff turns def off: its folders leave apps_dirs and its daemon
// leaves services, and what the release placed is removed.
func (s *Server) extraOff(def *extraDef) error {
	old := s.Config()
	nc := *old
	nc.AppsDirs = nil
	nc.Services = maps.Clone(old.Services)
	mem := s.extrasMemory()[def.Name]
	changed := false
	for _, d := range old.AppsDirs {
		clean := filepath.Clean(expandHome(d))
		if extraOwns(clean, def) {
			mem.Dir, changed = clean, true
			continue
		}
		nc.AppsDirs = append(nc.AppsDirs, d)
	}
	if def.Service != "" {
		if url, ok := nc.Services[def.Service]; ok {
			mem.Service, changed = url, true
			delete(nc.Services, def.Service)
		}
	}
	if changed {
		// where it was is written down first: a configuration saved
		// without it would leave a copy nobody can find again
		if err := s.extrasRemember(def.Name, mem); err != nil {
			return err
		}
		nc.Normalize()
		if err := s.extrasInit().store(&nc); err != nil {
			return err
		}
	}
	if def.Name == "apps" && release.AppsTracked(s.appsManifestFile()) {
		if _, _, err := release.RemoveApps(s.appsDir(), s.appsManifestFile()); err != nil {
			return err
		}
		// the record goes with them: an update no longer brings them
		if err := release.ForgetApps(s.appsManifestFile()); err != nil {
			return err
		}
	}
	return nil
}
