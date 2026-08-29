// Package sshexec runs commands and transfers files over SSH.
package sshexec

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

// DialFunc opens the TCP connection to a target, overriding net.Dial for
// backends whose guest network lives inside the daemon process.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type Target struct {
	Host    string
	User    string
	KeyPath string
	// Dialer, when set, replaces the default TCP dial to Host:22.
	Dialer DialFunc
}

func (t Target) dial(ctx context.Context) (*ssh.Client, error) {
	key, err := os.ReadFile(t.KeyPath)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	dialTCP := t.Dialer
	if dialTCP == nil {
		d := net.Dialer{Timeout: 10 * time.Second}
		dialTCP = d.DialContext
	}
	conn, err := dialTCP(ctx, "tcp", net.JoinHostPort(t.Host, "22"))
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, t.Host, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// Dial opens an SSH client connection to the target; the caller closes it.
func (t Target) Dial(ctx context.Context) (*ssh.Client, error) { return t.dial(ctx) }

// Run executes a command via the remote user's shell and returns combined
// output (truncated to maxOut bytes) plus the exit code.
func (t Target) Run(ctx context.Context, command string, maxOut int) (string, int, error) {
	client, err := t.dial(ctx)
	if err != nil {
		return "", -1, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer sess.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sess.Close()
			client.Close()
		case <-done:
		}
	}()
	out, err := sess.CombinedOutput(command)
	close(done)
	text := Truncate(string(out), maxOut)
	if err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			return text, ee.ExitStatus(), nil
		}
		if ctx.Err() != nil {
			return text, -1, fmt.Errorf("command canceled or timed out: %w", ctx.Err())
		}
		return text, -1, err
	}
	return text, 0, nil
}

func (t Target) WriteFile(ctx context.Context, p string, data []byte) error {
	client, err := t.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = bytes.NewReader(data)
	// Stage the upload in /tmp, then copy into the target in place: a
	// dropped connection mid-stream can only truncate the staging file,
	// never the target, and the in-place copy keeps every cat-> semantic
	// a rename would break — permission checks, inode/owner/hard links,
	// symlink write-through, umask, loud failure on directories.
	cmd := fmt.Sprintf(
		`t=$(mktemp) || exit 1; cat > "$t" && mkdir -p %s && cat "$t" > %s; s=$?; rm -f "$t"; exit $s`,
		shq(path.Dir(p)), shq(p))
	if out, err := sess.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("write %s: %v: %s", p, err, out)
	}
	return nil
}

// ReadFile returns the file's content, truncated to maxOut bytes. A
// positive offset starts at that 1-based line; a positive limit caps the
// number of lines read.
func (t Target) ReadFile(ctx context.Context, p string, offset, limit, maxOut int) (string, error) {
	clamp := func(v int) int { // model-supplied numbers: keep sed addresses sane
		return min(max(v, 0), 100_000_000)
	}
	offset, limit = clamp(offset), clamp(limit)
	ranged := offset > 0 || limit > 0
	cmd := "cat " + shq(p)
	if ranged {
		start := max(offset, 1)
		if limit > 0 {
			cmd = fmt.Sprintf("sed -n '%d,%dp' %s", start, start+limit-1, shq(p))
		} else {
			cmd = fmt.Sprintf("sed -n '%d,$p' %s", start, shq(p))
		}
	}
	out, code, err := t.Run(ctx, cmd, maxOut)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("read %s: exit %d: %s", p, code, strings.TrimSpace(out))
	}
	if ranged && out == "" {
		return "(no lines in that range — offset is past the end of the file)", nil
	}
	return out, nil
}

// editMax caps the file size EditFile will pull into memory; the read runs
// through head -c so a giant file never crosses the wire at all.
const editMax = 5 << 20

// EditFile applies a string-replace edit to a remote text file: read it in
// full (never truncated — an edit written back from an elided read would
// destroy the file), replace, write back. Returns the replacement count.
func (t Target) EditFile(ctx context.Context, p, oldStr, newStr string, replaceAll bool) (int, error) {
	out, code, err := t.Run(ctx, fmt.Sprintf("head -c %d %s", editMax+1, shq(p)), 0)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("read %s: exit %d: %s", p, code, strings.TrimSpace(out))
	}
	if len(out) > editMax {
		return 0, fmt.Errorf("%s exceeds %d bytes — too large for edit_file; use bash (sed, patch) instead", p, editMax)
	}
	edited, n, err := ReplaceEdit(out, oldStr, newStr, replaceAll)
	if err != nil {
		return 0, fmt.Errorf("edit %s: %w", p, err)
	}
	if err := t.WriteFile(ctx, p, []byte(edited)); err != nil {
		return 0, err
	}
	return n, nil
}

