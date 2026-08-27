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
)

const systemPromptTmpl = `You are exe-agent, an autonomous coding agent operating a Debian Linux VM named %s.
You are connected over SSH as user %s, who has passwordless sudo.

Rules:
- Use the bash tool to inspect and change the system. Install packages with: sudo apt-get install -y <pkg> (run sudo apt-get update once first).
- Change existing files with edit_file; use write_file only for new files or full rewrites — read_file elides the middle of large files, so never rebuild a large file from what you read.
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

// Run executes the agent loop until the model stops calling tools.
func Run(ctx context.Context, cfg Config, target sshexec.Target, vmName, prompt string, logf Logf) error {
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
		MkTool("read_file", "Read a text file from the VM.", map[string]any{
			"path": map[string]any{"type": "string"},
		}, []string{"path"}),
	}
	msgs := []Message{
		{Role: "system", Content: fmt.Sprintf(systemPromptTmpl, vmName, target.User)},
		{Role: "user", Content: prompt},
	}
	for turn := 0; turn < maxTurns; turn++ {
		resp, err := Chat(ctx, cfg, msgs, tools)
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
			result := execTool(ctx, target, tc, logf)
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

func execTool(ctx context.Context, target sshexec.Target, tc ToolCall, logf Logf) string {
	args := ParseArgs(tc.Function.Arguments)
	str := func(k string) string { v, _ := args[k].(string); return v }
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
		out, err := target.ReadFile(tctx, p, maxToolOutput)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return out
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
