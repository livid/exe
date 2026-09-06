package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"exe/internal/codex"
)

// An agent window's status line carries the session's figures — context
// in use, tokens, cost, the plan's usage windows — at its far right, next
// to the link state. Claude Code supplies them through its status-line
// hook: a command named in settings that gets the session's JSON on stdin
// once at launch and again after every assistant message
// (code.claude.com/docs/en/statusline). The daemon points that hook at a
// bridge that drops the JSON in a file of the window's own
// (agentStatusFile), follows the file for as long as a window is open
// (pushAgentStatus) and sends each new version down the window's
// WebSocket as a text frame, {"status":…} (agentStatus). Codex has no
// such hook; its window runs on the ChatGPT sign-in, so the daemon reads
// the subscription's usage windows itself, once a minute while a window
// is open, and sends them as {"usage":…} (pushOpenAIUsage) — the same
// figures the Chat window's status line and the Configuration window's
// OpenAI tab show.

// agentStatusFile is where the hook of one of an agent's sessions leaves
// its latest figures: the icon's own session keeps the plain name, the
// column's numbered ones carry their number (agentsessions.go).
func (s *Server) agentStatusFile(a hostAgent, session string) string {
	name := a.app
	if n := agentSessionNumber(a, session); n > 1 {
		name += "-" + strconv.Itoa(n)
	}
	return filepath.Join(s.StateDir, "agents", name+".status.json")
}

// claudeSettingsPath is Claude Code's per-user settings file.
func claudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// codexConfigPath is Codex's per-user config file: config.toml in
// $CODEX_HOME, ~/.codex by default.
func codexConfigPath() string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(h, ".codex")
	}
	return filepath.Join(home, "config.toml")
}

// agentSettingsPath is the CLI's per-user settings file, where a status
// line or notify command of the user's own would be set up.
func agentSettingsPath(a hostAgent) string {
	if a.notify {
		return codexConfigPath()
	}
	return claudeSettingsPath()
}

// claudeStatusLine reads the status line a user set up for Claude Code
// themselves: the command and its padding from settings.json, "" when
// there is none.
func claudeStatusLine(settings string) (string, *int) {
	b, err := os.ReadFile(settings)
	if err != nil {
		return "", nil
	}
	var st struct {
		StatusLine struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Padding *int   `json:"padding"`
		} `json:"statusLine"`
	}
	if json.Unmarshal(b, &st) != nil || st.StatusLine.Type != "command" {
		return "", nil
	}
	return st.StatusLine.Command, st.StatusLine.Padding
}

// agentStateFile is where the hooks of one of an agent's sessions leave
// its state for the session column (agentStateHooks): a sibling of the
// status file, ".state" for ".status.json".
func (s *Server) agentStateFile(a hostAgent, session string) string {
	return stateFileOf(s.agentStatusFile(a, session))
}

func stateFileOf(statusFile string) string {
	return strings.TrimSuffix(statusFile, ".status.json") + ".state"
}

// agentStatusArgs are the CLI arguments that install the hooks, none for
// an agent without them; settings is the user's own settings file for
// that CLI (agentSettingsPath). For Codex they are its config overrides
// (codexArgs). Claude Code's --settings takes a JSON object that ranks
// above every settings file, so a status line of the user's own
// (settings, normally ~/.claude/settings.json) would be hidden by the
// bridge: it is run on the same JSON afterwards and keeps drawing the
// in-terminal line. The bridge writes beside the file and moves the
// result into place last, so the daemon never reads half a write; it is
// a sh one-liner, which is why Windows does without. The same JSON
// carries the state hooks (agentStateHooks); hook lists merge across
// settings sources, so the user's own hooks keep running.
func agentStatusArgs(a hostAgent, file, settings string) []string {
	if a.notify {
		return codexArgs(file, settings)
	}
	if !a.statusLine {
		return nil
	}
	tmp := file + ".tmp"
	line := "cat > " + shQuote(tmp)
	sl := map[string]any{"type": "command"}
	if cmd, padding := claudeStatusLine(settings); cmd != "" {
		line += "; sh -c " + shQuote(cmd) + " < " + shQuote(tmp)
		if padding != nil {
			sl["padding"] = *padding
		}
	}
	sl["command"] = line + "; mv -f " + shQuote(tmp) + " " + shQuote(file)
	b, _ := json.Marshal(map[string]any{"statusLine": sl, "hooks": agentStateHooks(stateFileOf(file))})
	return []string{"--settings", string(b)}
}

