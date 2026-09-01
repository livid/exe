// Package server exposes the exe daemon's HTTP API and embedded web UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"

	"exe/internal/agent"
	"exe/internal/cf"
	"exe/internal/codex"
	"exe/internal/config"
	"exe/internal/github"
	"exe/internal/hostinfo"
	"exe/internal/peer"
	"exe/internal/proxy"
	"exe/internal/sshexec"
	"exe/internal/transcript"
	"exe/internal/vmm"
)

type Server struct {
	VMs      vmm.Manager
	Proxy    *proxy.Proxy
	KeyPath  string
	StateDir string

	// Logs, when set by main, holds the daemon log ring that GET /v1/logs
	// streams to the web UI.
	Logs *LogBuffer

	// OnRebind, when set, is called after a config PUT changes listen,
	// proxy_listen or ssh_listen so the daemon can re-bind those listeners
	// in-process — VMs live in this process and survive.
	OnRebind func(listen, proxyListen, sshListen string)

	// Peers, when set by main, is the node-to-node sync engine behind the
	// /v1/peers* and /v1/peer/* routes and the app-data write hooks.
	Peers *peer.Engine

	cfg        atomic.Pointer[config.Config]
	activeRuns sync.Map // transcript id -> struct{}

	// Cached Cloudflare heartbeat so UI polling doesn't hammer the CF API.
	cfHealthMu  sync.Mutex
	cfHealthAt  time.Time
	cfHealthKey string
	cfHealthRes map[string]any

	// Cached chat-backend detection for the Chat window, plus the in-flight
	// detached reply per chat session (chatrun.go) — one at most, so two
	// sends can't interleave a session's history.
	chatStatMu  sync.Mutex
	chatStatAt  time.Time
	chatStatKey string
	chatStatRes map[string]any
	chatRuns    sync.Map // session id -> *chatRun
	// chatSaveMu serializes all chat-session writes so the detached summary
	// writer (chatsummary.go) can't interleave with a run's saves.
	chatSaveMu sync.Mutex

	// ChatGPT sign-in state: cached ~/.exe/openai.json credentials and the
	// pending OAuth flow, if any (see openai.go).
	codexMu     sync.Mutex
	codexLoaded bool
	codexCache  *codex.Creds
	codexFlow   *codexFlow

	// GitHub sign-in state: cached ~/.exe/github.json credentials and the
	// pending device-code flow, if any (see github.go).
	ghMu     sync.Mutex
	ghLoaded bool
	ghCache  *github.Creds
	ghFlow   *ghFlow

	// Shared web UI window layout (see uistate.go).
	ui uiState

	// App-data change fanout to open desktops (see appevents.go).
	appEv appEvents

	// appSeq is the last accepted client sequence (X-Exe-Seq, a content
	// timestamp) per app-data file, so the daemon drops a PUT carrying older
	// content than one it already stored even if that older PUT's write lands
	// last — e.g. two of an app's own saves racing on window close.
	appSeqMu sync.Mutex
	appSeq   map[string]int64

	// One-writer guard for this node's Newsfeed journal (see newsfeed.go).
	newsMu  sync.Mutex
	newsSeq int64

	// Published-app icon cache: one fetch in flight per hostname (see
	// appicons.go).
	appIconMu sync.Map // hostname -> *sync.Mutex

	// The exe-hub agent: watches a hub and answers replies (see hubagent.go).
	hubAgent hubAgent
}

func New(cfg *config.Config, vms vmm.Manager, px *proxy.Proxy, keyPath, stateDir string) *Server {
	s := &Server{VMs: vms, Proxy: px, KeyPath: keyPath, StateDir: stateDir}
	s.hubAgent.kick = make(chan struct{}, 1)
	s.cfg.Store(cfg)
	s.ensureStateDirs()
	return s
}

