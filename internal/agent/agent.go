// Package agent implements a vibecoding loop: an Ollama (cloud or local)
// model drives bash / write_file / read_file tools over SSH inside a VM.
// The chat primitives (Message, Tool, Chat) are exported so the server can
// run other tool loops — the system-wide chat window — on the same client.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"exe/internal/sshexec"
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	// Effort maps to Ollama's think option for models that support it:
	// "low"/"medium"/"high" pick a thinking level, "off" disables thinking,
	// empty leaves the model's default (and sends nothing).
	Effort string
}

type Logf func(format string, args ...any)

const (
	maxTurns      = 60
	maxToolOutput = 12000
	toolTimeout   = 5 * time.Minute
	// maxAttempts bounds one model call: the first try plus retries on
	// transient failures (connection errors, 429, 5xx).
	maxAttempts = 3
)

// retryBase is the first retry's delay, doubling per attempt; a variable so
// tests don't sleep for real.
var retryBase = time.Second

// RetryStatus reports whether an HTTP status is worth retrying.
func RetryStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// RetryDelay is the backoff before retry number attempt (0-based), stretched
// (capped at 30s) by a Retry-After header when the server sent one.
func RetryDelay(attempt int, retryAfter string) time.Duration {
	d := retryBase << attempt
	if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s >= 0 {
		if ra := time.Duration(s) * time.Second; ra > d {
			d = min(ra, 30*time.Second)
		}
	}
	return d
}

// SleepCtx sleeps for d unless ctx ends first; false means it did.
func SleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// WrapUpNote is appended (ephemerally, never persisted) to the model's view
// of the conversation when a loop nears its turn cap, so the run ends with
// a usable summary instead of an abrupt turn-limit error.
func WrapUpNote(left int) string {
	return fmt.Sprintf("NOTE: at most %d tool turn(s) remain before this run is force-stopped. Wrap up now: make only the smallest remaining change, then reply without tool calls, summarizing what works and what is left to do.", left)
}

// RequireArgs returns a tool-result error naming the string arguments that
// are missing or empty, or "" when all are present — a malformed call must
// error back to the model, not run with zero values.
func RequireArgs(args map[string]any, keys ...string) string {
	var missing []string
	for _, k := range keys {
		if v, _ := args[k].(string); strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "error: missing required argument(s): " + strings.Join(missing, ", ")
}

const systemPromptTmpl = `You are exe-agent, an autonomous coding agent operating a Debian Linux VM named %s.
You are connected over SSH as user %s, who has passwordless sudo.

Rules:
- For a multi-step task, call plan first with a short markdown checklist ("- [ ] step"), and call it again with updated checkmarks ("- [x]") as steps complete — the user watches it as a live checklist. Skip it for trivial one-step requests.
- Use the bash tool to inspect and change the system. Install packages with: sudo apt-get install -y <pkg> (run sudo apt-get update once first).
- Change existing files with edit_file; use write_file only for new files or full rewrites — read_file elides the middle of large files, so never rebuild a large file from what you read; read an exact region with read_file offset/limit or grep -n instead.
- Build the project under ~/app unless the user says otherwise.
- If the deliverable is a web app or service: bind it to 0.0.0.0, and install a systemd unit (write /etc/systemd/system/app.service via sudo tee, then sudo systemctl enable --now app) so it keeps running after you finish.
- Servers must handle concurrent connections: browsers hold idle preconnections open, which wedges single-threaded servers. With Python's stdlib use ThreadingHTTPServer, never plain HTTPServer.
- Verify your work before finishing (e.g. curl -s http://localhost:PORT).
- When everything works, reply WITHOUT any tool call: a short summary of what you built and the port the service listens on.`

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
	// ToolCallID pairs a tool-result message with the ToolCall.ID it answers.
	// Ollama matches results by position and leaves it empty; the ChatGPT
	// backend requires the pairing.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// CodexItems holds an assistant turn's raw OpenAI Responses output items
	// (reasoning with encrypted content, message, function calls). The
	// ChatGPT backend is stateless (store:false) and rejects a function call
	// replayed without its reasoning item, so later turns resend these
	// verbatim instead of reconstructing the turn from Content/ToolCalls.
	CodexItems []json.RawMessage `json:"codex_items,omitempty"`
}