// ReplaceEdit replaces oldStr with newStr in content: exactly one match
// unless replaceAll. When no exact match exists it retries comparing line
// by line with trailing whitespace ignored — absorbing the two most common
// model slips (trailing spaces, CRLF endings), which can't relocate a
// match. Errors tell the model what to do differently.
func ReplaceEdit(content, oldStr, newStr string, replaceAll bool) (string, int, error) {
	if oldStr == "" {
		return "", 0, fmt.Errorf("old_string is empty — to create or fully rewrite a file, use write_file")
	}
	if oldStr == newStr {
		return "", 0, fmt.Errorf("old_string and new_string are identical")
	}
	if n := strings.Count(content, oldStr); n > 0 {
		if n > 1 && !replaceAll {
			return "", 0, fmt.Errorf("old_string appears %d times — include more surrounding lines to make it unique, or set replace_all", n)
		}
		return strings.Replace(content, oldStr, newStr, n), n, nil
	}
	// A trailing newline on oldStr would demand a matching extra line below;
	// drop it (and its twin on newStr) — the matched region excludes the
	// final line terminator anyway.
	fOld, fNew := oldStr, newStr
	wholeLines := strings.HasSuffix(fOld, "\n")
	if wholeLines {
		fOld = strings.TrimSuffix(fOld, "\n")
		fNew = strings.TrimSuffix(fNew, "\n")
	}
	spans := lineTrimmedFind(content, fOld)
	switch {
	case len(spans) == 0:
		return "", 0, notFoundErr(content, oldStr)
	case len(spans) > 1 && !replaceAll:
		return "", 0, fmt.Errorf("old_string matches %d places once trailing whitespace is ignored — include more surrounding lines to make it unique, or set replace_all", len(spans))
	}
	if wholeLines && fNew == "" {
		// Deleting whole lines: swallow the final line terminator too, so
		// the deletion doesn't leave a blank line behind.
		for i, s := range spans {
			if s.end < len(content) && content[s.end] == '\n' {
				spans[i].end = s.end + 1
			}
		}
	}
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(content[prev:s.start])
		b.WriteString(adaptEOL(fNew, content[s.start:s.end]))
		prev = s.end
	}
	b.WriteString(content[prev:])
	return b.String(), len(spans), nil
}

type span struct{ start, end int }

// lineTrimmedFind returns the byte range of each place oldStr matches
// content when every line is compared with trailing whitespace (spaces,
// tabs, \r) stripped. Ranges run from the start of the first matched line
// to the end of the last one, excluding its line terminator. Matches never
// overlap; an all-whitespace oldStr matches nothing.
func lineTrimmedFind(content, oldStr string) []span {
	old := strings.Split(oldStr, "\n")
	blank := true
	for i, l := range old {
		old[i] = trimEOL(l)
		if old[i] != "" {
			blank = false
		}
	}
	if blank {
		return nil
	}
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	lineEnd := func(i int) int { // end of line i's content, excluding \n
		if i+1 < len(starts) {
			return starts[i+1] - 1
		}
		return len(content)
	}
	n := len(starts)
	if strings.HasSuffix(content, "\n") {
		n-- // the phantom empty line after a final newline is not matchable
	}
	trimmed := make([]string, n)
	for i := range trimmed {
		trimmed[i] = trimEOL(content[starts[i]:lineEnd(i)])
	}
	var out []span
	for i := 0; i+len(old) <= n; i++ {
		ok := true
		for j := range old {
			if trimmed[i+j] != old[j] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, span{starts[i], lineEnd(i + len(old) - 1)})
			i += len(old) - 1
		}
	}
	return out
}

// trimEOL strips the trailing whitespace the fuzzy matcher ignores — the
// one definition shared by matching and diagnosis so they can't drift.
func trimEOL(s string) string { return strings.TrimRight(s, " \t\r") }

