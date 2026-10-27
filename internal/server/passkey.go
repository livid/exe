// Passkeys: a browser signs in to exe with a passkey in place of the API
// token, and gets the desk's session cookie for it (session.go).
//
// A desk that is already in adds a passkey in Configuration → Passkeys.
// ~/.exe/passkeys.json keeps each one: its credential ID and public key,
// the host it was made at (a passkey works there alone: the host is its
// relying party), and when it was made and last used. The API token stays:
// curl, agents and joined nodes use it, it adds the first passkey, and it
// is the way back in when every passkey is gone. A new token ends every
// session, a passkey's too; a passkey removed ends its own.
//
// Only what exe needs of WebAuthn is here: ES256, EdDSA and RS256 keys;
// attestation "none" (exe does not ask which device made a key); user
// verification required; and passkeys the browser finds by itself, so
// signing in names no account.
package server

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type passkey struct {
	ID      string    `json:"id"` // the credential ID, base64url
	Name    string    `json:"name"`
	RP      string    `json:"rp"` // the host it was made at
	Alg     int       `json:"alg"`
	Key     []byte    `json:"key"` // PKIX DER
	Count   uint32    `json:"count"`
	Created time.Time `json:"created"`
	Used    time.Time `json:"used,omitzero"`
}

type passkeyFile struct {
	// User is the user handle every passkey of this node carries: exe has
	// one user, the owner.
	User     []byte     `json:"user"`
	Passkeys []*passkey `json:"passkeys"`
}

type passkeyChallenge struct {
	kind, rp string
	until    time.Time
}

// passkeyStore is the file as last read or written (only the daemon writes
// it), and the challenges handed out and not yet spent.
type passkeyStore struct {
	mu   sync.Mutex
	file *passkeyFile
	chal map[string]passkeyChallenge
}

const (
	passkeyChallengeTTL  = 5 * time.Minute
	passkeyChallengesMax = 64 // asking to sign in needs no token: the map cannot grow past this
	passkeyBodyMax       = 64 << 10
)

var b64url = base64.RawURLEncoding

func (s *Server) passkeysPath() string { return filepath.Join(s.StateDir, "passkeys.json") }

// passkeysLocked is the file, read the first time; the caller holds s.pk.mu.
func (s *Server) passkeysLocked() *passkeyFile {
	if s.pk.file == nil {
		f := &passkeyFile{}
		if b, err := os.ReadFile(s.passkeysPath()); err == nil {
			json.Unmarshal(b, f)
		}
		s.pk.file = f
	}
	return s.pk.file
}

func (s *Server) passkeysSaveLocked() error {
	b, err := json.MarshalIndent(s.pk.file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.passkeysPath(), append(b, '\n'), 0o600)
}

// passkeyRef names a passkey in a session cookie: short, and no dots.
func passkeyRef(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:8])
}

// passkeyLive says whether the passkey a session cookie names is still here.
func (s *Server) passkeyLive(ref string) bool {
	s.pk.mu.Lock()
	defer s.pk.mu.Unlock()
	for _, k := range s.passkeysLocked().Passkeys {
		if passkeyRef(k.ID) == ref {
			return true
		}
	}
	return false
}

func (s *Server) passkeyChallenge(kind, rp string) string {
	b := make([]byte, 32)
	rand.Read(b)
	c := b64url.EncodeToString(b)
	now := time.Now()
	s.pk.mu.Lock()
	defer s.pk.mu.Unlock()
	if s.pk.chal == nil {
		s.pk.chal = map[string]passkeyChallenge{}
	}
	oldest := ""
	for k, v := range s.pk.chal {
		if now.After(v.until) {
			delete(s.pk.chal, k)
		} else if oldest == "" || v.until.Before(s.pk.chal[oldest].until) {
			oldest = k
		}
	}
	if len(s.pk.chal) >= passkeyChallengesMax {
		delete(s.pk.chal, oldest)
	}
	s.pk.chal[c] = passkeyChallenge{kind, rp, now.Add(passkeyChallengeTTL)}
	return c
}

// passkeySpend takes a challenge back: each is good once, for what it was
// handed out for, at the host it was handed out at.
func (s *Server) passkeySpend(c, kind, rp string) bool {
	s.pk.mu.Lock()
	defer s.pk.mu.Unlock()
	v, ok := s.pk.chal[c]
	delete(s.pk.chal, c)
	return ok && v.kind == kind && v.rp == rp && time.Now().Before(v.until)
}

var errPasskeyHere = errors.New("passkeys need exe at a name over HTTPS, such as its Tailscale Serve address")

