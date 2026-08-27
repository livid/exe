package agent

import (
	"fmt"
	"strings"

	"exe/internal/sshexec"
)

// Context management: a long conversation is never truncated on disk —
// instead the model's *view* of it is bounded. Once the live tail passes
// CompactAt (bytes stand in for tokens at roughly 4:1), the older messages
// are folded into a running digest by a model call; the view becomes
// system prompt(s) + digest + verbatim tail. Compaction runs proactively
// at turn start and reactively when a backend rejects the request for
// context length.

const (
	// CompactAt is the view size that triggers compaction — ~50k tokens,
	// comfortably inside every backend's window with room for the reply.
	CompactAt = 200 << 10
	// CompactKeep is the verbatim tail left after a compaction.
	CompactKeep = 48 << 10
	// DigestMax caps the running digest itself.
	DigestMax = 4000
)

// Compact is a conversation's compaction state: the model's view starts at
// message index Through, preceded by Digest, which summarizes everything
// before it. The zero value means nothing is compacted.
type Compact struct {
	Through int    `json:"through"`
	Digest  string `json:"digest"`
}

// Msg renders the digest as the system message that stands in for the
// folded history.
func (c *Compact) Msg() Message {
	return Message{Role: "system", Content: "Summary of the earlier conversation (older messages were condensed into this):\n" + c.Digest}
}

func msgSize(m Message) int {
	n := len(m.Content)
	for _, tc := range m.ToolCalls {
		n += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	for _, it := range m.CodexItems { // encrypted reasoning is replayed verbatim — it counts
		n += len(it)
	}
	return n
}

// ApproxSize estimates a message list's request weight in bytes.
func ApproxSize(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += msgSize(m)
	}
	return n
}

// CompactCut picks how many leading messages of conv to fold into the
// digest, leaving a verbatim tail of roughly keep bytes. The cut always
// lands on a user or assistant message so no tool result is orphaned from
// the call that produced it (the ChatGPT backend rejects an output whose
// call was dropped). 0 means no cut is possible.
func CompactCut(conv []Message, keep int) int {
	size := 0
	cut := len(conv)
	for i := len(conv) - 1; i > 0; i-- {
		size += msgSize(conv[i])
		if size > keep {
			break
		}
		if conv[i].Role == "user" || conv[i].Role == "assistant" {
			cut = i
		}
	}
	if cut < len(conv) {
		return cut
	}
	// no boundary yields a tail under keep (one enormous turn): take the
	// last boundary so compaction still makes progress
	for i := len(conv) - 1; i > 0; i-- {
		if conv[i].Role == "user" || conv[i].Role == "assistant" {
			return i
		}
	}
	return 0
}

// DigestSystem instructs the digest model call.
const DigestSystem = `You maintain the running summary of a long conversation so it can continue with its older messages replaced by the summary. Write the complete updated summary: the user's goals and requests, what was built or changed (VMs, files, services, ports, routes), key decisions and why, the current state, and unresolved items. Fold the previous summary in — keep what still matters, drop what no longer does. Be specific and compact, under 2000 characters. Reply with the summary text only.`

// RenderForDigest flattens the digest-so-far plus a conversation segment
// into the text the digest call reads. Long fields are elided — the digest
// needs the shape of what happened, not full payloads.
func RenderForDigest(prev string, seg []Message) string {
	var b strings.Builder
	if strings.TrimSpace(prev) != "" {
		b.WriteString("Current summary (extend it):\n" + prev + "\n\nNew conversation segment to fold in:\n")
	} else {
		b.WriteString("Conversation segment to summarize:\n")
	}
	for _, m := range seg {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "\n[user]\n%s\n", sshexec.Truncate(m.Content, 2000))
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "\n[assistant]\n%s\n", sshexec.Truncate(m.Content, 2000))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[called %s %s]\n", tc.Function.Name, sshexec.Truncate(string(tc.Function.Arguments), 300))
			}
		case "tool":
			fmt.Fprintf(&b, "[%s result] %s\n", m.ToolName, sshexec.Truncate(m.Content, 500))
		}
	}
	return sshexec.Truncate(b.String(), 64000)
}

// IsContextOverflow spots a backend rejecting the request for size, the
// signal for a reactive compaction and retry.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"context length", "context window", "context_length", "maximum context",
		"prompt is too long", "input is too long", "too many tokens", "exceeds the limit",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
