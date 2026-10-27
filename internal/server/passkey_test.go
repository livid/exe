package server

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"exe/internal/config"
	"math/big"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A software authenticator: the browser and the passkey manager in one.

type cmap [][2]any // a CBOR map, in order

func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 1<<8:
		return []byte{major<<5 | 24, byte(n)}
	case n < 1<<16:
		return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
	default:
		return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
	}
}

func cborEnc(v any) []byte {
	switch v := v.(type) {
	case int:
		if v < 0 {
			return cborHead(1, uint64(-1-v))
		}
		return cborHead(0, uint64(v))
	case []byte:
		return append(cborHead(2, uint64(len(v))), v...)
	case string:
		return append(cborHead(3, uint64(len(v))), v...)
	case cmap:
		b := cborHead(5, uint64(len(v)))
		for _, kv := range v {
			b = append(b, cborEnc(kv[0])...)
			b = append(b, cborEnc(kv[1])...)
		}
		return b
	}
	panic("cborEnc")
}

type softKey struct {
	alg  int
	id   []byte
	priv crypto.Signer
	cose cmap
}

func newSoftKey(t *testing.T, alg int) *softKey {
	t.Helper()
	k := &softKey{alg: alg, id: make([]byte, 16)}
	rand.Read(k.id)
	switch alg {
	case -7:
		p, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		x, y := make([]byte, 32), make([]byte, 32)
		p.X.FillBytes(x)
		p.Y.FillBytes(y)
		k.priv, k.cose = p, cmap{{1, 2}, {3, -7}, {-1, 1}, {-2, x}, {-3, y}}
	case -8:
		pub, p, _ := ed25519.GenerateKey(rand.Reader)
		k.priv, k.cose = p, cmap{{1, 1}, {3, -8}, {-1, 6}, {-2, []byte(pub)}}
	case -257:
		p, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		k.priv, k.cose = p, cmap{{1, 3}, {3, -257}, {-1, p.N.Bytes()}, {-2, big.NewInt(int64(p.E)).Bytes()}}
	}
	return k
}

func (k *softKey) sign(msg []byte) []byte {
	if k.alg == -8 {
		s, _ := k.priv.Sign(rand.Reader, msg, crypto.Hash(0))
		return s
	}
	h := sha256.Sum256(msg)
	s, _ := k.priv.Sign(rand.Reader, h[:], crypto.SHA256)
	return s
}

func authDataOf(rp string, flags byte, count uint32, attested []byte) []byte {
	h := sha256.Sum256([]byte(rp))
	b := append(h[:], flags)
	b = binary.BigEndian.AppendUint32(b, count)
	return append(b, attested...)
}

func clientData(kind, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// pkCall is a request from the desk page at origin: its Origin, same-origin
// fetch metadata, and the token when tok is set.
func pkCall(h http.Handler, method, origin, path string, body any, tok string, cookie *http.Cookie) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, origin+path, rd)
	r.Header.Set("Origin", origin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const pkOrigin = "https://exe.test"

func pkRegister(t *testing.T, h http.Handler, k *softKey, count uint32, flags byte) *httptest.ResponseRecorder {
	t.Helper()
	w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/new", nil, "test-secret", nil)
	if w.Code != 200 {
		t.Fatalf("new: %d %s", w.Code, w.Body)
	}
	var opts struct {
		Challenge string
		RP        struct{ ID string }
	}
	json.Unmarshal(w.Body.Bytes(), &opts)
	if opts.RP.ID != "exe.test" {
		t.Fatalf("rp %q", opts.RP.ID)
	}
	att := append(make([]byte, 16), byte(len(k.id)>>8), byte(len(k.id)))
	att = append(append(att, k.id...), cborEnc(k.cose)...)
	obj := cborEnc(cmap{{"fmt", "none"}, {"attStmt", cmap{}}, {"authData", authDataOf("exe.test", flags, count, att)}})
	return pkCall(h, "POST", pkOrigin, "/v1/passkeys", map[string]string{
		"id": b64url.EncodeToString(k.id), "name": "Chrome on Linux",
		"clientDataJSON":    b64url.EncodeToString(clientData("webauthn.create", opts.Challenge, pkOrigin)),
		"attestationObject": b64url.EncodeToString(obj),
	}, "test-secret", nil)
}

// pkLogin signs in with k; tweak may change the assertion before it is sent.
func pkLogin(t *testing.T, h http.Handler, k *softKey, count uint32, tweak func(a map[string][]byte)) *httptest.ResponseRecorder {
	t.Helper()
	w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/login", nil, "", nil)
	if w.Code != 200 {
		t.Fatalf("login begin: %d %s", w.Code, w.Body)
	}
	var opts struct{ Challenge, RpId string }
	json.Unmarshal(w.Body.Bytes(), &opts)
	a := map[string][]byte{
		"clientDataJSON":    clientData("webauthn.get", opts.Challenge, pkOrigin),
		"authenticatorData": authDataOf(opts.RpId, adUserPresent|adUserVerified, count, nil),
		"id":                k.id,
	}
	if tweak != nil {
		tweak(a)
	}
	if a["signature"] == nil {
		h := sha256.Sum256(a["clientDataJSON"])
		a["signature"] = k.sign(append(bytes.Clone(a["authenticatorData"]), h[:]...))
	}
	body := map[string]string{}
	for f, v := range a {
		body[f] = b64url.EncodeToString(v)
	}
	return pkCall(h, "POST", pkOrigin, "/v1/passkeys/login/finish", body, "", nil)
}

func pkCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "exe_session" {
			return c
		}
	}
	t.Fatalf("no session cookie: %d %s", w.Code, w.Body)
	return nil
}

