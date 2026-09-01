# Using exe

exe is a personal VM cloud: a single binary running on this machine that
creates persistent Linux virtual machines, lets you (and AI agents) work
inside them over SSH, and can publish any VM port to a real HTTPS address
through your Cloudflare Tunnel. This desktop is its control panel — and
everything you see here can also be driven from a terminal or by an agent
over HTTP and SSH.

## The desktop

The menu bar works like the classic Mac it resembles:

- **File** — New VM…, Upload to Workspace…, Close Window, Refresh.
- **Windows** — reopen the core windows: Virtual Machines, Chat, Newsfeed,
  Icon Editor, Configuration, Daemon Log.
- **Special** — Join… (pair another exe machine), Cloudflare Status and
  Setup Wizard, Set API Token….
- **Help** — this page, and the Agent Skill Guide for handing exe to a
  coding agent.

Right-click the desktop (long-press on a phone) for the **desktop menu**: a
NeXT-style menu that pops up at the pointer and reaches everything — New
VM, a terminal, the Workspace, every VM (each with its own submenu: Open,
Terminal, Start/Stop, Restart, Expose…), every app and open window, the
tools, Cloudflare and Help. **Customize…** at its bottom opens the menu's
text file in an editor: one item per line — a label, a tab or two spaces,
then an action — a label on its own starts a submenu (indent the lines
under it), `-` is a separator, and `@vms`, `@apps` and `@windows` are lists
that fill themselves in. The file's own header lists every action. Save
hands it to the daemon, which checks it — a bad line is named and nothing
changes — and then puts the new menu on every desk sharing this one; an
empty file restores the factory menu.

Desktop icons: **Workspace** (shared files), **Terminal** (a shell on this
host machine), one icon per VM, one per installed app, plus **Newsfeed**,
**Chat** (appears when Ollama is reachable) and the **Trash**. Double-click
opens things.

Windows behave like OS 9 windows: drag the title bar to move, drag the
left/right/bottom edges or the grow corner to resize, click the shade box to
collapse a window to its title bar, the zoom box to toggle its size. The
whole layout — positions, stacking, which windows are open — is saved on the
daemon and mirrored live to every browser looking at this desk, so dragging
a window here moves it on your other screens too.

On a phone the desktop becomes a home screen of icons and windows go
fullscreen, one at a time; closing a window walks back through the stack
like a phone's back button.

Every system icon is hand-plotted pixel art, and **Windows → Icon Editor**
lets you repaint it: the gallery lists each one (the VM Mac, folders,
documents, the Trash, the minis in search results, even the Apple menu),
and double-clicking opens a fat-bits editor — pencil, eraser, eyedropper,
fill, undo, the Platinum palette plus a custom color well. Save applies the
art everywhere at once, follows the desk into every browser and joined
node, and survives restarts; Revert brings the factory icon back. On a VM
icon, pixels painted the factory screen-green keep changing color with the
VM's state. **New Icon…** adds icons of your own on a 32×32 or 16×16 grid —
draw them, copy their SVG for use anywhere, delete them when done. System
icons can only be repainted, never deleted.

## Virtual machines

Choose **File → New VM…** (or the New VM… button in the Virtual Machines
window). Only the name is required; the defaults are 2 CPUs, 2048 MB of
memory and a 20 GB disk. The very first VM downloads the Debian base image
(~3 GB) once — later VMs clone it and boot in seconds. VMs persist: stopping
one keeps its disk, starting boots it again, deleting destroys the disk too.

Double-click a VM in the list to open its window. The tabs:

- **Services** — TCP ports listening inside the VM, with one-click links,
  plus the routes already published to the web. Servers must bind
  `0.0.0.0` (not `127.0.0.1`) to show up here or be exposable.
- **Terminal** — a full SSH terminal in the browser.
- **Agent** — tell the built-in coding agent what to build; it gets a shell
  in this VM and streams its work live.
- **Expose** — publish a VM port to an HTTPS subdomain (see below).
- **Sessions** — every chat pinned to this VM, agent runs included, each
  with a model-written one-line summary of what it accomplished; click one
  to reopen it in the Chat window.
- **Notes** — free-form notes about the VM, saved automatically. Agents are
  told to read these before working in an unfamiliar VM, so write down what
  runs where.

## SSH from your own terminal

The daemon speaks SSH on port **2222**, and the username picks where you
land:

```sh
ssh -p 2222 demo@this-host        # straight into the VM "demo" (auto-starts it)
scp -P 2222 app.py demo@this-host:~/          # scp, sftp, -L/-R all work
ssh -p 2222 -L 8000:localhost:8000 demo@this-host   # tunnel a VM port

ssh -p 2222 exe@this-host         # the lobby: ls, new, start, stop, rm,
                                  # ip, code, expose, routes (--json too)
```

Keys that get in: any public key in the daemon user's `~/.ssh`, the service
key in `~/.exe/ssh/`, and keys listed in `~/.exe/ssh/authorized_clients`
(authorized_keys format — add your phone or laptop key there; edits apply
immediately). There is no first-come key adoption, so the gate is safe to
leave on a LAN.

## The coding agent

Point exe at Ollama in **Windows → Configuration** (`ollama.base_url` and
`ollama.model`; `ollama.effort` sets the thinking effort on models that
support it, or `off` to disable thinking). A local signed-in Ollama at `http://127.0.0.1:11434` can
use cloud models like `glm-5.2:cloud` with no API key; `https://ollama.com`
needs one. Then:

- The **Agent** tab in a VM window runs the agent inside that VM. It can
  install packages, write code and start services — it has passwordless
  sudo *inside the VM*, and the VM is the sandbox boundary.
- The **Chat** icon and window appear once a chat backend is usable: a
  conversation that can see and drive your whole VM cloud. Replies run in
  the daemon, not in the browser: closing the tab (or losing the network)
  never interrupts a long task — reopen the chat and select the session,
  marked with a green dot while streaming, to rejoin it live. The **Stop**
  button actually cancels the run.

The Chat window can also run on a **ChatGPT subscription** instead of
Ollama: in **Windows → Configuration → OpenAI**, click **Sign in with
ChatGPT…** (the OAuth flow the Codex CLI uses — no API key), set
`chat_provider` to `openai`, pick a model (`gpt-5.4`, `gpt-5.4-codex`, …)
plus an optional reasoning effort, and Save. The browser sign-in redirects to `localhost:1455`; the daemon
listens on all interfaces there, so when it runs on another machine swap
`localhost` for the daemon's host in that final URL — or paste the URL
into the tab's paste field. Tokens live in `~/.exe/openai.json` and refresh themselves.
While signed in the tab also shows the subscription's rate-limit usage —
the rolling 5-hour and weekly windows, with their reset times — and any
credit balance. The per-VM Agent tab stays on Ollama.

Prefer your own agent? See **Help → Agent Skill Guide**: exe serves a
`/skill.md` file that teaches Claude Code, Codex or any other coding agent
how to drive the API and the VMs.

## Publishing to the web

One-time setup: run **Special → Cloudflare Setup Wizard…** with a Cloudflare
API token (Zone → DNS → Edit, Account → Cloudflare Tunnel → Edit) and a
remotely-managed tunnel. The Cloudflare dot in the menu bar shows tunnel
health at a glance.

Then, in a VM's **Expose** tab, pick a port and an optional subdomain (it
defaults to the VM name). exe creates the DNS record, updates the tunnel
ingress, and routes the hostname through its reverse proxy to the VM — one
click later the service is live at `https://<sub>.<your-domain>`. Current
routes are listed in the Services tab and in **Special → Cloudflare
Status…**, where they can be unpublished.

## Publishing to GitHub

Right-click a running VM and choose **Publish to GitHub…** to turn a
project folder inside it into a GitHub repository. One-time setup: create
an OAuth app under github.com → Settings → Developer settings → OAuth Apps
(enable **Device Flow**; no callback URL or client secret needed), put its
client ID in **Configuration → GitHub**, and sign in — a code appears here,
you enter it at github.com/login/device, done.

The dialog lists the folders in the VM's home; pick one, name the
repository (private by default), and Publish. exe installs git in the VM if
needed, commits any uncommitted work as your GitHub account's noreply
identity, creates the repository, and pushes. Publishing again later pushes
the new commits to the same repository.

The Chat agent can do the same: tell it to "push to github" and it uses the
daemon's github_push tool — a plain `git push` inside a VM always fails,
because that is the point.

The point of the design: **no GitHub credentials ever enter the VM.** The
sign-in token lives only on this machine (`~/.exe/github.json`), and the
push travels through a proxy that exists just for that one operation and
answers only for that one repository — the VM's git talks to it without
ever holding a token, on disk or in memory.

## Workspace and files

The **Workspace** is `~/.exe/workspace` on this machine: a shared folder
where you, agents and apps exchange files. The desktop icon opens a Finder
view — double-click text files to edit them in place, images to view them;
right-click for Get Info and Download; right-click a window's empty space
for New Folder, New Text File and Upload; **File → Upload to Workspace…**
brings files in from this browser. Files can also be dragged from your
computer onto the desktop (lands in the Workspace root), onto a Finder
window (lands in its folder), or onto a folder icon (lands in that folder).
New files brought in this way are announced on the Newsfeed, so every desk
in the mesh sees them arrive; overwriting an existing file stays quiet.

## Apps

Icons beyond the built-ins are desktop apps: folders in `~/.exe/apps`, each
just an `app.json` plus an `index.html`, served straight from disk — edit
one and reopen its window, no rebuild. Each app gets private storage under
`~/.exe/appdata` plus the shared Workspace. Apps are a good thing to ask a
coding agent to build for you.

## Joining desks together

**Special → Join…** pairs this exe with another one (say, your laptop's)
using a short one-time code. Joined desks sync continuously: app data,
Workspace files and the Newsfeed flow both ways, with conflicting edits
resolved automatically and the losing copy preserved next to the winner.

The **Newsfeed** is the shared timeline of the mesh: VMs created and
deleted, nodes joining, sync conflicts — and agents can post to it, so
finished work or problems show up on every desk.

## Configuration

**Windows → Configuration** edits `~/.exe/config.json` in place; most fields
hot-reload on Save, and fields marked `*` take effect after a daemon
restart. Highlights:

- `listen` — the address of this UI and API. Bind it to your Tailscale IP
  to use exe from your phone.
- `api_token` — set it before listening beyond localhost; every API call
  then needs it. Paste it into **Special → Set API Token…** in each browser
  (it is kept in localStorage).
- `ssh_user` — the user created in every VM (default `dev`).
- `ollama.*`, `chat_provider`, `openai.model`, `cloudflare.*` — the agent
  and publishing sections above.

**Windows → Daemon Log** streams the daemon's own log when something needs
a closer look. This page lives at `/docs.md`, and the machine-readable
counterpart for agents at `/skill.md`.
