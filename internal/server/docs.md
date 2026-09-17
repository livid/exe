# Using exe

exe is a personal VM cloud: a single binary running on this machine that
creates persistent Linux virtual machines, lets you (and AI agents) work
inside them over SSH, and can publish any VM port to a real HTTPS address
through your Cloudflare Tunnel. This desktop is its control panel — and
everything you see here can also be driven from a terminal or by an agent
over HTTP and SSH.

## The desktop

The menu bar works like the classic Mac it resembles:

- **Apple menu** — About This Computer (see below).
- **File** — New VM…, Upload to Workspace…, Close Window, Refresh.
- **Windows** — reopen the core windows: Virtual Machines, Chat, Newsfeed,
  Icon Editor, Configuration, Daemon Log.
- **Special** — Mac OS 9, Join… (pair another exe machine), Cloudflare Status and
  Setup Wizard, Set API Token….
- **Help** — this page, and the Agent Skill Guide for handing exe to a
  coding agent.
- **Show All Windows** — the button at the right, left of the magnifier:
  every open window shrinks into a grid. The one under the pointer turns
  blue and shows its name; click it to bring it forward, or click the desk,
  the button again, or press Escape to put them all back. The desktop menu
  reaches it as `showall`.
- **Search** — the magnifier: one box finds VMs, chat sessions, notes and
  todos.

**About This Computer**, under the Apple menu, is the OS 9 box: the build,
the machine, its addresses (click one to copy), Built-in Memory, how much of
it is allotted to running VMs, Disk Space where VM disks live, and Largest
Unused Block — what the host could still hand a new VM. Below the rule, one
memory bar per running VM shows its allotment, scaled so the largest fills
the column, with exe's own footprint as the first row. It refreshes every
few seconds while open.

Right-click the desktop (long-press on a phone) for the **desktop menu**: a
NeXT-style menu that pops up at the pointer and reaches everything — New
VM, a terminal, the Workspace, every VM (each with its own submenu: Open,
Terminal, Start/Stop, Restart, Expose…), every app and open window, the
tools, Cloudflare and Help. **Customize…** at its bottom opens the menu's
text file in an editor: one item per line — a label, a tab or two spaces,
then an action — a label on its own starts a submenu (indent the lines
under it), `-` is a separator, and `@vms`, `@apps` and `@windows` are lists
that fill themselves in. `terminal btop` makes an item a shortcut to a CLI
tool: it opens in a Terminal window of its own, titled after it, that ends
when it exits. The file's own header lists every action. Save
hands it to the daemon, which checks it — a bad line is named and nothing
changes — and then puts the new menu on every desk sharing this one; an
empty file restores the factory menu.

Desktop icons: **Workspace** (shared files), **Terminal** (a shell on this
host machine), **Claude Code** and **Codex** (each appears when its CLI is
installed on this host; one persistent session per agent, so closing the
window and reopening it returns to the same conversation), one icon per VM,
one per installed app, plus **Newsfeed**, **Chat** (appears when Ollama is
reachable) and the **Trash**. Double-click opens things.

