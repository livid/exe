// exe-hub client: the browser app never holds a key, so the daemon signs
// hub writes with this node's peer identity and forwards them. Reads go
// straight from the app to the hub (its GETs are public + CORS-open); only
// mutations and uploads pass through here.
package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"exe/internal/peer"
)

// hubPrefix domain-separates exe-hub envelope signatures; hubUploadPrefix
// covers upload authorizations. Both must match exe-hub's envelope package.
const (
	hubPrefix       = "exe-hub:v1\n"
	hubUploadPrefix = "exe-hub:v1\nupload\n"
	hubUploadMax    = 8 << 20
)

var hubClient = &http.Client{Timeout: 60 * time.Second}

var hubIdentOnce sync.Once
var hubIdent *peer.Identity
var hubIdentErr error

// hubIdentity loads the peer key lazily so hub publishing works even when
// the sync engine itself failed to start.
func (s *Server) hubIdentity() (*peer.Identity, error) {
	hubIdentOnce.Do(func() { hubIdent, hubIdentErr = peer.LoadIdentity(s.StateDir) })
	return hubIdent, hubIdentErr
}

// hubURL validates the app-supplied hub address.
func hubURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("hub must be an http(s) URL")
	}
	return u.Scheme + "://" + u.Host, nil
}

func (s *Server) handleHubWhoami(w http.ResponseWriter, r *http.Request) {
	id, err := s.hubIdentity()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id.ID, "name": id.Name, "pubkey": id.PubKey()})
}

// handleHubPublish signs one envelope and forwards it: the app sends
// {hub, type, body}, the daemon numbers it (asking the hub for the
// author's last seq), signs the exact bytes, and relays the hub's answer.
func (s *Server) handleHubPublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hub  string          `json:"hub"`
		Type string          `json:"type"`
		Body json.RawMessage `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hub, err := hubURL(in.Hub)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.hubIdentity()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	resp, err := hubSend(hub, id, in.Type, in.Body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()
	relayJSON(w, resp)
}

// hubSend numbers, signs and delivers one envelope as id: it asks the hub
// for the author's last seq, signs the exact bytes it then sends, and
// returns the hub's response — status and JSON body alike — for the
// caller to relay or decode.
func hubSend(hub string, id *peer.Identity, typ string, body json.RawMessage) (*http.Response, error) {
	seqResp, err := hubClient.Get(hub + "/v1/seq?author=" + url.QueryEscape(id.PubKey()))
	if err != nil {
		return nil, fmt.Errorf("hub unreachable: %w", err)
	}
	var seq struct {
		Seq int64 `json:"seq"`
	}
	err = json.NewDecoder(seqResp.Body).Decode(&seq)
	seqResp.Body.Close()
	if err != nil || seqResp.StatusCode != 200 {
		return nil, fmt.Errorf("hub seq: status %d", seqResp.StatusCode)
	}

	raw, err := json.Marshal(map[string]any{
		"type": typ, "author": id.PubKey(), "seq": seq.Seq + 1,
		"ts": time.Now().UnixMilli(), "body": body,
	})
	if err != nil {
		return nil, err
	}
	sig := id.Sign(append([]byte(hubPrefix), raw...))
	msg, _ := json.Marshal(map[string]string{
		"envelope": base64.StdEncoding.EncodeToString(raw), "sig": sig,
	})
	resp, err := hubClient.Post(hub+"/v1/msg", "application/json", bytes.NewReader(msg))
	if err != nil {
		return nil, fmt.Errorf("hub unreachable: %w", err)
	}
	return resp, nil
}

// handleHubUpload signs the body digest and forwards the bytes for the
// hub to pin; the response (cid, sniffed mime, size) relays unchanged.
// /v1/hub/avatar rides the same path to the hub's avatar minter (which
// crops/resizes to a 128×128 PNG before pinning).
func (s *Server) handleHubUpload(w http.ResponseWriter, r *http.Request) {
	target := "/v1/upload"
	if strings.HasSuffix(r.URL.Path, "/avatar") {
		target = "/v1/avatar"
	}
	hub, err := hubURL(r.URL.Query().Get("hub"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.hubIdentity()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hubUploadMax))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("embeds are capped at %dMB", hubUploadMax>>20))
		return
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sum := sha256.Sum256(body)
	sig := id.Sign([]byte(hubUploadPrefix + ts + "\n" + hex.EncodeToString(sum[:])))

	req, err := http.NewRequest("POST", hub+target, bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	req.Header.Set("X-Hub-Author", id.PubKey())
	req.Header.Set("X-Hub-Ts", ts)
	req.Header.Set("X-Hub-Sig", sig)
	resp, err := hubClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("hub unreachable: %w", err))
		return
	}
	defer resp.Body.Close()
	relayJSON(w, resp)
}

// relayJSON forwards a hub response, status and JSON body alike, so the
// app sees exactly what the hub said (gate denials included).
func relayJSON(w http.ResponseWriter, resp *http.Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
