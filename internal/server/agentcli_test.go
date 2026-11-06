package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"exe/internal/config"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"2.1.300 (Claude Code)\n": "2.1.300",
		"codex-cli 0.162.1\n":     "0.162.1",
		"1.0.0-beta.3":            "1.0.0-beta.3",
		"no version here":         "",
	} {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// The endpoint reports an installed CLI with its version, and one that is
// not there as not installed; a fake claude on PATH stands for the real
// one, and a reinstall (a changed file) is asked afresh.
func TestAgentCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "claude")
	write := func(v string) {
		if err := os.WriteFile(script, []byte("#!/bin/sh\necho '"+v+" (Claude Code)'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("9.9.9")
	// PATH is the fake folder alone, and HOME a folder with none of the
	// usual install places, so neither CLI is found anywhere else
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ask := func() map[string]any {
		rec := httptest.NewRecorder()
		s.handleAgentCLI(rec, httptest.NewRequest("GET", "/v1/agents/cli", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := ask()
	agents := out["agents"].(map[string]any)
	claude := agents["claude"].(map[string]any)
	if claude["installed"] != true || claude["version"] != "9.9.9" || claude["path"] != script || claude["title"] != "Claude Code" {
		t.Errorf("claude: %v", claude)
	}
	codex := agents["codex"].(map[string]any)
	if codex["installed"] != false || codex["version"] != nil || codex["title"] != "Codex" {
		t.Errorf("codex: %v", codex)
	}
	if out["os"] != runtime.GOOS || out["arch"] != runtime.GOARCH {
		t.Errorf("os/arch: %v %v", out["os"], out["arch"])
	}
	tools := out["tools"].(map[string]any)
	for _, name := range installerTools {
		if _, ok := tools[name]; !ok {
			t.Errorf("tools lacks %s: %v", name, tools)
		}
	}
	// the same file again is the cached answer; a new file is asked
	versionCache.Lock()
	versionCache.m[script] = versionEntry{size: versionCache.m[script].size, mod: versionCache.m[script].mod, version: "cached"}
	versionCache.Unlock()
	if v := ask()["agents"].(map[string]any)["claude"].(map[string]any)["version"]; v != "cached" {
		t.Errorf("a second ask ran the CLI again: %v", v)
	}
	write("10.0.1-rc.2 and more text")
	if v := ask()["agents"].(map[string]any)["claude"].(map[string]any)["version"]; v != "10.0.1-rc.2" {
		t.Errorf("a changed file was not asked afresh: %v", v)
	}
	// one that says nothing of a version is installed, with the reason
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'hello there'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := ask()["agents"].(map[string]any)["claude"].(map[string]any)
	if c["installed"] != true || c["version"] != nil || c["error"] != "--version said hello there" {
		t.Errorf("no version: %v", c)
	}
}

// Install tmux… runs what the machine's own package manager would: the
// first one found, through sudo unless the daemon is root, and nothing
// where there is no way to install it.
func TestTmuxInstall(t *testing.T) {
	has := func(names ...string) func(string, ...string) string {
		return func(name string, _ ...string) string {
			for _, n := range names {
				if n == name {
					return "/usr/bin/" + name
				}
			}
			return ""
		}
	}
	for _, c := range []struct {
		goos string
		euid int
		find func(string, ...string) string
		want string
	}{
		{"linux", 1000, has("apt-get", "sudo"), "sudo apt-get install -y tmux"},
		{"linux", 0, has("apt-get"), "apt-get install -y tmux"},
		{"linux", 1000, has("dnf", "yum", "sudo"), "sudo dnf install -y tmux"},
		{"linux", 1000, has("pacman", "sudo"), "sudo pacman -S --noconfirm tmux"},
		{"linux", 1000, has("apk", "sudo"), "sudo apk add tmux"},
		{"linux", 1000, has("apt-get"), ""}, // no sudo, not root
		{"linux", 1000, has("sudo"), ""},    // no package manager it knows
		{"darwin", 501, has(), ""},          // no Homebrew
		{"windows", -1, has("apt-get", "sudo"), ""},
	} {
		if got := tmuxInstall(c.goos, c.euid, c.find); got != c.want {
			t.Errorf("%s euid %d: %q, want %q", c.goos, c.euid, got, c.want)
		}
	}
	// a Homebrew the login shell would not find is run by its path
	if got := tmuxInstall("darwin", 501, func(n string, _ ...string) string {
		if n == "brew" {
			return "/nonexistent/homebrew/bin/brew"
		}
		return ""
	}); got != "/nonexistent/homebrew/bin/brew install tmux" {
		t.Errorf("brew off PATH: %q", got)
	}
}
