package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"exe/internal/agent"
	"exe/internal/chat"
	"exe/internal/codex"
	"exe/internal/config"
	"exe/internal/sshexec"
	"exe/internal/vmm"
)

// The Chat window's agent: unlike the per-VM vibecoding agent it operates
// the whole daemon — VMs, SSH into them, routes, logs — over a persisted
// multi-turn session.

const (
	chatMaxTurns      = 40
	chatToolTimeout   = 5 * time.Minute
	chatMaxToolOutput = 12000
)

const chatSystemTmpl = `You are the operator of exe, a personal VM cloud running on this Mac. You manage Debian Linux VMs (Virtualization.framework) through tools; inside every running VM you act as user %s over SSH, with passwordless sudo.
%s

Rules:
- VM names: lowercase letters, digits, hyphens, max 31 chars.
- To inspect or change anything inside a VM, use bash (non-interactive commands only). Install packages with sudo apt-get install -y. Services you set up should bind 0.0.0.0 and run under systemd so they survive. Change existing files with edit_file; write_file overwrites whole files, and read_file elides the middle of large ones.
- delete_vm and unexpose run only after the user approves an in-app confirmation dialog; still call them only when the user asked for that. If the confirmation is declined or unanswered, do not retry — ask what the user wants instead.
- Pushing to github.com: use github_push — VMs hold no GitHub credentials, so git push via bash always fails; never configure credentials or tokens inside a VM.
- Each VM has a persistent memory for future sessions: recall reads it, remember replaces it. Save durable facts — where projects live, how services start, gotchas — when you learn them.
- Creating a VM takes ~10 seconds and it boots with an IP; the very first creation ever downloads a 3 GB image.
- Format answers in Markdown (lists, tables, code blocks and links render nicely), and keep them concise. When you finish a task, summarize what changed and give the address where it can be reached.`

// The system prompt of a session pinned to one VM: same operator, but the
// fleet is out of scope and the tools already target the pinned VM.
const chatPinnedTmpl = `You are the operator of %q, one Debian Linux VM in exe, a personal VM cloud running on this Mac. All your tools act on this VM only — other VMs are out of scope for this conversation. Inside the VM you act as user %s over SSH, with passwordless sudo.
%s

Rules:
- To inspect or change anything inside the VM, use bash (non-interactive commands only). Install packages with sudo apt-get install -y. Services you set up should bind 0.0.0.0 and run under systemd so they survive. Change existing files with edit_file; write_file overwrites whole files, and read_file elides the middle of large ones.
- unexpose runs only after the user approves an in-app confirmation dialog; still call it only when the user asked for that. If the confirmation is declined or unanswered, do not retry — ask what the user wants instead.
- Pushing to github.com: use github_push — the VM holds no GitHub credentials, so git push via bash always fails; never configure credentials or tokens inside the VM.
- The VM's saved memory and current state are in your context; when you learn something durable, save the complete updated memory with remember.
- Format answers in Markdown (lists, tables, code blocks and links render nicely), and keep them concise. When you finish a task, summarize what changed and give the address where it can be reached.`

func (s *Server) chatDir() string { return filepath.Join(s.StateDir, "chat") }

func chatSystemPrompt(sshUser, domain, vm string) string {
	pub := "Publishing: cloudflare.domain is not configured, so expose only adds a local proxy route — suggest the Cloudflare wizard if the user wants public URLs."
	if domain != "" {
		pub = fmt.Sprintf("Publishing: expose makes a VM port reachable at https://<subdomain>.%s.", domain)
	}
	if vm != "" {
		return fmt.Sprintf(chatPinnedTmpl, vm, sshUser, pub)
	}
	return fmt.Sprintf(chatSystemTmpl, sshUser, pub)
}

// ---- backend detection ----

// chatProvider is the Chat window's backend: "openai" (ChatGPT
// subscription) when configured, otherwise "ollama".
func chatProvider(cfg *config.Config) string {
	if cfg.ChatProvider == "openai" {
		return "openai"
	}
	return "ollama"
}