type ToolCall struct {
	// ID is the provider's call id, set by the ChatGPT backend (empty from
	// Ollama, which has no per-call ids).
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type toolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function toolFunc `json:"function"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	// Think is Ollama's thinking control: a level string or false; nil
	// (omitted) keeps the model's default.
	Think  any  `json:"think,omitempty"`
	Stream bool `json:"stream"`
}

type ChatResponse struct {
	Message Message `json:"message"`
	Error   string  `json:"error,omitempty"`
}

func MkTool(name, desc string, props map[string]any, required []string) Tool {
	return Tool{Type: "function", Function: toolFunc{
		Name:        name,
		Description: desc,
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
	}}
}

// HostTool is a tool executed by the daemon itself rather than inside the
// VM — e.g. remember, which writes the VM's memory file on the host.
type HostTool struct {
	Tool Tool
	Exec func(ctx context.Context, args map[string]any) string
}

// Run executes the agent loop until the model stops calling tools.
// briefing, when non-empty, rides along as a second system message — the
// VM fact sheet the server assembles so a run doesn't start blind.
func Run(ctx context.Context, cfg Config, target sshexec.Target, vmName, prompt, briefing string, host []HostTool, logf Logf) error {
	tools := []Tool{
		MkTool("bash", "Run a shell command on the VM; returns combined output and exit code.", map[string]any{
			"command": map[string]any{"type": "string", "description": "shell command to run"},
		}, []string{"command"}),
		MkTool("write_file", "Create or overwrite a file on the VM; parent directories are created.", map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		}, []string{"path", "content"}),
		MkTool("edit_file", "Replace text in an existing file on the VM. old_string must match the current content exactly (whitespace included) and appear exactly once, unless replace_all is set. Prefer this over write_file for changing existing files.", map[string]any{
			"path":        map[string]any{"type": "string"},
			"old_string":  map[string]any{"type": "string", "description": "exact text to replace"},
			"new_string":  map[string]any{"type": "string", "description": "replacement text"},
			"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence (default false)"},
		}, []string{"path", "old_string", "new_string"}),
		MkTool("read_file", "Read a text file from the VM. Output beyond ~12KB is elided in the middle; pass offset/limit to read an exact region of a large file.", map[string]any{
			"path":   map[string]any{"type": "string"},
			"offset": map[string]any{"type": "integer", "description": "1-based line to start from (default 1)"},
			"limit":  map[string]any{"type": "integer", "description": "max lines to read (default: to the end)"},
		}, []string{"path"}),
		PlanTool(),
	}
	hostByName := map[string]HostTool{}
	for _, h := range host {
		tools = append(tools, h.Tool)
		hostByName[h.Tool.Function.Name] = h
	}
	msgs := []Message{
		{Role: "system", Content: fmt.Sprintf(systemPromptTmpl, vmName, target.User)},
	}
	if briefing != "" {
		msgs = append(msgs, Message{Role: "system", Content: briefing})
	}
	head := len(msgs) // the system messages above sit outside compaction
	msgs = append(msgs, Message{Role: "user", Content: prompt})
	var comp Compact
	// compact folds the conversation's older messages into the running
	// digest; msgs itself is untouched (the transcript logs everything),
	// only the view sent to the model shrinks.
	compact := func(keep int) bool {
		conv := msgs[head:]
		cut := CompactCut(conv[comp.Through:], keep)
		if cut <= 0 {
			return false
		}
		resp, err := Chat(ctx, cfg, []Message{
			{Role: "system", Content: DigestSystem},
			{Role: "user", Content: RenderForDigest(comp.Digest, conv[comp.Through:comp.Through+cut])},
		}, nil)
		if err != nil || strings.TrimSpace(resp.Message.Content) == "" {
			log.Printf("agent %s: compaction digest failed: %v", vmName, err)
			return false
		}
		comp = Compact{Through: comp.Through + cut,
			Digest: sshexec.Truncate(strings.TrimSpace(resp.Message.Content), DigestMax)}
		logf("[condensed %d earlier messages into a summary]\n", cut)
		return true
	}
	for turn := 0; turn < maxTurns; turn++ {
		if ApproxSize(msgs[head+comp.Through:]) > CompactAt {
			compact(CompactKeep)
		}
		view := func() []Message {
			call := append([]Message{}, msgs[:head]...)
			if comp.Through > 0 {
				call = append(call, comp.Msg())
			}
			call = append(call, msgs[head+comp.Through:]...)
			if left := maxTurns - turn; left <= 3 {
				call = append(call, Message{Role: "system", Content: WrapUpNote(left)})
			}
			return call
		}
		resp, err := Chat(ctx, cfg, view(), tools)
		// The budget is an estimate: when the backend still rejects the
		// request for size, compact harder and retry the turn once.
		if err != nil && IsContextOverflow(err) && ctx.Err() == nil && compact(CompactKeep/2) {
			resp, err = Chat(ctx, cfg, view(), tools)
		}
		if err != nil {
			return err
		}
		msgs = append(msgs, resp.Message)
		if c := strings.TrimSpace(resp.Message.Content); c != "" {
			logf("\n%s\n", c)
		}
		if len(resp.Message.ToolCalls) == 0 {
			return nil
		}
		for _, tc := range resp.Message.ToolCalls {
			var result string
			if h, ok := hostByName[tc.Function.Name]; ok {
				logf("[%s]\n", tc.Function.Name)
				result = h.Exec(ctx, ParseArgs(tc.Function.Arguments))
			} else {
				result = execTool(ctx, target, tc, logf)
			}
			msgs = append(msgs, Message{Role: "tool", ToolName: tc.Function.Name, Content: result})
		}
	}
	return fmt.Errorf("agent stopped after %d turns without finishing", maxTurns)
}

// Version reports the Ollama server's version (GET /api/version).
func Version(ctx context.Context, cfg Config) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(cfg.BaseURL, "/")+"/api/version", nil)
	if err != nil {
		return "", err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama version: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Version == "" {
		return "", fmt.Errorf("parse ollama version: %s", sshexec.Truncate(string(raw), 200))
	}
	return out.Version, nil
}

// Models lists the models the Ollama server offers (GET /api/tags).
func Models(ctx context.Context, cfg Config) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(cfg.BaseURL, "/")+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama models: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse ollama models: %s", sshexec.Truncate(string(raw), 200))
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// chatHTTP posts one /api/chat request and returns the raw response for the
// caller to decode; non-200s are drained into the error. cancel bounds the
// whole exchange (including streaming reads) to 5 minutes.
func chatHTTP(ctx context.Context, cfg Config, msgs []Message, tools []Tool, stream bool) (*http.Response, context.CancelFunc, error) {
	creq := chatRequest{Model: cfg.Model, Messages: msgs, Tools: tools, Stream: stream}
	switch cfg.Effort {
	case "":
	case "off", "false":
		creq.Think = false
	default:
		creq.Think = cfg.Effort
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	attempt := 0
	for {
		body, err := json.Marshal(creq)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(rctx, http.MethodPost,
			strings.TrimRight(cfg.BaseURL, "/")+"/api/chat", bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// transient network failure — retry with backoff inside the
			// 5-minute window unless the caller's context is what ended it
			if rctx.Err() == nil && attempt < maxAttempts-1 {
				log.Printf("ollama %s: %v; retrying", cfg.Model, err)
				attempt++
				if SleepCtx(rctx, RetryDelay(attempt-1, "")) {
					continue
				}
			}
			cancel()
			return nil, nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, cancel, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		// The configured effort doesn't fit this model or this Ollama
		// version ("does not support thinking", "invalid think value",
		// unknown field on old servers). The setting is best-effort:
		// drop it and run the request the way every model accepts.
		if creq.Think != nil && resp.StatusCode == http.StatusBadRequest &&
			strings.Contains(strings.ToLower(string(raw)), "think") {
			log.Printf("ollama %s: rejected think=%v (%s); retrying without it",
				cfg.Model, creq.Think, sshexec.Truncate(string(raw), 200))
			creq.Think = nil
			continue
		}
		if RetryStatus(resp.StatusCode) && attempt < maxAttempts-1 {
			log.Printf("ollama %s: HTTP %d; retrying", cfg.Model, resp.StatusCode)
			attempt++
			if SleepCtx(rctx, RetryDelay(attempt-1, resp.Header.Get("Retry-After"))) {
				continue
			}
		}
		cancel()
		return nil, nil, fmt.Errorf("ollama %s: HTTP %d: %s", cfg.Model, resp.StatusCode, sshexec.Truncate(string(raw), 2000))
	}
}

// Chat sends one non-streaming chat completion request to Ollama.
func Chat(ctx context.Context, cfg Config, msgs []Message, tools []Tool) (*ChatResponse, error) {
	resp, cancel, err := chatHTTP(ctx, cfg, msgs, tools, false)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse ollama response: %w: %s", err, sshexec.Truncate(string(raw), 2000))
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama: %s", out.Error)
	}
	return &out, nil
}

// ChatStream sends one streaming chat request, calling onDelta with each
// content fragment as it arrives, and returns the fully assembled message
// (content concatenated, tool calls collected across chunks).
func ChatStream(ctx context.Context, cfg Config, msgs []Message, tools []Tool, onDelta func(string)) (*Message, error) {
	resp, cancel, err := chatHTTP(ctx, cfg, msgs, tools, true)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	full := Message{Role: "assistant"}
	dec := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Message Message `json:"message"`
			Done    bool    `json:"done"`
			Error   string  `json:"error,omitempty"`
		}
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("stream ollama response: %w", err)
		}
		if chunk.Error != "" {
			return nil, fmt.Errorf("ollama: %s", chunk.Error)
		}
		if chunk.Message.Role != "" {
			full.Role = chunk.Message.Role
		}
		if chunk.Message.Content != "" {
			full.Content += chunk.Message.Content
			if onDelta != nil {
				onDelta(chunk.Message.Content)
			}
		}
		full.ToolCalls = append(full.ToolCalls, chunk.Message.ToolCalls...)
		if chunk.Done {
			break
		}
	}
	return &full, nil
}

// PlanTool is the checklist tool both loops offer: purely informational —
// the harness renders it for the user, nothing executes.
func PlanTool() Tool {
	return MkTool("plan",
		"Post or update your plan as a short markdown checklist (\"- [ ] step\", \"- [x] done\"). Call it before starting a multi-step task and again with updated checkmarks as steps complete; the user watches it as a live checklist. Always send the complete current checklist.",
		map[string]any{
			"text": map[string]any{"type": "string", "description": "the complete checklist, one \"- [ ]\"/\"- [x]\" line per step"},
		}, []string{"text"})
}

// requiredArgs names each tool's must-be-present string arguments; a call
// missing one errors back to the model instead of running on zero values
// (bash with an empty command, write_file to path "").
var requiredArgs = map[string][]string{
	"bash":       {"command"},
	"write_file": {"path"},
	"read_file":  {"path"},
	"edit_file":  {"path"},
	"plan":       {"text"},
}

func execTool(ctx context.Context, target sshexec.Target, tc ToolCall, logf Logf) string {
	args := ParseArgs(tc.Function.Arguments)
	str := func(k string) string { v, _ := args[k].(string); return v }
	if msg := RequireArgs(args, requiredArgs[tc.Function.Name]...); msg != "" {
		return msg
	}
	if _, ok := args["content"]; !ok && tc.Function.Name == "write_file" {
		return "error: missing required argument(s): content"
	}
	tctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	switch tc.Function.Name {
	case "bash":
		cmd := str("command")
		logf("$ %s\n", cmd)
		out, code, err := target.Run(tctx, cmd, maxToolOutput)
		if err != nil {
			out = fmt.Sprintf("error: %v\n%s", err, out)
		}
		if strings.TrimSpace(out) != "" {
			logf("%s\n", strings.TrimRight(out, "\n"))
		}
		if code != 0 {
			out += fmt.Sprintf("\n[exit code %d]", code)
		} else if strings.TrimSpace(out) == "" {
			out = "(no output, exit code 0)"
		}
		return out
	case "write_file":
		p := str("path")
		content := str("content")
		logf("[write %s, %d bytes]\n", p, len(content))
		if err := target.WriteFile(tctx, p, []byte(content)); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	case "edit_file":
		p := str("path")
		all, _ := args["replace_all"].(bool)
		logf("[edit %s]\n", p)
		n, err := target.EditFile(tctx, p, str("old_string"), str("new_string"), all)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return EditOK(n)
	case "read_file":
		p := str("path")
		logf("[read %s]\n", p)
		out, err := target.ReadFile(tctx, p, IntArg(args, "offset"), IntArg(args, "limit"), maxToolOutput)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return out
	case "plan":
		logf("\n[plan]\n%s\n\n", strings.TrimSpace(str("text")))
		return "ok — the user sees the checklist; continue"
	default:
		return fmt.Sprintf("unknown tool %q", tc.Function.Name)
	}
}

// EditOK phrases an edit_file success for the model; shared with the chat
// loop so both surfaces report edits identically.
func EditOK(n int) string {
	if n == 1 {
		return "ok, replaced 1 occurrence"
	}
	return fmt.Sprintf("ok, replaced %d occurrences", n)
}

// IntArg reads an integer tool argument, tolerating the number arriving
// as a JSON string. Absent or unparseable values return 0.
func IntArg(args map[string]any, k string) int {
	switch v := args[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// ParseArgs tolerates both a JSON object and a JSON-encoded string of one.
func ParseArgs(raw json.RawMessage) map[string]any {
	args := map[string]any{}
	if len(raw) == 0 {
		return args
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			json.Unmarshal([]byte(s), &args)
		}
	}
	if args == nil { // JSON null unmarshals into a nil map with no error
		args = map[string]any{}
	}
	return args
}
