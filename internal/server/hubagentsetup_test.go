package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"exe/internal/config"
	"exe/internal/peer"
)

// setupHub is a hub for the setup's questions: its info, profiles by id
// and by name (the agent's among them), and a gate that lets the agent post.
func setupHub(t *testing.T, agent *peer.Identity) *httptest.Server {
	mayPost := agent.PubKey()
	profiles := map[string]string{"1111111111111111": "Ada", "2222222222222222": "Grace", "3333333333333333": "Grace", agent.ID: "Helper"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hub", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"hub0","stats":{"profiles":3,"posts":12}}`))
	})
	mux.HandleFunc("GET /v1/profile/{id}", func(w http.ResponseWriter, r *http.Request) {
		name, ok := profiles[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": r.PathValue("id"), "name": name})
	})
	mux.HandleFunc("GET /v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		var out []hubNamed
		for id, name := range profiles {
			if strings.Contains(strings.ToLower(name), strings.ToLower(r.URL.Query().Get("q"))) {
				out = append(out, hubNamed{ID: id, Name: name})
			}
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /v1/gate", func(w http.ResponseWriter, r *http.Request) {
		gate := "below"
		if r.URL.Query().Get("author") == mayPost {
			gate = "invited"
		}
		json.NewEncoder(w).Encode(map[string]any{"gate": gate})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setupCheck(t *testing.T, s *Server, body map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/hub/agent/check", bytes.NewReader(b)))
	if rec.Code != 200 {
		t.Fatalf("%v: HTTP %d %s", body["step"], rec.Code, rec.Body)
	}
	var res map[string]any
	json.Unmarshal(rec.Body.Bytes(), &res)
	return res
}

// Each step of the Hub Agent Setup is checked before the next opens, and
// what does not work is said, not saved.
func TestHubAgentSetupChecks(t *testing.T) {
	keyPath, ident, _ := agentKey(t)
	hub := setupHub(t, ident)
	s := New(config.Default(), nil, nil, "", t.TempDir())
	ok := func(res map[string]any) bool { return res["ok"] == true }

	if res := setupCheck(t, s, map[string]any{"step": "hub", "hub": hub.URL + "/somewhere"}); !ok(res) || res["values"].(map[string]any)["hub"] != hub.URL {
		t.Errorf("hub: %v", res)
	}
	for _, bad := range []string{"not a url", "http://127.0.0.1:1"} {
		if res := setupCheck(t, s, map[string]any{"step": "hub", "hub": bad}); ok(res) {
			t.Errorf("hub %q passed", bad)
		}
	}

	// the key: one of its own, the node's own refused, the gate's word kept
	res := setupCheck(t, s, map[string]any{"step": "key", "hub": hub.URL, "key": keyPath})
	if !ok(res) || res["warning"] != nil || !strings.Contains(res["message"].(string), "Helper") {
		t.Errorf("key that may post: %v", res)
	}
	other, _, _ := agentKey(t)
	if res := setupCheck(t, s, map[string]any{"step": "key", "hub": hub.URL, "key": other}); !ok(res) || !strings.Contains(fmt.Sprint(res["warning"]), "invite") {
		t.Errorf("key the gate holds back: %v", res)
	}
	if _, err := s.hubIdentity(); err != nil {
		t.Fatal(err)
	}
	if res := setupCheck(t, s, map[string]any{"step": "key", "hub": hub.URL, "key": filepath.Join(s.StateDir, "peer_ed25519")}); ok(res) {
		t.Errorf("the node's own key passed")
	}
	if res := setupCheck(t, s, map[string]any{"step": "key", "hub": hub.URL, "key": filepath.Join(t.TempDir(), "none")}); ok(res) {
		t.Errorf("a missing key file passed")
	}

	// who it answers: ids and names, each once; unknown, two of a name, none
	res = setupCheck(t, s, map[string]any{"step": "answer", "hub": hub.URL, "key": keyPath, "answer": []string{"ada", "2222222222222222", "1111111111111111"}})
	if got, _ := json.Marshal(res["values"]); !ok(res) || string(got) != `{"answer":["1111111111111111","2222222222222222"]}` {
		t.Errorf("answer: %v", res)
	}
	for _, bad := range [][]string{{"nobody"}, {"Grace"}, {"4444444444444444"}, {}, {ident.ID}} {
		if res := setupCheck(t, s, map[string]any{"step": "answer", "hub": hub.URL, "key": keyPath, "answer": bad}); ok(res) {
			t.Errorf("answer %v passed", bad)
		}
	}

	// the model needs the CLI the agent runs
	bin, _ := fakeClaude(t, "9.9.9 (Claude Code)")
	s.hubAgent.bin = bin
	if res := setupCheck(t, s, map[string]any{"step": "model", "model": "", "max_per_day": 40, "max_per_thread": 8}); !ok(res) ||
		res["values"].(map[string]any)["model"] != "claude-fable-5" || !strings.Contains(res["message"].(string), "9.9.9") {
		t.Errorf("model: %v", res)
	}
	if res := setupCheck(t, s, map[string]any{"step": "model", "model": "m", "max_per_day": 0, "max_per_thread": 8}); ok(res) {
		t.Errorf("a limit of 0 passed")
	}

	// repos are git checkouts
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=someone@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "First thing shipped"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if res := setupCheck(t, s, map[string]any{"step": "repos", "repos": []string{repo, " "}}); !ok(res) || !strings.Contains(res["message"].(string), "First thing shipped") {
		t.Errorf("repos: %v", res)
	}
	if res := setupCheck(t, s, map[string]any{"step": "repos", "repos": []string{t.TempDir()}}); ok(res) {
		t.Errorf("a folder that is not a checkout passed")
	}
	if res := setupCheck(t, s, map[string]any{"step": "repos", "repos": []string{}}); !ok(res) {
		t.Errorf("no repos: %v", res)
	}
}

// Create a Key makes one key, in the state folder and private, and never
// another: asked again it answers with the same.
func TestHubAgentKeyMadeOnce(t *testing.T) {
	s := New(config.Default(), nil, nil, "", t.TempDir())
	make := func() map[string]any {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/hub/agent/key", nil))
		var res map[string]any
		json.Unmarshal(rec.Body.Bytes(), &res)
		return res
	}
	first, again := make(), make()
	if first["made"] != true || again["made"] != false || first["id"] != again["id"] || first["id"] == "" {
		t.Fatalf("first %v, again %v", first, again)
	}
	st, err := os.Stat(filepath.Join(s.StateDir, "hub-agent_ed25519"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v %v", st, err)
	}
	if node, _ := s.hubIdentity(); node != nil && node.ID == first["id"] {
		t.Errorf("the agent's key is the node's")
	}
}

// Configuration → Hub's summary: off until set, the setting that holds it
// when one does not work, and on with the hub's names when set.
func TestHubAgentStatus(t *testing.T) {
	keyPath, ident, _ := agentKey(t)
	hub := setupHub(t, ident)
	cfg := config.Default()
	s := New(cfg, nil, nil, "", t.TempDir())
	status := func() map[string]any {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/hub/agent", nil))
		var res map[string]any
		json.Unmarshal(rec.Body.Bytes(), &res)
		return res
	}
	if st := status(); st["state"] != "off" {
		t.Errorf("unset: %v", st)
	}
	cfg.Hub.URL, cfg.Hub.Agent.Key, cfg.Hub.Agent.Answer = hub.URL, filepath.Join(t.TempDir(), "none"), []string{"1111111111111111"}
	if st := status(); st["state"] != "error" || !strings.Contains(st["message"].(string), "hub.agent.key") {
		t.Errorf("a missing key: %v", st)
	}
	cfg.Hub.Agent.Key = keyPath
	st := status()
	answer, _ := json.Marshal(st["answer"])
	if st["state"] != "on" || st["agent"].(map[string]any)["id"] != ident.ID || string(answer) != `[{"id":"1111111111111111","name":"Ada"}]` {
		t.Errorf("set: %v", st)
	}
}
