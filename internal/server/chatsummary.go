package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"exe/internal/agent"
	"exe/internal/chat"
	"exe/internal/codex"
	"exe/internal/config"
)

// Session summaries: after a run in which the agent did meaningful work
// (executed tools), a detached model call writes a one-line summary of
// what the session accomplished into its Meta, where the session lists
// (the Chat window, the VM detail's Sessions tab) surface it. Sessions
// from before this feature are backfilled once per boot.

const (
	// chatSummaryChars caps the stored summary; the digest fed to the model
	// keeps the opening ask plus the most recent work.
	chatSummaryChars      = 200
	chatDigestHead        = 2000
	chatDigestTail        = 8000
	chatSummaryTimeout  = 90 * time.Second
	chatBackfillDelay   = 15 * time.Second
	chatBackfillSpacing = 2 * time.Second
	chatSummarySystem   = "You write one-line summaries of chat sessions in which an AI agent operates Linux VMs."
)

// saveChat persists a run's in-memory session. Every session write after
// creation goes through chatSaveMu so the summary writer's
// load-modify-write can never push stale messages back to disk next to a
// live run's save.
func (s *Server) saveChat(sess *chat.Session) error {
	s.chatSaveMu.Lock()
	defer s.chatSaveMu.Unlock()
	return chat.Save(s.chatDir(), sess)
}

func (s *Server) setChatSummary(id, summary string) error {
	s.chatSaveMu.Lock()
	defer s.chatSaveMu.Unlock()
	return chat.SetSummary(s.chatDir(), id, summary)
}

// summarizeChat decides whether sess deserves a (re)generated summary
// after a finished run and writes one. base is the run's first appended
// message index (run.base): tool messages from there on mean the run did
// meaningful work, which refreshes an existing summary; a session that
// only ever talked gets one summary and keeps it. Failures only log — the
// next qualifying run tries again.
func (s *Server) summarizeChat(cfg *config.Config, provider string, sess *chat.Session, base int) {
	worked := false
	if base < 0 {
		base = 0
	}
	for _, m := range sess.Messages[min(base, len(sess.Messages)):] {
		if m.Role == "tool" {
			worked = true
			break
		}
	}
	if !worked && sess.Summary != "" {
		return
	}
	replied := false
	for _, m := range sess.Messages {
		if m.Role == "assistant" && (strings.TrimSpace(m.Content) != "" || len(m.ToolCalls) > 0) {
			replied = true
			break
		}
	}
	if !replied {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatSummaryTimeout)
	defer cancel()
	summary, err := s.chatSummaryModel(ctx, cfg, provider, sess)
	if err != nil {
		log.Printf("chat summary %s: %v", sess.ID, err)
		return
	}
	if summary == "" {
		return
	}
	if err := s.setChatSummary(sess.ID, summary); err != nil {
		log.Printf("chat summary %s: %v", sess.ID, err)
	}
}

// chatDigest flattens the session into a compact transcript for the
// summarizer: prompts and replies trimmed, tool calls as their one-line
// summaries, tool output down to a taste. Long sessions keep the opening
// ask and the most recent work.
func chatDigest(sess *chat.Session) string {
	trim := func(t string, n int) string {
		t = strings.Join(strings.Fields(t), " ")
		if len(t) > n {
			t = t[:n] + "…"
		}
		return t
	}
	var lines []string
	for _, m := range sess.Messages {
		switch m.Role {
		case "user":
			lines = append(lines, "User: "+trim(m.Content, 500))
		case "assistant":
			if t := trim(m.Content, 500); t != "" {
				lines = append(lines, "Agent: "+t)
			}
			for _, tc := range m.ToolCalls {
				args := agent.ParseArgs(tc.Function.Arguments)
				lines = append(lines, "Ran: "+trim(chatToolSummary(tc.Function.Name, args), 200))
			}
		case "tool":
			if t := trim(m.Content, 200); t != "" {
				lines = append(lines, "Result: "+t)
			}
		}
	}
	d := strings.Join(lines, "\n")
	if len(d) > chatDigestHead+chatDigestTail {
		d = d[:chatDigestHead] + "\n[…]\n" + d[len(d)-chatDigestTail:]
	}
	return d
}

