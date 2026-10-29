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
	"unicode"
	"unicode/utf8"
)

// The session column over HTTP, for tools that want a conversation the
// desktop can show. The hub watcher is the first: an instruction Livid
// posts in one of Claude's threads used to run as a headless `claude -p`
// nobody could see; now it opens a numbered session ("exe-claude-3") of
// the same kind the column's New starts, with the build prompt as its
// first message, so the Claude Code window lists it while it works and
// the conversation stays open there to be taken over or continued.
//
//	GET    /v1/agents/{app}/sessions                 the rows of the column, with their states — "stopped" on one a restart of the
//	                                                 host ended, which nothing runs in (agentrestart.go) — and "threads", the
//	                                                 conversations started elsewhere (codexthreads.go, claudesessions.go):
//	                                                 the latest ten, or with ?threads=all every one (the column's Search)
//	POST   /v1/agents/{app}/sessions                 {prompt, resume, fork, session_id, permission_mode, model, name} → {name, number}
//	                                                 (Codex: prompt and resume, a thread id, alone)
//	POST   /v1/agents/{app}/sessions/{name}/prompt   {prompt, say}: one message, "say" typed and the prompt pasted after it
//	DELETE /v1/agents/{app}/sessions/{name}          ends the session, as the column's Archive does; answers with what it gave
//	                                                 back: freed, swap, available (bytes), measured (agentmemory.go). A
//	                                                 stopped session's row goes, and nothing is freed
//	GET    /v1/agents/{app}/sessions/{name}/info     what the column's Get Info shows: the conversation's title, summary,
//	                                                 first and last prompt, turns, replies, tokens, models, start, last
//	                                                 activity and folder, read from its transcript (claudeinfo.go; Claude Code only)
//	GET    /v1/agents/{app}/threads/{id}             the same for a conversation no session holds, by its id
//	GET    /v1/agents/{app}/sessions/{name}/prompts  every prompt of the session, whole, with when each was given: what the
//	                                                 line over the window's terminal opens (claudeprompts.go; Claude Code only)

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
	list = s.withStopped(a, list)
	s.orderSessionColumn(a, list)
	out := map[string]any{"sessions": list}
	if threads := s.agentThreadList(a, list, r.URL.Query().Get("threads") == "all"); threads != nil {
		out["threads"] = threads // conversations started elsewhere (codexthreads.go, claudesessions.go)
	}
	writeJSON(w, http.StatusOK, out)
}

// agentLaunchRequest is what a session may start with: Claude Code's
// resume (a fork of it, or the same conversation), a session id chosen
// up front so the caller can find the transcript, a permission mode, a
// model (the hub watcher's second try on Opus when Fable is at its usage
// limit), a display name (the column's title: Claude Code titles a session
// from its first prompt only, so a fork keeps its parent's title, or has
// none), and the first message. Codex takes a thread to resume — one started in
// the ChatGPT app, say (codexthreads.go) — and the message. Either takes
// dir, the folder the session opens in (agentSessionDir): a project's
// working directory, say, in place of exe's own project folder.
type agentLaunchRequest struct {
	Prompt         string `json:"prompt"`
	Resume         string `json:"resume"`
	Fork           bool   `json:"fork"`
	SessionID      string `json:"session_id"`
	PermissionMode string `json:"permission_mode"`
	Model          string `json:"model"`
	Name           string `json:"name"`
	Dir            string `json:"dir"`
}