// Config returns the live configuration (hot-swapped by PUT /v1/config).
func (s *Server) Config() *config.Config { return s.cfg.Load() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("GET /skill.md", handleSkill)
	mux.HandleFunc("GET /docs.md", handleDocs)
	mux.HandleFunc("GET /v1/vms", s.handleList)
	mux.HandleFunc("POST /v1/vms", s.handleCreate)
	mux.HandleFunc("GET /v1/vms/{name}", s.handleGet)
	mux.HandleFunc("POST /v1/vms/{name}/start", s.handleStart)
	mux.HandleFunc("POST /v1/vms/{name}/stop", s.handleStop)
	mux.HandleFunc("DELETE /v1/vms/{name}", s.handleDelete)
	mux.HandleFunc("POST /v1/vms/{name}/agent", s.handleAgent)
	mux.HandleFunc("POST /v1/vms/{name}/expose", s.handleExpose)
	mux.HandleFunc("GET /v1/vms/{name}/ports", s.handlePorts)
	mux.HandleFunc("GET /v1/vms/{name}/terminal", s.handleTerminal)
	mux.HandleFunc("GET /v1/host/terminal", s.handleHostTerminal)
	mux.HandleFunc("GET /v1/vms/{name}/transcripts", s.handleTranscripts)
	mux.HandleFunc("GET /v1/vms/{name}/transcripts/{id}", s.handleTranscript)
	mux.HandleFunc("GET /v1/vms/{name}/notes", s.handleNotesGet)
	mux.HandleFunc("PUT /v1/vms/{name}/notes", s.handleNotesPut)
	mux.HandleFunc("GET /v1/vms/{name}/memory", s.handleMemoryGet)
	mux.HandleFunc("DELETE /v1/vms/{name}/memory", s.handleMemoryDelete)
	mux.HandleFunc("GET /v1/hub/whoami", s.handleHubWhoami)
	mux.HandleFunc("POST /v1/hub/publish", s.handleHubPublish)
	mux.HandleFunc("POST /v1/hub/upload", s.handleHubUpload)
	mux.HandleFunc("POST /v1/hub/avatar", s.handleHubUpload)
	mux.HandleFunc("GET /v1/apps", s.handleApps)
	mux.HandleFunc("GET /v1/apps/events", s.handleAppDataEvents)
	mux.HandleFunc("GET /v1/apps/{app}/data", s.handleAppDataList)
	mux.HandleFunc("GET /v1/apps/{app}/data/{path...}", s.handleAppDataGet)
	mux.HandleFunc("PUT /v1/apps/{app}/data/{path...}", s.handleAppDataPut)
	mux.HandleFunc("DELETE /v1/apps/{app}/data/{path...}", s.handleAppDataDelete)
	mux.HandleFunc("GET /v1/workspace", s.handleWorkspaceList)
	mux.HandleFunc("GET /v1/workspace/{path...}", s.handleWorkspaceGet)
	mux.HandleFunc("PUT /v1/workspace/{path...}", s.handleWorkspacePut)
	mux.HandleFunc("POST /v1/workspace/{path...}", s.handleWorkspaceMove)
	mux.HandleFunc("DELETE /v1/workspace/{path...}", s.handleWorkspaceDelete)
	mux.HandleFunc("GET /v1/newsfeed", s.handleNewsfeedGet)
	mux.HandleFunc("POST /v1/newsfeed", s.handleNewsfeedPost)
	mux.HandleFunc("DELETE /v1/newsfeed/{id}", s.handleNewsfeedDelete)
	mux.HandleFunc("GET /v1/chat/status", s.handleChatStatus)
	mux.HandleFunc("GET /v1/chat/models", s.handleChatModels)
	mux.HandleFunc("GET /v1/chat/sessions", s.handleChatSessions)
	mux.HandleFunc("GET /v1/chat/sessions/{id}", s.handleChatSession)
	mux.HandleFunc("DELETE /v1/chat/sessions/{id}", s.handleChatSessionDelete)
	mux.HandleFunc("GET /v1/chat/sessions/{id}/stream", s.handleChatStream)
	mux.HandleFunc("POST /v1/chat/sessions/{id}/stop", s.handleChatStop)
	mux.HandleFunc("POST /v1/chat/sessions/{id}/queue", s.handleChatQueue)
	mux.HandleFunc("POST /v1/chat/sessions/{id}/confirm", s.handleChatConfirm)
	mux.HandleFunc("POST /v1/chat/send", s.handleChatSend)
	mux.HandleFunc("GET /v1/vms/{name}/publish/scan", s.handlePublishScan)
	mux.HandleFunc("POST /v1/vms/{name}/publish", s.handlePublish)
	mux.HandleFunc("POST /v1/github/oauth/start", s.handleGitHubStart)
	mux.HandleFunc("GET /v1/github/status", s.handleGitHubStatus)
	mux.HandleFunc("POST /v1/github/logout", s.handleGitHubLogout)
	mux.HandleFunc("POST /v1/openai/oauth/start", s.handleOpenAIStart)
	mux.HandleFunc("POST /v1/openai/oauth/complete", s.handleOpenAIComplete)
	mux.HandleFunc("GET /v1/openai/status", s.handleOpenAIStatus)
	mux.HandleFunc("GET /v1/openai/usage", s.handleOpenAIUsage)
	mux.HandleFunc("POST /v1/openai/logout", s.handleOpenAILogout)
	mux.HandleFunc("POST /v1/cloudflare/wizard", s.handleCFWizard)
	mux.HandleFunc("GET /v1/cloudflare/health", s.handleCFHealth)
	mux.HandleFunc("GET /v1/config", s.handleConfigGet)
	mux.HandleFunc("PUT /v1/config", s.handleConfigPut)
	mux.HandleFunc("POST /v1/daemon/restart", s.handleDaemonRestart)
	mux.HandleFunc("GET /v1/tailscale", s.handleTailscale)
	mux.HandleFunc("GET /v1/hostinfo", s.handleHostInfo)
	mux.HandleFunc("GET /v1/routes", s.handleRoutes)
	mux.HandleFunc("DELETE /v1/routes/{host}", s.handleRouteDelete)
	mux.HandleFunc("GET /v1/appicons/{host}", s.handleAppIcon)
	mux.HandleFunc("GET /v1/logs", s.handleLogs)
	mux.HandleFunc("GET /v1/ui/state", s.handleUIStateGet)
	mux.HandleFunc("PUT /v1/ui/state", s.handleUIStatePut)
	mux.HandleFunc("GET /v1/ui/events", s.handleUIStateEvents)
	mux.HandleFunc("GET /v1/ui/menu", s.handleDeskMenuGet)
	mux.HandleFunc("PUT /v1/ui/menu", s.handleDeskMenuPut)
	mux.HandleFunc("GET /v1/peers", s.handlePeersGet)
	mux.HandleFunc("POST /v1/peers/code", s.handlePeersCode)
	mux.HandleFunc("POST /v1/peers/join", s.handlePeersJoin)
	mux.HandleFunc("DELETE /v1/peers/{id}", s.handlePeersDelete)
	mux.HandleFunc("GET /v1/peers/status", s.handlePeersStatus)
	mux.HandleFunc("POST /v1/peer/pair", s.handlePeerPair)
	mux.HandleFunc("GET /v1/peer/ping", s.handlePeerPing)
	mux.HandleFunc("GET /v1/peer/manifest", s.handlePeerManifest)
	mux.HandleFunc("POST /v1/peer/unpair", s.handlePeerUnpair)
	mux.HandleFunc("GET /v1/peer/file/{app}/{path...}", s.handlePeerFileGet)
	mux.HandleFunc("PUT /v1/peer/file/{app}/{path...}", s.handlePeerFilePut)
	mux.Handle("GET /apps/", s.appStatic())
	mux.Handle("GET /ui/", uiStatic)
	mux.HandleFunc("GET /", s.handleUI)
	return s.auth(mux)
}