// passkeyHost is the relying party for an address: its host, lowercased,
// when that can have passkeys; an IP address cannot.
func passkeyHost(hostport string) string {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || net.ParseIP(h) != nil {
		return ""
	}
	return h
}

// passkeyOrigin is the origin of the desk page that sent r, and the relying
// party of a passkey made or used there. The browser makes passkeys only
// in a secure context: over HTTPS, or on localhost.
func passkeyOrigin(r *http.Request) (origin, rp string, err error) {
	origin = r.Header.Get("Origin")
	u, perr := url.Parse(origin)
	if origin == "" || perr != nil || !strings.EqualFold(u.Host, r.Host) || !sameOrigin(r) {
		return "", "", errors.New("not a page of this exe")
	}
	rp = passkeyHost(u.Host)
	local := rp == "localhost" || strings.HasSuffix(rp, ".localhost")
	if rp == "" || !(u.Scheme == "https" || u.Scheme == "http" && local) {
		return "", "", errPasskeyHere
	}
	return origin, rp, nil
}

// passkeyClientData checks what the browser says it signed: the ceremony,
// the page it was asked at, and not from inside another site's frame. It
// returns the challenge.
func passkeyClientData(raw []byte, kind, origin string) (string, error) {
	var cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin bool   `json:"crossOrigin"`
	}
	if err := json.Unmarshal(raw, &cd); err != nil {
		return "", errors.New("the browser's answer does not read")
	}
	if cd.Type != kind || cd.Origin != origin || cd.CrossOrigin {
		return "", errors.New("the passkey answered for another page")
	}
	return cd.Challenge, nil
}

// The authenticator data's flags.
const (
	adUserPresent  = 0x01
	adUserVerified = 0x04
	adAttested     = 0x40
)

type authData struct {
	rpHash []byte
	flags  byte
	count  uint32
	aaguid []byte
	credID []byte
	key    map[any]any // the COSE key, as made
}

func parseAuthData(b []byte, attested bool) (*authData, error) {
	if len(b) < 37 {
		return nil, errors.New("short authenticator data")
	}
	a := &authData{rpHash: b[:32], flags: b[32], count: binary.BigEndian.Uint32(b[33:37])}
	if !attested {
		return a, nil
	}
	if a.flags&adAttested == 0 || len(b) < 55 {
		return nil, errors.New("the passkey came without its key")
	}
	a.aaguid = b[37:53]
	n := int(binary.BigEndian.Uint16(b[53:55]))
	if n == 0 || n > 1023 || len(b) < 55+n {
		return nil, errors.New("a bad credential ID")
	}
	a.credID = b[55 : 55+n]
	k, _, err := cborItem(b[55+n:], 0)
	m, ok := k.(map[any]any)
	if err != nil || !ok {
		return nil, errors.New("the passkey's key does not read")
	}
	a.key = m
	return a, nil
}

// cborItem reads one CBOR item from b — what an attestation object and a
// COSE key are made of: integers, byte and text strings, arrays, maps,
// tags and simple values, all of definite length — and the bytes it took.
func cborItem(b []byte, depth int) (any, int, error) {
	if depth > 8 {
		return nil, 0, errors.New("cbor: too deep")
	}
	if len(b) == 0 {
		return nil, 0, io.ErrUnexpectedEOF
	}
	major, info := b[0]>>5, b[0]&0x1f
	var n uint64
	hdr := 1
	switch {
	case info < 24:
		n = uint64(info)
	case info <= 27:
		size := 1 << (info - 24) // 1, 2, 4 or 8 bytes
		if len(b) < 1+size {
			return nil, 0, io.ErrUnexpectedEOF
		}
		for _, c := range b[1 : 1+size] {
			n = n<<8 | uint64(c)
		}
		hdr += size
	default:
		return nil, 0, errors.New("cbor: indefinite or reserved length")
	}
	switch major {
	case 0, 1:
		if n > math.MaxInt64 {
			return nil, 0, errors.New("cbor: integer too large")
		}
		if major == 1 {
			return -1 - int64(n), hdr, nil
		}
		return int64(n), hdr, nil
	case 2, 3:
		if n > uint64(len(b)-hdr) {
			return nil, 0, io.ErrUnexpectedEOF
		}
		v := b[hdr : hdr+int(n)]
		if major == 3 {
			return string(v), hdr + int(n), nil
		}
		return bytes.Clone(v), hdr + int(n), nil
	case 4, 5:
		if n > 256 {
			return nil, 0, errors.New("cbor: too many items")
		}
		off := hdr
		next := func() (any, error) {
			v, used, err := cborItem(b[off:], depth+1)
			off += used
			return v, err
		}
		if major == 4 {
			arr := make([]any, 0, n)
			for range n {
				v, err := next()
				if err != nil {
					return nil, 0, err
				}
				arr = append(arr, v)
			}
			return arr, off, nil
		}
		m := make(map[any]any, n)
		for range n {
			k, err := next()
			if err != nil {
				return nil, 0, err
			}
			v, err := next()
			if err != nil {
				return nil, 0, err
			}
			switch k.(type) {
			case int64, string:
				m[k] = v
			default:
				return nil, 0, errors.New("cbor: a map key that is not a number or text")
			}
		}
		return m, off, nil
	case 6: // a tag: its item stands for it
		v, used, err := cborItem(b[hdr:], depth+1)
		return v, hdr + used, err
	default: // 7: false, true, null, undefined, floats
		switch info {
		case 20:
			return false, hdr, nil
		case 21:
			return true, hdr, nil
		}
		return nil, hdr, nil
	}
}

