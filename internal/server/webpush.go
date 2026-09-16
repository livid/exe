package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Web Push for the price alerts (RFC 8030 delivery, RFC 8291 encryption,
// RFC 8292 VAPID). The daemon owns one VAPID key pair, keeps every browser's
// subscription, and posts each alert, encrypted for that browser, to its
// push service; the service worker (ui/sw.js) shows it. Nothing here is
// specific to prices — any future notification can go the same road.

const (
	vapidFile   = "vapid.json"
	pushSubFile = "push.json"
)

var pushClient = &http.Client{Timeout: 15 * time.Second}

type vapidKeys struct {
	Private string `json:"private"` // base64url, the 32-byte scalar
	Public  string `json:"public"`  // base64url, the 65-byte uncompressed point
}

// pushSub is what PushManager.subscribe hands the page, as the page sends it.
type pushSub struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Added time.Time `json:"added,omitempty"`
	UA    string    `json:"ua,omitempty"`
}

var b64 = base64.RawURLEncoding

// vapid loads the daemon's key pair, making one on first use.
func (s *Server) vapid() (*ecdsa.PrivateKey, string, error) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.vapidKey != nil {
		return s.vapidKey, s.vapidPub, nil
	}
	path := filepath.Join(s.StateDir, vapidFile)
	var k vapidKeys
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &k) == nil && k.Private != "" {
		d, err := b64.DecodeString(k.Private)
		if err != nil {
			return nil, "", err
		}
		priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
		if err != nil {
			return nil, "", err
		}
		s.vapidKey, s.vapidPub = priv, k.Public
		return priv, k.Public, nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	d, err := priv.Bytes()
	if err != nil {
		return nil, "", err
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, "", err
	}
	k = vapidKeys{Private: b64.EncodeToString(d), Public: b64.EncodeToString(pub)}
	b, _ := json.Marshal(k)
	if err := os.MkdirAll(s.StateDir, 0o755); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, "", err
	}
	s.vapidKey, s.vapidPub = priv, k.Public
	return priv, k.Public, nil
}

func (s *Server) loadPushSubs() []pushSub {
	var subs []pushSub
	if b, err := os.ReadFile(filepath.Join(s.StateDir, pushSubFile)); err == nil {
		_ = json.Unmarshal(b, &subs)
	}
	return subs
}

func (s *Server) savePushSubs(subs []pushSub) error {
	b, _ := json.MarshalIndent(subs, "", " ")
	return os.WriteFile(filepath.Join(s.StateDir, pushSubFile), b, 0o600)
}

// GET /v1/push/key — the VAPID public key the page subscribes with.
func (s *Server) handlePushKey(w http.ResponseWriter, r *http.Request) {
	_, pub, err := s.vapid()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.pushMu.Lock()
	n := len(s.loadPushSubs())
	s.pushMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"key": pub, "subscriptions": n})
}

// POST /v1/push/subscribe {endpoint, keys} — adds or refreshes a browser;
// DELETE /v1/push/subscribe {endpoint} — removes it.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var sub pushSub
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&sub); err != nil || sub.Endpoint == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a push subscription (endpoint, keys) is required"))
		return
	}
	if u, err := url.Parse(sub.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" {
		writeErr(w, http.StatusBadRequest, errors.New("the endpoint must be an https URL"))
		return
	}
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	subs := s.loadPushSubs()
	kept := subs[:0]
	for _, x := range subs {
		if x.Endpoint != sub.Endpoint {
			kept = append(kept, x)
		}
	}
	if r.Method == http.MethodPost {
		if _, err := b64.DecodeString(sub.Keys.P256dh); err != nil || sub.Keys.Auth == "" {
			writeErr(w, http.StatusBadRequest, errors.New("the subscription's p256dh and auth keys are required"))
			return
		}
		sub.Added = time.Now().UTC()
		sub.UA = r.UserAgent()
		kept = append(kept, sub)
	}
	if err := s.savePushSubs(kept); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": len(kept)})
}

