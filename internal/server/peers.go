// Node-to-node sync endpoints. Two families with different auth:
//
//	/v1/peers*  — the desktop UI's view (token-guarded like every /v1 call):
//	              peer list, join codes, joining, leaving, latency.
//	/v1/peer/*  — daemon-to-daemon (exempt from the API token; each request
//	              is ed25519-signed by an enrolled peer, except pair, which
//	              a short-lived join code authenticates). A peer can reach
//	              ONLY these routes — never VMs, config or the host shell.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"exe/internal/config"
	"exe/internal/peer"
)

func (s *Server) peerEngine(w http.ResponseWriter) *peer.Engine {
	if s.Peers == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("node sync unavailable"))
		return nil
	}
	return s.Peers
}

// ---- UI-facing --------------------------------------------------------------

func (s *Server) handlePeersGet(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	ip := config.TailscaleIP()
	// Cached statuses only — probing blocks up to 4 s per unreachable peer,
	// and this is the Node Sync dialog's open path. The UI follows up with
	// /v1/peers/status?force=1 to fill in fresh latencies.
	writeJSON(w, http.StatusOK, map[string]any{
		"self": map[string]any{
			"id": e.SelfID(), "name": e.SelfName(), "ip": ip, "port": e.Port(),
			"tailscale": ip != "",
		},
		"peers": e.StatusCached(),
	})
}

func (s *Server) handlePeersCode(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	code, exp := e.MintCode()
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires": exp.UnixMilli()})
}

func (s *Server) handlePeersJoin(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	var req struct {
		Addr string `json:"addr"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	p, err := e.Join(req.Addr, req.Code)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	s.PostNews("node", "Node joined", "Now syncing with "+p.Name+".")
	writeJSON(w, http.StatusOK, map[string]any{"joined": p})
}

func (s *Server) handlePeersDelete(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	id := r.PathValue("id")
	name := id
	for _, p := range e.ListPeers() {
		if p.ID == id {
			name = p.Name
			break
		}
	}
	if err := e.Leave(id); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.PostNews("node", "Node left", "No longer syncing with "+name+".")
	writeJSON(w, http.StatusOK, map[string]string{"status": "left"})
}

func (s *Server) handlePeersStatus(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": e.Probe(r.URL.Query().Has("force"))})
}

// ---- daemon-to-daemon -------------------------------------------------------

func (s *Server) handlePeerPair(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	var req peer.PairRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	resp, err := e.HandlePair(host, req)
	if err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}
	s.PostNews("node", "Node joined", "Now syncing with "+req.Name+".")
	writeJSON(w, http.StatusOK, resp)
}

// verifiedPeer reads the (limited) body and authenticates the signature;
// nil, nil means the response was already written.
func (s *Server) verifiedPeer(w http.ResponseWriter, r *http.Request, maxBody int64) (*peer.Engine, []byte, bool) {
	e := s.peerEngine(w)
	if e == nil {
		return nil, nil, false
	}
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || int64(len(b)) > maxBody {
			writeErr(w, http.StatusRequestEntityTooLarge, errors.New("body too large"))
			return nil, nil, false
		}
		body = b
	}
	if _, err := e.VerifyPeer(r, body); err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return nil, nil, false
	}
	return e, body, true
}

func (s *Server) handlePeerPing(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.verifiedPeer(w, r, 4096)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": e.SelfID(), "name": e.SelfName(), "time": time.Now().UnixMilli(),
	})
}

func (s *Server) handlePeerManifest(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.verifiedPeer(w, r, 4096)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": e.SelfID(), "name": e.SelfName(), "port": e.Port(),
		"files": e.ManifestSnapshot(), "peers": e.PeerInfos(),
		"max_file": e.MaxFile(),
	})
}

func (s *Server) handlePeerUnpair(w http.ResponseWriter, r *http.Request) {
	e := s.peerEngine(w)
	if e == nil {
		return
	}
	p, err := e.VerifyPeer(r, nil)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	e.Unpair(p.ID)
	s.PostNews("node", "Node left", "No longer syncing with "+p.Name+".")
	writeJSON(w, http.StatusOK, map[string]string{"status": "unpaired"})
}

// peerFileKey validates the {app}/{path...} of a peer file request — the
// same jail rules as the app-data API, minus the installed-bundle check:
// synced data may arrive before its app does. The reserved @workspace
// namespace addresses the shared workspace tree.
func peerFileKey(r *http.Request) (app, rel string, err error) {
	app, rel = r.PathValue("app"), r.PathValue("path")
	if app != peer.WorkspaceNS && !validAppName(app) {
		return "", "", errors.New("bad app name")
	}
	if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", "", errors.New("bad path")
	}
	return app, rel, nil
}

func (s *Server) handlePeerFileGet(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.verifiedPeer(w, r, 4096)
	if !ok {
		return
	}
	app, rel, err := peerFileKey(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	body, ver, err := e.FileForPeer(app, rel, peer.Takes(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	w.Header().Set("X-Exe-Ver", peer.EncodeVector(ver))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(body)
}

func (s *Server) handlePeerFilePut(w http.ResponseWriter, r *http.Request) {
	e, body, ok := s.verifiedPeer(w, r, fileMax)
	if !ok {
		return
	}
	app, rel, err := peerFileKey(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rv := peer.ParseVersion(r.Header.Get("X-Exe-Ver"), r.Header.Get("X-Exe-Deleted") == "1")
	if len(rv.Vec) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("missing version"))
		return
	}
	outcome, err := e.ApplyRemote(app+"/"+rel, body, rv)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": outcome})
}
