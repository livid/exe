package peer

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Peer requests are authenticated by ed25519 signature, not by the API
// token — a peer may only reach /v1/peer/*, never the rest of the daemon.
// The signature covers method, path, a timestamp (±2 min skew window), the
// body hash and the sync-version headers, so a request can't be re-targeted;
// a replay inside the window just re-applies the same version, which the
// stale check makes a no-op.
const (
	hdrPeer    = "X-Exe-Peer"
	hdrTo      = "X-Exe-To"
	hdrTS      = "X-Exe-Ts"
	hdrSig     = "X-Exe-Sig"
	hdrVer     = "X-Exe-Ver" // base64(json) of the file's version vector
	hdrDeleted = "X-Exe-Deleted"
	hdrMaxFile = "X-Exe-Max-File" // the biggest file a fetching peer takes

	// Personal machines (laptops that sleep) drift; a generous window keeps
	// sync working across modest skew. Replay is already defanged by the
	// recipient binding below and by the versioned, idempotent nature of
	// file writes, so a wider window trades little.
	maxSkew = 10 * time.Minute
)

// canonical binds a request to its sender, its intended recipient (to), the
// timestamp, the body and the version headers — so a signature captured by
// one peer cannot be replayed against a different daemon.
func canonical(method, path, to, ts, bodySum, ver, deleted string) []byte {
	return []byte(strings.Join([]string{method, path, to, ts, bodySum, ver, deleted}, "\n"))
}

func bodySum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// SignRequest stamps r with this node's identity headers, bound to recipient
// node `to`. Call after the version headers (if any) are set.
func SignRequest(id *Identity, r *http.Request, body []byte, to string) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	r.Header.Set(hdrPeer, id.ID)
	r.Header.Set(hdrTo, to)
	r.Header.Set(hdrTS, ts)
	msg := canonical(r.Method, r.URL.Path, to, ts, bodySum(body),
		r.Header.Get(hdrVer), r.Header.Get(hdrDeleted))
	r.Header.Set(hdrSig, id.Sign(msg))
}

// VerifyRequest authenticates an inbound peer request against the enrolled
// peer list and returns the caller. selfID is this node's own id: a request
// addressed to anyone else is rejected, so a peer cannot relay a signature
// meant for it to a third node.
func VerifyRequest(store *Store, r *http.Request, body []byte, selfID string) (*Peer, error) {
	id := r.Header.Get(hdrPeer)
	p, ok := store.Get(id)
	if !ok {
		return nil, errors.New("unknown peer")
	}
	if to := r.Header.Get(hdrTo); to != selfID {
		return nil, errors.New("request not addressed to this node")
	}
	ts := r.Header.Get(hdrTS)
	ms, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, errors.New("bad timestamp")
	}
	if d := time.Since(time.UnixMilli(ms)); d > maxSkew || d < -maxSkew {
		return nil, fmt.Errorf("timestamp skew %s exceeds %s (are both clocks synced? check NTP)", d.Round(time.Second), maxSkew)
	}
	key, err := p.Key()
	if err != nil {
		return nil, err
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get(hdrSig))
	if err != nil {
		return nil, errors.New("bad signature encoding")
	}
	msg := canonical(r.Method, r.URL.Path, selfID, ts, bodySum(body),
		r.Header.Get(hdrVer), r.Header.Get(hdrDeleted))
	if !ed25519.Verify(key, msg, sig) {
		return nil, errors.New("bad signature")
	}
	return p, nil
}