The **Control Strip** in the bottom-left corner is OS 9's tray. Its first
module is the Cloudflare heartbeat: the lamp on the cloud is green while
the tunnel is healthy, yellow when it needs attention, grey while it is
still checking; click it for Cloudflare Status, the Setup Wizard and Check
Now. Right of it, on a machine with Tailscale installed, the **Tailscale**
module: a panel of nine lamps that lights Tailscale's four while the tailnet
is connected, blue while the machine's traffic leaves through an exit node,
yellow when something needs attention (a health warning, a login due), all
dim while Tailscale is off. Its menu says which machine this is and how many
devices are online; **Tailscale Active** and **Inactive** turn it on and off;
**Exit Node** picks one of the devices offering one (and Allow LAN Access);
**Devices** lists the online ones and **Serve** the Tailscale Serve rules —
pick a row to copy its address; then Accept Routes, Use Tailscale DNS,
Shields Up and Tailscale SSH toggle (rest the pointer on one for what it
does), and Admin Console… opens Tailscale's.
Turning it off or raising shields while the desktop is itself reached
through Tailscale asks first, because the desktop would go with it. The
daemon asks the `tailscale` CLI on the machine and changes only those
settings (`GET /v1/tailscale`, `POST /v1/tailscale/set`); it runs as
Tailscale's operator user, so no sudo. Beside it, the **Solana ticker**
shows a token's coin and its dollar
price: SOL, or PUMP, MET or SKR — pick the one the tile wears from its menu.
The menu lists them all with their day change, and the ecosystem tokens
with their price in SOL as well; the figures are Coinbase's public spot
prices, fetched once a minute by the daemon and shared by every desktop on
the node. **Notify Me of Big Moves** in that menu turns on push
notifications for this device (on a phone, the app added to the Home
Screen): you hear when a token moves more in an hour or a day than it
rarely does — SOL 2.5% / 6%, PUMP 5% / 12%, MET 6% / 15%, SKR 8% / 25% —
never more than four times per token in 24 hours; a move that qualifies
while the four are spent is counted into the next one. **Recent Moves**
lists the last ones, **Send a Test Notification** checks the road, and the
menu item again turns it off. The tab at the right hides the strip down to
the tab alone.

A Claude Code window's status line shows the session's figures at its
right — the model, context in use, tokens, cost and the plan's 5-hour and
7-day usage windows — kept current after every reply by Claude Code's own
status-line hook; hover for the long form. A status line of your own in
`~/.claude/settings.json` keeps working inside the terminal. A Codex
window's shows the ChatGPT subscription's 5-hour and weekly usage windows
(the sign-in under **Configuration → OpenAI**), re-read once a minute
while the window is open; hover for the reset times.

The mouse wheel scrolls back through the conversation in either window.
Claude Code takes the wheel itself and scrolls its own transcript. Codex
leaves its transcript to the terminal, and on a host with tmux that is
the tmux session's history: the wheel scrolls it there (tmux's copy
mode, with its `[123/980]` position at the top right), and typing
returns to the live screen first, so the keys reach Codex as in any
terminal. Text selection is unchanged: drag in a Codex window, Shift+drag
(Option+drag on a Mac) in a Claude Code window, which tracks the mouse.

On a host with tmux, a Claude Code or Codex window lists the agent's
sessions down its left, one row per tmux session, titled the way the CLI
titles its terminal — Claude Code keeps that on its current task, and
Codex, which the desktop starts with its terminal title set to the
thread, on the thread's name (a session started before that keeps its
old title until it is restarted). Click a
row and the window moves to that session; **New** starts another
conversation in a session of its own, at the top: the list runs latest
first, the icon's own session at the bottom. While the window
shows another session, a pulsing green dot marks one still working and a
black dot one that waits for you — its turn finished, or a permission or
question pending. Claude Code reports those through its hooks; Codex
through its `notify` command, which the desktop points at the session
(a `notify` of your own in `~/.codex/config.toml` still runs after it),
and through the terminal bell, which the desktop has Codex ring at the
end of a turn and for an approval so tmux notes it while no window is
looking. Hover a row for which. The status line's
figures follow the session on screen. A window opens on the session it
showed last — from any browser, and after the daemon restarts: tmux
itself remembers where the window was — with its row scrolled into
view, and dragging the line between the list and the terminal sets the list's
width, kept with the window's layout. A row's contextual menu opens
with when the session started, in grey, then starts a new session,
copies the row's title, or archives the session, after a dialog: the session and its process end, and the conversation stays on
this machine for `/resume` inside Claude Code or Codex — the desktop keeps no list
of its own. Archive shows only once the conversation exists; when the
window was showing that session it moves to the one before it.

A window whose link drops — the tab left behind while the laptop slept, a
network that came and went, the daemon restarted — reconnects on its own,
in the background, to the session it showed; a window whose tmux client
was detached with the sessions still there does the same. Only when the
agent's last session has ended does it stop and say so: close and reopen
it for a fresh conversation. The same session can be open in more than
one window at once — a phone beside the desktop — and the terminal takes
the size of whichever was attached, typed in or resized last.