// POST /v1/push/test — a hello to every subscribed browser, so a phone can
// be checked right after turning notifications on.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	n, errs := s.pushAll(r.Context(), pushMessage{Title: "exe notifications work", Body: "Big moves of SOL, PUMP, MET and SKR will arrive like this.", Tag: "px-test"})
	writeJSON(w, http.StatusOK, map[string]any{"sent": n, "errors": errs})
}

type pushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url,omitempty"`
}

// pushAll sends one message to every subscription, dropping the ones whose
// push service says are gone. It returns how many were sent and the
// failures, one line each.
func (s *Server) pushAll(ctx context.Context, msg pushMessage) (int, []string) {
	priv, pub, err := s.vapid()
	if err != nil {
		return 0, []string{err.Error()}
	}
	s.pushMu.Lock()
	subs := s.loadPushSubs()
	s.pushMu.Unlock()
	payload, _ := json.Marshal(msg)
	var errs []string
	var gone []string
	sent := 0
	for _, sub := range subs {
		drop, err := sendPush(ctx, sub, payload, priv, pub)
		if drop {
			gone = append(gone, sub.Endpoint)
		}
		if err != nil {
			errs = append(errs, shortEndpoint(sub.Endpoint)+": "+err.Error())
			continue
		}
		sent++
	}
	if len(gone) > 0 {
		s.pushMu.Lock()
		subs = s.loadPushSubs()
		kept := subs[:0]
		for _, x := range subs {
			if !contains(gone, x.Endpoint) {
				kept = append(kept, x)
			}
		}
		_ = s.savePushSubs(kept)
		s.pushMu.Unlock()
	}
	return sent, errs
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func shortEndpoint(e string) string {
	if u, err := url.Parse(e); err == nil {
		return u.Host
	}
	return e
}

// sendPush posts one encrypted message. drop is true when the push service
// says the subscription no longer exists.
func sendPush(ctx context.Context, sub pushSub, payload []byte, priv *ecdsa.PrivateKey, pub string) (drop bool, err error) {
	uaPub, err := b64.DecodeString(sub.Keys.P256dh)
	if err != nil {
		return true, fmt.Errorf("bad p256dh: %w", err)
	}
	auth, err := b64.DecodeString(sub.Keys.Auth)
	if err != nil {
		return true, fmt.Errorf("bad auth: %w", err)
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return false, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return false, err
	}
	body, err := encryptPush(uaPub, auth, payload, asPriv, salt)
	if err != nil {
		return false, err
	}
	authz, err := vapidAuthorization(priv, pub, sub.Endpoint)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", authz)
	res, err := pushClient.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
		return true, fmt.Errorf("subscription gone (%d)", res.StatusCode)
	}
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return false, fmt.Errorf("push service answered %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	return false, nil
}

// encryptPush is RFC 8291 for a single record: the aes128gcm header (salt,
// record size 4096, the server's ephemeral public key) followed by the
// AES-GCM ciphertext of the plaintext plus its 0x02 delimiter.
func encryptPush(uaPublic, authSecret, plaintext []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("bad user agent key: %w", err)
	}
	shared, err := asPriv.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	asPublic := asPriv.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := "WebPush: info\x00" + string(uaPublic) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(plaintext) > 4096-16-1 {
		return nil, errors.New("push payload too large for one record")
	}
	var out bytes.Buffer
	out.Write(salt)
	var rs [4]byte
	binary.BigEndian.PutUint32(rs[:], 4096)
	out.Write(rs[:])
	out.WriteByte(byte(len(asPublic)))
	out.Write(asPublic)
	record := append(append([]byte{}, plaintext...), 0x02)
	out.Write(gcm.Seal(nil, nonce, record, nil))
	return out.Bytes(), nil
}

// vapidAuthorization is the RFC 8292 header: an ES256 JWT for the push
// service's origin, good for twelve hours, and the public key it verifies with.
func vapidAuthorization(priv *ecdsa.PrivateKey, pub, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": "mailto:exe@" + hostnameOr("localhost"),
	})
	signing := header + "." + b64.EncodeToString(claims)
	h := sha256.Sum256([]byte(signing))
	rr, ss, err := ecdsa.Sign(rand.Reader, priv, h[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	rr.FillBytes(sig[:32])
	ss.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + pub, nil
}

func hostnameOr(def string) string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return def
}
