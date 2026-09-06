package server

import (
	"context"
	"encoding/json"
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
// an agent without them. Claude Code's --settings takes a JSON object
// that ranks above every settings file, so a status line of the user's
// own (settings, normally ~/.claude/settings.json) would be hidden by the
// bridge: it is run on the same JSON afterwards and keeps drawing the
// in-terminal line. The bridge writes beside the file and moves the
// result into place last, so the daemon never reads half a write; it is
// a sh one-liner, which is why Windows does without. The same JSON
// carries the state hooks (agentStateHooks); hook lists merge across
// settings sources, so the user's own hooks keep running.
func agentStatusArgs(a hostAgent, file, settings string) []string {
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

// readAgentResumable says whether a session's conversation is one the
// CLI's own resume picker can find: the status file names the session
// and its transcript, and the transcript exists (the CLI writes it with
// the first message; a fresh session has none). The daemon keeps no
// pointer of its own — archiving a session ends it, and /resume inside
// the CLI is the way back.
func readAgentResumable(statusFile string) bool {
	b, err := os.ReadFile(statusFile)
	if err != nil {
		return false
	}
	var h struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		ContextWindow  struct {
			TotalInputTokens int64 `json:"total_input_tokens"`
		} `json:"context_window"`
	}
	if json.Unmarshal(b, &h) != nil || h.SessionID == "" {
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
