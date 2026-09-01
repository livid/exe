// Package peer implements node-to-node sync between exe daemons: a stable
// ed25519 node identity, a manually-enrolled peer list (paired via short-
// lived join codes), signed peer-to-peer requests, a per-file version
// manifest, and an engine that pushes app-data changes to every peer and
// reconciles differences on an interval. Transport is plain HTTP between
// daemons — peers are expected to reach each other over a tailnet
// (WireGuard), which provides the encryption.
package peer

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
)

// Identity is this node's stable keypair. The key deliberately is NOT the
// SSH service key: that one logs in to VMs, this one only identifies the
// node to its peers.
type Identity struct {
	ID   string // hex sha256 fingerprint of the public key, shortened
	Name string // hostname, for display in peer lists
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// LoadIdentity reads ~/.exe/peer_ed25519, generating it on first use.
func LoadIdentity(stateDir string) (*Identity, error) {
	p := filepath.Join(stateDir, "peer_ed25519")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		var pub ed25519.PublicKey
		var priv ed25519.PrivateKey
		if pub, priv, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		b = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return nil, err
		}
		return newIdentity(priv, pub), nil
	} else if err != nil {
		return nil, err
	}
	return parseIdentity(b, "peer_ed25519")
}

// LoadIdentityFile reads an existing PKCS8 ed25519 PEM — an identity
// other than the node's own, such as an agent's hub key. It never
// generates one: a missing file is an error, not a fresh identity.
func LoadIdentityFile(path string) (*Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseIdentity(b, filepath.Base(path))
}

func parseIdentity(b []byte, name string) (*Identity, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New(name + ": not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New(name + ": not an ed25519 key")
	}
	return newIdentity(priv, priv.Public().(ed25519.PublicKey)), nil
}

func newIdentity(priv ed25519.PrivateKey, pub ed25519.PublicKey) *Identity {
	host, _ := os.Hostname()
	return &Identity{ID: Fingerprint(pub), Name: host, priv: priv, pub: pub}
}

// Fingerprint is the node ID for a public key: 16 hex chars of its sha256.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// PubKey is the base64 raw public key, the wire form stored in peers.json.
func (id *Identity) PubKey() string {
	return base64.StdEncoding.EncodeToString(id.pub)
}

func (id *Identity) Sign(msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(id.priv, msg))
}