// auth guards the API; the static UI page itself is public (it holds no
// data — every API call it makes carries the token). /v1/peer/* is exempt:
// those routes authenticate each request by peer signature (or join code)
// instead, and expose nothing beyond app-data sync.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/peer/") {
			next.ServeHTTP(w, r)
			return
		}
		if tok := s.Config().APIToken; tok != "" && strings.HasPrefix(r.URL.Path, "/v1/") {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				// Browsers cannot set headers on WebSocket connects.
				got = r.URL.Query().Get("token")
			}
			if got != tok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func errCode(err error) int {
	if errors.Is(err, vmm.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, vmm.ErrNotRunning) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	vms, err := s.VMs.List(r.Context())
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	if vms == nil {
		vms = []*vmm.Info{}
	}
	writeJSON(w, http.StatusOK, vms)
}

// fillSpec applies the configured defaults to unset VM spec fields.
func (s *Server) fillSpec(spec *vmm.Spec) {
	cfg := s.Config()
	if spec.CPUs <= 0 {
		spec.CPUs = cfg.DefaultCPUs
	}
	if spec.MemoryMB <= 0 {
		spec.MemoryMB = cfg.DefaultMemoryMB
	}
	if spec.DiskGB <= 0 {
		spec.DiskGB = cfg.DefaultDiskGB
	}
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var spec vmm.Spec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.fillSpec(&spec)
	info, err := s.VMs.Create(r.Context(), spec)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	s.PostNews("vm", "VM created", vmNewsLine(spec))
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	info, err := s.VMs.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	info, err := s.VMs.Start(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.VMs.Stop(r.Context(), r.PathValue("name")); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.VMs.Delete(r.Context(), r.PathValue("name")); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	s.PostNews("vm", "VM deleted", r.PathValue("name")+" and its disk were removed.")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// guestDial returns the backend's in-process guest dialer when it has one
// (Windows, where VM IPs only exist inside the daemon); nil means the host
// network reaches VMs directly.
func (s *Server) guestDial() sshexec.DialFunc {
	if gd, ok := s.VMs.(vmm.GuestDialer); ok {
		return gd.DialGuest
	}
	return nil
}

// vmTarget builds the SSH target for a VM, routed through the backend's
// guest network when necessary.
func (s *Server) vmTarget(info *vmm.Info) sshexec.Target {
	return sshexec.Target{Host: info.IP, User: s.Config().SSHUser, KeyPath: s.KeyPath, Dialer: s.guestDial()}
}

func (s *Server) runningVM(ctx context.Context, name string) (*vmm.Info, error) {
	info, err := s.VMs.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if info.State != "running" || info.IP == "" {
		return nil, fmt.Errorf("vm %s is not running with an IP (state=%s); start it first", name, info.State)
	}
	return info, nil
}

func (s *Server) transcriptDir(vm string) string {
	return filepath.Join(s.StateDir, "vms", vm, "transcripts")
}

// agentPrecheck validates a vibecode request and resolves the running VM.
func (s *Server) agentPrecheck(ctx context.Context, name, prompt string) (*vmm.Info, error) {
	cfg := s.Config()
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if cfg.Ollama.APIKey == "" && strings.Contains(cfg.Ollama.BaseURL, "ollama.com") {
		return nil, errors.New("ollama.api_key is not configured (or set OLLAMA_API_KEY)")
	}
	return s.runningVM(ctx, name)
}

// agentRun executes one vibecode run against info's VM, recording a
// transcript and streaming output through emit.
func (s *Server) agentRun(ctx context.Context, info *vmm.Info, name, prompt, model string, emit func(string)) error {
	cfg := s.Config()
	acfg := agent.Config{
		BaseURL: cfg.Ollama.BaseURL,
		APIKey:  cfg.Ollama.APIKey,
		Model:   cfg.Ollama.Model,
		Effort:  cfg.Ollama.Effort,
	}
	if model != "" {
		acfg.Model = model
	}
	rec, err := transcript.Start(s.transcriptDir(name), prompt, acfg.Model)
	if err != nil {
		return err
	}
	s.activeRuns.Store(rec.ID(), struct{}{})
	defer s.activeRuns.Delete(rec.ID())
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		rec.Append(line)
		emit(line)
	}
	target := s.vmTarget(info)
	logf("[agent] model %s on vm %s (%s)\n", acfg.Model, name, info.IP)
	// The run starts with the VM briefing (state, notes, memory, recent
	// sessions) and can save durable facts for future runs; remember is a
	// host tool — the memory file lives beside the VM's notes, not in it.
	remember := agent.HostTool{
		Tool: rememberTool(false),
		Exec: func(_ context.Context, args map[string]any) string {
			content, ok := args["content"].(string)
			if !ok {
				return "error: missing required argument(s): content"
			}
			if err := s.writeVMMemory(name, content); err != nil {
				return "error: " + err.Error()
			}
			return "ok, memory saved"
		},
	}
	runErr := agent.Run(ctx, acfg, target, name, prompt,
		s.vmBriefing(ctx, name, ""), []agent.HostTool{remember}, logf)
	if runErr != nil {
		logf("\n[agent] ERROR: %v\n", runErr)
	} else {
		logf("\n[agent] done\n")
	}
	rec.Finish(runErr)
	return runErr
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	info, err := s.agentPrecheck(r.Context(), name, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	s.agentRun(r.Context(), info, name, req.Prompt, req.Model, func(line string) {
		fmt.Fprint(w, line)
		if fl != nil {
			fl.Flush()
		}
	})
}

// exposeVM wires <sub>.<domain> through the local proxy to the VM's port
// and, when Cloudflare is configured, ensures the DNS record and tunnel
// advertiseService is the origin URL cloudflared should dial to reach this
// daemon's reverse proxy: http://<advertise_host>:<proxy port>. Empty when
// advertise_host is unset.
func advertiseService(cfg *config.Config) string {
	if cfg.AdvertiseHost == "" {
		return ""
	}
	port := cfg.ProxyListen
	if i := strings.LastIndex(port, ":"); i >= 0 {
		port = port[i+1:]
	}
	return "http://" + net.JoinHostPort(cfg.AdvertiseHost, port)
}

// syncIngressRoutes repoints the tunnel ingress rule of every exposed
// hostname at svc, adding rules that are missing (e.g. exposed while
// advertise_host was still empty). Returns the hostnames changed plus a
// warning describing anything skipped or failed.
func (s *Server) syncIngressRoutes(ctx context.Context, cfg *config.Config, svc string) ([]string, string) {
	if svc == "" {
		return nil, "advertise_host is empty; existing tunnel ingress rules were left as-is"
	}
	cfc := &cf.Client{
		Token:     cfg.Cloudflare.APIToken,
		AccountID: cfg.Cloudflare.AccountID,
		ZoneID:    cfg.Cloudflare.ZoneID,
		TunnelID:  cfg.Cloudflare.TunnelID,
		Domain:    cfg.Cloudflare.Domain,
	}
	if !cfc.Configured() {
		return nil, "cloudflare not fully configured; tunnel ingress rules were not updated"
	}
	suffix := "." + cfg.Cloudflare.Domain
	services := map[string]string{}
	for host := range s.Proxy.Snapshot() {
		if strings.HasSuffix(host, suffix) {
			services[host] = svc
		}
	}
	if len(services) == 0 {
		return []string{}, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	changed, err := cfc.SyncIngress(ctx, services)
	if err != nil {
		log.Printf("config: ingress sync to %s: %v", svc, err)
		return nil, "ingress sync failed: " + err.Error()
	}
	if changed == nil {
		changed = []string{}
	}
	log.Printf("config: repointed %d of %d ingress rule(s) to %s", len(changed), len(services), svc)
	return changed, ""
}

// ingress rule. The caller validates port and cloudflare.domain first.
func (s *Server) exposeVM(ctx context.Context, info *vmm.Info, name, sub string, port int) (map[string]any, error) {
	cfg := s.Config()
	if sub == "" {
		sub = name
	}
	fqdn := sub + "." + cfg.Cloudflare.Domain
	backend := "http://" + net.JoinHostPort(info.IP, strconv.Itoa(port))
	if err := s.Proxy.Set(fqdn, backend); err != nil {
		log.Printf("expose %s: proxy: %v", fqdn, err)
		return nil, err
	}

	res := map[string]any{
		"host":    fqdn,
		"backend": backend,
		"url":     "https://" + fqdn,
	}
	var warnings []string
	cfc := &cf.Client{
		Token:     cfg.Cloudflare.APIToken,
		AccountID: cfg.Cloudflare.AccountID,
		ZoneID:    cfg.Cloudflare.ZoneID,
		TunnelID:  cfg.Cloudflare.TunnelID,
		Domain:    cfg.Cloudflare.Domain,
	}
	if cfc.Configured() {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if err := cfc.EnsureDNS(ctx, fqdn); err != nil {
			warnings = append(warnings, "dns: "+err.Error())
		} else {
			res["dns"] = "ok"
		}
		if svc := advertiseService(cfg); svc == "" {
			warnings = append(warnings, "advertise_host not set; skipped tunnel ingress update")
		} else if err := cfc.EnsureIngress(ctx, fqdn, svc); err != nil {
			warnings = append(warnings, "ingress: "+err.Error())
		} else {
			res["ingress"] = svc
		}
	} else {
		warnings = append(warnings, "cloudflare not fully configured; only the local proxy route was added")
	}
	if len(warnings) > 0 {
		res["warnings"] = warnings
		log.Printf("expose %s -> %s: %s", fqdn, backend, strings.Join(warnings, "; "))
	} else {
		log.Printf("expose %s -> %s", fqdn, backend)
	}
	return res, nil
}

func (s *Server) handleExpose(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	name := r.PathValue("name")
	var req struct {
		Subdomain string `json:"subdomain"`
		Port      int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		writeErr(w, http.StatusBadRequest, errors.New("port is required"))
		return
	}
	if cfg.Cloudflare.Domain == "" {
		writeErr(w, http.StatusBadRequest, errors.New("cloudflare.domain is not configured"))
		return
	}
	info, err := s.runningVM(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	res, err := s.exposeVM(r.Context(), info, name, req.Subdomain, req.Port)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleDaemonRestart restarts the daemon: running VMs are stopped (they
// live inside this process), a fresh copy of the binary is spawned with
// EXE_AUTOSTART naming them so it starts them again, and this process
// exits.
func (s *Server) handleDaemonRestart(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Executable(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	running := s.RunningVMNames(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"status": "restarting", "autostart": running})
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	go s.RestartDaemon(400*time.Millisecond, running) // delay lets the response reach the client
}

// RunningVMNames returns the names of the VMs currently in the running state.
func (s *Server) RunningVMNames(ctx context.Context) []string {
	var running []string
	if vms, err := s.VMs.List(ctx); err == nil {
		for _, v := range vms {
			if v.State == "running" {
				running = append(running, v.Name)
			}
		}
	}
	return running
}

// StopVMs stops the named VMs in parallel and waits for all of them.
func (s *Server) StopVMs(ctx context.Context, names []string) {
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if err := s.VMs.Stop(ctx, n); err != nil {
				log.Printf("stop %s: %v", n, err)
			}
		}(name)
	}
	wg.Wait()
}

// RestartDaemon stops the named VMs, spawns a fresh copy of the binary with
// EXE_AUTOSTART naming them so the new process starts them again, and exits
// this process. Spawn-then-exit rather than exec-in-place:
// Virtualization.framework keeps non-Go threads alive that wedge
// syscall.Exec's runtime hooks. Called from the restart API and the macOS
// menu bar icon.
func (s *Server) RestartDaemon(delay time.Duration, running []string) {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("restart: %v", err)
		return
	}
	time.Sleep(delay)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Let detached chat replies persist what they have and tell their
	// clients why they ended before the process goes away.
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	s.DrainChatRuns(dctx, "stopped: the daemon is restarting")
	dcancel()
	s.StopVMs(ctx, running)
	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "EXE_AUTOSTART="+strings.Join(running, ","))
	cmd.Stdout, cmd.Stderr = restartStdio(s.StateDir)
	cmd.SysProcAttr = restartSysProcAttr()
	if err := cmd.Start(); err != nil {
		log.Printf("restart: spawn failed: %v", err)
		return
	}
	log.Printf("restart: handing over to pid %d (autostart: %s)", cmd.Process.Pid, strings.Join(running, ","))
	os.Exit(0)
}

// restartStdio picks the handed-over daemon's stdout/stderr. Inheriting is
// only safe when they are a real terminal: under a supervisor they are its
// log pipe, which the supervisor closes once this process exits, and the
// child's next write would raise SIGPIPE and kill it. Non-terminal stdio
// goes to restart.log in the state dir rather than /dev/null so panic
// traces (which bypass the daemon.log ring) are not lost.
func restartStdio(stateDir string) (stdout, stderr *os.File) {
	if term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd())) {
		return os.Stdout, os.Stderr
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "restart.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil // exec.Cmd sends nil stdio to /dev/null
	}
	return f, f
}