// coseKey is the public key a passkey was made with: P-256 for ES256,
// Ed25519 for EdDSA, RSA of 2048 bits or more for RS256.
func coseKey(m map[any]any) (int, crypto.PublicKey, error) {
	num := func(k int64) int64 { v, _ := m[k].(int64); return v }
	byt := func(k int64) []byte { v, _ := m[k].([]byte); return v }
	alg := int(num(3))
	switch alg {
	case -7:
		x, y := byt(-2), byt(-3)
		if num(1) != 2 || num(-1) != 1 || len(x) != 32 || len(y) != 32 {
			break
		}
		if _, err := ecdh.P256().NewPublicKey(append(append([]byte{4}, x...), y...)); err != nil {
			return 0, nil, errors.New("the passkey's key is not on its curve")
		}
		return alg, &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	case -8:
		if x := byt(-2); num(1) == 1 && num(-1) == 6 && len(x) == ed25519.PublicKeySize {
			return alg, ed25519.PublicKey(x), nil
		}
	case -257:
		n, e := new(big.Int).SetBytes(byt(-1)), new(big.Int).SetBytes(byt(-2))
		if num(1) == 3 && n.BitLen() >= 2048 && e.IsInt64() && e.Int64() > 1 && e.Int64() < 1<<31 && e.Bit(0) == 1 {
			return alg, &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
		}
	}
	return 0, nil, fmt.Errorf("exe takes ES256, EdDSA and RS256 passkeys, not this one (alg %d)", alg)
}

func passkeyVerify(alg int, pub crypto.PublicKey, msg, sig []byte) bool {
	h := sha256.Sum256(msg)
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return alg == -7 && ecdsa.VerifyASN1(k, h[:], sig)
	case ed25519.PublicKey:
		return alg == -8 && ed25519.Verify(k, msg, sig)
	case *rsa.PublicKey:
		return alg == -257 && rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
	}
	return false
}

// passkeyMakers names the passkey managers whose AAGUID says who they are;
// any other passkey is named for the browser that made it.
var passkeyMakers = map[string]string{
	"fbfc3007-154e-4ecc-8c0b-6e020557d7bd": "iCloud Keychain",
	"ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4": "Google Password Manager",
	"adce0002-35bc-c60a-648b-0b25f1f05503": "Chrome on Mac",
	"08987058-cadc-4b81-b6e1-30de50dcbe96": "Windows Hello",
	"9ddd1817-af5a-4672-a2b9-3e3dd95000a9": "Windows Hello",
	"6028b017-b1d4-4c02-b4b3-afcdafc96bb2": "Windows Hello",
	"bada5566-a7aa-401f-bd96-45619a55120d": "1Password",
	"d548826e-79b4-db40-a3d8-11116f7e8349": "Bitwarden",
}

