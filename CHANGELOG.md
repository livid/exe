# What changed in each release

Every release of exe and what it told its users had changed, newest first.
These are the notes Software Update shows before **Update Now** is pressed.
A release's [page on GitHub](https://github.com/livid/exe/releases) says
more: what was tried before it went out, and what to know before installing.

<!--
`deploy/release.sh publish` adds a release here when it is out, over the
older ones, and nobody else writes its section. What the next release will
say is in NOTES.md. This file is in the binary (changelog.go) and is the
page https://exe.v2core.com/docs/changelog, without this comment, so what
is written here is Markdown that page renders: headings, paragraphs, lists,
`code`, **bold** and links with an address in full.
-->

## 2026.10.15

- On Windows, `exe ssh` reaches a VM again on a PC where Codex has run: the VM key is kept to its user.
- On iPhone and iPad, the desktop checks for updates when reopened; notifications come without its worker.
- The installed app keeps the screen awake while you watch Claude Code or Codex in the active window.
- **Mac OS X** has an **Agent** button: a Chat that works inside Tiger and uploads Workspace files.
- Claude Code window: **Get Info** on a session; its latest prompt over the terminal, a click lists them all.
- On a phone, pinch a picture to zoom it; press and hold a terminal for **Copy**, **Paste**, **Select All**.
- Welcome's **Extras…** installs **Projects**, which sends an issue to Claude Code or Codex from its menu.
- Welcome's Claude Code and Codex card has **Set Up…**: each CLI's version, and how to install a missing one.

## 2026.10.12

- **Software Update** in the Apple menu installs a new release from the desktop.
- **Welcome…** in the Help menu is exe in six cards. **Extras…** on its Apps card turns extra apps on and off.
- **Join a Hub…** in the Hub app: four steps, from a hub's address to your profile.
- **Node Sync…** in the Special menu is the old **Join…**: it pairs this exe with another.
- **Mac OS X** in the Special menu: a Power Mac G4 running Mac OS X 10.4 Tiger, beside Mac OS 9.
- **Search**, beside **New** in the Claude Code window, finds any conversation, archived ones too.
- Right-click the empty part of a Workspace window for **View**: **as Icons** or **as List**, kept per folder.
- Right-click a window's title bar for **Save a Screenshot**: a PNG of it lands in Workspace/Screenshots.

## 2026.10.10.2

This release carried no notes: these were written afterwards, from its page.

- **Windows.** One line in PowerShell installs exe: `irm https://exe.v2core.com/install.ps1 | iex`.
- **`vm_dir`** in the configuration says where the VMs' disks and the base images are kept.
- **`exe daemon start`** and **`exe daemon restart`** start and restart an installed exe by hand.

## 2026.10.10

The first release: until then exe was built from a checkout. It carried no
notes either, so this is what exe could do on that day, written afterwards
from its manual.

### Virtual machines

- Linux VMs that keep their disks, **Debian 13** or **Alpine 3.24**, on Virtualization.framework on a Mac and on KVM through Firecracker on Linux.
- **New VM…** in the File menu needs only a name: 2 CPUs, 2048 MB of memory and a 20 GB disk unless you say otherwise.
- A VM's window has six tabs: **Services**, **Terminal**, **Agent**, **Expose**, **Sessions** and **Notes**.
- Right-click a running VM for **btop**, its process monitor in a terminal window.
- A machine without a hypervisor, such as a NAS or a container, runs the desktop without VMs.

### SSH

- The daemon speaks SSH on port 2222. `ssh -p 2222 exe@host` is a lobby that lists, makes, starts, stops and removes VMs.
- `ssh -p 2222 <vm>@host` lands inside the VM, with scp, sftp and port forwarding.

### Agents

- `exe code <vm> "…"` and a VM's **Agent** tab run the built-in coding agent inside the VM, on a model from Ollama.
- **Chat** carries the conversation on, with Ollama or with a ChatGPT subscription.
- **Claude Code** and **Codex** have a window each when the CLI is installed.
- With tmux on the host, an agent's sessions are listed down its window's left and go on working after the window, or the browser, is closed.
- Drag a file onto an agent's window to hand it over.
- **Agent Skill Guide** in the Help menu: `/skill.md` teaches any coding agent to drive exe's API and its VMs.

### Publishing

- `exe expose <vm> -port 8000 -sub name`, or a VM's **Expose** tab, publishes a port at `https://name.<your domain>` through a Cloudflare Tunnel.
- **Cloudflare Setup Wizard** in the Special menu sets the tunnel up, once.
- A hostname can also be a redirect, or lead to a service on this machine that is not a VM.
- **Publish to GitHub…** on a running VM turns a folder in it into a repository. No GitHub credentials enter the VM.
- **Analytics** shows the traffic Cloudflare saw for every hostname this node publishes.
- `exe site` publishes exe's own homepage and documentation, served out of the binary.

### The desktop

- A Mac OS 9 Platinum desktop in the browser: a menu bar, and windows that move, resize, shade and zoom, their layout remembered.
- **Terminal** is a shell on the host machine. With tmux it outlives a reload or a closed browser.
- **Show All Windows** shrinks every open window into a grid. **Search** finds VMs, chat sessions, notes and todos.
- The **Control Strip**: the Cloudflare tunnel's lamp, Tailscale, token prices, the agents' usage, and charts of CPU, memory, GPU, network and disk.
- Right-click the desktop for a menu of shortcuts, and **Customize…** to make it yours.
- **About This Computer**, **Configuration**, the **Log Viewer** and the **Newsfeed**.
- **Icon Editor** repaints any system icon.
- On a phone the desktop is a home screen of icons with fullscreen windows. Over HTTPS it installs as an app.
- Notifications: a big price move, rain within the hour, an agent that has finished, or a script's own words.
- **Mac OS 9** in the Special menu: an emulated Power Mac G4 running Mac OS 9.2.2 in a window.

### Files and apps

- **Workspace**, a folder shared by you, agents and apps, opens as a Finder window: edit text, look at pictures and web pages, play movies and sounds.
- An app is a folder with an `app.json` and an `index.html`, served from disk: a good thing to ask an agent to build.
- Six extra apps: **Notes**, **Paint**, **Tides**, **Todo**, **Weather** and **World Clock**.
- **Blue Pencil** proofreads what you write, on your own model.
- **Dict** is a dictionary from English, German, French, Spanish, Italian or Latin into Chinese, Japanese or Korean, its entries written by Codex.

### Nodes and the Hub

- **Join…** in the Special menu pairs two exe machines with a one-time code. App data, Workspace files and the Newsfeed then sync both ways.
- **Hub** is a small public feed shared between exe nodes: posts signed by this node's key, with pictures, movies and drawings that draw themselves.

### Installing

- One line installs exe on Linux and macOS, x86-64 and ARM64: `curl -fsSL https://exe.v2core.com/install.sh | sh`.
- **`exe update`** moves an installed exe to the next release. **`exe uninstall`** removes it and keeps your data.
