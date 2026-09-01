package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
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
// such hook, so its status line shows the link state alone.

// agentStatusFile is where an agent's hook leaves its latest figures.
func (s *Server) agentStatusFile(a hostAgent) string {
	return filepath.Join(s.StateDir, "agents", a.app+".status.json")
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

// agentStatusArgs are the CLI arguments that install the hook, none for
// an agent without one. Claude Code's --settings takes a JSON object that
// ranks above every settings file, so a status line of the user's own
// (settings, normally ~/.claude/settings.json) would be hidden by the
// bridge: it is run on the same JSON afterwards and keeps drawing the
// in-terminal line. The bridge writes beside the file and moves the
// result into place last, so the daemon never reads half a write; it is
// a sh one-liner, which is why Windows does without.
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
	b, _ := json.Marshal(map[string]any{"statusLine": sl})
	return []string{"--settings", string(b)}
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

// pushAgentStatus follows an agent's status file for the life of a
// window, sending the figures as they stand on connect and each new
// version after. A poll, not a watch: the file changes a few times a
// minute at most, and a second's lag is invisible under the reply that
// caused it.
func pushAgentStatus(ctx context.Context, out *wsWriter, file string) {
	var seen time.Time
	var seenSize int64
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if st, err := os.Stat(file); err == nil && (!st.ModTime().Equal(seen) || st.Size() != seenSize) {
			seen, seenSize = st.ModTime(), st.Size()
			if b, err := os.ReadFile(file); err == nil {
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
