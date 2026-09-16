package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"exe/internal/config"
	"exe/internal/transcript"
	"exe/internal/vmm"
)

//go:embed ui/index.html
var uiHTML []byte

//go:embed skill.md
var skillMD []byte

// handleSkill serves the agent skill guide: a markdown file any coding agent
// can fetch to learn how to drive this daemon's API and VMs.
func handleSkill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write(skillMD)
}

//go:embed docs.md
var docsMD []byte

// handleDocs serves the user manual — the page the web UI's Help →
// exe Documentation window renders.
func handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write(docsMD)
}

//go:embed all:ui
var uiFS embed.FS

//go:embed ui/sw.js
var uiSWSrc []byte

// uiBuild and uiETags come from the bytes the binary ships: an ETag per
// /ui/ file (index.html included) so a reload revalidates with a 304
// instead of downloading the vendor scripts again, and one hash over all
// of them that stamps the service worker — a deploy that changes any UI
// byte hands the browser a byte-different sw.js, which installs afresh and
// retires the old offline cache.
var uiBuild, uiETags = func() (string, map[string]string) {
	tags := map[string]string{}
	var names []string
	err := fs.WalkDir(uiFS, "ui", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := uiFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel := strings.TrimPrefix(p, "ui/")
		tags[rel] = `"` + hex.EncodeToString(sum[:8]) + `"`
		names = append(names, rel)
		return nil
	})
	if err != nil {
		panic(err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		io.WriteString(h, n+" "+tags[n]+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))[:12], tags
}()

// uiSW is the service worker with the UI build stamped in.
var uiSW = bytes.ReplaceAll(uiSWSrc, []byte("__EXE_BUILD__"), []byte(uiBuild))

// uiStatic serves the vendored UI assets (xterm.js etc.) at /ui/.
var uiStatic = func() http.Handler {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/ui/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/ui/")
		if rel == "sw.js" {
			// the worker is served stamped, from the root (handleServiceWorker)
			http.NotFound(w, r)
			return
		}
		if tag, ok := uiETags[rel]; ok {
			// embedded files carry no modtime, so the ETag is what lets
			// ServeContent answer If-None-Match with a 304; no-cache makes
			// every use revalidate — the files change with a deploy
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}()

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	// the UI ships embedded in the binary and changes with every deploy;
	// no-cache makes a plain reload always revalidate to the new build, and
	// the ETag lets that revalidation come back as a 304
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", uiETags["index.html"])
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(uiHTML))
}

// handleServiceWorker serves the desktop's service worker from the root so
// its scope is the whole origin. Browsers only register one on a secure
// origin, so the plain-HTTP address never asks for it.
func handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("ETag", `"sw-`+uiBuild+`"`)
	http.ServeContent(w, r, "sw.js", time.Time{}, bytes.NewReader(uiSW))
}

var ssProcessRE = regexp.MustCompile(`users:\(\("([^"]+)"`)

type vmPort struct {
	Port    int    `json:"port"`
	Process string `json:"process,omitempty"`
	// Local is a host-local "127.0.0.1:NNNN" forward to this port, set when
	// the backend's guest IPs are unreachable from the host (Windows) so the
	// UI's Local links still open in a browser on this machine.
	Local string `json:"local,omitempty"`
}

