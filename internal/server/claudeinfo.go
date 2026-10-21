package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Get Info on a Claude Code conversation — a row of the window's column,
// or one started elsewhere — reads its transcript whole for what the
// column's own reading (readClaudeTranscript) leaves out: how many turns
// it ran and how many replies came back, the tokens those replies cost,
// the models that gave them, and the last prompt beside the first, so a
// session can be weighed before it is archived. The CLI writes an
// assistant message as one line per content block, each line carrying
// the whole message's usage, so tokens are counted once per message id.
// A transcript is read again only when it has grown (claudeInfoCache
// keys on the size read), as the column's reading is.

// claudeSessionInfo is what Get Info shows for one conversation.
type claudeSessionInfo struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`       // the person's own name for it, else the CLI's; "" for none
	Summary    string       `json:"summary"`     // the CLI's summary of it (a compaction summary, else its title), else the first prompt
	Prompt     string       `json:"prompt"`      // the first prompt
	LastPrompt string       `json:"last_prompt"` // the latest one
	Turns      int          `json:"turns"`       // prompts the person sent
	Replies    int          `json:"replies"`     // messages the model sent back
	Tokens     claudeTokens `json:"tokens"`
	Models     []string     `json:"models"` // in the order they first replied
	Created    int64        `json:"created"`
	Activity   int64        `json:"activity"` // the transcript's last write
	Cwd        string       `json:"cwd"`
	Origin     string       `json:"origin"` // where it was started, in words (claudeOrigin)
	size       int64
}

// claudeTokens is a conversation's token count, as the API reported it
// per reply: the input the model read, the output it wrote, and how much
// of the input came from the prompt cache or was written to it.
type claudeTokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

var claudeInfoCache sync.Map // path → *claudeSessionInfo

// claudeInfoLine is how much of a prompt Get Info shows: its first line,
// cut to this many runes.
const claudeInfoLine = 200

// claudeSessionInfoOf is a transcript's Get Info reading, from the cache
// when the file is the size it was read at; nil when the file cannot be
// read or holds no prompt yet.
func claudeSessionInfoOf(path string) *claudeSessionInfo {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if v, ok := claudeInfoCache.Load(path); ok {
		if i := v.(*claudeSessionInfo); i.size == st.Size() {
			i.Activity = st.ModTime().Unix()
			return i
		}
	}
	i := readClaudeSessionInfo(path, st.Size())
	if i != nil {
		i.Activity = st.ModTime().Unix()
		claudeInfoCache.Store(path, i)
	}
	return i
}

// readClaudeSessionInfo reads a transcript whole. A user entry is a turn
// when the person wrote it: not the CLI's own (meta, or opening with a
// tag), not a tool's result, and not a sub-agent's (a sidechain). An
// assistant entry counts once per message id — its blocks share the id
// and the usage — and a synthetic one (an error the CLI wrote in the
// model's place) not at all. nil for a transcript with no turn yet.
func readClaudeSessionInfo(path string, size int64) *claudeSessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	i := &claudeSessionInfo{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), size: size, Models: []string{}}
	var ai, custom, summary, lastID string
	seenModel := map[string]bool{}
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case bytes.Contains(line, []byte(`"type":"user"`)):
			var u struct {
				Type        string `json:"type"`
				IsMeta      bool   `json:"isMeta"`
				IsSidechain bool   `json:"isSidechain"`
				Entrypoint  string `json:"entrypoint"`
				Cwd         string `json:"cwd"`
				Timestamp   string `json:"timestamp"`
				Message     struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &u) != nil || u.Type != "user" || u.Message.Role != "user" {
				continue
			}
			if i.Cwd == "" {
				i.Cwd = u.Cwd
			}
			if i.Origin == "" && u.Entrypoint != "" {
				i.Origin = claudeOrigin(u.Entrypoint)
			}
			if i.Created == 0 {
				if ts, err := time.Parse(time.RFC3339Nano, u.Timestamp); err == nil {
					i.Created = ts.Unix()
				}
			}
			if u.IsMeta || u.IsSidechain {
				continue
			}
			if p := claudeTurnPrompt(u.Message.Content); p != "" {
				i.Turns++
				if i.Prompt == "" {
					i.Prompt = p
				}
				i.LastPrompt = p
			}
		case bytes.Contains(line, []byte(`"type":"assistant"`)):
			var a struct {
				Type    string `json:"type"`
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage struct {
						Input      int64 `json:"input_tokens"`
						Output     int64 `json:"output_tokens"`
						CacheWrite int64 `json:"cache_creation_input_tokens"`
						CacheRead  int64 `json:"cache_read_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &a) != nil || a.Type != "assistant" || a.Message.ID == "" || a.Message.ID == lastID {
				continue
			}
			if strings.HasPrefix(a.Message.Model, "<") { // "<synthetic>": the CLI's own words, no reply
				continue
			}
			lastID = a.Message.ID
			i.Replies++
			i.Tokens.Input += a.Message.Usage.Input
			i.Tokens.Output += a.Message.Usage.Output
			i.Tokens.CacheRead += a.Message.Usage.CacheRead
			i.Tokens.CacheWrite += a.Message.Usage.CacheWrite
			if m := a.Message.Model; m != "" && !seenModel[m] {
				seenModel[m] = true
				i.Models = append(i.Models, m)
			}
		case bytes.Contains(line, []byte(`"type":"ai-title"`)):
			var l struct {
				Type  string `json:"type"`
				Title string `json:"aiTitle"`
			}
			if json.Unmarshal(line, &l) == nil && l.Type == "ai-title" && l.Title != "" {
				ai = l.Title
			}
		case bytes.Contains(line, []byte(`"type":"custom-title"`)):
			var l struct {
				Type  string `json:"type"`
				Title string `json:"customTitle"`
			}
			if json.Unmarshal(line, &l) == nil && l.Type == "custom-title" && l.Title != "" {
				custom = l.Title
			}
		case bytes.Contains(line, []byte(`"type":"summary"`)):
			// the CLI's own summary of the conversation so far, written
			// when it compacts the context; the last is the latest
			var l struct {
				Type    string `json:"type"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal(line, &l) == nil && l.Type == "summary" && l.Summary != "" {
				summary = l.Summary
			}
		}
	}
	if i.Turns == 0 {
		return nil
	}
	if i.Title = custom; i.Title == "" {
		i.Title = ai
	}
	i.Title = firstLine(i.Title, 60)
	switch {
	case summary != "":
		i.Summary = firstLine(summary, claudeInfoLine)
	case ai != "":
		i.Summary = firstLine(ai, claudeInfoLine)
	default:
		i.Summary = i.Prompt
	}
	if i.Origin == "" {
		i.Origin = claudeOrigin("")
	}
	return i
}

// claudeTurnPrompt is the person's text in a user entry's content, as
// claudePrompt reads it but to Get Info's length, and "" for a tool's
// result — an entry whose blocks hold one is the tool's turn, not the
// person's, whatever text rides along.
func claudeTurnPrompt(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return claudeTurnText(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return ""
		}
	}
	for _, b := range blocks {
		if b.Type == "text" {
			if t := claudeTurnText(b.Text); t != "" {
				return t
			}
		}
	}
	return ""
}

func claudeTurnText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || claudeOwnText(s) { // the CLI's own words (claudeprompts.go), not a prompt that opens with pasted markup
		return ""
	}
	if said, ok := watcherPromptInput(s); ok { // the posts in a hub watcher's build prompt (claudeprompts.go)
		s = said
	}
	return firstLine(s, claudeInfoLine)
}

// readClaudeStatusSession is the session and transcript a column
// session's status file names (the status-line hook's JSON, or the stub
// noteClaudeSession leaves): "" for a session with no conversation yet.
func readClaudeStatusSession(statusFile string) (id, transcript string) {
	b, err := os.ReadFile(statusFile)
	if err != nil {
		return "", ""
	}
	var h struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
	}
	json.Unmarshal(b, &h)
	return h.SessionID, h.TranscriptPath
}