func pkIn(h http.Handler, c *http.Cookie) int {
	return pkCall(h, "GET", pkOrigin, "/v1/auth", nil, "", c).Code
}

func TestPasskeySignIn(t *testing.T) {
	for _, alg := range []int{-7, -8, -257} {
		t.Run(map[int]string{-7: "ES256", -8: "EdDSA", -257: "RS256"}[alg], func(t *testing.T) {
			s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
			h := s.Handler()
			k := newSoftKey(t, alg)
			if w := pkCall(h, "GET", pkOrigin, "/v1/passkeys/login", nil, "", nil); !strings.Contains(w.Body.String(), `"any":false`) {
				t.Fatalf("before a passkey: %s", w.Body)
			}
			if w := pkRegister(t, h, k, 0, adUserPresent|adUserVerified|adAttested); w.Code != 201 {
				t.Fatalf("register: %d %s", w.Code, w.Body)
			}
			if w := pkCall(h, "GET", pkOrigin, "/v1/passkeys/login", nil, "", nil); !strings.Contains(w.Body.String(), `"any":true`) {
				t.Fatalf("after a passkey: %s", w.Body)
			}
			w := pkLogin(t, h, k, 0, nil)
			if w.Code != 200 {
				t.Fatalf("login: %d %s", w.Code, w.Body)
			}
			c := pkCookie(t, w)
			if !strings.HasPrefix(c.Value, "1.p") || !c.HttpOnly || !c.Secure {
				t.Fatalf("passkey cookie %+v", c)
			}
			if got := pkIn(h, c); got != 204 {
				t.Fatalf("the passkey's session: %d", got)
			}
			// asking for a session with the cookie alone keeps the passkey's
			if w := pkCall(h, "POST", pkOrigin, "/v1/auth/session", nil, "", c); len(w.Result().Cookies()) != 0 {
				t.Fatal("a passkey's session was swapped for a token's")
			}
			// removed, the passkey ends its session
			var list struct{ Passkeys []passkeyView }
			json.Unmarshal(pkCall(h, "GET", pkOrigin, "/v1/passkeys", nil, "test-secret", nil).Body.Bytes(), &list)
			if len(list.Passkeys) != 1 || list.Passkeys[0].Name != "Chrome on Linux" || list.Passkeys[0].Used.IsZero() {
				t.Fatalf("list %+v", list)
			}
			if w := pkCall(h, "DELETE", pkOrigin, "/v1/passkeys/"+list.Passkeys[0].ID, nil, "test-secret", nil); w.Code != 204 {
				t.Fatalf("delete: %d", w.Code)
			}
			if got := pkIn(h, c); got != 401 {
				t.Fatalf("a removed passkey's session: %d", got)
			}
		})
	}
}