// The session column's states. A session's hooks write one word to its
// state file (agentStateHooks) and the column reads it back
// (markAgentStates): the CLI is mid-turn, has finished a turn and waits
// at its prompt, or waits on the person right now — a permission, a
// question. No file, no word: a fresh session, or an agent without hooks.
const (
	agentStateWorking = "working"
	agentStateDone    = "done"
	agentStateWaiting = "waiting"
)

// agentStateHooks is the hooks block for a session's --settings: Claude
// Code runs a command on each event, and these write the state file.
// UserPromptSubmit opens a turn, Stop (and a turn that dies of an API
// error) closes it at the prompt, and a Notification that asks for the
// person — a permission prompt, an idle prompt, a question — marks it
// waiting. Written beside the file and moved into place, like the
// status bridge, so a read never sees half a word.
func agentStateHooks(state string) map[string]any {
	tmp := state + ".tmp"
	write := func(word string) []map[string]any {
		cmd := "printf " + word + " > " + shQuote(tmp) + " && mv -f " + shQuote(tmp) + " " + shQuote(state)
		return []map[string]any{{"type": "command", "command": cmd}}
	}
	return map[string]any{
		"UserPromptSubmit": []map[string]any{{"hooks": write(agentStateWorking)}},
		"Stop":             []map[string]any{{"hooks": write(agentStateDone)}},
		"StopFailure":      []map[string]any{{"hooks": write(agentStateDone)}},
		"Notification": []map[string]any{{
			"matcher": "permission_prompt|idle_prompt|agent_needs_input|elicitation_dialog|elicitation_url_dialog",
			"hooks":   write(agentStateWaiting)}},
	}
}

// codexArgs are the config overrides a Codex session is launched with:
// -c key=value pairs that outrank config.toml for that run alone (the
// user's config is not written). Codex has no status-line hook, but
// three of its settings give the session column what Claude Code's hooks
// give it. notify runs a command at the end of every turn with a JSON
// argument that names the thread: the command drops that JSON in the
// session's status file — the thread id is what makes the conversation
// resumable (readAgentResumable) — and the word "done" in its state
// file, written beside and moved into place like the status bridge; a
// notify command of the user's own (config, normally ~/.codex/config.toml)
// runs afterwards with the same argument. tui.terminal_title puts the
// thread's title on the terminal, a spinner in front while a turn runs:
// the column's row title and its working mark (parseAgentSessions) —
// before the first prompt the title is the thread's id, which the column
// shows as no title. tui.notifications rings the terminal bell at the
// end of a turn and when an approval waits, whether or not Codex thinks
// the terminal is focused (inside tmux it cannot tell), so tmux's bell
// flag marks a session that wants someone while no window shows it.
func codexArgs(file, config string) []string {
	tmp, state := file+".tmp", stateFileOf(file)
	script := "printf %s \"$1\" > " + shQuote(tmp) + " && mv -f " + shQuote(tmp) + " " + shQuote(file) +
		" && printf done > " + shQuote(state+".tmp") + " && mv -f " + shQuote(state+".tmp") + " " + shQuote(state)
	if user := codexNotify(config); len(user) > 0 {
		script += " && exec"
		for _, w := range user {
			script += " " + shQuote(w)
		}
		script += " \"$1\""
	}
	return []string{
		"-c", "notify=" + tomlStrings([]string{"sh", "-c", script, "exe-notify"}),
		"-c", "tui.notifications=true",
		"-c", `tui.notification_method="bel"`,
		"-c", `tui.notification_condition="always"`,
		"-c", `tui.terminal_title=["activity","thread-title"]`,
	}
}

// codexNotify reads the notify command a user set up for Codex
// themselves: the top-level notify array in config.toml, nil when there
// is none — or when it is written in more TOML than this reader knows
// (a multi-line string, say), which is to say the command is not chained
// rather than misread.
func codexNotify(config string) []string {
	b, err := os.ReadFile(config)
	if err != nil {
		return nil
	}
	var arr string
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		if !found {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "[") {
				return nil // the tables begin: a top-level key cannot follow
			}
			k, v, ok := strings.Cut(t, "=")
			if !ok || strings.TrimSpace(k) != "notify" {
				continue
			}
			found, arr = true, v
		} else {
			arr += "\n" + line
		}
		if list, ok := tomlStringArray(arr); ok {
			return list
		}
	}
	return nil
}

