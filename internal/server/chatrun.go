package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"exe/internal/agent"
	"exe/internal/chat"
	"exe/internal/codex"
	"exe/internal/config"
	"exe/internal/sshexec"
)

// Detached chat runs: POST /v1/chat/send starts the agent loop in a
// background goroutine owned by the daemon, not by the HTTP request, so a
// closed tab or laptop lid doesn't kill a long task. Every event the loop
// emits is buffered in the run; the send response and any later
// GET /v1/chat/sessions/{id}/stream attachments replay that buffer and then
// follow live until done. Both model backends and every tool bound each
// step to ~5 minutes, so a run always terminates on its own.

// chatRun is one in-flight reply for a session, registered in s.chatRuns
// from send until the loop finishes.
type chatRun struct {
	// base is the session's message count at run start, the triggering user
	// message included: an attaching client renders the session truncated to
	// base and replays events from the top, so mid-run saves never render
	// twice. userMsg lets the session GET serve that message even in the
	// instant before its save lands on disk.
	base    int
	userMsg string
	cancel  context.CancelFunc

	mu     sync.Mutex
	cond   *sync.Cond
	events [][]byte // marshaled NDJSON events, in emit order
	done   bool
}

func (r *chatRun) emit(ev map[string]any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, b)
	r.mu.Unlock()
	r.cond.Broadcast()
}

// startChatRun registers a run for the session and launches the agent loop
// in the background. The user message is appended and saved before this
// returns, so the session on disk carries it as soon as the run is visible.
// Fails when the session already has a reply streaming.
func (s *Server) startChatRun(cfg *config.Config, provider string, sess *chat.Session, message string) (*chatRun, error) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &chatRun{base: len(sess.Messages) + 1, userMsg: message, cancel: cancel}
	run.cond = sync.NewCond(&run.mu)
	if _, loaded := s.chatRuns.LoadOrStore(sess.ID, run); loaded {
		cancel()
		return nil, errors.New("a reply is already streaming in this session")
	}
	run.emit(map[string]any{"type": "session", "meta": sess.Meta})
	sess.Messages = append(sess.Messages, agent.Message{Role: "user", Content: message})
	if err := chat.Save(s.chatDir(), sess); err != nil {
		run.emit(map[string]any{"type": "error", "error": "save session: " + err.Error()})
	}
	go func() {
		defer cancel()
		s.runChatLoop(ctx, cfg, provider, sess, run)
		run.emit(map[string]any{"type": "done", "meta": sess.Meta})
		s.chatRuns.Delete(sess.ID)
		run.mu.Lock()
		run.done = true
		run.mu.Unlock()
		run.cond.Broadcast()
	}()
	return run, nil
}

// runChatLoop is the agent loop: call the model, run the tool calls it
// asks for, feed the results back, until a turn ends with plain text.
func (s *Server) runChatLoop(ctx context.Context, cfg *config.Config, provider string, sess *chat.Session, run *chatRun) {
	dir := s.chatDir()
	save := func() {
		if err := chat.Save(dir, sess); err != nil {
			run.emit(map[string]any{"type": "error", "error": "save session: " + err.Error()})
		}
	}
	acfg := agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey,
		Model: cfg.Ollama.Model, Effort: cfg.Ollama.Effort}
	system := agent.Message{Role: "system", Content: chatSystemPrompt(cfg.SSHUser, cfg.Cloudflare.Domain, sess.VM)}
	tools := chatTools(sess.VM != "")
	// callModel runs one turn on the configured backend. The ChatGPT path
	// resolves (and auto-refreshes) the token per turn and retries once
	// after a 401 in case the token was revoked out from under us.
	callModel := func(ctx context.Context, msgs []agent.Message, onDelta func(string)) (*agent.Message, error) {
		if provider != "openai" {
			return agent.ChatStream(ctx, acfg, msgs, tools, onDelta)
		}
		creds, err := s.codexToken(ctx, false)
		if err != nil {
			return nil, err
		}
		ccfg := codex.ClientConfig{AccessToken: creds.AccessToken, AccountID: creds.AccountID,
			Model: cfg.OpenAI.Model, Effort: cfg.OpenAI.Effort, SessionKey: sess.ID}
		msg, err := codex.ChatStream(ctx, ccfg, msgs, tools, onDelta)
		if errors.Is(err, codex.ErrUnauthorized) {
			if creds, err = s.codexToken(ctx, true); err != nil {
				return nil, err
			}
			ccfg.AccessToken, ccfg.AccountID = creds.AccessToken, creds.AccountID
			msg, err = codex.ChatStream(ctx, ccfg, msgs, tools, onDelta)
		}
		return msg, err
	}
	for turn := 0; turn < chatMaxTurns; turn++ {
		msg, err := callModel(ctx, append([]agent.Message{system}, sess.Messages...),
			func(d string) { run.emit(map[string]any{"type": "delta", "text": d}) })
		if err != nil {
			if ctx.Err() != nil { // canceled via the stop endpoint
				run.emit(map[string]any{"type": "error", "error": "stopped"})
			} else {
				run.emit(map[string]any{"type": "error", "error": err.Error()})
			}
			return
		}
		sess.Messages = append(sess.Messages, *msg)
		if len(msg.ToolCalls) == 0 {
			save()
			return
		}
		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			args := agent.ParseArgs(tc.Function.Arguments)
			if sess.VM != "" {
				pinChatArgs(name, args, sess.VM)
			}
			run.emit(map[string]any{"type": "tool_call", "name": name, "summary": chatToolSummary(name, args)})
			result := s.execChatTool(ctx, name, args, sess.VM)
			run.emit(map[string]any{"type": "tool_result", "name": name, "output": sshexec.Truncate(result, 4000)})
			sess.Messages = append(sess.Messages, agent.Message{Role: "tool", ToolName: name, ToolCallID: tc.ID, Content: result})
		}
		save()
	}
}

// streamChatRun replays the run's buffered events on w as NDJSON and
// follows it live until the run finishes or the client goes away; the run
// itself is unaffected by either.
func streamChatRun(w http.ResponseWriter, ctx context.Context, run *chatRun) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	// cond.Wait can't watch ctx, so a client disconnect pokes it awake
	defer context.AfterFunc(ctx, run.cond.Broadcast)()
	for i := 0; ; {
		run.mu.Lock()
		for i >= len(run.events) && !run.done && ctx.Err() == nil {
			run.cond.Wait()
		}
		evs := run.events[i:]
		i = len(run.events)
		done := run.done
		run.mu.Unlock()
		for _, b := range evs {
			w.Write(b)
			w.Write([]byte{'\n'})
		}
		if fl != nil && len(evs) > 0 {
			fl.Flush()
		}
		if done || ctx.Err() != nil {
			return
		}
	}
}

// handleChatStream re-attaches a client to a session's in-flight reply,
// replaying it from the start; 204 when nothing is streaming.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	v, ok := s.chatRuns.Load(r.PathValue("id"))
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	streamChatRun(w, r.Context(), v.(*chatRun))
}

// handleChatStop cancels a session's in-flight reply; the loop winds down
// and reports "stopped" to everyone attached.
func (s *Server) handleChatStop(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.chatRuns.Load(r.PathValue("id")); ok {
		v.(*chatRun).cancel()
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "idle"})
}