// agentModelPattern is an alias or a full name, with a context suffix:
// "opus", "claude-opus-5", "claude-opus-5[1m]"
var agentModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(\[[0-9a-z]{1,8}\])?$`)

// agentNameOK is a display name that reads as one: a line of words, not an
// option. It lands on a command line (quoted) and in the terminal title.
func agentNameOK(name string) bool {
	if strings.HasPrefix(name, "-") || utf8.RuneCountInString(name) > 80 || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

var claudePermissionModes = map[string]bool{"default": true, "acceptEdits": true, "plan": true, "auto": true}

// agentLaunchArgs turns a request into the CLI's extra arguments. The
// prompt is not one of them: it is the session's first message, which
// newAgentTaskSession hands over apart from the command line.
func agentLaunchArgs(a hostAgent, req agentLaunchRequest) ([]string, error) {
	if err := agentTaskOK(req.Prompt); err != nil {
		return nil, fmt.Errorf("prompt: %w", err)
	}
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
		if req.Model != "" {
			if !agentModelPattern.MatchString(req.Model) {
				return nil, errors.New("model: not a model name")
			}
			args = append(args, "--model", req.Model)
		}
		if req.Name != "" {
			if !agentNameOK(req.Name) {
				return nil, errors.New("name: one line of at most 80 characters, not starting with -")
			}
			args = append(args, "--name", req.Name)
		}
	} else {
		if req.Fork || req.SessionID != "" || req.PermissionMode != "" || req.Model != "" || req.Name != "" {
			return nil, fmt.Errorf("%s sessions take only a prompt, or a thread to resume", a.title)
		}
		if req.Resume != "" {
			if !agentIDPattern.MatchString(req.Resume) {
				return nil, errors.New("resume: not a thread id")
			}
			args = append(args, "resume", req.Resume)
		}
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
	name := s.nextAgentSession(a)
	n := agentSessionNumber(a, name)
	dir, err := agentSessionDir(req.Dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if dir == "" && a.notify && req.Resume != "" {
		dir = codexThreadDir(req.Resume)
	}
	if err := s.newAgentTaskSession(a, name, dir, req.Prompt, extra...); err != nil {
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
//
// Claude Code hands a paste to the model as <pasted_content>: data, whose
// instructions count only where the typed part of the message asks for
// them, so a paste alone is declined ("say so in your own words"). "say"
// is that typed part: one line, sent as keystrokes ahead of the paste.
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
		Say    string `json:"say"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("prompt: text wanted"))
		return
	}
	say := strings.Join(strings.Fields(req.Say), " ") // a newline typed as a key would send early
	// say, paste and Return are one message: a second delivery in between
	// would load its text over this one's buffer, or land inside its line
	s.agentPromptMu.Lock()
	defer s.agentPromptMu.Unlock()
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
	// paste-buffer and send-keys want a pane: "=name" names a session but
	// not a pane ("can't find pane"), "=name:" is its current window's
	pane := "=" + name + ":"
	if say != "" {
		if out, err := tmuxCmd("send-keys", "-t", pane, "-l", "--", say+" ").CombinedOutput(); err != nil {
			writeErr(w, http.StatusNotFound, fmt.Errorf("tmux send-keys: %s", strings.TrimSpace(string(out))))
			return
		}
		time.Sleep(300 * time.Millisecond) // the keys are read as typing before the paste begins
	}
	if out, err := tmuxCmd("paste-buffer", "-p", "-d", "-b", "exe-prompt", "-t", pane).CombinedOutput(); err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("tmux paste-buffer: %s", strings.TrimSpace(string(out))))
		return
	}
	time.Sleep(400 * time.Millisecond) // the CLI takes the paste in before Return lands
	if out, err := tmuxCmd("send-keys", "-t", pane, "Enter").CombinedOutput(); err != nil {
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
	if s.forgetStopped(a, name) {
		writeJSON(w, http.StatusOK, archiveFigures{}) // a stopped session: nothing ran, nothing is freed
		return
	}
	cmd := tmuxCmd("kill-session", "-t", "="+name)
	if cmd == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("sessions need tmux on this host"))
		return
	}
	m := measureSession(name)
	if out, err := cmd.CombinedOutput(); err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(string(out))))
		return
	}
	os.Remove(s.agentStatusFile(a, name))
	os.Remove(s.agentStateFile(a, name))
	s.unnoteAgentSession(a, name)
	writeJSON(w, http.StatusOK, m.settle()) // what the session gave back (agentmemory.go), once its processes are gone
}

// handleAgentSessionInfo answers Get Info on one of the column's sessions:
// its status file names the conversation and the transcript (the
// status-line hook's JSON, or the stub a resume left), and the transcript
// is read whole (claudeSessionInfoOf). Claude Code only: Codex keeps no
// transcript the daemon reads this far.
func (s *Server) handleAgentSessionInfo(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if agentSessionNumber(a, name) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("not a %s session: %q", a.title, name))
		return
	}
	if a.notify {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no info on a %s session yet", a.title))
		return
	}
	id, path := readClaudeStatusSession(s.agentStatusFile(a, name))
	if id == "" {
		writeErr(w, http.StatusNotFound, errors.New("no conversation yet"))
		return
	}
	if _, err := os.Stat(path); path == "" || err != nil {
		t := claudeSessionByID(claudeHome(), id)
		if t == nil {
			writeErr(w, http.StatusNotFound, errors.New("no conversation yet"))
			return
		}
		path = t.path
	}
	info := claudeSessionInfoOf(path)
	if info == nil {
		writeErr(w, http.StatusNotFound, errors.New("no conversation yet"))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleAgentThreadInfo answers Get Info on a conversation no session
// holds — one started elsewhere, or archived — by its id.
func (s *Server) handleAgentThreadInfo(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	if a.notify {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no info on a %s thread yet", a.title))
		return
	}
	id := r.PathValue("id")
	if !agentIDPattern.MatchString(id) {
		writeErr(w, http.StatusBadRequest, errors.New("not a session id"))
		return
	}
	t := claudeSessionByID(claudeHome(), id)
	if t == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no %s session %s on this machine", a.title, id))
		return
	}
	info := claudeSessionInfoOf(t.path)
	if info == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no %s session %s on this machine", a.title, id))
		return
	}
	writeJSON(w, http.StatusOK, info)
}
