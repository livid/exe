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
	"math/big"
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

// Address is the same public key as a Solana wallet writes one: its 32
// bytes in base58. A token-gated hub looks for its token at this
// address, so it is what the node's owner funds, or hands an admin to be
// invited by.
func (id *Identity) Address() string { return Base58(id.pub) }

const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Base58 is Bitcoin's base58, the one Solana addresses are in: the bytes
// as one big number, and a "1" for every leading zero byte.
func Base58(b []byte) string {
	n := new(big.Int).SetBytes(b)
	radix := big.NewInt(58)
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, radix, mod)
		out = append(out, b58Alphabet[mod.Int64()])
	}
	for _, c := range b {
		if c != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func (id *Identity) Sign(msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(id.priv, msg))
}