// tomlStringArray parses a TOML array of strings — ["a", 'b'] over any
// number of lines, comments and a trailing comma allowed — into its
// strings; false when s is not one, or not complete yet.
func tomlStringArray(s string) ([]string, bool) {
	i := 0
	skip := func() { // whitespace, newlines and comments
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\r', '\n':
				i++
			case '#':
				for i < len(s) && s[i] != '\n' {
					i++
				}
			default:
				return
			}
		}
	}
	skip()
	if i >= len(s) || s[i] != '[' {
		return nil, false
	}
	i++
	out := []string{}
	for {
		skip()
		if i >= len(s) {
			return nil, false
		}
		if s[i] == ']' {
			return out, true
		}
		if len(out) > 0 {
			if s[i] != ',' {
				return nil, false
			}
			i++
			skip()
			if i >= len(s) {
				return nil, false
			}
			if s[i] == ']' {
				return out, true
			}
		}
		q := s[i]
		if q != '"' && q != '\'' {
			return nil, false
		}
		i++
		var sb strings.Builder
		for {
			if i >= len(s) {
				return nil, false
			}
			c := s[i]
			if c == q {
				i++
				break
			}
			if c == '\n' && q == '\'' || c == '\\' && q == '"' && i+1 >= len(s) {
				return nil, false
			}
			if c == '\\' && q == '"' {
				i++
				switch s[i] {
				case 'n':
					sb.WriteByte('\n')
				case 't':
					sb.WriteByte('\t')
				case 'r':
					sb.WriteByte('\r')
				case '"', '\\':
					sb.WriteByte(s[i])
				case 'u', 'U':
					n := 4
					if s[i] == 'U' {
						n = 8
					}
					if i+n >= len(s) {
						return nil, false
					}
					r, err := strconv.ParseUint(s[i+1:i+1+n], 16, 32)
					if err != nil {
						return nil, false
					}
					sb.WriteRune(rune(r))
					i += n
				default:
					return nil, false
				}
				i++
				continue
			}
			sb.WriteByte(c)
			i++
		}
		out = append(out, sb.String())
	}
}