func TestPasskeyRefusals(t *testing.T) {
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
	h := s.Handler()
	k := newSoftKey(t, -7)
	if w := pkRegister(t, h, k, 5, adUserPresent|adAttested); w.Code != 400 {
		t.Fatalf("a passkey that did not verify the user was kept: %d", w.Code)
	}
	if w := pkRegister(t, h, k, 5, adUserPresent|adUserVerified|adAttested); w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	if w := pkRegister(t, h, k, 5, adUserPresent|adUserVerified|adAttested); w.Code != 409 {
		t.Fatalf("the same passkey twice: %d", w.Code)
	}
	other := newSoftKey(t, -7)
	for _, tc := range []struct {
		name  string
		key   *softKey
		count uint32
		tweak func(a map[string][]byte)
	}{
		{"another page's", k, 6, func(a map[string][]byte) {
			var cd map[string]any
			json.Unmarshal(a["clientDataJSON"], &cd)
			a["clientDataJSON"] = clientData("webauthn.get", cd["challenge"].(string), "https://evil.test")
		}},
		{"made for another host", k, 6, func(a map[string][]byte) {
			a["authenticatorData"] = authDataOf("evil.test", adUserPresent|adUserVerified, 6, nil)
		}},
		{"no user verification", k, 6, func(a map[string][]byte) {
			a["authenticatorData"] = authDataOf("exe.test", adUserPresent, 6, nil)
		}},
		{"a forged signature", k, 6, func(a map[string][]byte) { a["signature"] = other.sign([]byte("x")) }},
		{"an unknown passkey", other, 6, nil},
		{"a counter that went back", k, 5, nil},
		{"a challenge made up", k, 6, func(a map[string][]byte) {
			a["clientDataJSON"] = clientData("webauthn.get", "bm90LWlzc3VlZA", pkOrigin)
		}},
	} {
		w := pkLogin(t, h, tc.key, tc.count, tc.tweak)
		if w.Code != 403 {
			t.Errorf("%s: %d %s", tc.name, w.Code, w.Body)
		}
		t.Logf("%s: %s", tc.name, strings.TrimSpace(w.Body.String()))
	}
	if w := pkLogin(t, h, k, 6, nil); w.Code != 200 {
		t.Fatalf("the good one: %d %s", w.Code, w.Body)
	}
	// a challenge is spent once
	var replay map[string]string
	w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/login", nil, "", nil)
	var opts struct{ Challenge string }
	json.Unmarshal(w.Body.Bytes(), &opts)
	cd := clientData("webauthn.get", opts.Challenge, pkOrigin)
	ad := authDataOf("exe.test", adUserPresent|adUserVerified, 7, nil)
	hh := sha256.Sum256(cd)
	replay = map[string]string{"id": b64url.EncodeToString(k.id), "clientDataJSON": b64url.EncodeToString(cd),
		"authenticatorData": b64url.EncodeToString(ad), "signature": b64url.EncodeToString(k.sign(append(bytes.Clone(ad), hh[:]...)))}
	if w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/login/finish", replay, "", nil); w.Code != 200 {
		t.Fatalf("first use: %d %s", w.Code, w.Body)
	}
	if w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/login/finish", replay, "", nil); w.Code != 403 {
		t.Fatalf("a replay: %d", w.Code)
	}
	// where a passkey cannot be: an IP address, plain HTTP at a name, another site
	for _, origin := range []string{"https://100.116.32.57:7777", "http://exe.test"} {
		if w := pkCall(h, "POST", origin, "/v1/passkeys/login", nil, "", nil); w.Code != 400 {
			t.Errorf("sign in at %s: %d", origin, w.Code)
		}
	}
	r := httptest.NewRequest("POST", pkOrigin+"/v1/passkeys/login", nil)
	r.Header.Set("Origin", "https://evil.test")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != 400 {
		t.Errorf("sign in from another site: %d", rw.Code)
	}
	// localhost is a secure context over HTTP
	if w := pkCall(h, "POST", "http://localhost:7797", "/v1/passkeys/login", nil, "", nil); w.Code != 200 {
		t.Errorf("sign in at localhost: %d %s", w.Code, w.Body)
	}
	// adding a passkey needs the token, or a session
	if w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/new", nil, "", nil); w.Code != 401 {
		t.Errorf("new without the token: %d", w.Code)
	}
}

func TestPasskeyNeedsToken(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	h := s.Handler()
	if w := pkCall(h, "POST", pkOrigin, "/v1/passkeys/new", nil, "", nil); w.Code != 409 {
		t.Fatalf("new on a node with no token: %d", w.Code)
	}
	if w := pkCall(h, "GET", pkOrigin, "/v1/passkeys", nil, "", nil); !strings.Contains(w.Body.String(), `"token":false`) {
		t.Fatalf("list on a node with no token: %s", w.Body)
	}
}

// cborItem reads only what it is given: cut short or made of noise, an
// attestation object is an error, never a panic.
func TestCBORNoise(t *testing.T) {
	k := newSoftKey(t, -257)
	good := cborEnc(cmap{{"fmt", "none"}, {"attStmt", cmap{}}, {"authData", cborEnc(k.cose)}})
	if _, n, err := cborItem(good, 0); err != nil || n != len(good) {
		t.Fatalf("good: %d of %d, %v", n, len(good), err)
	}
	for i := range good {
		cborItem(good[:i], 0)
	}
	rnd := mrand.New(mrand.NewSource(1))
	for range 20000 {
		b := make([]byte, rnd.Intn(64))
		rnd.Read(b)
		cborItem(b, 0)
		parseAuthData(b, true)
	}
	deep := bytes.Repeat([]byte{0x81}, 100) // arrays in arrays
	if _, _, err := cborItem(deep, 0); err == nil {
		t.Fatal("no limit on depth")
	}
}