// handleChatStatus reports whether the chat backend is usable, cached for a
// minute so the UI can ask freely; ?force=1 bypasses the cache. The ChatGPT
// path is a local credentials check — no probe, no cache needed.
func (s *Server) handleChatStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	if chatProvider(cfg) == "openai" {
		res := map[string]any{"provider": "openai", "detected": false,
			"model": cfg.OpenAI.Model, "effort": cfg.OpenAI.Effort}
		if c := s.codexCreds(); c != nil {
			res["detected"] = true
			res["plan"] = c.Plan
			res["email"] = c.Email
		} else {
			res["reason"] = "not signed in to ChatGPT (Configuration → OpenAI)"
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	key := cfg.Ollama.BaseURL + "|" + cfg.Ollama.APIKey + "|" + cfg.Ollama.Model + "|" + cfg.Ollama.Effort
	s.chatStatMu.Lock()
	if s.chatStatRes != nil && s.chatStatKey == key &&
		time.Since(s.chatStatAt) < time.Minute && r.URL.Query().Get("force") != "1" {
		res := s.chatStatRes
		s.chatStatMu.Unlock()
		writeJSON(w, http.StatusOK, res)
		return
	}
	s.chatStatMu.Unlock()

	res := map[string]any{"provider": "ollama", "detected": false,
		"model": cfg.Ollama.Model, "effort": cfg.Ollama.Effort, "base_url": cfg.Ollama.BaseURL}
	switch {
	case cfg.Ollama.BaseURL == "":
		res["reason"] = "ollama.base_url is not configured"
	case cfg.Ollama.APIKey == "" && strings.Contains(cfg.Ollama.BaseURL, "ollama.com"):
		res["reason"] = "ollama.api_key is not configured (or set OLLAMA_API_KEY)"
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		v, err := agent.Version(ctx, agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey})
		if err != nil {
			res["reason"] = err.Error()
		} else {
			res["detected"] = true
			res["version"] = v
		}
	}
	s.chatStatMu.Lock()
	s.chatStatAt, s.chatStatKey, s.chatStatRes = time.Now(), key, res
	s.chatStatMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// handleChatModels lists the models a backend offers, for the
// Configuration window's model dropdowns. ?provider=ollama|openai picks
// which backend to ask (default: the active chat provider).
func (s *Server) handleChatModels(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = chatProvider(cfg)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var models []string
	var err error
	switch provider {
	case "openai":
		var creds *codex.Creds
		if creds, err = s.codexToken(ctx, false); err == nil {
			models, err = codex.Models(ctx, creds.AccessToken, creds.AccountID)
		}
	case "ollama":
		if cfg.Ollama.BaseURL == "" {
			err = errors.New("ollama.base_url is not configured")
		} else {
			models, err = agent.Models(ctx, agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey})
		}
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown provider %q", provider))
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "models": models})
}

// ---- session CRUD ----