// adaptEOL rewrites newStr's line endings to match the region it replaces,
// so a fuzzy match never changes the file's convention in either direction
// (LF text spliced into a CRLF file, or CRLF text into an LF file).
func adaptEOL(newStr, region string) string {
	s := strings.TrimSuffix(strings.ReplaceAll(newStr, "\r\n", "\n"), "\r")
	if strings.Contains(region, "\r\n") || strings.HasSuffix(region, "\r") {
		s = strings.ReplaceAll(s, "\n", "\r\n")
		if strings.HasSuffix(region, "\r") {
			s += "\r"
		}
	}
	return s
}

// ReadCap is the byte cap the tool surfaces apply to read_file output;
// past it the middle of the file is elided, so a model that read a bigger
// file has never seen all of it. agent and server derive their caps from
// this constant so the elision hint below can't drift out of sync.
const ReadCap = 12000

// unescaper decodes the escape sequences an over-escaping model leaves in
// old_string, for the diagnosis in notFoundErr.
var unescaper = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r")

// notFoundErr diagnoses why oldStr missed so the model can correct in one
// step instead of retrying blind.
func notFoundErr(content, oldStr string) error {
	const base = "old_string not found — it must match the file exactly, whitespace included"
	// An old_string containing \\ is likely quoting source code whose
	// backslash sequences are literal; the decode test is unreliable there.
	if !strings.Contains(oldStr, `\\`) {
		if un := unescaper.Replace(oldStr); un != oldStr && strings.Contains(content, un) {
			return fmt.Errorf(base + `; old_string arrived with literal \n/\t escape sequences where the file has real newlines and tabs — resend old_string and new_string without the extra escaping`)
		}
	}
	var hints []string
	if h := nearMissHint(content, oldStr); h != "" {
		hints = append(hints, h)
	}
	if len(content) > ReadCap {
		hints = append(hints, fmt.Sprintf("the file is %d bytes and read_file elides the middle of files this large — locate the exact current text with bash: grep -n, or read_file with offset/limit", len(content)))
	} else {
		hints = append(hints, "read_file to check the current content")
	}
	return fmt.Errorf("%s; %s", base, strings.Join(hints, "; "))
}

// nearMissHint locates the line window closest to oldStr and names the
// first line that differs, showing the model where its copy diverged.
func nearMissHint(content, oldStr string) string {
	oldRaw := strings.Split(strings.TrimSuffix(oldStr, "\n"), "\n")
	old := make([]string, len(oldRaw))
	anchor := -1
	for i, l := range oldRaw {
		old[i] = trimEOL(l)
		if anchor < 0 && old[i] != "" {
			anchor = i
		}
	}
	if anchor < 0 {
		return ""
	}
	fileRaw := strings.Split(content, "\n")
	fileTrim := make([]string, len(fileRaw))
	for i, l := range fileRaw {
		fileTrim[i] = trimEOL(l)
	}
	best, bestStart, scored := -1, 0, 0
	for i := range fileTrim {
		if fileTrim[i] != old[anchor] || i < anchor {
			continue
		}
		start, score := i-anchor, 0
		for j := 0; j < len(old) && start+j < len(fileTrim); j++ {
			if fileTrim[start+j] == old[j] {
				score++
			}
		}
		if score > best {
			best, bestStart = score, start
		}
		// The hint needs a plausible candidate, not the global optimum:
		// cap the work on pathological files full of anchor matches.
		if scored++; scored >= 1000 {
			break
		}
	}
	if best < 0 {
		return fmt.Sprintf("the first line of old_string (%q) appears nowhere in the file — old_string may be from stale or misremembered content", clip(oldRaw[anchor]))
	}
	for j := range old {
		if bestStart+j >= len(fileTrim) || fileTrim[bestStart+j] != old[j] {
			fileLine := "<end of file>"
			if bestStart+j < len(fileRaw) {
				fileLine = fileRaw[bestStart+j]
			}
			return fmt.Sprintf("the closest match starts at line %d but diverges at line %d, where the file has %q and old_string has %q",
				bestStart+1, bestStart+j+1, clip(fileLine), clip(oldRaw[j]))
		}
	}
	return ""
}

// clip bounds a line quoted into an error message, cutting on a rune
// boundary so the quoted text never ends mid-character.
func clip(s string) string {
	if len(s) <= 80 {
		return s
	}
	i := 77
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i] + "..."
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Quote returns s single-quoted for a POSIX shell.
func Quote(s string) string { return shq(s) }

// Truncate keeps the head and tail of s when it exceeds max bytes.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	half := max / 2
	return s[:half] + fmt.Sprintf("\n... [%d bytes truncated] ...\n", len(s)-max) + s[len(s)-half:]
}