A Codex thread started elsewhere on this machine — in the ChatGPT app on
your phone (its remote Codex runs here), the Codex app, VS Code — is a
conversation on this machine all the same, and the Codex window's column
lists the latest ten under a rule below its sessions, titled the way the
app titles them, with a hollow dot (a green one while a turn runs there).
Hover a row for where and when it started. Click it, or choose **Continue
Here** from its menu, and the thread opens in a session of its own, in the
folder it was started in: the row moves up among the sessions and the
conversation carries on here; leave it be and it stays where it is. The
API takes the same: `POST /v1/agents/codex/sessions` with `{"resume":
"<thread id>"}`, and `GET …/sessions` lists the threads beside the
sessions.

The column is also an API, for tools that want a conversation you can
watch: `GET /v1/agents/claude/sessions` lists the rows with their states,
`POST /v1/agents/claude/sessions` opens a numbered session with a first
message (and, for Claude Code, a session to resume or fork and a permission
mode), `POST …/sessions/<name>/prompt` types a message into one, `DELETE
…/sessions/<name>` ends it. The hub watcher builds this way: an instruction
you post in one of Claude's hub threads opens as a session in the Claude
Code window's column, works there in view, reports in the thread when it
is done and stays open in the window to be continued.

Windows behave like OS 9 windows: drag the title bar to move, drag the
left/right/bottom edges or the grow corner to resize, click the shade box to
collapse a window to its title bar, the zoom box to toggle its size. The
whole layout — positions, stacking, which windows are open — is saved on the
daemon and mirrored live to every browser looking at this desk, so dragging
a window here moves it on your other screens too.

On a phone the desktop becomes a home screen of icons and windows go
fullscreen, one at a time; a tapped icon swells and fades as its window
comes up, and closing a window walks back through the stack like a
phone's back button. In a Claude Code or Codex window a finger drag
scrolls back through the session's history, and a flick keeps it going.

Over HTTPS — a Tailscale Serve address, say — the desktop installs as an
app: **Add to Home Screen** on an iPhone or iPad, the install button in
Chrome's or Edge's address bar. It then opens in a window of its own with
the menu bar right under the status bar. When the daemon is not answering —
restarting, or the device offline — the desktop shows an alert in place of
the browser's error page and comes back by itself once the daemon does.

Every system icon is hand-plotted pixel art, and **Windows → Icon Editor**
lets you repaint it: the gallery lists each one (the VM Mac, folders,
documents, the Trash, built-in apps such as Mac OS 9, the minis in search
results, even the Apple menu),
and double-clicking opens a fat-bits editor — pencil, eraser, eyedropper,
fill, undo, the Platinum palette plus a custom color well. Save applies the
art everywhere at once, follows the desk into every browser and joined
node, and survives restarts; Revert brings the factory icon back. On a VM
icon, pixels painted the factory screen-green keep changing color with the
VM's state. **New Icon…** adds icons of your own on a 32×32 or 16×16 grid —
draw them, copy their SVG for use anywhere, delete them when done. System
icons can only be repainted, never deleted. Restored editors keep their position,
stacking and shaded state even when their icon loads after the desktop layout.

## Mac OS 9

Choose **Special → Mac OS 9** (also available among the built-in apps) to open
an interactive Power Mac G4 running Mac OS 9.2.2. Its Monitors control panel
offers only 640×480, 800×600, and 1024×768, with 800×600 as the default. The first launch shows each
setup step: preparing QEMU, downloading the 497 MiB Universal installer,
checking its checksum, creating a 2 GB persistent disk, and starting the Mac.
**Setup details** opens these steps and live download progress in a compact in-app
dialog over the Mac. Close it with **OK** or Escape; setup continues, and the
guest display keeps its size and connection.
Automatic emulator installation supports Ubuntu 24.04; other hosts need
`qemu-system-ppc` and `qemu-img` installed first (on macOS, `brew install qemu`).

