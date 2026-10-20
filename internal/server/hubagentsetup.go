// The hub agent's setup, for Configuration → Hub: what the tab shows
// (GET /v1/hub/agent), each step of the Hub Agent Setup wizard checked
// before the next opens (POST /v1/hub/agent/check), and a key of the
// agent's own made on this machine (POST /v1/hub/agent/key). The settings
// themselves are saved with PUT /v1/config like any other.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"exe/internal/peer"
)

// hubAskClient reads a hub's public answers for the setup: a hub that
// does not answer in a few seconds is one the wizard says so about.
var hubAskClient = &http.Client{Timeout: 6 * time.Second}

func hubAskJSON(ctx context.Context, u string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := hubAskClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return resp.StatusCode, fmt.Errorf("status %d", resp.StatusCode)
	}
	return 200, json.NewDecoder(resp.Body).Decode(out)
}

// hubProfileName is a profile's name on the hub: "" for one without a
// name, and an error for one the hub does not know.
func hubProfileName(ctx context.Context, hub, id string) (string, error) {
	var p struct {
		Name string `json:"name"`
	}
	if code, err := hubAskJSON(ctx, hub+"/v1/profile/"+url.PathEscape(id), &p); err != nil {
		if code == http.StatusNotFound {
			return "", errNoProfile
		}
		return "", err
	}
	return p.Name, nil
}

var errNoProfile = errors.New("no such profile on this hub")

var hubProfileID = regexp.MustCompile(`^[0-9a-f]{16}$`)