func aaguidString(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func passkeyName(aaguid []byte, asked string) string {
	if n := passkeyMakers[aaguidString(aaguid)]; n != "" {
		return n
	}
	asked = strings.TrimSpace(asked)
	for utf8.RuneCountInString(asked) > 64 {
		_, size := utf8.DecodeLastRuneInString(asked)
		asked = asked[:len(asked)-size]
	}
	if asked == "" {
		return "Passkey"
	}
	return asked
}

// passkeyRequest reads the body of a passkey ceremony's second half,
// decoding the browser's base64url fields.
func passkeyRequest(w http.ResponseWriter, r *http.Request, fields map[string]*[]byte, name *string) bool {
	var req map[string]string
	if err := json.NewDecoder(io.LimitReader(r.Body, passkeyBodyMax)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	for k, dst := range fields {
		b, err := b64url.DecodeString(strings.TrimRight(req[k], "="))
		if err != nil || (len(b) == 0 && k != "userHandle") {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s is missing or does not read", k))
			return false
		}
		*dst = b
	}
	if name != nil {
		*name = req["name"]
	}
	return true
}

type passkeyView struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	RP      string    `json:"rp"`
	Created time.Time `json:"created"`
	Used    time.Time `json:"used,omitzero"`
}

func (k *passkey) view() passkeyView {
	return passkeyView{k.ID, k.Name, k.RP, k.Created, k.Used}
}

// GET /v1/passkeys: every passkey of this node, for Configuration.
func (s *Server) handlePasskeys(w http.ResponseWriter, r *http.Request) {
	s.pk.mu.Lock()
	list := []passkeyView{}
	for _, k := range s.passkeysLocked().Passkeys {
		list = append(list, k.view())
	}
	s.pk.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": list, "token": s.Config().APIToken != ""})
}

// DELETE /v1/passkeys/{id}: the passkey goes, and every session it began.
func (s *Server) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.pk.mu.Lock()
	defer s.pk.mu.Unlock()
	f := s.passkeysLocked()
	for i, k := range f.Passkeys {
		if k.ID == id {
			f.Passkeys = append(f.Passkeys[:i], f.Passkeys[i+1:]...)
			if err := s.passkeysSaveLocked(); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeErr(w, http.StatusNotFound, errors.New("no such passkey"))
}

// POST /v1/passkeys/new: what the browser needs to make a passkey here.
func (s *Server) handlePasskeyNew(w http.ResponseWriter, r *http.Request) {
	_, rp, err := passkeyOrigin(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.Config().APIToken == "" {
		writeErr(w, http.StatusConflict, errors.New("this exe has no API token, so it has nothing for a passkey to stand in for"))
		return
	}
	s.pk.mu.Lock()
	f := s.passkeysLocked()
	if len(f.User) == 0 {
		f.User = make([]byte, 16)
		rand.Read(f.User)
		if err := s.passkeysSaveLocked(); err != nil {
			s.pk.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	user := b64url.EncodeToString(f.User)
	exclude := []map[string]string{}
	for _, k := range f.Passkeys {
		if k.RP == rp {
			exclude = append(exclude, map[string]string{"type": "public-key", "id": k.ID})
		}
	}
	s.pk.mu.Unlock()
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge": s.passkeyChallenge("create", rp),
		"rp":        map[string]string{"id": rp, "name": "exe"},
		"user":      map[string]string{"id": user, "name": "exe on " + host, "displayName": "exe on " + host},
		"pubKeyCredParams": []map[string]any{
			{"type": "public-key", "alg": -7}, {"type": "public-key", "alg": -8}, {"type": "public-key", "alg": -257},
		},
		"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": "required"},
		"attestation":            "none",
		"excludeCredentials":     exclude,
		"timeout":                int(passkeyChallengeTTL / time.Millisecond),
	})
}

// POST /v1/passkeys: the passkey the browser made, to keep.
func (s *Server) handlePasskeyAdd(w http.ResponseWriter, r *http.Request) {
	origin, rp, err := passkeyOrigin(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var id, cdj, att []byte
	var name string
	if !passkeyRequest(w, r, map[string]*[]byte{"id": &id, "clientDataJSON": &cdj, "attestationObject": &att}, &name) {
		return
	}
	ch, err := passkeyClientData(cdj, "webauthn.create", origin)
	if err == nil && !s.passkeySpend(ch, "create", rp) {
		err = errors.New("that request for a passkey ran out or was used: try again")
	}
	var ad *authData
	if err == nil {
		obj, _, cerr := cborItem(att, 0)
		m, _ := obj.(map[any]any)
		raw, _ := m["authData"].([]byte)
		if cerr != nil || raw == nil {
			err = errors.New("the browser's attestation does not read")
		} else {
			ad, err = parseAuthData(raw, true)
		}
	}
	rpHash := sha256.Sum256([]byte(rp))
	if err == nil && !bytes.Equal(ad.rpHash, rpHash[:]) {
		err = errors.New("the passkey was made for another address")
	}
	if err == nil && ad.flags&(adUserPresent|adUserVerified) != adUserPresent|adUserVerified {
		err = errors.New("the passkey did not check who you are")
	}
	if err == nil && !bytes.Equal(ad.credID, id) {
		err = errors.New("the passkey's ID does not match")
	}
	var alg int
	var der []byte
	if err == nil {
		var pub crypto.PublicKey
		if alg, pub, err = coseKey(ad.key); err == nil {
			der, err = x509.MarshalPKIXPublicKey(pub)
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	k := &passkey{ID: b64url.EncodeToString(id), Name: passkeyName(ad.aaguid, name), RP: rp,
		Alg: alg, Key: der, Count: ad.count, Created: time.Now().UTC()}
	s.pk.mu.Lock()
	defer s.pk.mu.Unlock()
	f := s.passkeysLocked()
	for _, o := range f.Passkeys {
		if o.ID == k.ID {
			writeErr(w, http.StatusConflict, errors.New("this passkey is here already"))
			return
		}
	}
	f.Passkeys = append(f.Passkeys, k)
	if err := s.passkeysSaveLocked(); err != nil {
		f.Passkeys = f.Passkeys[:len(f.Passkeys)-1]
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, k.view())
}

// GET /v1/passkeys/login: whether a passkey can sign in at this address —
// what the API Token dialog asks before it offers one. Like the rest of
// /v1/passkeys/login it needs no token: it is how a browser without one
// gets in.
func (s *Server) handlePasskeyAny(w http.ResponseWriter, r *http.Request) {
	rp, any := passkeyHost(r.Host), false
	s.pk.mu.Lock()
	for _, k := range s.passkeysLocked().Passkeys {
		any = any || (rp != "" && k.RP == rp)
	}
	s.pk.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"any": any && s.Config().APIToken != ""})
}

// POST /v1/passkeys/login: a challenge for a passkey to sign.
func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	_, rp, err := passkeyOrigin(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge": s.passkeyChallenge("get", rp), "rpId": rp,
		"userVerification": "required", "timeout": int(passkeyChallengeTTL / time.Millisecond),
	})
}

// POST /v1/passkeys/login/finish: the signed challenge; a good signature
// gets the session cookie.
func (s *Server) handlePasskeyLogin(w http.ResponseWriter, r *http.Request) {
	tok := s.Config().APIToken
	origin, rp, err := passkeyOrigin(r)
	if err == nil && tok == "" {
		err = errors.New("this exe has no API token, so there is nothing to sign in to")
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var id, cdj, adRaw, sig, handle []byte
	if !passkeyRequest(w, r, map[string]*[]byte{"id": &id, "clientDataJSON": &cdj,
		"authenticatorData": &adRaw, "signature": &sig, "userHandle": &handle}, nil) {
		return
	}
	refuse := func(why string) { writeErr(w, http.StatusForbidden, errors.New(why)) }
	ch, err := passkeyClientData(cdj, "webauthn.get", origin)
	if err != nil {
		refuse(err.Error())
		return
	}
	if !s.passkeySpend(ch, "get", rp) {
		refuse("that sign-in ran out or was used: try again")
		return
	}
	ad, err := parseAuthData(adRaw, false)
	rpHash := sha256.Sum256([]byte(rp))
	if err != nil || !bytes.Equal(ad.rpHash, rpHash[:]) || ad.flags&(adUserPresent|adUserVerified) != adUserPresent|adUserVerified {
		refuse("the passkey did not answer for this address, or did not check who you are")
		return
	}
	s.pk.mu.Lock()
	defer s.pk.mu.Unlock()
	f := s.passkeysLocked()
	var k *passkey
	for _, o := range f.Passkeys {
		if o.ID == b64url.EncodeToString(id) && o.RP == rp {
			k = o
		}
	}
	if k == nil || (len(handle) > 0 && !bytes.Equal(handle, f.User)) {
		refuse("exe does not know this passkey")
		return
	}
	pub, err := x509.ParsePKIXPublicKey(k.Key)
	cdHash := sha256.Sum256(cdj)
	if err != nil || !passkeyVerify(k.Alg, pub, append(bytes.Clone(adRaw), cdHash[:]...), sig) {
		refuse("the passkey's signature is not good")
		return
	}
	// a counter that does not go up is a copy of the key at work; a
	// passkey that syncs counts nothing, and stays at zero
	if (ad.count != 0 || k.Count != 0) && ad.count <= k.Count {
		refuse("this passkey's counter went back: it may have been copied")
		return
	}
	k.Count, k.Used = ad.count, time.Now().UTC()
	s.passkeysSaveLocked()
	http.SetCookie(w, sessionCookie(r, tok, "p"+passkeyRef(k.ID), time.Now()))
	writeJSON(w, http.StatusOK, k.view())
}
