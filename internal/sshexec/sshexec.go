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
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s", shq(path.Dir(p)), shq(p))
	if out, err := sess.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("write %s: %v: %s", p, err, out)
	}
	return nil
}

func (t Target) ReadFile(ctx context.Context, p string, maxOut int) (string, error) {
	out, code, err := t.Run(ctx, "cat "+shq(p), maxOut)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("cat exited %d: %s", code, out)
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
// unless replaceAll. Errors tell the model what to do differently.
func ReplaceEdit(content, oldStr, newStr string, replaceAll bool) (string, int, error) {
	if oldStr == "" {
		return "", 0, fmt.Errorf("old_string is empty — to create or fully rewrite a file, use write_file")
	}
	if oldStr == newStr {
		return "", 0, fmt.Errorf("old_string and new_string are identical")
	}
	n := strings.Count(content, oldStr)
	switch {
	case n == 0:
		return "", 0, fmt.Errorf("old_string not found — it must match the file exactly, whitespace included; read_file to check the current content")
	case n > 1 && !replaceAll:
		return "", 0, fmt.Errorf("old_string appears %d times — include more surrounding lines to make it unique, or set replace_all", n)
	}
	return strings.Replace(content, oldStr, newStr, n), n, nil
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
