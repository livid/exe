// Package chat persists chat sessions for the web UI's Chat window: one
// JSON file per session under ~/.exe/chat, holding the full Ollama message
// history (including tool calls) so a session can be resumed any time.
package chat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"exe/internal/agent"
)

type Meta struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// VM pins the session to a single VM: the agent's tools are scoped to
	// it server-side. Empty for whole-daemon operator sessions.
	VM        string    `json:"vm,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Session struct {
	Meta
	Messages []agent.Message `json:"messages"`
}

// New creates and saves an empty session titled after the first prompt,
// pinned to vm when non-empty.
func New(dir, title, vm string) (*Session, error) {
	suffix := make([]byte, 2)
	rand.Read(suffix)
	now := time.Now().UTC()
	s := &Session{Meta: Meta{
		ID:        fmt.Sprintf("%d-%s", now.UnixMilli(), hex.EncodeToString(suffix)),
		Title:     Title(title),
		VM:        vm,
		CreatedAt: now,
		UpdatedAt: now,
	}}
	return s, Save(dir, s)
}

// Title trims a prompt into a session list label.
func Title(prompt string) string {
	t := strings.Join(strings.Fields(prompt), " ")
	if len(t) > 60 {
		t = t[:60] + "…"
	}
	if t == "" {
		t = "New chat"
	}
	return t
}

func path(dir, id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return "", fmt.Errorf("bad session id")
	}
	return filepath.Join(dir, id+".json"), nil
}

func Save(dir string, s *Session) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p, err := path(dir, s.ID)
	if err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	// Temp file + rename: sessions are re-read while a reply streams (the
	// session list, re-attaching clients), so a truncate-in-place write
	// would hand those readers a torn file.
	tmp, err := os.CreateTemp(dir, s.ID+"-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func Load(dir, id string) (*Session, error) {
	p, err := path(dir, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func Delete(dir, id string) error {
	p, err := path(dir, id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// List returns session metadata, most recently updated first. A missing
// directory is empty.
func List(dir string) ([]Meta, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Meta{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Meta{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if json.Unmarshal(b, &s) == nil && s.ID != "" {
			out = append(out, s.Meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