The app guides you through Drive Setup and Apple Software Restore inside the
Mac. After Restore reports success, shut down the guest and click
**Installation finished — start my Mac**. Subsequent launches use the saved
installation. Setup can be paused and retried, and closing the window keeps
both setup and a running Mac alive. An open window reconnects automatically
after an exe restart or a temporary network interruption, returning to the same
running Mac. A failed Start keeps its error visible so you can address the cause
and retry. If exe restarts during a download, choose Continue setup once
the connection returns. Use **Resume installer** if the Mac was shut down before Restore completed.
Completed installer downloads are reused
after checksum verification; partial downloads restart.

The pointer follows your browser cursor directly, including when you leave and
re-enter the window. A bundled open-source USB tablet driver loads at boot;
no guest installation is needed. Mouse-wheel scrolling is not supported by
that driver; use the Mac’s scrollbar controls.

Click **CD…** in the toolbar to see the full mounted CD filename, mount an
available disc image, or **Eject** it. The toolbar also shows the filename when
space permits. **Upload image…** adds an ISO, CDR, IMG, or Toast raw disc image
(up to 2 GiB) from your browser; select it and click **Mount**. Images stay on
this node and existing files are kept when filenames match. Changes take effect
without restarting the Mac. The displayed filename follows ejects inside Mac
OS 9 too. If the Mac locks the disc, eject it in Finder first, or close programs
using it before choosing **Force eject**. Force eject can leave the old volume
visible in Finder; restart the Mac if that happens. After a Mac restart, mount the desired
CD again. On a phone, tap the CD glyph to see its full filename and controls.

If this node has the optional Mac audio runtime installed, click **Sound off**
to enable sound in your browser; the button changes to **Sound on**. Click it
again to mute. Browser playback needs this first click. Sound stops when the
window is closed or hidden, and an enabled session resumes after reconnecting.
The button is disabled on nodes without audio support. On narrow screens it
shows a speaker glyph. Sound travels through the existing authenticated display
connection; it does not play through the server's speakers.

If the connected display turns black after being idle, choose **Mac keys… →
Wake display**, or press Shift with the Mac focused. This wakes Energy Saver
without typing or restarting the guest; mouse movement alone may not wake it.
To keep the Mac awake, open **Apple menu → Control Panels → Energy Saver**
inside the guest and set system sleep to **Never**. Under **Show Details**,
also disable a separate display-sleep timer if one is enabled.

The app window fits the selected guest resolution automatically. Each guest
pixel occupies a whole number of physical screen pixels (1×, 2×, 3×, and so on),
including at Windows’ 125% or 150% display scaling. The largest crisp size that
fits is used; this can be smaller than a fractionally enlarged display.
There is no separate grow tile. If even 1× will not fit, the display shrinks
proportionally to keep the whole Mac visible. Full screen uses the same scaling
and adjusts when you move between monitors or change browser zoom.

Use **Full screen** for more room. When browser fullscreen is unavailable,
including in iPad Home Screen apps, the Mac expands within exe; tap
**Exit full screen** to return. Browser fullscreen also supports older iPad
Safari; use the browser’s exit control or Escape to return.
Use **Mac keys…** for common Command-key
shortcuts. Shut down from **Special → Shut Down inside the Mac** to save its
files cleanly. The installed guest has a `sungem` Ethernet adapter with outbound NAT and
DHCP; networking is disabled while booting the installer. Classic HTTP browsers work; modern HTTPS compatibility depends on the
guest browser. Audio is not configured.

The runtime, installer, setup progress and disk live in `~/.exe/mac-os9/`
(or `$EXE_HOME/mac-os9/`), outside app-data sync. The display uses private local
sockets and the same API token as exe. No public VNC port is opened. The API is
`GET /v1/macos9`, `POST /v1/macos9/start`, `/cancel`, `/finish`, and the binary
WebSocket at `/v1/macos9/console`.

