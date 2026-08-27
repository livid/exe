package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"exe/internal/chat"
	"exe/internal/sshexec"
)

// Per-VM agent memory: a small model-maintained file beside the user's
// notes.md. The remember tool replaces it wholesale — the current content
// is always in the model's context (the briefing, or a recall call), so a
// rewrite is a conscious merge and stale facts get evicted instead of
// accreting.

// memoryMax caps a VM's agent memory; the briefing carries it in full.
const memoryMax = 8 << 10

func (s *Server) memoryPath(vm string) string {
	return filepath.Join(s.StateDir, "vms", vm, "memory.md")
}

func (s *Server) readVMMemory(vm string) string {
	b, _ := os.ReadFile(s.memoryPath(vm))
	return string(b)
}

// writeVMMemory replaces the VM's memory; empty content removes the file.
func (s *Server) writeVMMemory(vm, content string) error {
	if len(content) > memoryMax {
		return fmt.Errorf("memory is %d bytes — keep it under %d: drop what no longer matters", len(content), memoryMax)
	}
	p := s.memoryPath(vm)
	if strings.TrimSpace(content) == "" {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// vmBriefing assembles what a run pinned to one VM starts out knowing:
// live state (specs, ports, routes), the user's notes, the agent's own
// memory, and recent session summaries — so a new run doesn't rediscover
// the VM from scratch. selfID excludes the session being briefed from the
// recent-sessions list (its own history is already in context). Sections
// with nothing to say are omitted; on a stopped VM the live parts are.
func (s *Server) vmBriefing(ctx context.Context, vm, selfID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "What you know about the VM %q (gathered automatically; live facts are current as of the start of this run):\n", vm)

	if info, err := s.VMs.Get(ctx, vm); err == nil {
		fmt.Fprintf(&b, "\nState: %s, %d CPUs, %d MB memory, %d GB disk", info.State, info.CPUs, info.MemoryMB, info.DiskGB)
		if info.IP != "" {
			fmt.Fprintf(&b, ", IP %s", info.IP)
		}
		b.WriteString("\n")
		if info.State == "running" {
			if ports, err := s.scanPorts(ctx, info); err == nil && len(ports) > 0 {
				b.WriteString("Listening ports:")
				for _, p := range ports {
					fmt.Fprintf(&b, " %d", p.Port)
					if p.Process != "" {
						fmt.Fprintf(&b, " (%s)", p.Process)
					}
				}
				b.WriteString("\n")
			}
			var routes []string
			for host, backend := range s.Proxy.Snapshot() {
				if u, err := url.Parse(backend); err == nil && u.Hostname() == info.IP {
					routes = append(routes, fmt.Sprintf("%s -> :%s", host, u.Port()))
				}
			}
			if len(routes) > 0 {
				fmt.Fprintf(&b, "Published routes: %s\n", strings.Join(routes, ", "))
			}
		}
	}

	if notes, err := os.ReadFile(s.notesPath(vm)); err == nil && strings.TrimSpace(string(notes)) != "" {
		fmt.Fprintf(&b, "\nThe user's notes on this VM (from its Notes tab — written by the user, read-only to you):\n%s\n",
			strings.TrimSpace(sshexec.Truncate(string(notes), 4000)))
	}

	if mem := s.readVMMemory(vm); strings.TrimSpace(mem) != "" {
		fmt.Fprintf(&b, "\nYour memory of this VM (you wrote this in earlier sessions; the remember tool replaces it):\n%s\n",
			strings.TrimSpace(mem))
	}

	if metas, err := chat.List(s.chatDir()); err == nil {
		var lines []string
		for _, m := range metas { // newest first
			if m.VM != vm || m.ID == selfID || m.Summary == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", m.UpdatedAt.Format("2006-01-02"), m.Summary))
			if len(lines) == 5 {
				break
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\nRecent sessions on this VM:\n%s\n", strings.Join(lines, "\n"))
		}
	}

	b.WriteString("\nWhen you learn something durable about this VM — where a project lives, how its service starts, decisions made, gotchas hit — save it with the remember tool. Write the complete memory: it replaces the text above, so keep what still matters and drop what doesn't.")
	return b.String()
}

// ---- HTTP: the detail window shows (and can clear) the agent's memory ----

func (s *Server) handleMemoryGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.VMs.Get(r.Context(), name); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"memory": s.readVMMemory(name)})
}

func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.VMs.Get(r.Context(), name); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	if err := s.writeVMMemory(name, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "forgotten"})
}