// TailscaleIP is this host's Tailscale IPv4; see config.TailscaleIP.
func TailscaleIP() string { return config.TailscaleIP() }

func (s *Server) handleTailscale(w http.ResponseWriter, r *http.Request) {
	if ip := TailscaleIP(); ip != "" {
		writeJSON(w, http.StatusOK, map[string]any{"detected": true, "ip": ip})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"detected": false})
}

// handleHostInfo reports this host's identity for the About window: hostname,
// machine model, LAN IPv4, and the Tailscale IPv4 when on a tailnet — plus
// whether the Claude Code CLI is installed (the desktop shows its icon only
// then) and the project dir its sessions open in.
func (s *Server) handleHostInfo(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	ts := config.TailscaleIP()
	lan := config.LANIP()
	if lan == ts {
		lan = "" // the default route is the tailnet; don't show the same IP twice
	}
	info := map[string]any{
		"hostname": host, "machine": hostinfo.Model(), "lan_ip": lan, "tailscale_ip": ts,
	}
	if claudePath() != "" {
		info["claude_code"] = true
		info["claude_dir"] = s.claudeProjectDir()
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Proxy.Snapshot())
}

// removeRoute unpublishes a hostname: Cloudflare DNS record and tunnel
// ingress rule first, then the local proxy route. Cloudflare failures abort
// before local state changes, so a failed unexpose leaves the route visible;
// both Cloudflare deletes no-op when the record is already gone, so retries
// after a partial failure are safe.
func (s *Server) removeRoute(ctx context.Context, host string) (map[string]any, error) {
	cfg := s.Config()
	res := map[string]any{"status": "removed"}
	cfc := &cf.Client{
		Token:     cfg.Cloudflare.APIToken,
		AccountID: cfg.Cloudflare.AccountID,
		ZoneID:    cfg.Cloudflare.ZoneID,
		TunnelID:  cfg.Cloudflare.TunnelID,
		Domain:    cfg.Cloudflare.Domain,
	}
	if cfc.Configured() {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if err := cfc.DeleteDNS(ctx, host); err != nil {
			log.Printf("unexpose %s: dns: %v", host, err)
			return nil, fmt.Errorf("dns: %w", err)
		}
		res["dns"] = "removed"
		if err := cfc.RemoveIngress(ctx, host); err != nil {
			log.Printf("unexpose %s: ingress: %v", host, err)
			return nil, fmt.Errorf("ingress: %w", err)
		}
		res["ingress"] = "removed"
	}
	if err := s.Proxy.Remove(host); err != nil {
		log.Printf("unexpose %s: proxy: %v", host, err)
		return nil, err
	}
	log.Printf("unexpose %s: removed", host)
	return res, nil
}

// handleLogs streams the daemon log as plain text: the buffered backlog
// first, then live lines until the client disconnects.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, http.StatusNotFound, errors.New("log streaming not available"))
		return
	}
	backlog, ch, cancel := s.Logs.Subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	for _, ln := range backlog {
		fmt.Fprintln(w, ln)
	}
	if fl != nil {
		fl.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case ln := <-ch:
			fmt.Fprintln(w, ln)
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

func (s *Server) handleRouteDelete(w http.ResponseWriter, r *http.Request) {
	res, err := s.removeRoute(r.Context(), r.PathValue("host"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