// scanPorts lists TCP ports listening on non-loopback addresses inside the
// VM (SSH excluded); shared by the Services tab and the chat agent.
func (s *Server) scanPorts(ctx context.Context, info *vmm.Info) ([]vmPort, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	target := s.vmTarget(info)
	out, code, err := target.Run(ctx, `sudo -n ss -tlnp 2>/dev/null || ss -tln`, 65536)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("ss exited %d: %s", code, out)
	}
	seen := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		addr := fields[3]
		i := strings.LastIndexByte(addr, ':')
		if i < 0 {
			continue
		}
		host := addr[:i]
		port, err := strconv.Atoi(addr[i+1:])
		if err != nil || port == 22 {
			continue
		}
		if strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "[::1]") ||
			strings.Contains(host, "%lo") || strings.HasPrefix(host, "127.0.0.53%") {
			continue
		}
		proc := ""
		if m := ssProcessRE.FindStringSubmatch(line); m != nil {
			proc = m[1]
		}
		if strings.HasPrefix(proc, "systemd-") {
			continue
		}
		if cur, ok := seen[port]; !ok || cur == "" {
			seen[port] = proc
		}
	}
	services := []vmPort{}
	for port, proc := range seen {
		services = append(services, vmPort{Port: port, Process: proc})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Port < services[j].Port })
	return services, nil
}

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request) {
	info, err := s.runningVM(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	services, err := s.scanPorts(r.Context(), info)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if pf, ok := s.VMs.(vmm.PortForwarder); ok {
		for i := range services {
			if local, err := pf.ForwardGuestPort(info.Name, services[i].Port); err == nil {
				services[i].Local = local
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": info.IP, "ports": services})
}

func (s *Server) handleTranscripts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.VMs.Get(r.Context(), name); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	metas, err := transcript.List(s.transcriptDir(name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// A run marked "running" whose recorder is gone died with a previous
	// daemon process or a dropped connection.
	for i, m := range metas {
		if m.Status == "running" {
			if _, active := s.activeRuns.Load(m.ID); !active {
				metas[i].Status = "interrupted"
			}
		}
	}
	writeJSON(w, http.StatusOK, metas)
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	meta, logText, err := transcript.Load(s.transcriptDir(name), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if meta.Status == "running" {
		if _, active := s.activeRuns.Load(meta.ID); !active {
			meta.Status = "interrupted"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"meta": meta, "log": logText})
}

// notesPath is the free-form per-VM notes file edited in the detail window.
// It lives beside the transcripts so it survives VM stop/start and, like
// them, outlives the VM itself.
func (s *Server) notesPath(vm string) string {
	return filepath.Join(s.StateDir, "vms", vm, "notes.md")
}

// notesMax caps a VM's notes blob.
const notesMax = 256 << 10

func (s *Server) handleNotesGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.VMs.Get(r.Context(), name); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	b, err := os.ReadFile(s.notesPath(name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"notes": string(b)})
}

func (s *Server) handleNotesPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.VMs.Get(r.Context(), name); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	var req struct {
		Notes *string `json:"notes"`
	}
	// generous slack over notesMax: the JSON encoding of a maximal note
	// (escapes, the envelope) is bigger than the note itself
	if err := json.NewDecoder(io.LimitReader(r.Body, 2*notesMax)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Notes == nil || len(*req.Notes) > notesMax {
		writeErr(w, http.StatusBadRequest, errors.New("notes missing or too large"))
		return
	}
	path := s.notesPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(path, []byte(*req.Notes), 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Config())
}

// handleConfigPut validates, persists, and hot-swaps the configuration.
// Fields the daemon only reads at startup are reported in restart_required.
func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	old := s.Config()
	nc := *old // unknown-in-request fields keep their current values
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&nc); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	nc.Normalize()
	b, err := json.MarshalIndent(nc, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(config.Path(), append(b, '\n'), 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.cfg.Store(&nc)
	s.hubAgentKick()

	var restart []string
	for _, f := range []struct{ name, oldV, newV string }{
		{"ssh_user", old.SSHUser, nc.SSHUser},
		{"image_url", old.ImageURL, nc.ImageURL},
		{"firecracker.binary", old.Firecracker.Binary, nc.Firecracker.Binary},
		{"firecracker.kernel_url", old.Firecracker.KernelURL, nc.Firecracker.KernelURL},
		{"firecracker.network_helper", old.Firecracker.NetworkHelper, nc.Firecracker.NetworkHelper},
		{"firecracker.network_cidr", old.Firecracker.NetworkCIDR, nc.Firecracker.NetworkCIDR},
		{"firecracker.outbound_interface", old.Firecracker.OutboundInterface, nc.Firecracker.OutboundInterface},
		{"qemu.binary", old.QEMU.Binary, nc.QEMU.Binary},
		{"qemu.firmware_dir", old.QEMU.FirmwareDir, nc.QEMU.FirmwareDir},
		{"qemu.network_cidr", old.QEMU.NetworkCIDR, nc.QEMU.NetworkCIDR},
	} {
		if f.oldV != f.newV {
			restart = append(restart, f.name)
		}
	}
	var rebinding []string
	if old.Listen != nc.Listen {
		rebinding = append(rebinding, "listen")
	}
	if old.ProxyListen != nc.ProxyListen {
		rebinding = append(rebinding, "proxy_listen")
	}
	if old.SSHListen != nc.SSHListen {
		rebinding = append(rebinding, "ssh_listen")
	}
	res := map[string]any{"status": "saved"}
	if len(rebinding) > 0 {
		if s.OnRebind != nil {
			s.OnRebind(nc.Listen, nc.ProxyListen, nc.SSHListen)
			res["rebinding"] = rebinding
		} else {
			restart = append(restart, rebinding...)
		}
	}
	if restart != nil {
		res["restart_required"] = restart
	}
	// The advertised proxy origin changed (new advertise_host or proxy
	// port): repoint every exposed hostname's tunnel ingress rule so
	// published sites keep working.
	if svc := advertiseService(&nc); svc != advertiseService(old) {
		synced, warn := s.syncIngressRoutes(r.Context(), &nc, svc)
		if synced != nil {
			res["ingress_synced"] = synced
		}
		if warn != "" {
			res["ingress_warning"] = warn
		}
	}
	writeJSON(w, http.StatusOK, res)
}
