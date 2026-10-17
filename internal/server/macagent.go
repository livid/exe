package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"exe/internal/agent"
	"exe/internal/chat"
	"exe/internal/sshexec"
)

// A Chat pinned to the Mac OS X app's Mac: the Agent button of its window
// opens one, and the chat's tools run inside Tiger over Remote Login, on
// the loopback port the Mac forwards (internal/macos9/agent.go), as the
// account Agent setup names, with the key that dialog hands out. The
// Linux VMs are out of its scope as another VM is out of a pinned chat's;
// expose, list_ports and github_push, which read a VM's network or its
// record, are not offered.

// macChatPin is the pin of a chat with the Mac, where a VM's chat carries
// the VM's name. No VM can be called this: vmm's names are lowercase
// without spaces.
const macChatPin = "Mac OS X"

// macUnavailable are the tools a chat with the Mac is not offered: they
// read a VM's network, its record or its GitHub standing.
var macUnavailable = map[string]bool{"list_ports": true, "list_routes": true, "expose": true, "unexpose": true, "github_push": true, "start_vm": true, "stop_vm": true}

// macTarget is the Mac as the chat's tools reach it.
func (s *Server) macTarget() (sshexec.Target, error) {
	m := s.macOSXManager()
	a, err := m.Agent()
	if err != nil {
		return sshexec.Target{}, err
	}
	if a == nil || a.Port == 0 {
		return sshexec.Target{}, errors.New("the Mac is not running: start it from the Mac OS X window first")
	}
	if a.User == "" {
		return sshexec.Target{}, errors.New("no account for the agent on the Mac yet: under Agent setup in the Mac OS X window's Setup menu, enter the Mac account to work as and paste the key line into the Mac's Terminal")
	}
	return sshexec.Target{Host: "127.0.0.1", Port: a.Port, User: a.User, KeyPath: m.AgentKeyPath(), Legacy: true}, nil
}

const chatMacTmpl = `You are the operator of the Mac in exe's Mac OS X window: an emulated Power Mac G4 running Mac OS X 10.4 Tiger (2005), whose screen the user sees in that window beside this chat. All your tools act on this Mac only; the Linux VMs of exe are out of scope for this conversation. Inside the Mac you act as the account %[1]q over SSH (Tiger's Remote Login). sudo asks for a password you do not have: stay within the account unless the user gives one.
%[2]s
The Mac runs Mac OS X 10.4 Tiger on PowerPC, a BSD userland of 2005: bash 2.05b (/bin/sh is bash too), perl 5.8, python 2.3, ruby 1.8. head has no -c (use tail -c, or dd with 2>/dev/null); mktemp needs a template (mktemp /tmp/x.XXXXXX); there is no base64, wget, git or Homebrew; curl speaks no current TLS, so most https sites fail while plain http works. Apple's tools: open launches a file or an app; osascript -e '…' drives the Finder and the apps on the user's screen (tell application "Finder" to set desktop picture to POSIX file "/path/to/picture"); screencapture -x /path.png photographs the screen; defaults, installer -pkg … -target /, hdiutil attach for disk images; launchctl with a plist in ~/Library/LaunchAgents keeps something running. Xcode 2.5 (gcc 4.0, PowerPC) is there only if the user installed it: check /Developer. The account's Desktop is /Users/%[1]s/Desktop, and what lands there shows on the screen.

Rules:
- For a multi-step task, call plan first with a short markdown checklist ("- [ ] step"), and update it via plan with "- [x]" checkmarks as steps complete — the user watches it as a live checklist. Skip it for trivial requests.
- bash runs non-interactive commands only; nothing may wait at a prompt. Change existing files with edit_file; write_file writes whole text files (text only); upload copies a file of the user's Workspace, a picture included, into the Mac; read_file elides the middle of large files — read an exact region with read_file offset/limit or grep -n.
- The screen is the user's: the Finder, the Dock and the open windows are theirs. Do not log out, restart or shut down the Mac, and run no Software Update, unless the user asked for that.
- The Mac's saved memory and current state are in your context; when you learn something durable, save the complete updated memory with remember.
- Format answers in Markdown (lists, tables, code blocks and links render nicely), and keep them concise. When you finish a task, say what changed and where to look for it on the Mac's screen.`

// macSystemPrompt is the operator's prompt for a chat pinned to the Mac.
func (s *Server) macSystemPrompt() string {
	user := ""
	if a, err := s.macOSXManager().Agent(); err == nil && a != nil {
		user = a.User
	}
	note := ""
	if user == "" {
		user = "(not chosen yet)"
		note = "\nNo account is chosen yet, so every tool will fail until the user enters one under Agent setup in the Mac OS X window's Setup menu and pastes the key line into the Mac's Terminal: say so first.\n"
	}
	return fmt.Sprintf(chatMacTmpl, user, note)
}