func (s *Server) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	list, err := chat.List(s.chatDir())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// metas grow a streaming flag so the session list can mark live replies
	type meta struct {
		chat.Meta
		Streaming bool `json:"streaming,omitempty"`
	}
	out := make([]meta, len(list))
	for i, m := range list {
		_, live := s.chatRuns.Load(m.ID)
		out[i] = meta{m, live}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleChatSession(w http.ResponseWriter, r *http.Request) {
	run, live := s.chatRuns.Load(r.PathValue("id"))
	sess, err := chat.Load(s.chatDir(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if !live {
		writeJSON(w, http.StatusOK, sess)
		return
	}
	// While a reply streams, hand back the history as of the run's start
	// plus streaming:true — the client renders that and replays the run's
	// events from /stream, so mid-run saves never render twice.
	cr := run.(*chatRun)
	if len(sess.Messages) >= cr.base {
		sess.Messages = sess.Messages[:cr.base]
	} else { // the run registered before its user-message save hit the disk
		sess.Messages = append(sess.Messages, agent.Message{Role: "user", Content: cr.userMsg})
	}
	writeJSON(w, http.StatusOK, struct {
		*chat.Session
		Streaming bool `json:"streaming"`
	}{sess, true})
}

func (s *Server) handleChatSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, live := s.chatRuns.Load(id); live {
		writeErr(w, http.StatusConflict, errors.New("a reply is streaming in this session — stop it first"))
		return
	}
	if err := chat.Delete(s.chatDir(), id); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	// A send may have slipped its run registration in between the live
	// check and the file removal; cancel it and sweep the file again once
	// it unregisters, so its saves can't resurrect the deleted session.
	if v, ok := s.chatRuns.Load(id); ok {
		v.(*chatRun).stop("stopped: the session was deleted")
		go func() {
			for range 100 {
				if _, ok := s.chatRuns.Load(id); !ok {
					chat.Delete(s.chatDir(), id)
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---- send: the tool loop ----

// handleChatSend appends a user message to a session (creating one when the
// id is empty), starts the agent loop as a detached run (chatrun.go) and
// streams it back as NDJSON events: session, delta (one per streamed
// content fragment), tool_call, tool_result, error, done. The run belongs
// to the daemon — a dropped connection only detaches this stream, and
// GET /v1/chat/sessions/{id}/stream re-attaches to it.
func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	var req struct {
		Session string `json:"session"`
		Message string `json:"message"`
		// VM pins a NEW session to one VM; ignored when Session is set —
		// the pin is a property of the session, not of the request.
		VM string `json:"vm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	provider := chatProvider(cfg)
	if provider == "openai" {
		if s.codexCreds() == nil {
			writeErr(w, http.StatusConflict, errors.New("not signed in to ChatGPT (Configuration → OpenAI)"))
			return
		}
	} else {
		if cfg.Ollama.BaseURL == "" {
			writeErr(w, http.StatusConflict, errors.New("ollama.base_url is not configured"))
			return
		}
		if cfg.Ollama.APIKey == "" && strings.Contains(cfg.Ollama.BaseURL, "ollama.com") {
			writeErr(w, http.StatusConflict, errors.New("ollama.api_key is not configured (or set OLLAMA_API_KEY)"))
			return
		}
	}

	var sess *chat.Session
	var err error
	if req.Session == "" {
		if req.VM != "" {
			if _, err := s.VMs.Get(r.Context(), req.VM); err != nil {
				writeErr(w, http.StatusNotFound, err)
				return
			}
		}
		sess, err = chat.New(s.chatDir(), req.Message, req.VM)
	} else {
		sess, err = chat.Load(s.chatDir(), req.Session)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	run, err := s.startChatRun(cfg, provider, sess, req.Message)
	if err != nil {
		code := http.StatusNotFound // the session vanished under us
		if errors.Is(err, errChatBusy) {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	streamChatRun(w, r.Context(), run)
}

// ---- tools ----

func chatTools(pinned bool) []agent.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	vm := str("VM name")
	tools := []agent.Tool{
		agent.MkTool("list_vms", "List all VMs with state, IP and specs.", map[string]any{}, nil),
		agent.MkTool("create_vm", "Create and boot a new VM; unset specs use the configured defaults.", map[string]any{
			"name": vm, "cpus": num("CPU count"), "memory_mb": num("memory in MB"), "disk_gb": num("disk in GB"),
		}, []string{"name"}),
		agent.MkTool("start_vm", "Start a stopped VM.", map[string]any{"name": vm}, []string{"name"}),
		agent.MkTool("stop_vm", "Stop a running VM.", map[string]any{"name": vm}, []string{"name"}),
		agent.MkTool("delete_vm", "Permanently delete a VM and its disk. Only when the user explicitly asked.", map[string]any{"name": vm}, []string{"name"}),
		agent.MkTool("bash", "Run a shell command inside a running VM; returns combined output and exit code.", map[string]any{
			"vm": vm, "command": str("shell command to run"),
		}, []string{"vm", "command"}),
		agent.MkTool("write_file", "Create or overwrite a file inside a running VM; parent directories are created.", map[string]any{
			"vm": vm, "path": str("absolute path"), "content": str("file content"),
		}, []string{"vm", "path", "content"}),
		agent.MkTool("edit_file", "Replace text in an existing file inside a running VM. old_string must match the current content exactly (whitespace included) and appear exactly once, unless replace_all is set. Prefer this over write_file for changing existing files.", map[string]any{
			"vm": vm, "path": str("absolute path"),
			"old_string":  str("exact text to replace"),
			"new_string":  str("replacement text"),
			"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence (default false)"},
		}, []string{"vm", "path", "old_string", "new_string"}),
		agent.MkTool("read_file", "Read a text file from a running VM.", map[string]any{
			"vm": vm, "path": str("absolute path"),
		}, []string{"vm", "path"}),
		agent.MkTool("list_ports", "List TCP services listening inside a running VM (SSH excluded).", map[string]any{"vm": vm}, []string{"vm"}),
		agent.MkTool("list_routes", "List published routes: hostname -> VM backend.", map[string]any{}, nil),
		agent.MkTool("expose", "Publish a VM port on the internet as https://<subdomain>.<configured domain>.", map[string]any{
			"vm": vm, "port": num("VM port to publish"), "subdomain": str("subdomain (defaults to the VM name)"),
		}, []string{"vm", "port"}),
		agent.MkTool("unexpose", "Remove a published hostname (DNS, tunnel ingress, proxy route). Only when the user explicitly asked.", map[string]any{
			"host": str("full hostname as shown by list_routes"),
		}, []string{"host"}),
		agent.MkTool("github_push", "Push a project folder in a VM to the user's GitHub. The daemon holds the credentials and pushes on the VM's behalf — a plain `git push` inside the VM always fails, so use this tool for any push to github.com. It commits uncommitted work, creates the repository if needed, and pushes the current branch.", map[string]any{
			"vm":      vm,
			"path":    str("absolute project folder in the VM, e.g. /home/dev/app"),
			"repo":    str("repository name — omit to reuse the folder's origin remote (or the folder name on a first publish)"),
			"private": map[string]any{"type": "boolean", "description": "create the repository as private (default true; ignored when it already exists)"},
		}, []string{"vm", "path"}),
		agent.MkTool("daemon_logs", "Read the tail of the exe daemon's own log — useful to diagnose expose/DNS/VM issues.", map[string]any{
			"lines": num("how many lines (default 100, max 400)"),
		}, nil),
		rememberTool(true),
		agent.MkTool("recall", "Read a VM's saved memory — the durable notes earlier sessions kept about it. Check it before working on a VM you haven't touched in this conversation.", map[string]any{
			"vm": vm,
		}, []string{"vm"}),
	}
	if !pinned {
		return tools
	}
	// A pinned session can't even express touching another VM: the fleet
	// tools disappear and the VM-name parameter is stripped from every
	// schema — pinChatArgs injects the pinned name at call time instead.
	out := tools[:0]
	for _, t := range tools {
		switch t.Function.Name {
		case "list_vms", "create_vm", "delete_vm":
			continue
		}
		props := t.Function.Parameters["properties"].(map[string]any)
		delete(props, "vm")
		delete(props, "name")
		if req, ok := t.Function.Parameters["required"].([]string); ok {
			keep := req[:0]
			for _, k := range req {
				if k != "vm" && k != "name" {
					keep = append(keep, k)
				}
			}
			t.Function.Parameters["required"] = keep
		}
		out = append(out, t)
	}
	return out
}

// pinChatArgs overwrites a tool call's VM-target argument with the session's
// pinned VM. The pinned schemas no longer carry the parameter, and a
// hallucinated value must not win either — overwriting beats validating.
func pinChatArgs(name string, args map[string]any, vm string) {
	switch name {
	case "bash", "write_file", "edit_file", "read_file", "list_ports", "expose", "github_push", "remember", "recall":
		args["vm"] = vm
	case "start_vm", "stop_vm":
		args["name"] = vm
	}
}

// routeBelongsTo verifies that host's proxy route points at the named VM's
// current IP, so a pinned chat can only unexpose its own routes.
func (s *Server) routeBelongsTo(ctx context.Context, vmName, host string) error {
	backend, ok := s.Proxy.Snapshot()[strings.ToLower(host)]
	if !ok {
		return fmt.Errorf("no route for %q", host)
	}
	info, err := s.runningVM(ctx, vmName)
	if err != nil {
		return err
	}
	u, err := url.Parse(backend)
	if err != nil || u.Hostname() == "" || u.Hostname() != info.IP {
		return fmt.Errorf("%s does not route to VM %s", host, vmName)
	}
	return nil
}

// rememberTool is the schema of the memory-writing tool. Fleet chat
// addresses a VM by name; pinned sessions and vibecode runs have the
// target injected, so their schema carries no vm parameter.
func rememberTool(withVM bool) agent.Tool {
	props := map[string]any{
		"content": map[string]any{"type": "string",
			"description": "the complete new memory (Markdown) — it replaces what was saved before; empty forgets everything"},
	}
	required := []string{"content"}
	if withVM {
		props["vm"] = map[string]any{"type": "string", "description": "VM name"}
		required = []string{"vm", "content"}
	}
	return agent.MkTool("remember",
		"Save durable notes about the VM for future sessions: where projects live, how services start, decisions made, gotchas hit. Replaces the VM's saved memory wholesale — write the complete memory, keeping what still matters.",
		props, required)
}

// confirmPrompt returns the alert copy for tools that only run after an
// in-app confirmation — the enforcement behind the "only when the user
// asked" prompt rules. Pinned sessions skip delete_vm here: execChatTool
// refuses it outright, so there is nothing to confirm.
func confirmPrompt(name string, args map[string]any, pin string) (message, detail, action string, gated bool) {
	str := func(k string) string { v, _ := args[k].(string); return v }
	switch name {
	case "delete_vm":
		if pin != "" {
			return
		}
		return fmt.Sprintf("Delete the VM “%s”?", str("name")),
			"The assistant wants to delete this VM. Its disk and everything on it are removed permanently.",
			"Delete", true
	case "unexpose":
		return fmt.Sprintf("Remove the route “%s”?", str("host")),
			"The assistant wants to unpublish this hostname. Its public URL stops working immediately.",
			"Remove", true
	}
	return
}

// chatToolSummary is the one-liner the UI shows for a tool call.
func chatToolSummary(name string, args map[string]any) string {
	str := func(k string) string { v, _ := args[k].(string); return v }
	switch name {
	case "bash":
		return str("vm") + " $ " + str("command")
	case "write_file":
		return fmt.Sprintf("write %s:%s (%d bytes)", str("vm"), str("path"), len(str("content")))
	case "edit_file":
		return fmt.Sprintf("edit %s:%s", str("vm"), str("path"))
	case "read_file":
		return fmt.Sprintf("read %s:%s", str("vm"), str("path"))
	case "expose":
		p, _ := args["port"].(float64)
		return fmt.Sprintf("expose %s:%d as %q", str("vm"), int(p), str("subdomain"))
	case "github_push":
		return fmt.Sprintf("push %s:%s to github", str("vm"), str("path"))
	case "remember":
		return fmt.Sprintf("remember %s (%d bytes)", str("vm"), len(str("content")))
	case "recall":
		return "recall " + str("vm")
	default:
		b, _ := json.Marshal(args)
		if len(args) == 0 {
			return name
		}
		return name + " " + string(b)
	}
}

// chatRequiredArgs names each chat tool's must-be-present string arguments.
var chatRequiredArgs = map[string][]string{
	"create_vm":   {"name"},
	"start_vm":    {"name"},
	"stop_vm":     {"name"},
	"delete_vm":   {"name"},
	"bash":        {"vm", "command"},
	"write_file":  {"vm", "path"},
	"edit_file":   {"vm", "path"},
	"read_file":   {"vm", "path"},
	"list_ports":  {"vm"},
	"expose":      {"vm"},
	"unexpose":    {"host"},
	"github_push": {"vm", "path"},
	"remember":    {"vm"},
	"recall":      {"vm"},
}

func (s *Server) execChatTool(ctx context.Context, name string, args map[string]any, pin string) string {
	cfg := s.Config()
	str := func(k string) string { v, _ := args[k].(string); return v }
	num := func(k string) int { v, _ := args[k].(float64); return int(v) }
	asJSON := func(v any, err error) string {
		if err != nil {
			return "error: " + err.Error()
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	target := func(ctx context.Context) (sshexec.Target, error) {
		info, err := s.runningVM(ctx, str("vm"))
		if err != nil {
			return sshexec.Target{}, err
		}
		return s.vmTarget(info), nil
	}
	// A call missing a required argument errors back to the model instead of
	// running on zero values (bash with an empty command, VM name "").
	// pinChatArgs ran already, so pinned sessions always carry vm here.
	if msg := agent.RequireArgs(args, chatRequiredArgs[name]...); msg != "" {
		return msg
	}
	if _, ok := args["content"]; !ok && (name == "write_file" || name == "remember") {
		return "error: missing required argument(s): content"
	}
	tctx, cancel := context.WithTimeout(ctx, chatToolTimeout)
	defer cancel()

	// Belt and braces for pinned sessions: the fleet tools aren't offered,
	// but a model may still emit one from prior context; and unexpose takes
	// a hostname, which schema shaping can't scope to the pin.
	if pin != "" {
		switch name {
		case "list_vms", "create_vm", "delete_vm":
			return fmt.Sprintf("error: this chat is pinned to VM %s; %s is not available here", pin, name)
		case "unexpose":
			if err := s.routeBelongsTo(tctx, pin, str("host")); err != nil {
				return "error: " + err.Error()
			}
		}
	}

	switch name {
	case "list_vms":
		return asJSON(s.VMs.List(tctx))
	case "create_vm":
		spec := vmm.Spec{Name: str("name"), CPUs: num("cpus"), MemoryMB: num("memory_mb"), DiskGB: num("disk_gb")}
		s.fillSpec(&spec)
		info, err := s.VMs.Create(tctx, spec)
		if err == nil {
			s.PostNews("vm", "VM created", vmNewsLine(spec))
		}
		return asJSON(info, err)
	case "start_vm":
		return asJSON(s.VMs.Start(tctx, str("name")))
	case "stop_vm":
		if err := s.VMs.Stop(tctx, str("name")); err != nil {
			return "error: " + err.Error()
		}
		return "stopped"
	case "delete_vm":
		if err := s.VMs.Delete(tctx, str("name")); err != nil {
			return "error: " + err.Error()
		}
		s.PostNews("vm", "VM deleted", str("name")+" and its disk were removed.")
		return "deleted"
	case "bash":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		out, code, err := t.Run(tctx, str("command"), chatMaxToolOutput)
		if err != nil {
			out = fmt.Sprintf("error: %v\n%s", err, out)
		}
		if code != 0 {
			out += fmt.Sprintf("\n[exit code %d]", code)
		} else if strings.TrimSpace(out) == "" {
			out = "(no output, exit code 0)"
		}
		return out
	case "write_file":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		if err := t.WriteFile(tctx, str("path"), []byte(str("content"))); err != nil {
			return "error: " + err.Error()
		}
		return "ok"
	case "edit_file":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		all, _ := args["replace_all"].(bool)
		n, err := t.EditFile(tctx, str("path"), str("old_string"), str("new_string"), all)
		if err != nil {
			return "error: " + err.Error()
		}
		return agent.EditOK(n)
	case "read_file":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		out, err := t.ReadFile(tctx, str("path"), chatMaxToolOutput)
		if err != nil {
			return "error: " + err.Error()
		}
		return out
	case "remember":
		if _, err := s.VMs.Get(tctx, str("vm")); err != nil {
			return "error: " + err.Error()
		}
		if err := s.writeVMMemory(str("vm"), str("content")); err != nil {
			return "error: " + err.Error()
		}
		return "ok, memory saved"
	case "recall":
		if _, err := s.VMs.Get(tctx, str("vm")); err != nil {
			return "error: " + err.Error()
		}
		mem := s.readVMMemory(str("vm"))
		if strings.TrimSpace(mem) == "" {
			return "(no memory saved for this VM)"
		}
		return mem
	case "list_ports":
		info, err := s.runningVM(tctx, str("vm"))
		if err != nil {
			return "error: " + err.Error()
		}
		ports, err := s.scanPorts(tctx, info)
		if err != nil {
			return "error: " + err.Error()
		}
		return asJSON(map[string]any{"ip": info.IP, "ports": ports}, nil)
	case "list_routes":
		return asJSON(s.Proxy.Snapshot(), nil)
	case "expose":
		port := num("port")
		if port <= 0 || port > 65535 {
			return "error: port is required (1-65535)"
		}
		if cfg.Cloudflare.Domain == "" {
			return "error: cloudflare.domain is not configured — ask the user to run the Cloudflare wizard"
		}
		info, err := s.runningVM(tctx, str("vm"))
		if err != nil {
			return "error: " + err.Error()
		}
		return asJSON(s.exposeVM(tctx, info, str("vm"), str("subdomain"), port))
	case "unexpose":
		return asJSON(s.removeRoute(tctx, str("host")))
	case "github_push":
		creds, err := s.ghRequireAuth(tctx)
		if err != nil {
			return "error: " + err.Error() + " — ask the user to sign in there, then retry"
		}
		info, err := s.runningVM(tctx, str("vm"))
		if err != nil {
			return "error: " + err.Error()
		}
		private := true
		if v, ok := args["private"].(bool); ok {
			private = v
		}
		var steps strings.Builder
		step := func(format string, a ...any) { fmt.Fprintf(&steps, format+"\n", a...) }
		// Its own deadline: a first publish may apt-get install git, and a
		// large push can outgrow the budget for quick commands.
		pctx, pcancel := context.WithTimeout(ctx, publishTimeout)
		defer pcancel()
		repo, err := s.publishVM(pctx, s.vmTarget(info), creds, str("path"), str("repo"), private, "", step)
		if err != nil {
			return steps.String() + "error: " + err.Error()
		}
		s.PostNews("vm", "Published to GitHub", str("vm")+": "+str("path")+" → "+repo.HTMLURL)
		return steps.String() + "pushed: " + repo.HTMLURL
	case "daemon_logs":
		if s.Logs == nil {
			return "error: daemon log not available"
		}
		n := num("lines")
		if n <= 0 {
			n = 100
		}
		if n > 400 {
			n = 400
		}
		backlog, _, cancelSub := s.Logs.Subscribe()
		cancelSub()
		if len(backlog) > n {
			backlog = backlog[len(backlog)-n:]
		}
		return strings.Join(backlog, "\n")
	default:
		return fmt.Sprintf("unknown tool %q", name)
	}
}