// tomlStrings writes a TOML array of strings, and tomlString one basic
// string, quoted and escaped so any path or command survives.
func tomlStrings(list []string) string {
	parts := make([]string, len(list))
	for i, s := range list {
		parts[i] = tomlString(s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func tomlString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			sb.WriteString(fmt.Sprintf(`\u%04X`, r))
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// readAgentResumable says whether a session's conversation is one the
// CLI's own resume picker can find. For Claude Code the status file
// names the session and its transcript, and the transcript exists (the
// CLI writes it with the first message; a fresh session has none). For
// Codex the file is its notify command's JSON, written at the end of a
// turn, when the thread it names is on disk. The daemon keeps no pointer
// of its own — archiving a session ends it, and /resume inside the CLI
// is the way back.
func readAgentResumable(statusFile string) bool {
	b, err := os.ReadFile(statusFile)
	if err != nil {
		return false
	}
	var h struct {
		SessionID      string `json:"session_id"`
		ThreadID       string `json:"thread-id"`
		TranscriptPath string `json:"transcript_path"`
		ContextWindow  struct {
			TotalInputTokens int64 `json:"total_input_tokens"`
		} `json:"context_window"`
	}
	if json.Unmarshal(b, &h) != nil {
		return false
	}
	if h.ThreadID != "" {
		return true
	}
	if h.SessionID == "" {
		return false
	}
	if h.TranscriptPath != "" {
		_, err := os.Stat(h.TranscriptPath)
		return err == nil
	}
	return h.ContextWindow.TotalInputTokens > 0
}

// readAgentState is the word in a session's state file, "" for none.
func readAgentState(file string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	switch w := strings.TrimSpace(string(b)); w {
	case agentStateWorking, agentStateDone, agentStateWaiting:
		return w
	}
	return ""
}

// agentStatus is what the window's status line shows: the parts of the
// hook's JSON it uses.
type agentStatus struct {
	Model        string   `json:"model"`
	ContextPct   *float64 `json:"context_pct"` // null until the first reply
	ContextSize  int64    `json:"context_size"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CostUSD      float64  `json:"cost_usd"`
	// five_hour and seven_day, the plan's usage windows — present for a
	// subscription and only after the first reply
	Limits map[string]agentLimit `json:"limits,omitempty"`
}

type agentLimit struct {
	UsedPct  float64         `json:"used_pct"`
	ResetsAt json.RawMessage `json:"resets_at,omitempty"`
}

// agentStatusMessage distills the hook's JSON into the {"status":…} frame
// the window gets; false when the file is not the hook's JSON.
func agentStatusMessage(b []byte) ([]byte, bool) {
	var h struct {
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
		ContextWindow *struct {
			TotalInputTokens  int64    `json:"total_input_tokens"`
			TotalOutputTokens int64    `json:"total_output_tokens"`
			ContextWindowSize int64    `json:"context_window_size"`
			UsedPercentage    *float64 `json:"used_percentage"`
		} `json:"context_window"`
		Cost struct {
			TotalCostUSD float64 `json:"total_cost_usd"`
		} `json:"cost"`
		RateLimits map[string]struct {
			UsedPercentage float64         `json:"used_percentage"`
			ResetsAt       json.RawMessage `json:"resets_at"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(b, &h) != nil || h.ContextWindow == nil {
		return nil, false
	}
	st := agentStatus{
		Model:        h.Model.DisplayName,
		ContextPct:   h.ContextWindow.UsedPercentage,
		ContextSize:  h.ContextWindow.ContextWindowSize,
		InputTokens:  h.ContextWindow.TotalInputTokens,
		OutputTokens: h.ContextWindow.TotalOutputTokens,
		CostUSD:      h.Cost.TotalCostUSD,
	}
	for name, l := range h.RateLimits {
		if name != "five_hour" && name != "seven_day" {
			continue
		}
		if st.Limits == nil {
			st.Limits = map[string]agentLimit{}
		}
		st.Limits[name] = agentLimit{UsedPct: l.UsedPercentage, ResetsAt: l.ResetsAt}
	}
	msg, _ := json.Marshal(map[string]agentStatus{"status": st})
	return msg, true
}

// pushAgentStatus follows an agent window's status file for the life of
// the window, sending the figures as they stand on connect and each new
// version after. A poll, not a watch: the file changes a few times a
// minute at most, and a second's lag is invisible under the reply that
// caused it. file is asked each time: it is the current session's, and
// when the session column moves the window the figures follow —
// {"status":null} first when the new session has none yet.
func pushAgentStatus(ctx context.Context, out *wsWriter, file func() string) {
	var cur string
	var seen time.Time
	var seenSize int64
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if f := file(); f != cur {
			cur, seen, seenSize = f, time.Time{}, 0
			if _, err := os.Stat(f); err != nil && out.WriteText([]byte(`{"status":null}`)) != nil {
				return
			}
		}
		if st, err := os.Stat(cur); err == nil && (!st.ModTime().Equal(seen) || st.Size() != seenSize) {
			seen, seenSize = st.ModTime(), st.Size()
			if b, err := os.ReadFile(cur); err == nil {
				if msg, ok := agentStatusMessage(b); ok && out.WriteText(msg) != nil {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// openaiUsageEvery is how often a Codex window's usage is re-read: the
// Chat window's own cadence, and a minute's lag is nothing next to the
// windows' five-hour and weekly scale.
const openaiUsageEvery = time.Minute

// pushOpenAIUsage keeps a Codex window's status line current for the life
// of the window: the subscription's usage as fetch reads it on connect and
// every interval after, sent as {"usage":…}. A read that fails once
// figures are up — the sign-in gone, the backend not answering — sends
// {"usage":null}, which clears them until a read succeeds again; failing
// from the start (no sign-in) sends nothing, and the status line shows
// the link state alone.
func pushOpenAIUsage(ctx context.Context, out *wsWriter, fetch func(context.Context) (*codex.Usage, error), every time.Duration) {
	shown := false
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		u, err := fetch(ctx)
		if err == nil {
			msg, _ := json.Marshal(map[string]*codex.Usage{"usage": u})
			if out.WriteText(msg) != nil {
				return
			}
			shown = true
		} else if shown {
			if out.WriteText([]byte(`{"usage":null}`)) != nil {
				return
			}
			shown = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
