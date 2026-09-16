package server

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"

	"exe/internal/config"
)

// RFC 8291, Section 5 and Appendix A: the worked example, byte for byte.
func TestEncryptPushRFC8291(t *testing.T) {
	dec := func(s string) []byte {
		b, err := b64.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	asPriv, err := ecdh.P256().NewPrivateKey(dec("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	uaPublic := dec("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	auth := dec("BTBZMqHH6r4Tts7J_aSIgg")
	salt := dec("DGv6ra1nlYgDCS1FRnbzlw")
	got, err := encryptPush(uaPublic, auth, []byte("When I grow up, I want to be a watermelon"), asPriv, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if b64.EncodeToString(got) != want {
		t.Fatalf("ciphertext differs\n got %s\nwant %s", b64.EncodeToString(got), want)
	}
}

// The VAPID header carries a JWT the push service can verify with the key
// beside it, for its own origin.
func TestVapidAuthorization(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pubBytes, _ := priv.PublicKey.Bytes()
	pub := b64.EncodeToString(pubBytes)
	h, err := vapidAuthorization(priv, pub, "https://web.push.apple.com/QAbc123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "vapid t=") || !strings.HasSuffix(h, ", k="+pub) {
		t.Fatalf("header shape: %s", h)
	}
	jwt := strings.TrimSuffix(strings.TrimPrefix(h, "vapid t="), ", k="+pub)
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt parts: %d", len(parts))
	}
	var claims map[string]any
	cb, _ := b64.DecodeString(parts[1])
	if err := json.Unmarshal(cb, &claims); err != nil || claims["aud"] != "https://web.push.apple.com" {
		t.Fatalf("claims: %s", cb)
	}
	sig, _ := b64.DecodeString(parts[2])
	if len(sig) != 64 {
		t.Fatalf("raw signature length %d", len(sig))
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&priv.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
}

// A browser's subscription is stored, refreshed and removed by endpoint; the
// key endpoint reports how many there are.
func TestPushSubscribeRoundTrip(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		switch path {
		case "/v1/push/key":
			s.handlePushKey(rec, req)
		default:
			s.handlePushSubscribe(rec, req)
		}
		return rec
	}
	sub := `{"endpoint":"https://web.push.apple.com/abc","keys":{"p256dh":"BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4","auth":"BTBZMqHH6r4Tts7J_aSIgg"}}`
	if rec := call("POST", "/v1/push/subscribe", sub); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"subscriptions":1`) {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/v1/push/subscribe", sub); !strings.Contains(rec.Body.String(), `"subscriptions":1`) {
		t.Fatalf("a repeat must refresh, not duplicate: %s", rec.Body)
	}
	if rec := call("POST", "/v1/push/subscribe", `{"endpoint":"http://plain/x","keys":{"p256dh":"AA","auth":"BB"}}`); rec.Code != 400 {
		t.Fatalf("plain http endpoint accepted: %d", rec.Code)
	}
	rec := call("GET", "/v1/push/key", "")
	var key struct {
		Key  string `json:"key"`
		Subs int    `json:"subscriptions"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &key) != nil || key.Subs != 1 {
		t.Fatalf("key: %s", rec.Body)
	}
	if raw, err := b64.DecodeString(key.Key); err != nil || len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("public key is not an uncompressed P-256 point: %q", key.Key)
	}
	// the same key comes back after a reload from disk
	s2 := New(&config.Config{}, nil, nil, "", s.StateDir)
	if _, pub, _ := s2.vapid(); pub != key.Key {
		t.Fatal("the VAPID key did not persist")
	}
	if rec := call("DELETE", "/v1/push/subscribe", `{"endpoint":"https://web.push.apple.com/abc"}`); !strings.Contains(rec.Body.String(), `"subscriptions":0`) {
		t.Fatalf("delete: %s", rec.Body)
	}
}
