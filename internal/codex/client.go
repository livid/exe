package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"exe/internal/agent"
)

const responsesURL = "https://chatgpt.com/backend-api/codex/responses"

// modelsURL is the ChatGPT backend's model catalog; client_version mirrors
// a current Codex CLI release (the backend rejects unversioned requests).
const modelsURL = "https://chatgpt.com/backend-api/codex/models?client_version=0.147.0"

// ErrUnauthorized marks an HTTP 401 so the caller can refresh the access
// token and retry once.
var ErrUnauthorized = errors.New("chatgpt: unauthorized")

type ClientConfig struct {
	AccessToken string
	AccountID   string
	Model       string
	// Effort is the reasoning effort (minimal/low/medium/high/xhigh);
	// empty leaves the model's default in place.
	Effort string
	// SessionKey keys server-side prompt caching; the chat session id.
	SessionKey string
}

type respTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type respReasoning struct {
	Effort string `json:"effort"`
}

type respRequest struct {
	Model        string            `json:"model"`
	Instructions string            `json:"instructions"`
	Input        []json.RawMessage `json:"input"`
	Tools        []respTool        `json:"tools,omitempty"`
	Reasoning    *respReasoning    `json:"reasoning,omitempty"`
	// The ChatGPT backend rejects store:true and only serves streams.
	Store          bool     `json:"store"`
	Stream         bool     `json:"stream"`
	Include        []string `json:"include"`
	PromptCacheKey string   `json:"prompt_cache_key,omitempty"`
}

func textItem(role, kind, text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type": "message", "role": role,
		"content": []map[string]any{{"type": kind, "text": text}},
	})
	return b
}

// buildInput converts the persisted chat history into Responses input items.
// Assistant turns produced by this backend replay their raw CodexItems;
// turns from the Ollama backend (no item ids, no reasoning items to pair)
// ride along as plain text so the request is never rejected.
func buildInput(msgs []agent.Message) (instructions string, input []json.RawMessage) {
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if instructions != "" {
				instructions += "\n\n"
			}
			instructions += m.Content
		case "user":
			input = append(input, textItem("user", "input_text", m.Content))
		case "assistant":
			if len(m.CodexItems) > 0 {
				input = append(input, m.CodexItems...)
				continue
			}
			text := m.Content
			for _, tc := range m.ToolCalls {
				text += fmt.Sprintf("\n[called %s with %s]", tc.Function.Name, tc.Function.Arguments)
			}
			if strings.TrimSpace(text) != "" {
				input = append(input, textItem("assistant", "output_text", text))
			}
		case "tool":
			if m.ToolCallID != "" {
				b, _ := json.Marshal(map[string]any{
					"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content,
				})
				input = append(input, b)
			} else {
				input = append(input, textItem("user", "input_text",
					fmt.Sprintf("[%s result]\n%s", m.ToolName, m.Content)))
			}
		}
	}
	return instructions, input
}