// macBriefing is what the run opens with: the Mac's state, what its shell
// answered a moment ago (or why it did not), the agent's memory of the
// Mac and the summaries of recent sessions with it.
func (s *Server) macBriefing(ctx context.Context, selfID string) string {
	var b strings.Builder
	b.WriteString("What you know about the Mac (gathered automatically; live facts are current as of the start of this run):\n")
	st := s.macOSXManager().Status()
	switch {
	case !st.Installed:
		b.WriteString("\nState: Mac OS X is not installed on this computer yet. Nothing can run inside it; tell the user so.\n")
	case !st.Running:
		b.WriteString("\nState: the Mac is shut down. Every tool will fail until the user starts it from the Mac OS X window; say so.\n")
	default:
		b.WriteString("\nState: running, PowerPC G4 with 1 GB of memory, the screen in the Mac OS X window.\n")
		if t, err := s.macTarget(); err != nil {
			fmt.Fprintf(&b, "Shell: not reachable: %v\n", err)
		} else {
			tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			out, code, err := t.Run(tctx, "sw_vers -productVersion; uname -mp; ls /Applications /Developer 2>/dev/null | head -60", 4000)
			cancel()
			switch {
			case err != nil:
				fmt.Fprintf(&b, "Shell: the Mac did not answer over SSH (%v). Remote Login may be off in the Mac's System Preferences → Sharing, the account may not have authorized the key from Setup details yet, or Tiger may still be starting. Say so, tell the user what to check, and try bash once more only after they say it is done.\n", err)
			case code != 0:
				fmt.Fprintf(&b, "Shell: answered with exit %d: %s\n", code, strings.TrimSpace(out))
			default:
				fmt.Fprintf(&b, "Shell: answers. sw_vers, uname and the contents of /Applications and /Developer:\n%s\n", strings.TrimSpace(out))
			}
		}
	}
	if mem := s.readVMMemory(macChatPin); strings.TrimSpace(mem) != "" {
		fmt.Fprintf(&b, "\nYour memory of this Mac (you wrote this in earlier sessions; the remember tool replaces it):\n%s\n", strings.TrimSpace(mem))
	}
	if metas, err := chat.List(s.chatDir()); err == nil {
		var lines []string
		for _, m := range metas { // newest first
			if m.VM != macChatPin || m.ID == selfID || m.Summary == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", m.UpdatedAt.Format("2006-01-02"), m.Summary))
			if len(lines) == 5 {
				break
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\nRecent sessions on this Mac:\n%s\n", strings.Join(lines, "\n"))
		}
	}
	b.WriteString("\nWhen you learn something durable about this Mac — what is installed where, how the user likes it, gotchas hit — save it with the remember tool. Write the complete memory: it replaces the text above, so keep what still matters and drop what doesn't.")
	return b.String()
}

// uploadMax caps what the upload tool carries into a guest in one call.
const uploadMax = 64 << 20

// workspaceFile reads a file of the Workspace for the upload tool: a path
// inside the Workspace, a file and not a folder, under uploadMax.
func (s *Server) workspaceFile(rel string) ([]byte, error) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
	if rel == "" {
		return nil, errors.New("source is required: a file's path inside the Workspace, such as Paintings/sunset.png")
	}
	p, err := scopedPath(s.workspaceDir(), rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("no such file in the Workspace: %s", rel)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a folder; upload takes one file", rel)
	}
	if st.Size() > uploadMax {
		return nil, fmt.Errorf("%s is %d bytes; upload carries at most %d", rel, st.Size(), uploadMax)
	}
	return os.ReadFile(p)
}

// uploadTool is the schema of the Workspace-to-guest copy.
func uploadTool(vm map[string]any) agent.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return agent.MkTool("upload", "Copy a file from the user's Workspace (the files in exe's Finder on this machine) into the guest, bytes intact — pictures and other binaries included. Parent directories are created.", map[string]any{
		"vm":     vm,
		"source": str("the file's path inside the Workspace, as the user sees it (e.g. Paintings/sunset.png)"),
		"path":   str("absolute destination path inside the guest, file name included"),
	}, []string{"vm", "source", "path"})
}

// handleMacAgentPut records the account the agent signs in as:
// PUT /v1/macosx/agent {"user": "exe"}. The Mac OS 9 Mac has no agent.
func (s *Server) handleMacAgentPut(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	m := s.mac(r)
	if a, err := m.Agent(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if a == nil {
		http.Error(w, "This Mac has no shell for an agent.", http.StatusNotFound)
		return
	}
	var req struct {
		User string `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := m.SetAgentUser(req.User); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.handleMacOS9Status(w, r)
}

// macMemoryPath is where the agent's memory of the Mac lives: with the
// Mac, which is this node's alone, not among the VMs.
func (s *Server) macMemoryPath() string { return filepath.Join(s.StateDir, "mac-osx", "memory.md") }