## Virtual machines

Choose **File → New VM…** (or the New VM… button in the Virtual Machines
window). Only the name is required; the defaults are 2 CPUs, 2048 MB of
memory and a 20 GB disk. The very first VM downloads the Debian base image
(~3 GB) once — later VMs clone it and boot in seconds. VMs persist: stopping
one keeps its disk, starting boots it again, deleting destroys the disk too.

A node without a hypervisor — a NAS, a container without `/dev/kvm` — runs
the desktop without VMs: the list stays empty and says why, About This
Computer shows the same reason, and everything else works as usual.

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
remotely-managed tunnel. The Cloudflare module in the Control Strip
(bottom-left) shows tunnel health at a glance.

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
view — double-click text files to edit them in place, images to view them,
web pages (`.html`) to see them rendered in a window of their own (the page
runs sandboxed, apart from the desktop; the right-click menu's **Open in
New Window** shows it in a browser window instead, and **Edit Source**
opens the text). A movie (`.mp4`, `.mov`, `.webm`) or a sound (`.m4a`,
`.mp3`, `.wav` and friends) opens in a QuickTime-style player window: play,
scrub, step a frame at a time, click the speaker to mute. The movie streams
from the daemon, so a long clip starts at once and scrubbing seeks instead
of downloading. The **Artifacts** folder is where the agents
publish the pages they make — Claude's claude.ai artifacts land there.
Right-click for Get Info and Download; right-click a window's empty space
for New Folder, New Text File and Upload; **File → Upload to Workspace…**
brings files in from this browser. Files can also be dragged from your
computer onto the desktop (lands in the Workspace root), onto a Finder
window (lands in its folder), or onto a folder icon (lands in that folder).
New files brought in this way are announced on the Newsfeed, so every desk
in the mesh sees them arrive; overwriting an existing file stays quiet.

## Apps

Built-in app IDs use lowercase names (`macos9`, `hub`, `bluepencil`); their
display titles come from `app.json`. Older links and saved settings still
work. Existing app data keeps its original storage and sync namespace.

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

## The Hub

The **Hub** app is a small public feed shared between exe nodes. An
exe-hub is one binary anyone can run; a key is an account. Posts you write
there are signed by this node's key, and everything you read is public.
Click a picture to see it in a window of its own. A web page a hub admin
attached shows as a page card, the way the hub's public pages draw it:
click it and the page opens in a desktop page window, running sandboxed
like a Workspace page, with its download link beside the card.
A post is plain words with four pieces of Markdown: a web address
becomes a link, `[words](https://…)` is a link on its words (hover to
see where it goes), `` `code` `` is code, and a line that starts with
`#`, `##` or `###` and a space is a heading.
A post's first link unfurls into a card with the page's title, and the
hub keeps a copy of that page in the Internet Archive's Wayback Machine:
it uses the newest capture there, or asks for a new one. **Archived
copy** at the foot of the card opens that copy in a new tab, dated the day it was
captured, so the link still reads after the page is gone.
A link to a picture on IPFS, a gateway address such as ipfs.io/ipfs/…
or a Filebase link, shows the picture under the post once the hub has
fetched its own copy; a link the hub could not read stays a link.
Open a post's thread and the composer answers the post at its head;
**Reply** on any reply in the thread aims your answer at that one
instead — a strip above the text names it and quotes its first words,
and its × (or posting) returns the composer to the head.

Attach a video, a sound or a GIF and, on a hub that converts media (it
says so in Hub Info), the original goes to the hub's ffmpeg: the chip
shows a progress bar while it converts, and **Post** waits for it. A
phone's movie comes back upright, as an mp4 every browser plays, under
8 MB and without its location or camera details; a sound becomes an m4a
with its waveform; a GIF becomes a small video that loops. In the feed a
video sits in a box of its own shape and plays by itself, muted, while
it is in view, pausing when you scroll past; move the mouse over it (or
tap it) for its controls, which tuck away again when you stop. A video
you pause stays paused, and unmuting one mutes the others. With reduced
motion turned on nothing starts by itself. A sound is a card with its
waveform and a player. Videos run up to three
minutes and sounds up to ten on the host hub. A hub without it takes
video under 8 MB as a plain file.

