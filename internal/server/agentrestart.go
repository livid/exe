package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

// Sessions a host restart ended. The agents' sessions live in tmux, whose
// server outlives a restart of the daemon but not one of the host: every
// conversation in the column ends with it, though nobody archived one. The
// conversations are still on disk, and each session's status file still
// names its own (readAgentThreadID), so the column can offer them again.
// While the daemon runs, RunAgentNotes keeps a note of the agents' live
// sessions — agents/live.json, under the host's boot id — and before the
// daemon serves, NoteHostRestart compares it with the boot it starts in:
// under another boot, each noted session that tmux no longer has, and
// whose conversation is still here, becomes a stopped session
// (agents/stopped.json). One that ended while the daemon watched — its CLI
// exited, Archive took it — had left the note by then, so only what the
// restart took comes back. A stopped session keeps its row in the column
// — its name, number, title and day — with the hollow dot of a
// conversation that is not here; a click continues it (wakeAgentSession:
// the CLI resumes the conversation in a tmux session of the same name, in
// the folder it was started in), and Archive lets it go. They come back
// as they are wanted rather than all at boot: each CLI holds a few
// hundred megabytes, and a long-lived column holds many sessions nobody
// has looked at in weeks. A window opened while none of its agent's
// sessions runs lands where it was, as it would have before the restart:
// its last session is continued for it (wakeLastStopped).

// agentNote is one live session as the note keeps it: enough to show its
// row again.
type agentNote struct {
	Name         string `json:"name"`
	Number       int    `json:"number"`
	Title        string `json:"title,omitempty"`
	Created      int64  `json:"created"`
	LastAttached int64  `json:"last_attached,omitempty"`
}

// agentLiveNote is agents/live.json: the boot it was written in, and
// each agent's live sessions, by app.
type agentLiveNote struct {
	Boot     string                 `json:"boot"`
	Sessions map[string][]agentNote `json:"sessions"`
}

// stoppedSession is one session a host restart ended: its note, the
// conversation it carried — a Claude Code session id, a Codex thread id —
// and when the daemon found it gone.
type stoppedSession struct {
	agentNote
	Thread  string `json:"thread"`
	Stopped int64  `json:"stopped"`
}

// agentNotesEvery is how often RunAgentNotes looks at the sessions; one
// started within it before the host went down is not in the note.
const agentNotesEvery = 5 * time.Second

// agentLiveFile is the note; agentStoppedFile the stopped sessions.
func (s *Server) agentLiveFile() string {
	return filepath.Join(s.StateDir, "agents", "live.json")
}

func (s *Server) agentStoppedFile() string {
	return filepath.Join(s.StateDir, "agents", "stopped.json")
}

// hostBootID names the boot the host is in: Linux's boot id, the boot
// time on macOS; "" where neither is known, and then no restart is told.
func hostBootID() string {
	if b, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		return strings.TrimSpace(string(b))
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sysctl", "-n", "kern.boottime").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// agentList is hostAgents in a fixed order.
func agentList() []hostAgent {
	list := make([]hostAgent, 0, len(hostAgents))
	for _, a := range hostAgents {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].app < list[j].app })
	return list
}

// agentNoteNow is the note of the agents' live sessions under boot, from
// one listing so every agent's is from the same moment; false when there
// is no tmux server to ask — the host going down can take it before the
// daemon, and a note written then would say nothing was running.
func (s *Server) agentNoteNow(agents []hostAgent, boot string) (agentLiveNote, bool) {
	note := agentLiveNote{Boot: boot, Sessions: map[string][]agentNote{}}
	out, untitled, err := s.agentPanes()
	if err != nil {
		return note, false
	}
	now := time.Now().Unix()
	for _, a := range agents {
		for _, l := range parseAgentSessions(a, out, untitled, now) {
			note.Sessions[a.app] = append(note.Sessions[a.app], agentNote{Name: l.Name, Number: l.Number,
				Title: l.Title, Created: l.Created, LastAttached: l.lastAttached})
		}
	}
	return note, true
}

// noteAgentSessions writes the note when it differs from last, the one
// written before, and returns what is in the file now.
func (s *Server) noteAgentSessions(agents []hostAgent, boot string, last []byte) []byte {
	note, ok := s.agentNoteNow(agents, boot)
	if !ok {
		return last
	}
	b, _ := json.Marshal(note)
	if bytes.Equal(b, last) {
		return last
	}
	s.agentStoppedMu.Lock()
	defer s.agentStoppedMu.Unlock()
	if s.writeAgentFile(s.agentLiveFile(), b) != nil {
		return last
	}
	return b
}

