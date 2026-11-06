package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// The agent CLIs on this machine, for the Agent Setup window (Welcome's
// Claude Code and Codex card) and the card itself: whether each is
// installed, where, and which version, and what an installer would lean
// on here.
//
//	GET /v1/agents/cli
//	{"os": "linux", "arch": "arm64", "tmux": true,
//	 "tmux_install": "sudo apt-get install -y tmux",
//	 "tools": {"curl": true, "node": true, "npm": true, "brew": false},
//	 "agents": {"claude": {"title": "Claude Code", "installed": true,
//	                       "path": "~/.local/bin/claude", "version": "2.1.300"},
//	            "codex": {"title": "Codex", "installed": false}}}
//
// hostinfo says only whether a CLI is there (agents: {claude: {dir}}),
// because the desktop polls it and nothing there may be slow. The
// version is the CLI's own answer to --version, which starts a Node
// program for one of them, so it is asked here, once per installed
// binary: the answer is kept with the file's size and modification time,
// and a reinstall is asked afresh. A CLI that does not answer in time, or
// answers with something that holds no version, is still installed; its
// row says so without a version, and why (error).
type agentCLI struct {
	Title     string `json:"title"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// versionCache remembers each binary's --version by its path, size and
// mtime; a miss runs it again.
var versionCache = struct {
	sync.Mutex
	m map[string]versionEntry
}{m: map[string]versionEntry{}}

type versionEntry struct {
	size    int64
	mod     time.Time
	version string
	err     string
}

// cliVersionTimeout is how long --version may take: a cold Node start
// on a slow disk takes a few seconds.
const cliVersionTimeout = 15 * time.Second

var versionRE = regexp.MustCompile(`\d+\.\d+(?:\.\d+)*(?:-[0-9A-Za-z.]+)?`)

// parseVersion picks the version out of what a CLI printed: "2.1.300
// (Claude Code)" and "codex-cli 0.162.1" both hold one number of the
// shape digits.digits[.digits].
func parseVersion(out string) string {
	return versionRE.FindString(out)
}

// cliVersion asks the binary at path for its version, or returns what it
// said last time if the file has not changed since.
func cliVersion(path string) (string, string) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err.Error()
	}
	versionCache.Lock()
	if e, ok := versionCache.m[path]; ok && e.size == st.Size() && e.mod.Equal(st.ModTime()) {
		versionCache.Unlock()
		return e.version, e.err
	}
	versionCache.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), cliVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = cliEnv(path)
	out, runErr := cmd.CombinedOutput()
	e := versionEntry{size: st.Size(), mod: st.ModTime(), version: parseVersion(string(out))}
	if e.version == "" {
		switch {
		case ctx.Err() != nil:
			e.err = "--version did not answer within " + cliVersionTimeout.String()
		case runErr != nil:
			e.err = strings.TrimSpace(firstLine(string(out), 120) + " " + runErr.Error())
		default:
			e.err = "--version said " + firstLine(string(out), 120)
		}
	}
	versionCache.Lock()
	versionCache.m[path] = e
	versionCache.Unlock()
	return e.version, e.err
}

// tildePath writes a path under the home folder with "~", as the manual
// does (extraInfo.said).
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == string(filepath.Separator) || p == "" {
		return p
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || !filepath.IsLocal(rel) {
		return p
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}

// installerTools are what the Agent Setup window's install pages lean
// on: the CLIs come from Anthropic's installer (curl), npm (node) or
// Homebrew.
var installerTools = []string{"curl", "node", "npm", "brew"}

func (s *Server) handleAgentCLI(w http.ResponseWriter, r *http.Request) {
	agents := map[string]agentCLI{}
	names := make([]string, 0, len(hostAgents))
	for name := range hostAgents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := hostAgents[name]
		row := agentCLI{Title: a.title}
		if p := agentPath(a); p != "" {
			row.Installed, row.Path = true, tildePath(p)
			row.Version, row.Error = cliVersion(p)
		}
		agents[name] = row
	}
	tools := map[string]bool{}
	for _, t := range installerTools {
		tools[t] = cliPath(t) != ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"os": runtime.GOOS, "arch": runtime.GOARCH,
		"tmux":         tmuxCmd("-V") != nil,
		"tmux_install": tmuxInstall(runtime.GOOS, os.Geteuid(), cliPath),
		"tools":        tools,
		"agents":       agents,
	})
}

// tmuxInstall is the command line that installs tmux on this machine, for
// Welcome's Install tmux…, which runs it in a Terminal so that sudo asks
// for the password where it is typed: Homebrew's on a Mac; on Linux the
// system's package manager, through sudo unless the daemon runs as root.
// It is "" where exe knows no way — Windows, which has no tmux, a Mac
// without Homebrew, a Linux without sudo or a package manager it knows —
// and the card then says to install tmux by hand. find is cliPath: a
// program's path, or "".
func tmuxInstall(goos string, euid int, find func(name string, homeDirs ...string) string) string {
	switch goos {
	case "darwin":
		brew := find("brew")
		if brew == "" {
			return ""
		}
		if p, err := exec.LookPath("brew"); err != nil || p != brew {
			return brew + " install tmux" // Homebrew is not on the login shell's PATH
		}
		return "brew install tmux"
	case "linux":
		for _, m := range [][2]string{
			{"apt-get", "apt-get install -y tmux"},
			{"dnf", "dnf install -y tmux"},
			{"yum", "yum install -y tmux"},
			{"zypper", "zypper --non-interactive install tmux"},
			{"pacman", "pacman -S --noconfirm tmux"},
			{"apk", "apk add tmux"},
		} {
			if find(m[0]) == "" {
				continue
			}
			if euid == 0 {
				return m[1]
			}
			if find("sudo") == "" {
				return ""
			}
			return "sudo " + m[1]
		}
	}
	return ""
}
