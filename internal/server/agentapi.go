package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// The session column over HTTP, for tools that want a conversation the
// desktop can show. The hub watcher is the first: an instruction Livid
// posts in one of Claude's threads used to run as a headless `claude -p`
// nobody could see; now it opens a numbered session ("exe-claude-3") of
// the same kind the column's New starts, with the build prompt as its
// first message, so the Claude Code window lists it while it works and
// the conversation stays open there to be taken over or continued.
//
//	GET    /v1/agents/{app}/sessions                 the rows of the column, with their states; for Codex also
//	                                                 "threads", the threads started elsewhere (codexthreads.go)
//	POST   /v1/agents/{app}/sessions                 {prompt, resume, fork, session_id, permission_mode} → {name, number}
//	                                                 (Codex: prompt and resume, a thread id, alone)
//	POST   /v1/agents/{app}/sessions/{name}/prompt   {prompt}: pasted into the session as one message
//	DELETE /v1/agents/{app}/sessions/{name}          ends the session, as the column's Archive does

var agentIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

func (s *Server) agentOf(w http.ResponseWriter, r *http.Request) (hostAgent, bool) {
	a, ok := hostAgents[r.PathValue("app")]
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("no such agent"))
	}
	return a, ok
}

func (s *Server) handleAgentSessionsList(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	list := s.agentSessions(a)
	s.markAgentStates(a, list)
	out := map[string]any{"sessions": list}
	if threads := s.agentThreads(a, list); threads != nil {
		out["threads"] = threads // Codex: threads started elsewhere (codexthreads.go)
	}
	writeJSON(w, http.StatusOK, out)
}

// agentLaunchRequest is what a session may start with: Claude Code's
// resume (a fork of it, or the same conversation), a session id chosen
// up front so the caller can find the transcript, a permission mode, and
// the first message. Codex takes a thread to resume — one started in
// the ChatGPT app, say (codexthreads.go) — and the message.
type agentLaunchRequest struct {
	Prompt         string `json:"prompt"`
	Resume         string `json:"resume"`
	Fork           bool   `json:"fork"`
	SessionID      string `json:"session_id"`
	PermissionMode string `json:"permission_mode"`
}

var claudePermissionModes = map[string]bool{"default": true, "acceptEdits": true, "plan": true, "auto": true}

// agentLaunchArgs turns a request into the CLI's extra arguments, the
// prompt last since it is positional.
func agentLaunchArgs(a hostAgent, req agentLaunchRequest) ([]string, error) {
	var args []string
	if a.app == "claude" {
		if req.Resume != "" {
			if !agentIDPattern.MatchString(req.Resume) {
				return nil, errors.New("resume: not a session id")
			}
			args = append(args, "--resume", req.Resume)
			if req.Fork {
				args = append(args, "--fork-session")
			}
		}
		if req.SessionID != "" {
			if !agentIDPattern.MatchString(req.SessionID) {
				return nil, errors.New("session_id: not a session id")
			}
			args = append(args, "--session-id", req.SessionID)
		}
		if req.PermissionMode != "" {
			if !claudePermissionModes[req.PermissionMode] {
				return nil, fmt.Errorf("permission_mode: %q is not one of default, acceptEdits, plan, auto", req.PermissionMode)
			}
			args = append(args, "--permission-mode", req.PermissionMode)
		}
	} else {
		if req.Fork || req.SessionID != "" || req.PermissionMode != "" {
			return nil, fmt.Errorf("%s sessions take only a prompt, or a thread to resume", a.title)
		}
		if req.Resume != "" {
			if !agentIDPattern.MatchString(req.Resume) {
				return nil, errors.New("resume: not a thread id")
			}
			args = append(args, "resume", req.Resume)
		}
	}
	if req.Prompt != "" {
		args = append(args, req.Prompt)
	}
	return args, nil
}

func (s *Server) handleAgentSessionCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	var req agentLaunchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
		return
	}
	extra, err := agentLaunchArgs(a, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	n := 2
	for _, ss := range s.agentSessions(a) {
		if ss.Number >= n {
			n = ss.Number + 1
		}
	}
	name := agentSessionName(a, n)
	dir := ""
	if a.notify && req.Resume != "" {
		dir = codexThreadDir(req.Resume)
	}
	if err := s.newAgentSessionIn(a, name, dir, extra...); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if a.notify && req.Resume != "" {
		s.noteCodexThread(a, name, req.Resume)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": name, "number": n, "state_file": s.agentStateFile(a, name)})
}

// handleAgentSessionPrompt types a message into a running session: the
// text goes in as one bracketed paste, so the CLI keeps its newlines as
// text and shows it as a pasted block, then Return sends it.
func (s *Server) handleAgentSessionPrompt(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if agentSessionNumber(a, name) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("not a %s session: %q", a.title, name))
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("prompt: text wanted"))
		return
	}
	load := tmuxCmd("load-buffer", "-b", "exe-prompt", "-")
	if load == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("sessions need tmux on this host"))
		return
	}
	load.Stdin = strings.NewReader(req.Prompt)
	if out, err := load.CombinedOutput(); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("tmux load-buffer: %s", strings.TrimSpace(string(out))))
		return
	}
	if out, err := tmuxCmd("paste-buffer", "-p", "-d", "-b", "exe-prompt", "-t", "="+name).CombinedOutput(); err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("tmux paste-buffer: %s", strings.TrimSpace(string(out))))
		return
	}
	time.Sleep(400 * time.Millisecond) // the CLI takes the paste in before Return lands
	if out, err := tmuxCmd("send-keys", "-t", "="+name, "Enter").CombinedOutput(); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("tmux send-keys: %s", strings.TrimSpace(string(out))))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentSessionDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if agentSessionNumber(a, name) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("not a %s session: %q", a.title, name))
		return
	}
	cmd := tmuxCmd("kill-session", "-t", "="+name)
	if cmd == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("sessions need tmux on this host"))
		return
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(string(out))))
		return
	}
	os.Remove(s.agentStatusFile(a, name))
	os.Remove(s.agentStateFile(a, name))
	w.WriteHeader(http.StatusNoContent)
}