func convertTools(tools []agent.Tool) []respTool {
	out := make([]respTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, respTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return out
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Models lists the models this ChatGPT subscription serves, in the
// backend's picker order.
func Models(ctx context.Context, accessToken, accountID string) ([]string, error) {
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("originator", originator)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chatgpt models: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var out struct {
		Models []struct {
			Slug         string `json:"slug"`
			ID           string `json:"id"`
			Visibility   string `json:"visibility"`
			ShowInPicker *bool  `json:"show_in_picker"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse chatgpt models: %w", err)
	}
	var names []string
	for _, m := range out.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" || (m.Visibility != "" && m.Visibility != "list") ||
			(m.ShowInPicker != nil && !*m.ShowInPicker) {
			continue
		}
		names = append(names, id)
	}
	return names, nil
}

// ChatStream sends one turn to the ChatGPT backend, calling onDelta with
// each streamed text fragment, and returns the assembled assistant message
// (with CodexItems populated for verbatim replay on later turns).
func ChatStream(ctx context.Context, cfg ClientConfig, msgs []agent.Message, tools []agent.Tool, onDelta func(string)) (*agent.Message, error) {
	instructions, input := buildInput(msgs)
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	var reasoning *respReasoning
	if cfg.Effort != "" {
		reasoning = &respReasoning{Effort: cfg.Effort}
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	reqID := newUUID()
	attempt := 0
	for {
		body, err := json.Marshal(respRequest{
			Model:          cfg.Model,
			Instructions:   instructions,
			Input:          input,
			Tools:          convertTools(tools),
			Reasoning:      reasoning,
			Store:          false,
			Stream:         true,
			Include:        []string{"reasoning.encrypted_content"},
			PromptCacheKey: cfg.SessionKey,
		})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, responsesURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
		req.Header.Set("chatgpt-account-id", cfg.AccountID)
		req.Header.Set("originator", originator)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("session_id", reqID)
		req.Header.Set("x-client-request-id", reqID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// transient network failure — retry with backoff inside the
			// 5-minute window unless the caller's context is what ended it
			if rctx.Err() == nil && attempt < 2 {
				log.Printf("chatgpt %s: %v; retrying", cfg.Model, err)
				attempt++
				if agent.SleepCtx(rctx, agent.RetryDelay(attempt-1, "")) {
					continue
				}
			}
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			return readStream(resp.Body, onDelta)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, ErrUnauthorized
		}
		// The configured effort doesn't fit this model: the reasoning
		// setting is best-effort, so drop it and let the model run with
		// its default rather than surfacing an error to the user.
		low := strings.ToLower(string(raw))
		if reasoning != nil && resp.StatusCode == http.StatusBadRequest &&
			(strings.Contains(low, "reasoning") || strings.Contains(low, "effort")) {
			log.Printf("chatgpt %s: rejected effort=%q (%s); retrying without it",
				cfg.Model, cfg.Effort, truncate(string(raw), 200))
			reasoning = nil
			continue
		}
		if agent.RetryStatus(resp.StatusCode) && attempt < 2 {
			log.Printf("chatgpt %s: HTTP %d; retrying", cfg.Model, resp.StatusCode)
			attempt++
			if agent.SleepCtx(rctx, agent.RetryDelay(attempt-1, resp.Header.Get("Retry-After"))) {
				continue
			}
		}
		return nil, fmt.Errorf("chatgpt %s: HTTP %d: %s", cfg.Model, resp.StatusCode, truncate(string(raw), 2000))
	}
}

func readStream(r io.Reader, onDelta func(string)) (*agent.Message, error) {
	full := agent.Message{Role: "assistant"}
	br := bufio.NewReaderSize(r, 64<<10)
	var data strings.Builder
	done := false
	for !done {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else if line == "" && data.Len() > 0 {
			d, herr := handleEvent(data.String(), &full, onDelta)
			data.Reset()
			if herr != nil {
				return nil, herr
			}
			done = d
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("stream chatgpt response: %w", err)
		}
	}
	if !done && full.Content == "" && len(full.ToolCalls) == 0 {
		return nil, errors.New("chatgpt stream ended before any output")
	}
	return &full, nil
}

// outputItem is the subset of a Responses output item this client reads;
// the raw JSON is what gets stored and replayed.
type outputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func handleEvent(data string, full *agent.Message, onDelta func(string)) (done bool, err error) {
	if data == "[DONE]" {
		return true, nil
	}
	var ev struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta"`
		Item     json.RawMessage `json:"item"`
		Code     string          `json:"code"`
		Message  string          `json:"message"`
		Error    *struct{ Code, Message string } `json:"error"`
		Response struct {
			Error *struct{ Code, Message string } `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(data), &ev) != nil {
		return false, nil // tolerate unknown frames
	}
	switch ev.Type {
	case "response.output_text.delta":
		full.Content += ev.Delta
		if onDelta != nil && ev.Delta != "" {
			onDelta(ev.Delta)
		}
	case "response.output_item.done":
		var item outputItem
		if json.Unmarshal(ev.Item, &item) != nil {
			return false, nil
		}
		full.CodexItems = append(full.CodexItems, json.RawMessage(ev.Item))
		if item.Type == "function_call" {
			tc := agent.ToolCall{ID: item.CallID}
			tc.Function.Name = item.Name
			args, _ := json.Marshal(item.Arguments)
			tc.Function.Arguments = args
			full.ToolCalls = append(full.ToolCalls, tc)
		}
	case "response.completed", "response.done", "response.incomplete":
		return true, nil
	case "response.failed":
		msg := "response failed"
		if ev.Response.Error != nil && ev.Response.Error.Message != "" {
			msg = ev.Response.Error.Message
		}
		return false, fmt.Errorf("chatgpt: %s", msg)
	case "error":
		msg := ev.Message
		if msg == "" && ev.Error != nil {
			msg = ev.Error.Message
		}
		if msg == "" {
			msg = ev.Code
		}
		if msg == "" {
			msg = truncate(data, 300)
		}
		return false, fmt.Errorf("chatgpt: %s", msg)
	}
	return false, nil
}
