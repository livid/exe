package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

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

var errChatBusy = errors.New("a reply is already streaming in this session")

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

	mu      sync.Mutex
	cond    *sync.Cond
	events  [][]byte // marshaled NDJSON events, in emit order
	done    bool
	stopMsg string // why the run was canceled, for the client-facing error
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

// stop cancels the run, recording msg as the reason shown to clients.
func (r *chatRun) stop(msg string) {
	r.mu.Lock()
	if r.stopMsg == "" {
		r.stopMsg = msg
	}
	r.mu.Unlock()
	r.cancel()
}

func (r *chatRun) stopReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopMsg != "" {
		return r.stopMsg
	}
	return "stopped"
}

// startChatRun registers a run for the session and launches the agent loop
// in the background. The user message is appended and saved before this
// returns, so the session on disk carries it as soon as the run is visible.
// Fails when the session already has a reply streaming (errChatBusy) or no
// longer exists.
func (s *Server) startChatRun(cfg *config.Config, provider string, sess *chat.Session, message string) (*chatRun, error) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &chatRun{base: len(sess.Messages) + 1, userMsg: message, cancel: cancel}
	run.cond = sync.NewCond(&run.mu)
	if _, loaded := s.chatRuns.LoadOrStore(sess.ID, run); loaded {
		cancel()
		return nil, errChatBusy
	}
	// The caller loaded sess before this run owned the session, so a
	// previous run's final save may have landed in between — re-read now
	// that no other run can be writing, or the loop would push stale
	// history back to disk. (A brand-new session re-reads its own save.)
	fresh, err := chat.Load(s.chatDir(), sess.ID)
	if err != nil {
		s.chatRuns.Delete(sess.ID)
		cancel()
		return nil, err
	}
	*sess = *fresh
	run.base = len(sess.Messages) + 1
	run.emit(map[string]any{"type": "session", "meta": sess.Meta})
	sess.Messages = append(sess.Messages, agent.Message{Role: "user", Content: message})
	if err := chat.Save(s.chatDir(), sess); err != nil {
		run.emit(map[string]any{"type": "error", "error": "save session: " + err.Error()})
	}
	go func() {
		defer cancel()
		defer func() {
			// The loop runs outside net/http's panic recovery now; a panic
			// here must not take the daemon (and every VM) down with it.
			if v := recover(); v != nil {
				log.Printf("chat run %s: panic: %v\n%s", sess.ID, v, debug.Stack())
				run.emit(map[string]any{"type": "error", "error": fmt.Sprintf("internal error: %v", v)})
			}
			// Unregister before announcing done so a session GET can't pair
			// a complete file with a stale run entry and serve it truncated.
			s.chatRuns.Delete(sess.ID)
			run.emit(map[string]any{"type": "done", "meta": sess.Meta})
			run.mu.Lock()
			run.done = true
			run.mu.Unlock()
			run.cond.Broadcast()
		}()
		s.runChatLoop(ctx, cfg, provider, sess, run)
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
		var partial strings.Builder
		msg, err := callModel(ctx, append([]agent.Message{system}, sess.Messages...),
			func(d string) {
				partial.WriteString(d)
				run.emit(map[string]any{"type": "delta", "text": d})
			})
		if err != nil {
			// keep what already streamed — without this the partial reply
			// lives only in the run's event buffer and dies with the run
			if partial.Len() > 0 {
				sess.Messages = append(sess.Messages, agent.Message{Role: "assistant", Content: partial.String()})
				save()
			}
			reason := err.Error()
			if ctx.Err() != nil {
				reason = run.stopReason()
			}
			run.emit(map[string]any{"type": "error", "error": reason})
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
			// Stop must hold between the tools of a turn: a canceled ctx
			// skips execution outright — some tools (VM delete) would
			// otherwise still act despite the dead context.
			result := "error: " + run.stopReason()
			if ctx.Err() == nil {
				result = s.execChatTool(ctx, name, args, sess.VM)
			}
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
	// cond.Wait can't watch ctx, so a client disconnect pokes it awake. The
	// lock/unlock orders the broadcast after any in-progress predicate
	// check, so the waiter can't slip into Wait having missed both the
	// cancellation and the wakeup (lost-wakeup race).
	defer context.AfterFunc(ctx, func() {
		run.mu.Lock()
		run.mu.Unlock()
		run.cond.Broadcast()
	})()
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
		v.(*chatRun).stop("stopped")
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "idle"})
}

// DrainChatRuns cancels every in-flight chat reply and waits (bounded by
// ctx) for the loops to persist what they have and unregister, so a
// restart or shutdown doesn't kill detached runs mid-turn with nothing
// saved and no error event to attached clients. reason is the
// client-facing stop message ("stopped: …") — it also lands in the saved
// transcript as the result of any skipped tool, so it must say what
// actually happened (restart vs shutdown).
func (s *Server) DrainChatRuns(ctx context.Context, reason string) {
	n := 0
	s.chatRuns.Range(func(_, v any) bool {
		n++
		v.(*chatRun).stop(reason)
		return true
	})
	if n == 0 {
		return
	}
	log.Printf("draining %d chat run(s)", n)
	for {
		n = 0
		s.chatRuns.Range(func(_, _ any) bool { n++; return true })
		if n == 0 {
			return
		}
		select {
		case <-ctx.Done():
			log.Printf("drain: %d chat run(s) still winding down", n)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