type hubNamed struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// handleHubAgentStatus is what Configuration → Hub shows: whether the
// agent is on, off or held by a setting that does not work, and what it
// is set to, with the hub's names for the profiles.
func (s *Server) handleHubAgentStatus(w http.ResponseWriter, r *http.Request) {
	hc := s.Config().Hub
	res := map[string]any{
		"hub": hc.URL, "key": hc.Agent.Key, "model": hc.Agent.Model, "repos": hc.Agent.Repos,
		"max_per_day": hc.Agent.MaxPerDay, "max_per_thread": hc.Agent.MaxPerThread,
	}
	set, err := s.hubAgentSetup()
	switch {
	case err != nil:
		res["state"], res["message"] = "error", err.Error()
	case set == nil:
		res["state"] = "off"
	default:
		res["state"], res["live"] = "on", s.hubAgent.live.Load()
		res["model"], res["max_per_day"], res["max_per_thread"] = set.model, set.perDay, set.perThread
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	hub, _ := hubURL(hc.URL)
	name := func(id string) hubNamed {
		n := hubNamed{ID: id}
		if hub != "" {
			n.Name, _ = hubProfileName(ctx, hub, id)
		}
		return n
	}
	if hc.Agent.Key != "" {
		if ident, err := peer.LoadIdentityFile(expandHome(hc.Agent.Key)); err == nil {
			res["agent"] = name(ident.ID)
		}
	}
	answer := []hubNamed{}
	for _, id := range hc.Agent.Answer {
		answer = append(answer, name(id))
	}
	res["answer"] = answer
	writeJSON(w, http.StatusOK, res)
}

// handleHubAgentCheck checks one step of the wizard. A setting that does
// not work is ok:false (HTTP 200), as the Cloudflare wizard does. Each
// answer has a code and its details, which the desk words in its own
// language, and the same in English as message for any other caller;
// values are the step's settings as they will be saved (a name turned
// into its profile id), and info what the review shows.
func (s *Server) handleHubAgentCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Step         string   `json:"step"`
		Hub          string   `json:"hub"`
		Key          string   `json:"key"`
		Answer       []string `json:"answer"`
		Model        string   `json:"model"`
		MaxPerDay    int      `json:"max_per_day"`
		MaxPerThread int      `json:"max_per_thread"`
		Repos        []string `json:"repos"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	fail := func(code, msg string, kv ...any) {
		res := map[string]any{"ok": false, "code": code, "message": msg}
		for i := 0; i+1 < len(kv); i += 2 {
			res[kv[i].(string)] = kv[i+1]
		}
		writeJSON(w, http.StatusOK, res)
	}
	hub, herr := hubURL(strings.TrimSpace(req.Hub))

	switch req.Step {
	case "hub":
		if herr != nil {
			fail("address", "Type the hub's address, such as http://100.64.0.2:7788.")
			return
		}
		var info struct {
			ID    string         `json:"id"`
			Stats map[string]int `json:"stats"`
		}
		if _, err := hubAskJSON(ctx, hub+"/v1/hub", &info); err != nil || info.ID == "" {
			fail("unreachable", fmt.Sprintf("No exe-hub answered at %s: %v.", hub, orNot(err)), "hub", hub, "detail", orNot(err).Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "values": map[string]any{"hub": hub},
			"profiles": info.Stats["profiles"], "posts": info.Stats["posts"],
			"message": fmt.Sprintf("An exe-hub answered: %d profiles, %d posts.", info.Stats["profiles"], info.Stats["posts"])})

	case "key":
		path := strings.TrimSpace(req.Key)
		if path == "" {
			fail("empty", "Give the path of the agent's key file, or click Create a Key.")
			return
		}
		ident, err := peer.LoadIdentityFile(expandHome(path))
		if err != nil {
			fail("unreadable", "The key file does not work: "+err.Error()+".", "detail", err.Error())
			return
		}
		if node, err := s.hubIdentity(); err == nil && node.ID == ident.ID {
			fail("node", "That is this node's own key. The agent needs a key of its own.")
			return
		}
		res := map[string]any{"ok": true, "values": map[string]any{"key": path}}
		agent := hubNamed{ID: ident.ID}
		var warn, warnings []string
		if herr == nil {
			name, err := hubProfileName(ctx, hub, ident.ID)
			switch {
			case errors.Is(err, errNoProfile):
				warn = append(warn, "The hub has no profile for this key yet; the agent's first post makes one, with no name.")
				warnings = append(warnings, "no-profile")
			case err == nil:
				agent.Name = name
			}
			var gate struct {
				Gate   string `json:"gate"`
				Banned bool   `json:"banned"`
			}
			if _, err := hubAskJSON(ctx, hub+"/v1/gate?author="+url.QueryEscape(ident.PubKey()), &gate); err == nil {
				switch {
				case gate.Banned:
					warn = append(warn, "The hub has banned this key: the agent cannot post there.")
					warnings = append(warnings, "banned")
				case gate.Gate == "below" || gate.Gate == "unavailable":
					warn = append(warn, "The hub does not let this key post yet (its gate says "+gate.Gate+"). An admin of the hub can invite it.")
					warnings = append(warnings, "gate")
				}
			}
		}
		who := ident.ID
		if agent.Name != "" {
			who = agent.Name + " (" + ident.ID + ")"
		}
		res["message"] = "The key is the agent " + who + "."
		res["info"] = map[string]any{"agent": agent}
		if len(warn) > 0 {
			res["warning"], res["warnings"] = strings.Join(warn, " "), warnings
		}
		writeJSON(w, http.StatusOK, res)

	case "answer":
		if herr != nil {
			fail("hub", "The hub's address does not work: go back to its step.")
			return
		}
		var agentID string
		if ident, err := peer.LoadIdentityFile(expandHome(strings.TrimSpace(req.Key))); err == nil {
			agentID = ident.ID
		}
		var ids []string
		var named []hubNamed
		seen := map[string]bool{}
		for _, raw := range req.Answer {
			who := strings.TrimSpace(raw)
			if who == "" {
				continue
			}
			var n hubNamed
			if id := strings.ToLower(who); hubProfileID.MatchString(id) {
				name, err := hubProfileName(ctx, hub, id)
				if err != nil {
					fail("profile", fmt.Sprintf("%s: %v.", id, err), "who", id, "unknown", errors.Is(err, errNoProfile))
					return
				}
				n = hubNamed{ID: id, Name: name}
			} else {
				var found []hubNamed
				if _, err := hubAskJSON(ctx, hub+"/v1/profiles?q="+url.QueryEscape(who), &found); err != nil {
					fail("search", fmt.Sprintf("The hub did not search for %q: %v.", who, err), "who", who)
					return
				}
				var exact []hubNamed
				for _, p := range found {
					if strings.EqualFold(p.Name, who) {
						exact = append(exact, p)
					}
				}
				switch len(exact) {
				case 0:
					fail("unknown", fmt.Sprintf("No profile on the hub is named %q.", who), "who", who)
					return
				case 1:
					n = exact[0]
				default:
					fail("ambiguous", fmt.Sprintf("%d profiles are named %q: give the one you mean by its id.", len(exact), who), "who", who, "n", len(exact))
					return
				}
			}
			if n.ID == agentID {
				fail("self", "The agent never answers itself: take its own profile off the list.")
				return
			}
			if !seen[n.ID] {
				seen[n.ID] = true
				ids = append(ids, n.ID)
				named = append(named, n)
			}
		}
		if len(ids) == 0 {
			fail("none", "Name at least one profile: the agent answers nobody else.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "values": map[string]any{"answer": ids},
			"info": map[string]any{"answer": named}, "n": len(ids),
			"message": fmt.Sprintf("The agent answers %d profile(s), and nobody else.", len(ids))})

	case "model":
		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = "claude-fable-5"
		}
		if req.MaxPerDay < 1 || req.MaxPerThread < 1 {
			fail("limits", "Both limits are 1 or more.")
			return
		}
		bin := s.hubAgent.bin // as hubAgentAsk finds it
		if bin == "" {
			bin = claudePath()
		}
		if bin == "" {
			fail("no-cli", "Claude Code is not installed on this host, and the agent writes its replies with it.")
			return
		}
		version := ""
		if out, err := exec.CommandContext(ctx, bin, "--version").Output(); err == nil {
			version = " " + strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": strings.TrimSpace(version),
			"values":  map[string]any{"model": model, "max_per_day": req.MaxPerDay, "max_per_thread": req.MaxPerThread},
			"message": "Claude Code" + version + " is on this host."})

	case "repos":
		var repos []string
		var subjects []string
		for _, raw := range req.Repos {
			p := strings.TrimSpace(raw)
			if p == "" {
				continue
			}
			out, err := exec.CommandContext(ctx, "git", "-C", expandHome(p), "log", "-1", "--format=%s").Output()
			if err != nil {
				fail("not-git", p+" is not a git checkout on this host.", "path", p)
				return
			}
			repos = append(repos, p)
			subjects = append(subjects, filepath.Base(expandHome(p))+": "+strings.TrimSpace(string(out)))
		}
		msg := "No repos: the agent talks about the thread alone."
		if len(repos) > 0 {
			msg = "Latest commits — " + strings.Join(subjects, " · ")
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "values": map[string]any{"repos": repos}, "subjects": subjects, "message": msg})

	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown step %q", req.Step))
	}
}

func orNot(err error) error {
	if err == nil {
		return errors.New("its answer has no hub id")
	}
	return err
}

// handleHubAgentKey makes the agent a key of its own, in the state folder
// beside the node's peer key, and never replaces one made before: asked
// again, it answers with the same key.
func (s *Server) handleHubAgentKey(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join(s.StateDir, "hub-agent_ed25519")
	made := false
	if _, err := os.Stat(p); os.IsNotExist(err) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		_, err = f.Write(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(p)
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		made = true
	}
	ident, err := peer.LoadIdentityFile(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": tildePath(p), "id": ident.ID, "made": made})
}