// chatSummaryModel asks the session's own backend for the one-liner —
// non-streaming, no tools, minimal effort.
func (s *Server) chatSummaryModel(ctx context.Context, cfg *config.Config, provider string, sess *chat.Session) (string, error) {
	scope := "a personal VM cloud"
	if sess.VM != "" {
		scope = fmt.Sprintf("the VM %q", sess.VM)
	}
	msgs := []agent.Message{
		{Role: "system", Content: chatSummarySystem},
		{Role: "user", Content: fmt.Sprintf(
			"Transcript digest of a session operating %s:\n\n%s\n\n"+
				"Write ONE sentence (under 140 characters) stating concretely what was accomplished or answered — name the app, service, port, URL, fix or answer. Plain text: no preamble, no quotes, no markdown.",
			scope, chatDigest(sess))},
	}
	var text string
	if provider == "openai" {
		creds, err := s.codexToken(ctx, false)
		if err != nil {
			return "", err
		}
		ccfg := codex.ClientConfig{AccessToken: creds.AccessToken, AccountID: creds.AccountID,
			Model: cfg.OpenAI.Model, Effort: "low", SessionKey: sess.ID + "-summary"}
		msg, err := codex.ChatStream(ctx, ccfg, msgs, nil, nil)
		if errors.Is(err, codex.ErrUnauthorized) {
			if creds, err = s.codexToken(ctx, true); err != nil {
				return "", err
			}
			ccfg.AccessToken, ccfg.AccountID = creds.AccessToken, creds.AccountID
			msg, err = codex.ChatStream(ctx, ccfg, msgs, nil, nil)
		}
		if err != nil {
			return "", err
		}
		text = msg.Content
	} else {
		// The lowest thinking level, not "off": a reasoning model told not
		// to think (glm-5.3) reasons out loud in the content instead, and
		// the summary would open with its chain of thought.
		acfg := agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey,
			Model: cfg.Ollama.Model, Effort: "low"}
		resp, err := agent.Chat(ctx, acfg, msgs, nil)
		if err != nil {
			return "", err
		}
		text = resp.Message.Content
	}
	return trimSummary(text), nil
}

// trimSummary flattens the model's reply into the stored one-liner.
func trimSummary(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	t = strings.Trim(t, `"“”'`)
	if len(t) > chatSummaryChars {
		cut := t[:chatSummaryChars]
		if i := strings.LastIndexByte(cut, ' '); i > chatSummaryChars/2 {
			cut = cut[:i]
		}
		t = cut + "…"
	}
	return t
}

// BackfillChatMeta repairs stored session metadata on boot: titles cut
// under the old 60-byte cap are re-derived from the first user message,
// and sessions that predate summaries get one, spaced out so a boot
// never bursts model calls. Both passes are no-ops once every session is
// current, so running once per boot converges.
func (s *Server) BackfillChatMeta() {
	time.Sleep(chatBackfillDelay)
	metas, err := chat.List(s.chatDir())
	if err != nil {
		log.Printf("chat backfill: %v", err)
		return
	}
	s.backfillChatTitles(metas)
	cfg := s.Config()
	provider := chatProvider(cfg)
	if provider == "openai" {
		if s.codexCreds() == nil {
			return // not signed in — next boot retries the summaries
		}
	} else if cfg.Ollama.BaseURL == "" {
		return
	}
	n := 0
	for _, m := range metas {
		if m.Summary != "" {
			continue
		}
		if _, live := s.chatRuns.Load(m.ID); live {
			continue // its run will summarize when it finishes
		}
		sess, err := chat.Load(s.chatDir(), m.ID)
		if err != nil {
			continue
		}
		if n > 0 {
			time.Sleep(chatBackfillSpacing)
		}
		n++
		s.summarizeChat(cfg, provider, sess, 0)
	}
	if n > 0 {
		log.Printf("chat summaries: backfilled up to %d session(s)", n)
	}
}

// backfillChatTitles re-derives each stored title from the session's
// first user message. No model involved, so it runs even with no chat
// backend configured; a title already matching its re-derivation (every
// session created after the wider cap) costs one read and no write.
func (s *Server) backfillChatTitles(metas []chat.Meta) {
	n := 0
	for _, m := range metas {
		if _, live := s.chatRuns.Load(m.ID); live {
			continue
		}
		sess, err := chat.Load(s.chatDir(), m.ID)
		if err != nil {
			continue
		}
		want := ""
		for _, msg := range sess.Messages {
			if msg.Role == "user" {
				want = chat.Title(msg.Content)
				break
			}
		}
		if want == "" || want == sess.Title {
			continue
		}
		s.chatSaveMu.Lock()
		err = chat.SetTitle(s.chatDir(), m.ID, want)
		s.chatSaveMu.Unlock()
		if err != nil {
			log.Printf("chat title %s: %v", m.ID, err)
			continue
		}
		n++
	}
	if n > 0 {
		log.Printf("chat titles: re-derived %d session title(s)", n)
	}
}