// RunAgentNotes keeps agents/live.json for the life of the daemon.
func (s *Server) RunAgentNotes(ctx context.Context) {
	boot := hostBootID()
	if boot == "" {
		return // no restart can be told on this host
	}
	agents := agentList()
	var last []byte
	t := time.NewTicker(agentNotesEvery)
	defer t.Stop()
	for {
		last = s.noteAgentSessions(agents, boot, last)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// NoteHostRestart turns the sessions a restart of the host ended into
// stopped ones. It runs once, before the daemon serves: a window opened
// first would start a session over one of their names.
func (s *Server) NoteHostRestart() {
	s.noteHostRestart(agentList(), hostBootID())
}

// noteHostRestart is NoteHostRestart for these agents in the boot named.
// The note is written again under this boot at once, whatever tmux says,
// so the restart is taken in once: a daemon restarted before the first
// session of the new boot would otherwise bring back a row Archive had let
// go.
func (s *Server) noteHostRestart(agents []hostAgent, boot string) {
	b, err := os.ReadFile(s.agentLiveFile())
	if err != nil || boot == "" {
		return
	}
	var note agentLiveNote
	if json.Unmarshal(b, &note) != nil || note.Boot == "" || note.Boot == boot {
		return
	}
	now, _ := s.agentNoteNow(agents, boot) // after a restart there is normally no server at all
	live := map[string]bool{}
	for _, list := range now.Sessions {
		for _, n := range list {
			live[n.Name] = true
		}
	}
	at := time.Now().Unix()
	s.agentStoppedMu.Lock()
	defer s.agentStoppedMu.Unlock()
	s.loadStoppedLocked()
	added := 0
	for _, a := range agents {
		for _, n := range note.Sessions[a.app] {
			if live[n.Name] || agentSessionNumber(a, n.Name) != n.Number {
				continue
			}
			id := s.agentConversation(a, n.Name)
			if id == "" {
				continue // no conversation yet: nothing to continue
			}
			list := slices.DeleteFunc(s.agentStopped[a.app], func(st stoppedSession) bool { return st.Name == n.Name })
			s.agentStopped[a.app] = append(list, stoppedSession{agentNote: n, Thread: id, Stopped: at})
			added++
		}
	}
	if added > 0 {
		s.saveStoppedLocked()
		log.Printf("host restarted: %d agent sessions stopped, to be continued from their columns", added)
	}
	nb, _ := json.Marshal(now)
	s.writeAgentFile(s.agentLiveFile(), nb)
}

// agentConversation is the conversation a session carried, as a resume
// needs it: the id its status file names — for Claude Code, only while its
// transcript is on this machine, which the CLI writes with the first
// prompt — "" for a session that had none yet.
func (s *Server) agentConversation(a hostAgent, name string) string {
	id := readAgentThreadID(s.agentStatusFile(a, name))
	if !agentIDPattern.MatchString(id) {
		return ""
	}
	if !a.notify && claudeSessionByID(claudeHome(), id) == nil {
		return ""
	}
	return id
}

// unnoteAgentSession takes a session Archive ended out of the note at
// once: when it was the last on the server, tmux goes with it, and the
// note is not written again while there is no server.
func (s *Server) unnoteAgentSession(a hostAgent, name string) {
	s.agentStoppedMu.Lock()
	defer s.agentStoppedMu.Unlock()
	b, err := os.ReadFile(s.agentLiveFile())
	if err != nil {
		return
	}
	var note agentLiveNote
	if json.Unmarshal(b, &note) != nil {
		return
	}
	list := note.Sessions[a.app]
	if i := slices.IndexFunc(list, func(n agentNote) bool { return n.Name == name }); i >= 0 {
		note.Sessions[a.app] = slices.Delete(list, i, i+1)
		nb, _ := json.Marshal(note)
		s.writeAgentFile(s.agentLiveFile(), nb)
	}
}

// writeAgentFile writes one of the files beside the status files.
func (s *Server) writeAgentFile(file string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// loadStoppedLocked reads agents/stopped.json the first time it is
// wanted; agentStoppedMu is held.
func (s *Server) loadStoppedLocked() {
	if s.agentStopped != nil {
		return
	}
	s.agentStopped = map[string][]stoppedSession{}
	if b, err := os.ReadFile(s.agentStoppedFile()); err == nil {
		json.Unmarshal(b, &s.agentStopped)
	}
}

// saveStoppedLocked writes the stopped sessions back; agentStoppedMu is
// held. The file goes once the last of them has.
func (s *Server) saveStoppedLocked() {
	for app, list := range s.agentStopped {
		if len(list) == 0 {
			delete(s.agentStopped, app)
		}
	}
	if len(s.agentStopped) == 0 {
		os.Remove(s.agentStoppedFile())
		return
	}
	b, _ := json.Marshal(s.agentStopped)
	s.writeAgentFile(s.agentStoppedFile(), b)
}

// stoppedSessions is the agent's stopped sessions, a copy.
func (s *Server) stoppedSessions(a hostAgent) []stoppedSession {
	s.agentStoppedMu.Lock()
	defer s.agentStoppedMu.Unlock()
	s.loadStoppedLocked()
	return slices.Clone(s.agentStopped[a.app])
}

// withStopped is the column's rows: the live sessions and, in their
// places by start, the stopped ones — less any whose name a live
// session has taken again.
func (s *Server) withStopped(a hostAgent, live []agentSession) []agentSession {
	stopped := s.stoppedSessions(a)
	if len(stopped) == 0 {
		return live
	}
	taken := map[string]bool{}
	for _, l := range live {
		taken[l.Name] = true
	}
	list := live
	for _, st := range stopped {
		if !taken[st.Name] {
			list = append(list, agentSession{Name: st.Name, Number: st.Number, Title: st.Title, Created: st.Created,
				Stopped: true, Resumable: true, lastAttached: st.LastAttached})
		}
	}
	sortAgentSessions(list)
	return list
}

// wakeAgentSession continues a stopped session under its own name: the
// CLI resumes its conversation — Claude Code in the folder its transcript
// was started in, Codex in its thread's — in a new tmux session, marked
// with the conversation at once as the column's resume marks one
// (noteClaudeSession, noteCodexThread). false, with no error, for a name
// that is not stopped, or that a live session holds again. A conversation
// gone from this machine since takes its row with it.
func (s *Server) wakeAgentSession(a hostAgent, name string) (bool, error) {
	s.agentStoppedMu.Lock()
	defer s.agentStoppedMu.Unlock()
	s.loadStoppedLocked()
	i := slices.IndexFunc(s.agentStopped[a.app], func(st stoppedSession) bool { return st.Name == name })
	if i < 0 {
		return false, nil
	}
	st := s.agentStopped[a.app][i]
	drop := func() {
		s.agentStopped[a.app] = slices.Delete(s.agentStopped[a.app], i, i+1)
		s.saveStoppedLocked()
	}
	if has := tmuxCmd("has-session", "-t", "="+name); has != nil && has.Run() == nil {
		drop()
		return false, nil
	}
	if a.notify {
		if err := s.newAgentSessionIn(a, name, codexThreadDir(st.Thread), "resume", st.Thread); err != nil {
			return false, err
		}
		s.noteCodexThread(a, name, st.Thread)
	} else {
		t := claudeSessionByID(claudeHome(), st.Thread)
		if t == nil {
			drop()
			return false, fmt.Errorf("the conversation of %s is no longer on this machine", name)
		}
		if err := s.newAgentSessionIn(a, name, existingDir(t.cwd), "--resume", st.Thread); err != nil {
			return false, err
		}
		s.noteClaudeSession(a, name, st.Thread, t.path)
	}
	drop()
	return true, nil
}

// wakeLastStopped continues, for a window opened while none of the
// agent's sessions runs, the stopped session it showed last — the latest
// a client landed on, else the latest started — and names it; "" when
// there is none, or it would not start.
func (s *Server) wakeLastStopped(a hostAgent) string {
	list := s.stoppedSessions(a)
	if len(list) == 0 {
		return ""
	}
	best := list[0]
	for _, st := range list[1:] {
		if st.LastAttached != best.LastAttached {
			if st.LastAttached > best.LastAttached {
				best = st
			}
		} else if st.Created > best.Created || st.Created == best.Created && st.Number > best.Number {
			best = st
		}
	}
	woke, err := s.wakeAgentSession(a, best.Name)
	if err != nil {
		log.Printf("%s: continuing %s: %v", a.title, best.Name, err)
	}
	if !woke {
		return ""
	}
	return best.Name
}

// wakeAllStopped continues every stopped session of the agent's, the
// latest first; the first failure is the answer, the rest still start.
func (s *Server) wakeAllStopped(a hostAgent, each func()) error {
	list := s.stoppedSessions(a)
	sort.Slice(list, func(i, j int) bool { return list[i].Created > list[j].Created })
	var first error
	for _, st := range list {
		if _, err := s.wakeAgentSession(a, st.Name); err != nil && first == nil {
			first = err
		}
		if each != nil {
			each()
		}
	}
	return first
}

// forgetStopped lets a stopped session go — Archive on its row: the row
// leaves, and its status files with it, as Archive's do; the conversation
// stays on disk for Search and the CLI's own resume. false for a name
// that is not stopped, or that a live session holds again.
func (s *Server) forgetStopped(a hostAgent, name string) bool {
	s.agentStoppedMu.Lock()
	defer s.agentStoppedMu.Unlock()
	s.loadStoppedLocked()
	i := slices.IndexFunc(s.agentStopped[a.app], func(st stoppedSession) bool { return st.Name == name })
	if i < 0 {
		return false
	}
	s.agentStopped[a.app] = slices.Delete(s.agentStopped[a.app], i, i+1)
	s.saveStoppedLocked()
	if has := tmuxCmd("has-session", "-t", "="+name); has != nil && has.Run() == nil {
		return false // a live session holds the name again: the status files and the Archive are its
	}
	os.Remove(s.agentStatusFile(a, name))
	os.Remove(s.agentStateFile(a, name))
	return true
}