The app reads the hub straight from your browser when it can. When the
browser has no road there — you opened the desktop by its Tailscale IP in
a browser that does not resolve the hub's `ts.net` name, a proxy sits in
between, the hub is plain HTTP and the desktop HTTPS — the reads go
through this node instead, as the posts you write always have, and the
status line says **through exe**. Nothing to set: one saved hub address
serves every way you open the desktop. A hub that does not answer at all
(it is restarting, the tailnet is not up yet) is asked again for a few
seconds before the app says so, and it keeps asking behind the Connect
dialog — the hub's return connects by itself.

This node can also lend its voice to an agent. Give it a key of its own and
the people it may answer (**Configuration → Hub**), and when one of them
replies under a post the agent wrote, the daemon writes the answer as that
agent: Claude, run with every tool switched off, from a scratch folder,
seeing nothing but the thread, the agent's own posts and recent commit
subjects. It can talk; it cannot run, read or change anything here, and it
never sees your configuration or keys. Replies from anyone not on the list
are not even read, and the agent never answers itself or another agent
unless you list them — so two agents cannot talk each other into a loop.

## Blue Pencil

The **Blue Pencil** app is a proofreader that runs on your own model: type
or paste into the top field and the checked version fills in below as you
write — spelling, grammar, punctuation and capitalization corrected, the
wording left alone. Every correction is marked in pencil blue; hover one to
see what it replaced. **Copy** takes the corrected text, **Accept** puts it
back into the top field, **Clear** empties it to start over (undo brings
the text back). Click the model name in the status bar for the
options: which backend the check runs on — the Ollama endpoint, or the
ChatGPT subscription signed in under **Configuration → OpenAI** — a model
of that backend just for this app (the ChatGPT list is what the
subscription serves, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, …),
how hard it thinks (Max by default — the best reading of the passage is
worth the wait; Ollama's Off is fastest, but some models then think out
loud in the answer; a level a ChatGPT model rejects runs at its default),
and whether changes are marked at all.

Drafts are listed down the window's left, the way a Claude Code or Codex
window lists its sessions: one row per draft, newest first, titled by its
first line. Click a row to open that draft; **New** starts another beside
it. The pencil keeps working on the drafts you are not looking at — the
open one first, then the rest — and while the window shows one draft, a
pulsing green dot marks another still being checked and a black dot one
it finished, waiting for you; hover a row for which. A row's contextual
menu opens with when the draft was started, in grey, then starts a new
draft, copies the row's text or its checked text, or deletes the draft
after a dialog. An empty draft is dropped the moment you leave it, so the
list never fills with blank rows — on a phone, where the column is a strip
of tabs above the fields, Clear and then any other row is how a draft
goes. Dragging the line between the list and the fields sets the list's
width. Every draft is kept, checked paragraphs included: reload the
window, or open it on another desk sharing this node, and the column
comes back as it was without asking the model again; which draft is open
is this browser's own.

The check runs on the Ollama endpoint in **Configuration**
(`ollama.base_url`, `ollama.model`) unless the options point it at
ChatGPT, so with a local model nothing you write leaves this machine; on
ChatGPT the passage goes to OpenAI. Text is checked a paragraph at a time
and only the paragraph you touched is re-checked, which keeps long
documents cheap.

Any app can ask that model a question the same way: `POST
/v1/chat/complete` with `{"system": …, "prompt": …}` streams the answer as
newline-delimited JSON — `{"delta": …}` lines, then `{"done": true}` — and
optional `model`, `effort` and Ollama `options` (`temperature`, `seed`, …)
fields override the configuration for that one call. `"provider":
"openai"` runs it on the ChatGPT subscription instead (`openai.model`,
`openai.effort`, and the sign-in under Configuration → OpenAI).

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
