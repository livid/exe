package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// A plain Terminal window is the view of a tmux session of its own —
// "exe-term-1", "exe-term-2", … — on the tmux server the agent sessions
// live on (agentsessions.go), so the shell outlives the window's link: a
// reload, a closed browser, a laptop asleep, a network gone or the daemon
// restarted only detach the window's tmux client, the window reconnects
// on its own, and a desk that loads reopens a window for every session
// still alive. The close box ends the session and the shell with it, as
// a terminal emulator's does; so does the shell's own exit. tmux stays
// out of sight in these sessions: no status line (the window has its
// own), no prefix key (every key reaches the shell, C-b included), and
// TMUX unset in the shell, so tmux run inside it attaches as it would in
// any terminal. Without tmux a Terminal is a one-off shell, as it always
// was (startHostShell).
//
// A window's link to a session ends with a reason the window acts on
// (termEndReason): "detached" — the session lives on without this
// client, so the window reconnects; "closed" — a window's close box
// ended it (handleTermSessionEnd), so every other window of it closes
// too; "session ended" — the shell exited, and the window stays with
// its last screen, dead.

const termSessionPrefix = "exe-term-"

// errNoTmux: Terminal sessions need tmux on the host.
var errNoTmux = errors.New("Terminal sessions need tmux on this host")

// termSessionName is the tmux session behind Terminal number n.
func termSessionName(n int) string { return termSessionPrefix + strconv.Itoa(n) }

// termSessionNumber is the Terminal number behind a session name, 0 for
// any other session on the tmux server.
func termSessionNumber(name string) int {
	rest, ok := strings.CutPrefix(name, termSessionPrefix)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 || strconv.Itoa(n) != rest {
		return 0
	}
	return n
}

// termSession is one live Terminal session.
type termSession struct {
	Name     string `json:"name"`
	Number   int    `json:"number"`
	Created  int64  `json:"created"`
	Attached bool   `json:"attached"` // a window (or any tmux client) shows it
}

// tmuxTermFormat is the list-sessions format termSessions reads; a
// session name cannot hold a colon.
const tmuxTermFormat = "#{session_name}:#{session_created}:#{session_attached}"

// parseTermSessions picks the Terminal sessions out of list-sessions
// output in tmuxTermFormat, in number order.
func parseTermSessions(out string) []termSession {
	list := []termSession{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, ":")
		if len(f) != 3 {
			continue
		}
		n := termSessionNumber(f[0])
		if n == 0 {
			continue
		}
		created, _ := strconv.ParseInt(f[1], 10, 64)
		list = append(list, termSession{Name: f[0], Number: n, Created: created, Attached: f[2] != "0" && f[2] != ""})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Number < list[j].Number })
	return list
}

// termSessions lists the live Terminal sessions — none without tmux, or
// without a tmux server running.
func (s *Server) termSessions() []termSession {
	cmd := tmuxCmd("list-sessions", "-F", tmuxTermFormat)
	if cmd == nil {
		return []termSession{}
	}
	out, err := cmd.Output()
	if err != nil {
		return []termSession{}
	}
	return parseTermSessions(string(out))
}

// termAlive says whether the named session exists.
func (s *Server) termAlive(name string) bool {
	cmd := tmuxCmd("has-session", "-t", "="+name)
	return cmd != nil && cmd.Run() == nil
}

// termEndReason is why a window's link to the named session ended, or
// why one to it cannot start: "detached" while the session lives,
// "closed" when a close box ended it, "session ended" when its shell
// exited (or it is simply not there).
func (s *Server) termEndReason(name string) string {
	if s.termAlive(name) {
		return "detached"
	}
	s.termMu.Lock()
	defer s.termMu.Unlock()
	if s.termClosed[name] {
		return "closed"
	}
	return "session ended"
}

// endTermSession ends a Terminal session and the shell in it — the
// window's close box. The name is marked closed first, so every other
// window of it hears "closed" as its client goes, and closes too.
func (s *Server) endTermSession(name string) error {
	s.termMu.Lock()
	if s.termClosed == nil {
		s.termClosed = map[string]bool{}
	}
	s.termClosed[name] = true
	s.termMu.Unlock()
	cmd := tmuxCmd("kill-session", "-t", "="+name)
	if cmd == nil {
		return errNoTmux
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// termEnv is the environment of the tmux commands behind a Terminal:
// the daemon's own, with TERM for the browser's terminal and no TMUX
// (withoutTMUX).
func termEnv() []string {
	env := []string{"TERM=xterm-256color"}
	for _, kv := range withoutTMUX(os.Environ()) {
		if !strings.HasPrefix(kv, "TERM=") {
			env = append(env, kv)
		}
	}
	return env
}

// handleTermSessions lists the live Terminal sessions — the desk opens
// a window for each when it loads: {"terminals":[…]}.
func (s *Server) handleTermSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"terminals": s.termSessions()})
}

// handleTermSessionNew starts a Terminal session for a new window, which
// then attaches with /v1/host/terminal?term=<number>. 501 on a host
// without tmux: the window runs a one-off shell instead. Every desk
// hears of it on the layout stream (uiAnnounce) and opens its own
// window of the session; the desk that asked names itself in the body,
// {"client":…}, and the event carries that on, so it is not news there.
func (s *Server) handleTermSessionNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Client string `json:"client"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req) // no body: no desk to leave out
	t, err := s.newTermSession()
	if errors.Is(err, errNoTmux) {
		writeErr(w, http.StatusNotImplemented, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.uiAnnounce(map[string]any{"term": t, "client": req.Client})
	writeJSON(w, http.StatusCreated, t)
}

// handleTermSessionEnd ends Terminal number {n}: its window's close box.
func (s *Server) handleTermSessionEnd(w http.ResponseWriter, r *http.Request) {
	n := termSessionNumber(termSessionPrefix + r.PathValue("n"))
	if n == 0 || !s.termAlive(termSessionName(n)) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no Terminal session %q", r.PathValue("n")))
		return
	}
	if err := s.endTermSession(termSessionName(n)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ended"})
}
