package macos9

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// An agent's way into the Mac: a Chat pinned to it works inside the Mac
// over Tiger's Remote Login, on the "ssh" forward, as an account of the
// Mac, with a key of this machine's that the account authorizes once (the
// line Agent setup shows, pasted into the Mac's Terminal). Mac OS 9 has
// no shell, so the Mac OS 9 app's Mac has no agent.

// Agent is the way in as the status tells it to the window and the chat.
type Agent struct {
	// User is the Mac account the agent signs in as; "" until chosen.
	User string `json:"user"`
	// Key is the public key the account authorizes, one line.
	Key string `json:"key"`
	// Port is this machine's loopback port of Remote Login while the Mac
	// runs, 0 otherwise.
	Port int `json:"port,omitempty"`
}

// accountRE is a Mac account's short name, as Tiger's Accounts pane sets
// it: letters, digits, "_", "-" and ".", no spaces.
var accountRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}$`)

func (m *Manager) hasShell() bool {
	for _, f := range m.sys.forwards {
		if f.name == "ssh" {
			return true
		}
	}
	return false
}

// AgentKeyPath is the agent's private key; Agent mints it when missing.
func (m *Manager) AgentKeyPath() string { return m.path(filepath.Join("ssh", "id_rsa")) }

// Agent is the agent's way in, with the key minted on the first ask. nil
// for a Mac without a shell.
func (m *Manager) Agent() (*Agent, error) {
	if !m.hasShell() {
		return nil, nil
	}
	key, err := m.ensureAgentKey()
	if err != nil {
		return nil, err
	}
	a := &Agent{User: m.agentUser(), Key: key}
	m.mu.Lock()
	if m.running() && m.installed() {
		a.Port = m.forwardedLocked()["ssh"]
	}
	m.mu.Unlock()
	return a, nil
}

func (m *Manager) agentUser() string {
	var a struct {
		User string `json:"user"`
	}
	if b, err := os.ReadFile(m.path("agent.json")); err == nil {
		_ = json.Unmarshal(b, &a)
	}
	return a.User
}

// SetAgentUser records the Mac account the agent signs in as; "" forgets it.
func (m *Manager) SetAgentUser(user string) error {
	user = strings.TrimSpace(user)
	if user != "" && !accountRE.MatchString(user) {
		return errors.New("An account is its short name, as the Mac's Accounts pane shows it: letters, digits, _ - and ., no spaces.")
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(struct {
		User string `json:"user"`
	}{user})
	return os.WriteFile(m.path("agent.json"), append(b, '\n'), 0o600)
}

// ensureAgentKey returns the public key line, minting the pair when the
// Mac has none. An RSA key: Tiger's sshd knows no ed25519. A pair made
// by hand before this (ssh-keygen's) is kept, its public line read back
// from the private key when its .pub is missing.
func (m *Manager) ensureAgentKey() (string, error) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	priv := m.AgentKeyPath()
	if b, err := os.ReadFile(priv + ".pub"); err == nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n"); strings.HasPrefix(line, "ssh-") {
			return line, nil
		}
	}
	var signer ssh.Signer
	if b, err := os.ReadFile(priv); err == nil {
		if signer, err = ssh.ParsePrivateKey(b); err != nil {
			return "", err
		}
	} else {
		key, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return "", err
		}
		block, err := ssh.MarshalPrivateKey(key, "exe agent")
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(priv), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(priv, pem.EncodeToMemory(block), 0o600); err != nil {
			return "", err
		}
		if signer, err = ssh.NewSignerFromKey(key); err != nil {
			return "", err
		}
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " exe-agent"
	if err := os.WriteFile(priv+".pub", []byte(line+"\n"), 0o644); err != nil {
		return "", err
	}
	return line, nil
}
