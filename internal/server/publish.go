package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	gopath "path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"exe/internal/github"
	"exe/internal/sshexec"
)

// Publish to GitHub: the daemon creates (or reuses) a repository with the
// credentials it holds, then pushes the VM's project through a git
// smart-HTTP proxy served over an SSH remote forward onto the guest's
// loopback. The token never enters the VM — not on disk, not in guest
// memory: git talks plain HTTP to 127.0.0.1 and the daemon injects the
// Authorization header on the host side.

const (
	publishTimeout = 10 * time.Minute
	publishMaxOut  = 32 << 10
)

var (
	repoNameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	repoSanitizeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// parseGitHubRemote extracts owner and name from a github.com remote URL
// in https or ssh form; ok is false for any other remote.
func parseGitHubRemote(u string) (owner, name string, ok bool) {
	var rest string
	switch {
	case strings.HasPrefix(u, "https://github.com/"):
		rest = strings.TrimPrefix(u, "https://github.com/")
	case strings.HasPrefix(u, "git@github.com:"):
		rest = strings.TrimPrefix(u, "git@github.com:")
	case strings.HasPrefix(u, "ssh://git@github.com/"):
		rest = strings.TrimPrefix(u, "ssh://git@github.com/")
	default:
		return "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// handlePublishScan lists the candidate project folders for the Publish
// dialog: the non-hidden directories directly under the VM user's home.
func (s *Server) handlePublishScan(w http.ResponseWriter, r *http.Request) {
	info, err := s.runningVM(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, code, err := s.vmTarget(info).Run(ctx,
		`find "$HOME" -mindepth 1 -maxdepth 1 -type d ! -name '.*' | sort`, publishMaxOut)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if code != 0 {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("scan failed: %s", strings.TrimSpace(out)))
		return
	}
	dirs := []string{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			dirs = append(dirs, l)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dirs": dirs})
}

// handlePublish streams NDJSON progress events: {"type":"step","text"},
// then {"type":"done","repo","url"} or {"type":"error","error"}.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Path        string `json:"path"`
		Repo        string `json:"repo"`
		Private     bool   `json:"private"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Path, req.Repo = strings.TrimSpace(req.Path), strings.TrimSpace(req.Repo)
	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	if req.Repo != "" && !repoNameRe.MatchString(req.Repo) {
		writeErr(w, http.StatusBadRequest, errors.New("repository name must be letters, digits, '.', '-' or '_'"))
		return
	}
	info, err := s.runningVM(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	creds, err := s.ghRequireAuth(r.Context())
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), publishTimeout)
	defer cancel()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	emit := func(ev map[string]string) {
		enc.Encode(ev)
		if fl != nil {
			fl.Flush()
		}
	}
	step := func(format string, args ...any) {
		emit(map[string]string{"type": "step", "text": fmt.Sprintf(format, args...)})
	}

	repo, sha, err := s.publishVM(ctx, s.vmTarget(info), creds, req.Path, req.Repo, req.Private, req.Description, step)
	if err != nil {
		emit(map[string]string{"type": "error", "error": err.Error()})
		return
	}
	s.PostNews("vm", "Published to GitHub", publishNews(name, req.Path, repo, sha))
	emit(map[string]string{"type": "done", "repo": repo.FullName, "url": repo.HTMLURL, "commit": sha})
}

// publishNews is the feed line for a completed publish. The commit rides
// along as a [short-sha](url) span, which the Newsfeed body renderer turns
// into a link to that commit on GitHub.
func publishNews(vm, path string, repo *github.Repo, sha string) string {
	line := vm + ": " + path + " → " + repo.HTMLURL
	if len(sha) >= 7 {
		line += " @ [" + sha[:7] + "](" + repo.HTMLURL + "/commit/" + sha + ")"
	}
	return line
}

// publishVM runs the publish steps against one VM and returns the repo
// plus the pushed commit id. Every guest command goes over SSH as the VM
// user; nothing GitHub-related persists in the guest beyond the
// repository's canonical https remote URL.
func (s *Server) publishVM(ctx context.Context, target sshexec.Target, creds *github.Creds,
	path, repoName string, private bool, description string, step func(string, ...any)) (*github.Repo, string, error) {

	q := sshexec.Quote
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = "/home/" + s.Config().SSHUser + strings.TrimPrefix(path, "~")
	}
	run := func(cmd string) (string, int, error) { return target.Run(ctx, cmd, publishMaxOut) }
	fail := func(what, out string) error {
		return fmt.Errorf("%s: %s", what, sshexec.Truncate(strings.TrimSpace(out), 600))
	}

	out, code, err := run("cd " + q(path) + " && pwd -P")
	if err != nil {
		return nil, "", err
	}
	if code != 0 {
		return nil, "", fmt.Errorf("%s is not a directory in the VM", path)
	}
	path = strings.TrimSpace(out)
	git := "git -C " + q(path) + " "

	if _, code, err = run("command -v git >/dev/null 2>&1"); err != nil {
		return nil, "", err
	} else if code != 0 {
		step("Installing git in the VM (first publish)…")
		out, code, err = run("sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq 2>&1 && " +
			"sudo env DEBIAN_FRONTEND=noninteractive apt-get install -qq -y git 2>&1")
		if err != nil {
			return nil, "", err
		}
		if code != 0 {
			return nil, "", fail("installing git failed", out)
		}
	}

	out, code, err = run(git + "rev-parse --is-inside-work-tree 2>/dev/null")
	if err != nil {
		return nil, "", err
	}
	if code == 0 && strings.TrimSpace(out) == "true" {
		// Refuse a subfolder of a larger repository: pushing it would
		// publish the whole worktree's history, not the folder.
		out, code, err = run(git + "rev-parse --show-toplevel")
		if err != nil {
			return nil, "", err
		}
		if top := strings.TrimSpace(out); code == 0 && top != path {
			return nil, "", fmt.Errorf("%s is inside the git repository at %s — publish that folder instead", path, top)
		}
	} else {
		step("Initializing a git repository in %s", path)
		if out, code, err = run(git + "init -q -b main 2>&1"); err != nil {
			return nil, "", err
		} else if code != 0 {
			return nil, "", fail("git init failed", out)
		}
	}

	// Commits are attributed to the GitHub account via its noreply address,
	// set repo-locally so the guest keeps no global identity.
	if out, code, err = run(git + "config --get user.email >/dev/null 2>&1 || { " +
		git + "config user.name " + q(creds.Login) + " && " +
		git + "config user.email " + q(creds.NoreplyEmail()) + "; }"); err != nil {
		return nil, "", err
	} else if code != 0 {
		return nil, "", fail("setting the git identity failed", out)
	}

	_, headCode, err := run(git + "rev-parse -q --verify HEAD >/dev/null 2>&1")
	if err != nil {
		return nil, "", err
	}
	out, _, err = run(git + "status --porcelain | head -1")
	if err != nil {
		return nil, "", err
	}
	if headCode != 0 || strings.TrimSpace(out) != "" {
		step("Committing files")
		msg := "Publish from exe"
		if headCode != 0 {
			msg = "Initial commit"
		}
		out, code, err = run(git + "add -A 2>&1 && " + git + "commit -q -m " + q(msg) + " 2>&1")
		if err != nil {
			return nil, "", err
		}
		if code != 0 {
			if headCode != 0 {
				return nil, "", fail("nothing to publish — no committable files in "+path, out)
			}
			return nil, "", fail("git commit failed", out)
		}
	}

	out, code, err = run(git + "symbolic-ref --short -q HEAD")
	if err != nil {
		return nil, "", err
	}
	branch := strings.TrimSpace(out)
	if code != 0 || branch == "" {
		return nil, "", errors.New("the repository is on a detached HEAD — check out a branch first")
	}

	// No name given (the chat tool's default): reuse the folder's origin
	// remote when it already points at the signed-in account, else fall
	// back to the folder's own name.
	if repoName == "" {
		out, code, err = run(git + "remote get-url origin 2>/dev/null")
		if err != nil {
			return nil, "", err
		}
		if code == 0 {
			if owner, name, ok := parseGitHubRemote(strings.TrimSpace(out)); ok {
				if !strings.EqualFold(owner, creds.Login) {
					return nil, "", fmt.Errorf("origin points at %s/%s, which is not the signed-in account %s — pass a repository name to publish a copy", owner, name, creds.Login)
				}
				repoName = name
			}
		}
		if repoName == "" {
			repoName = strings.Trim(repoSanitizeRe.ReplaceAllString(gopath.Base(path), "-"), "-.")
		}
	}
	if !repoNameRe.MatchString(repoName) {
		return nil, "", fmt.Errorf("no usable repository name (got %q) — pass one explicitly (letters, digits, '.', '-', '_')", repoName)
	}

	repo, err := github.EnsureRepo(ctx, creds, repoName, private, description)
	if err != nil {
		return nil, "", err
	}
	if repo.Created {
		visibility := "public"
		if private {
			visibility = "private"
		}
		step("Created %s repository %s", visibility, repo.FullName)
	} else {
		step("Using existing repository %s", repo.FullName)
	}

	remote := "https://github.com/" + repo.FullName + ".git"
	if out, code, err = run(git + "remote get-url origin >/dev/null 2>&1 && " +
		git + "remote set-url origin " + q(remote) + " || " +
		git + "remote add origin " + q(remote)); err != nil {
		return nil, "", err
	} else if code != 0 {
		return nil, "", fail("setting the origin remote failed", out)
	}

	// The push path: a host-held listener remote-forwarded onto the guest's
	// 127.0.0.1, alive only for this operation and pinned to this one repo.
	client, err := target.Dial(ctx)
	if err != nil {
		return nil, "", err
	}
	defer client.Close()
	ln, err := client.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("forward a port into the guest: %w", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return nil, "", fmt.Errorf("unexpected forward address %v", ln.Addr())
	}
	local := "http://127.0.0.1:" + strconv.Itoa(addr.Port)
	srv := &http.Server{Handler: githubProxy(&url.URL{Scheme: "https", Host: "github.com"},
		creds.AccessToken, "/"+repo.FullName+".git", local)}
	go srv.Serve(ln)
	defer srv.Close()

	step("Pushing %s → %s (the token stays on this host)…", branch, repo.FullName)
	rewrite := q("url." + local + "/.insteadOf=https://github.com/")
	out, code, err = run("git -C " + q(path) + " -c " + rewrite + " push -u origin " + q(branch) + " 2>&1")
	if err != nil {
		return nil, "", err
	}
	if code != 0 {
		return nil, "", fail("git push failed", out)
	}
	// The pushed commit id — worth surfacing, but not worth failing a
	// publish that already landed on GitHub.
	sha := ""
	if out, code, err = run(git + "rev-parse HEAD"); err == nil && code == 0 {
		sha = strings.TrimSpace(out)
	}
	return repo, sha, nil
}

// githubProxy serves the git smart-HTTP protocol for exactly one
// repository, forwarding to upstream with the daemon's token injected as
// Basic auth. The path pin keeps anything else in the guest that races the
// short-lived forward from reaching other repositories, and redirect
// Locations are rewritten so git never leaves the proxy mid-conversation.
func githubProxy(upstream *url.URL, token, repoPath, localBase string) http.Handler {
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	upstreamBase := upstream.String() + "/"
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
			req.Header.Set("Authorization", auth)
		},
		ModifyResponse: func(resp *http.Response) error {
			if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, upstreamBase) {
				resp.Header.Set("Location", localBase+"/"+strings.TrimPrefix(loc, upstreamBase))
			}
			return nil
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != repoPath && !strings.HasPrefix(r.URL.Path, repoPath+"/") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		rp.ServeHTTP(w, r)
	})
}
