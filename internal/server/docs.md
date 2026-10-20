# Using exe

exe is a personal VM cloud: a single binary running on this machine that
creates persistent Linux virtual machines, lets you (and AI agents) work
inside them over SSH, and can publish any VM port to a real HTTPS address
through your Cloudflare Tunnel. This desktop is its control panel — and
everything you see here can also be driven from a terminal or by an agent
over HTTP and SSH.

On the [documentation website](https://exe.v2core.com/docs/), the toolbar's
joined sun and moon buttons choose light or dark paper. The pressed segment
shows the current choice, which this browser remembers between visits.

## The desktop

The desktop is exe's control panel. It is a page in your browser, drawn as a
Mac OS 9 desk: this machine's virtual machines (VMs), agent sessions, apps
and published sites are its icons and windows. It opens on a computer and on
a phone.

### What the desktop is for

For you:

- **Run your VMs.** Every VM has an icon whose screen shows its state.
  Double-click it for the VM's window, or choose **File → New VM…** to make
  another.
- **Work beside your agents.** **Claude Code** and **Codex** open in windows
  of their own, and on a machine with tmux their sessions keep running after
  you close the browser. The desktop counts the tokens they use, and a
  notification can tell you when a turn ends.
- **Open apps and files.** Every installed app has an icon, and
  **Workspace** is the folder of files you, your agents and your apps share.
- **Watch what you have published.** A lamp says whether your Cloudflare
  tunnel is healthy, and **Windows → My Apps** shows every address you have
  published.
- **See how the machine is doing.** **About This Computer** shows its memory
  and disk, and a small chart follows its CPU, memory, GPU, network or disk.
- **Find the desk as you left it.** The layout of the windows is saved on
  the machine and mirrored to every browser that has the desk open, so a
  window you move here moves on your other screens too.

A coding agent, such as Claude Code or Codex, does not need the desktop. It
drives the same exe over HTTP, the command line and SSH, and **Help → Agent
Skill Guide…** gives you a prompt that teaches it how. Three things an agent
or a script does that way show on the desk:

- **A note in the Newsfeed.** `POST /v1/newsfeed` with a title and a body
  puts a note in the Newsfeed of this machine and of every machine joined to
  it: finished work, or a problem you should see.
- **A notification.** A script on the exe machine sends its own words to
  every device that has notifications on (`POST /v1/push`).
- **The desktop menu.** `GET /v1/ui/menu` and `PUT /v1/ui/menu` read and
  replace the right-click menu. An agent changes it only when you ask.

### The menu bar

The menu bar runs along the top and works like the classic Mac it resembles.
The small Mac at its left end is the **Apple menu**.

| Menu | Item | What it does |
| --- | --- | --- |
| Apple | **About This Computer** | Shows the machine, its memory and disk, and each running VM. |
| Apple | **Software Update…** | Moves an installed exe to the newest release. See [Updating](https://exe.v2core.com/docs/using/updating). |
| Apple | **Reload for Update** | Reloads the desktop into a new build. It is there only while unsaved work holds that reload back. |
| Apple | The name of an open window | Brings that window to the front. |
| File | **New VM…** | Makes a VM. See [Virtual machines](https://exe.v2core.com/docs/using/virtual-machines). |
| File | **Upload to Workspace…** | Puts a file from this device, 10 MB at most, in the Workspace. |
| File | **Close Window** | Closes the window in front. |
| File | **Refresh** | Loads the lists of VMs and apps again. |
| Windows | **Virtual Machines** | The list of VMs. |
| Windows | **My Apps** | Every address you have published, an icon each. |
| Windows | **Chat** | The Chat window. It is in the menu once Chat has a model to work with. |
| Windows | **Newsfeed** | Events from this machine and the machines joined to it. |
| Windows | **Icon Editor** | Repaints the desktop's icons. |
| Windows | **Configuration** | Edits exe's settings. See [Configuration](https://exe.v2core.com/docs/using/configuration). |
| Windows | **Log Viewer** | The daemon's log and the log of requests to the API. |
| Special | **Mac OS 9** | Opens a Mac running Mac OS 9. See [Mac OS 9](https://exe.v2core.com/docs/using/mac-os-9). |
| Special | **Mac OS X** | Opens a Mac running Mac OS X 10.4 Tiger. See [Mac OS X](https://exe.v2core.com/docs/using/mac-os-x). |
| Special | **Node Sync…** | Pairs this exe with another one. See [Joining desks together](https://exe.v2core.com/docs/using/joining-desks-together). |
| Special | **Join a Hub…** | Joins a hub, a step at a time. See [The Hub](https://exe.v2core.com/docs/using/the-hub). |
| Special | **Cloudflare Status…** | The tunnel's state and its live counters. |
| Special | **Cloudflare Setup Wizard…** | Sets up the tunnel and the domain. See [Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web). |
| Special | **Set API Token…** | Asks for the API token this browser sends to exe. |
| Help | **Welcome…** | exe in six cards. |
| Help | **exe Documentation…** | This manual. |
| Help | **Agent Skill Guide…** | A prompt that hands exe to a coding agent. |

Under a rule, the Apple menu lists every open window by name, with a check
mark beside the one in front. On a phone, where one window fills the screen,
that list is how you go from one open window to another.

A diamond beside the small Mac, and beside **Software Update…** in its menu,
says a newer release is waiting.

At the right end of the bar:

- **Show Desktop** — the button with a bare desk on it.
- **Show All Windows** — the button with four small windows on it.
- **Search** — the magnifier.
- The clock. Rest the pointer on it for the date. A click opens the World
  Clock app, when that app is installed.

### Icons on the desktop

Double-click an icon to open it. A single click selects it.

- **Workspace** — the folder of files you, your agents and your apps share.
  See
  [Workspace and files](https://exe.v2core.com/docs/using/workspace-and-files).
- **Terminal** — a shell on the machine exe runs on.
- **Claude Code** and **Codex** — each appears when its CLI is installed on
  this machine. The three have a chapter of their own,
  [Claude Code, Codex and Terminal](https://exe.v2core.com/docs/using/claude-code-codex-and-terminal).
- One icon for each VM. Its screen is green while the VM runs, amber while
  it starts or stops, dark when it is stopped and red when it is in error.
- One icon for each installed app. See
  [Apps](https://exe.v2core.com/docs/using/apps).
- **Newsfeed**, **Chat** and the **Trash**. **Chat** appears once Chat has a
  model to work with: Ollama answers, or, when Chat runs on a ChatGPT
  subscription, you are signed in.

### Move, resize and close windows

Windows behave like OS 9 windows. The close box is at the left end of the
title bar; the zoom box and the shade box, where a window has them, are at
the right end.

| To | Do this |
| --- | --- |
| Move a window | Drag its title bar. |
| Resize it | Drag its left, right or bottom edge, or the grow box in its bottom-right corner. Not every window can be resized. |
| Collapse it to its title bar | Click the shade box, or double-click the title bar. The same again brings it back. |
| Switch between two sizes | Click the zoom box. A window of one size, such as Welcome, goes back to that size. |
| Close it | Click the close box. **File → Close Window** closes the window in front. |
| Bring it to the front | Click it, or choose its name in the Apple menu. |

The whole layout is saved on the daemon: positions, stacking, which windows
are open. It is mirrored live to every browser looking at this desk, so
dragging a window here moves it on your other screens too. A phone shows one
window at a time and keeps its own.

Terminal, Claude Code and Codex windows are mirrored the same way. One you open
here opens on your other screens, and its close box closes it on all of them.
A terminal window keeps the same columns and rows on every screen, so a program
in it looks the same on each, even where the font is wider.

### Save a screenshot of a window

Right-click a window's title bar (on a touch screen, press and hold it) and
choose **Save a Screenshot**. A PNG of that window alone lands in the
Workspace's **Screenshots** folder, named for the window and the time: ready
for a post or a bug report, and where an agent can read it.

- The picture is the window with its frame and shadow, on a clear ground.
  Nothing of the desktop or of a window lying over it is in it.
- The window is saved as it stands: scrolled where you left it, the field
  you were typing in still ringed. Dialogs have the menu too.
- On a sharp screen the picture is sharp too: two pixels to the point on
  most, at Windows' 150 percent as well, and three on a phone. The file
  says so the way a Mac's own screenshots do (144 to the inch at two), so
  Preview and Quick Look open it at the window's size, not twice it.
- Three things come out empty or plain: a web page from the Workspace and a
  player from another site (the desktop cannot see into them), a 3D view
  that is not redrawn every frame, and selected text, which shows unselected.

### The desktop menu

Right-click an empty part of the desktop (on a touch screen, press and hold
it) for the **desktop menu**: a NeXT-style menu at the pointer, with
shortcuts to VMs, apps, windows and tools. Choose **Customize…** to edit it.

The format, every supported action and examples are in the chapter
[Desktop context menu](https://exe.v2core.com/docs/using/desktop-context-menu).

### Show Desktop

The button with the bare desk on it slides every open window off to the
nearest edge of the screen, a sliver of each left showing, so the desktop
and its icons are clear to use.

- Click a window's sliver to bring them all back, that one in front.
- The button again, or Escape, brings them back too; so does opening a
  window from the desk.

The desktop menu reaches it as `showdesk`. A phone has no such button.

### Show All Windows

The button left of the magnifier shrinks every open window into a grid, so
you see them all at once.

- The window under the pointer turns blue and shows its name. Click it to
  bring it forward.
- Click the desk or the button again, or press Escape, to put them all back.

The desktop menu reaches it as `showall`. A phone has no such button.

### Search for a VM, a chat, a note or a todo

The magnifier opens one box that finds VMs, chat sessions, notes and todos.

- A VM is found by its name or its address, a chat by its title or summary,
  a note or a todo by its text.
- Click a result, or move through them with the arrow keys and press Return.
  The box closes and the thing itself opens: the VM's window, the chat, or
  the note or todo in its app.
- Escape closes the box.

### Welcome

**Help → Welcome…** is exe in six cards: **Virtual Machines**, **Chat**,
**Claude Code and Codex**, **The Hub**, **Cloudflare** and **Apps**.

<img src="https://hub.v2core.com/v1/embed/bafkreifwrbkkq74w5knqzrsbhxcbri7ehkq2n6patknhucbb2tck5tvzmu" alt="The Welcome window: a night sky over a row of Macs, and under it the six features, two to a row" width="600" height="497">

- Click a card for a short account of that feature. **Back** returns to the
  six.
- Each card says how the feature stands on this machine: ready, or what it
  still needs. A ready feature has a green check beside its name on the
  first page.
- The card's button goes there: **New VM…**, the pane of Configuration that
  sets Chat up, **Set Up…** on the Claude Code and Codex card, the
  Cloudflare Setup Wizard, **Join a Hub…** until a hub is joined and the
  Hub after, and **Extras…** on the Apps card.
- **Read More…** opens this manual on that feature's chapter.

On a new install the window opens with the desktop each time, until you
clear **Show this window when the desktop opens**. That box belongs to this
machine, not to the browser. An exe that was installed before the window
existed opens it from the menu only.

### Read and search the manual

**Help → exe Documentation…** opens this manual in a window of the desktop.
It opens on its contents and shows a chapter at a time.

- Click a chapter's name to read it, or turn the pages with **Contents**,
  **Previous** and **Next**.
- A chapter's page shows the icon of what it is about beside its name: the
  VM's Mac, the Workspace's folder, an app's own icon.
- A reference from one chapter to another turns to it in the same window.
- Close the window and open it again, and it comes back where you left it.
- It opens just tall enough to show the whole contents page, and keeps a
  height you drag it to.

To find something, type in the field over the page, **Search the manual**:

- The contents narrow to the chapters that hold every word you typed, in
  capitals or not and inside longer words.
- Each found chapter stands under its own number, with how many places hold
  the words and up to three lines of its text. The found words are marked in
  yellow, in the list and on the pages.
- A chapter's name opens it at the top, and a line opens it at that word.
  While the field holds words, **Contents** (or Return in the field) brings
  the list back.
- Emptying the field ends the search: press Escape, or click the small
  circled × that stands at the field's right end while it holds anything.
  The yellow goes, and if you opened none of the found chapters you are back
  on the page you were reading.

### Hand exe to a coding agent

**Help → Agent Skill Guide…** is for handing exe to a coding agent. Its
window gives the address of the skill file, which teaches an agent to create
VMs, run commands inside them over SSH and publish their ports, and a prompt
to paste into the agent. **Copy Prompt** copies it. When this browser holds
an API token, the prompt includes it and the window says so.

### About This Computer

**Apple menu → About This Computer** is the OS 9 box, about the machine exe
runs on.

<img src="https://hub.v2core.com/v1/embed/bafkreightb5vb7wkbiirymaoj7ewmkedjeea2mcpl3x66gsscdylpjs434" alt="About This Computer: the build, the machine, its addresses, memory and disk, and a memory bar for exe and each running VM" width="482" height="378">

| Line | What it says |
| --- | --- |
| **Version** | The build of exe. |
| **System**, **Machine** | The operating system, the processor architecture and the number of CPUs; the machine's model and its name. |
| **Addresses** | Its addresses on the local network and on Tailscale. Click one to copy it. |
| **Built-in Memory** | The machine's memory. |
| **VM Memory** | How much of it is allotted to running VMs. |
| **Disk Space** | The space free on the disk where VM disks live. |
| **Largest Unused Block** | The memory the host could still hand a new VM. |

Below the rule, one memory bar per running VM shows its allotment, scaled so
the largest fills the column. exe's own footprint is the first row. The
window refreshes every few seconds while it is open.

### The Control Strip

The **Control Strip** in the bottom-left corner is OS 9's tray: a row of
small modules, each with a live figure about this machine. Click a module
for its menu.

<img src="https://hub.v2core.com/v1/embed/bafkreiadh2tua4bcsuus4cm7hrzhgz7hisurbvughod6t2ytfoqocedfny" alt="The Control Strip: the Cloudflare, Tailscale, Solana ticker and agent usage modules" width="271" height="24">

| Module | Its tile shows | It is there |
| --- | --- | --- |
| Cloudflare | A lamp for the tunnel and, on a computer, the bytes a second going out. | Always. |
| Tailscale | A panel of lamps for the tailnet. | On a machine with Tailscale installed. |
| Solana ticker | A token's coin and its dollar price. | Always. |
| Agent usage meter | The tokens Claude Code or Codex used today, or how full its plan's usage window is. | Once Claude Code or Codex is installed. |
| Activity | A chart of CPU, memory, GPU, network or disk, and its figure now. | On Linux. |

The tab at the right end hides the strip down to the tab alone; click it
again to bring the strip back. The arrows at its two ends scroll the row a
module at a time when it holds more than it shows. This browser remembers
whether the strip is hidden and what you chose for each tile.

### Control Strip: Cloudflare

The lamp on the cloud is green while the tunnel is healthy, yellow when it
needs attention or is not set up yet, and grey while it is still checking.

On a computer, the figure beside the lamp is how many bytes a second this
machine's connector is sending out: its answers, what visitors download. A
phone's tile is the lamp alone.

The menu shows:

- The tunnel's replicas and edge connections.
- This machine's own connections and active requests, its request rate, the
  bytes a second it sends and receives, its total of requests and its origin
  errors.
- **Cloudflare Status…**, a window with the same live counters and the
  connector's uptime.
- **Analytics…**, the traffic Cloudflare counted. See
  [Analytics](https://exe.v2core.com/docs/using/analytics).
- **Cloudflare Setup Wizard…** and **Check Now**.

How to read the figures:

- Local traffic refreshes every five seconds while the strip is open on a
  computer, and while the menu or the status window is open on a phone.
  Tunnel-wide connection counts refresh every 30 seconds.
- Bytes come from cloudflared's QUIC connections to Cloudflare, so a
  connector on HTTP/2 shows a dash.
- Files Cloudflare answers from its cache never reach the tunnel and are not
  counted.
- Traffic totals are for this machine's connector, include every hostname on
  its tunnel, and reset when cloudflared restarts.
- Unavailable data is shown as a dash.

### Control Strip: Tailscale

On a machine with Tailscale installed, the module is a panel of nine lamps.

| Lamps | What they say |
| --- | --- |
| Tailscale's four are lit | The tailnet is connected. |
| The four are blue | The machine's traffic leaves through an exit node. |
| The four are yellow | Something needs attention: a health warning, a login due. |
| All are dim | Tailscale is off. |

The menu's first lines say which machine this is and how many devices are
online. Under them:

| Item | What it does |
| --- | --- |
| **Tailscale Active**, **Tailscale Inactive** | Turn Tailscale on and off. |
| **Log In…** | Stands in place of that pair when Tailscale needs a login, and opens the login link. |
| **Exit Node** | Picks one of the devices that offer one, or **None**. **Allow LAN Access** is in the same submenu. |
| **Devices** | Lists the devices that are online. Pick a row to copy its address. |
| **Serve** | Lists the Tailscale Serve rules. Pick a row to copy its address. |
| **Accept Routes**, **Use Tailscale DNS**, **Shields Up**, **Tailscale SSH** | Switch each setting on or off. Rest the pointer on one for what it does. |
| **Copy This Machine's Address** | Copies this machine's name on the tailnet, or its address. |
| **Admin Console…** | Opens Tailscale's admin console. |
| **Check Now** | Asks Tailscale again. |

The rows from **Exit Node** to **Copy This Machine's Address** are there
while Tailscale is connected.

Three changes ask first, because the desktop could go with them: turning
Tailscale off or raising the shields while the desktop is itself reached
through Tailscale, and choosing an exit node while you reach the desktop
over the local network with **Allow LAN Access** off.

The daemon asks the `tailscale` CLI on the machine and changes only those
settings (`GET /v1/tailscale`, `POST /v1/tailscale/set`). It runs as
Tailscale's operator user, so it needs no sudo.

### Control Strip: Solana ticker

The tile shows a token's coin and its dollar price: SOL, PUMP, MET or SKR.
Pick the one the tile wears from its menu.

The menu lists all four with their day change, and PUMP, MET and SKR with
their price in SOL as well. Its first line names the source of the prices
and their time, and **Update Now** fetches them again.

A long run of zeros after the point folds into a small sunk count, so PUMP's
0.00003717 SOL reads 0.0₄3717 SOL, MET's 0.002404 SOL reads 0.0₂2404 SOL,
and the column lines up. A price in SOL folds from two zeros, a dollar price
from three.

The figures are Coinbase's public spot prices, fetched once a minute by the
daemon and shared by every desktop open on this machine. Jupiter's prices
stand in for a token Coinbase cannot quote, or when Coinbase does not
answer; the menu's first line then names Jupiter. A Jupiter API key from
portal.jup.ag goes in `jupiter.api_key` of `~/.exe/config.json`.

The same menu turns notifications on. **Notifications: turn them on**,
further down, says how.

### Control Strip: agent usage

The **agent usage meter** shows how many tokens Claude Code or Codex has
used on this machine today. From its menu:

- Pick the agent the tile wears.
- **Show Plan Limit** puts the fullest of that agent's usage windows on the
  tile instead (`46% wk`). **Show Tokens Today** puts the tokens back.
- **Update Now** reads the figures again.

The menu lists both agents with today's tokens and their five-hour and
weekly windows. The windows stand in columns, so one agent's week is under
the other's; a window an agent lacks stays blank.

Each agent's submenu has the detail:

- Today and the last seven days, each split into tokens sent fresh, read
  back from the prompt cache and written.
- The week as a column a day, with a figure on the busiest day and on today.
  Every day's own split is in its tooltip.
- How full each usage window is, and when it resets.

The daemon counts from the CLIs' own logs, Claude Code's transcripts and
Codex's rollouts, so every session on the machine is in, not only the ones
opened from this desktop (`GET /v1/agents/usage`). Claude Code's usage
windows are as fresh as its last reply in a Claude Code window here. Codex's
are read live on the ChatGPT sign-in.

An agent has its row once it is installed and has left figures. One that is
installed and has left none is named with the words **installed, no usage on
record**. With neither agent installed, the tile stays away.

### Control Strip: activity

The **activity module**, a little monitor at the end of the strip, charts
this machine: CPU, memory, GPU, network and disk, a sample every two
seconds.

- The tile shows the last minute of one of them and its figure now: `23%`,
  or `1.2 MB/s` for network and disk, both directions added.
- The menu shows all five as charts over the last four minutes (about two on
  a phone's narrow screen), each with its words. Pick a row to put it on the
  tile.

| Row | Its words |
| --- | --- |
| CPU | Its share of all cores, and its hottest sensor's temperature. |
| Memory | Memory in use: the total less what the kernel says is available. |
| GPU | Its use, temperature and power, from `nvidia-smi`. A host without `nvidia-smi` has no GPU row. |
| Network | Bytes a second in and out over the physical network interfaces. |
| Disk | Bytes a second read and written on the physical disks. |

Percentages fill their chart at 100%. Rates fill it at their busiest moment
in view, with two-way rates stacked. Counting only physical interfaces and
disks counts every byte once: a VM's traffic crosses its tap, the bridge and
the NIC.

The daemon samples only while a desktop reads (`GET /v1/host/monitor`). The
figures start when the strip opens and stop ten minutes after the last
desktop folds it away or goes hidden, and the daemon keeps the last ten
minutes. It reads Linux so far; on another system the tile stays away.

### Notifications: turn them on

exe can send push notifications to this device: a browser on a computer, or,
on a phone, the desktop added to the Home Screen (**Install the desktop as
an app**, further down).

1. Click the Solana ticker in the Control Strip.
2. Choose **Notify Me of Big Moves**, and allow notifications when the
  browser asks.
3. Choose **Send a Test Notification** in the same menu to check that one
  reaches the device.

The menu item again turns them off. That one switch covers everything in the
four sections that follow: none of them has a setting of its own.

On iPhone and iPad Home Screen apps (iOS 18.4 or later), and Safari on Mac
(18.5 or later), notifications include a browser-readable fallback. An
agent-finished alert, for example, can still appear and open its session
if the desktop's background worker cannot run. Open the desktop once after
updating so it can refresh the subscription's address. Other browsers keep
receiving the same notifications through the background worker. Permission,
connectivity and the device's notification settings still apply.

### Notifications: big price moves

You hear when SOL, PUMP, MET or SKR makes a move that is rare for it, within
an hour or within a day:

| Token | In an hour | In a day |
| --- | --- | --- |
| SOL | 2.5% | 6% |
| PUMP | 5% | 12% |
| MET | 6% | 15% |
| SKR | 8% | 25% |

There are never more than four per token in 24 hours. A move that qualifies
while the four are spent is counted into the next one. **Recent Moves** in
the ticker's menu lists the last eight.

### Notifications: weather

With notifications on, the daemon also watches the sky over the first city
in the Weather app, the row your drag put on top. Reorder the Weather list
and the watcher follows.

| Push | When it comes |
| --- | --- |
| **Rain possible soon**, or **Rain likely** when the chance is high | Open-Meteo's quarter-hour forecast puts rain in the next 60 minutes. |
| **Next hour looks dry** | A fresh forecast clears the rain. |
| **Dangerous heat** | A feels-like temperature of 105°F. |
| **High wind** | Gusts of 35 mph. |
| **Fire weather** | Humidity at or under 15% meets those gusts. |

The heat, wind and fire lines are the National Weather Service's own. A
fire-weather hour is a red-flag hour, and it keeps the plain wind push
quiet, so a Santa Ana taps once.

Each push names the city, and each hazard clears with its own easing push.
There are at most four warnings per hazard in 24 hours. They speak °F and
mph for a US city, °C and km/h elsewhere.

### Notifications: an agent has finished

The same switch tells you when an agent is done.

| Push | When it comes |
| --- | --- |
| **Claude Code finished** or **Codex finished**, with the session's title | A turn ends in a session of a Claude Code or Codex window. |
| **Chat finished**, with the chat's title | A Chat run ends on its own. A VM's Agent tab starts one too. |
| **Chat stopped**, with the error that ended it | A Chat run ends in an error. |

For a chat pinned to a VM the push says **Chat with** and the VM's name. Tap
the notification and the desktop opens that window on that session or chat,
whether it was already open or has to be loaded first.

Some turns stay quiet:

- A turn that ends within a minute of your last keystroke in that session
  from a desktop window, or of your last message in that chat. You are still
  there.
- A run you stopped yourself.
- A session that was not started from the desktop's windows. A `claude` run
  in a shell of your own carries none of the desktop's hooks.

One notification stands per session: a later turn's replaces the earlier
one.

### Notifications: from your own script

A script on the exe machine can send its own words the same way:

```
$ curl -s 127.0.0.1:7777/v1/push -d '{"title":"Backup","body":"Done."}'
```

It reaches every browser that turned notifications on. A title and a body
are both needed.

- A `tag` makes a later push with the same tag replace the earlier one.
- A `url` (a path, such as `/`) is what a tap opens.

Only callers on the machine itself are accepted; the same request over
Tailscale is refused.

### On a phone

On a phone the desktop becomes a home screen of icons, and windows go
fullscreen, one at a time. They cannot be moved or resized there.

- Tap an icon to open it. It swells and fades as its window comes up.
- Closing a window walks back through the windows you opened before it, like
  a phone's back button.
- Press and hold the desktop for the desktop menu, and a window's title bar
  for **Save a Screenshot**.
- The clock is a small clock face. Tap it and it spells the time out for a
  few seconds, or opens the World Clock app when that app is installed.
- In a Claude Code or Codex window a finger drag scrolls back through the
  session's history, and a flick keeps it going. Press and hold the terminal
  for **Copy**, **Paste** and **Select All**: how to paste into Claude Code's
  composer, since the phone's keyboard has no paste key.
- A row of keys under the terminal sends what the phone's keyboard has not:
  **Esc**, **Tab**, **Shift-Tab** and the four arrows. Esc stops Claude
  Code's turn or clears its composer, Shift-Tab cycles its modes, and the
  arrows walk a menu or the prompt history. The keyboard stays up while you
  tap them. Terminal windows have the row too.
- In Todo and Weather a finger scrolls the list. To move a row, press and
  hold it for half a second until it tints, then drag it to its new place.
- A picture window swipes down to close, the window under it showing through
  the gap as it goes. Pinch the picture to zoom it, or double-tap it; zoomed
  in, a finger drags it about, and a double tap brings it back. A picture
  opened from an issue in Projects swipes sideways to the issue's next or
  previous picture.

### Install the desktop as an app

Over HTTPS (a Tailscale Serve address, say) the desktop installs as an app:

- On an iPhone or iPad, use **Add to Home Screen**.
- In Chrome or Edge, use the install button in the address bar.

It then opens in a window of its own, with the menu bar right under the
status bar.

When the daemon is not answering, because it is restarting or the device is
offline, the desktop shows an alert in place of the browser's error page and
comes back by itself once the daemon does.

An app can keep a copy of what it shows on the device, to be read
meanwhile; Notes and Projects do. The alert names the apps that have one
and has a button for each: **Open** and the app's name. The app opens in a
new tab, beside the alert, or in the alert's place when exe is installed as
an app, which has no tabs. It shows what the daemon last had, says how old
its copy is, and dims whatever would change something until the daemon
answers again. The copy is renewed each time the desktop
is opened and each time a notification arrives, so it is as old as the
last time the device heard from the daemon.

### Updates to the desktop

Updates arrive by themselves. When the daemon restarts with a new desktop or
new system apps, an open desktop notices and reloads: a few seconds after
the daemon is back, and at once in a tab you are not looking at. A restart
that changes nothing the browser runs leaves your windows alone.

Returning to the installed app on an iPhone or iPad checks for a new
desktop and refreshes the app list, even when the phone resumes an old
page. If the connection is still waking up, it tries again while open.
There is no need to force-close the app to pick up an update.

The reload waits:

- While you are typing or clicking.
- While a menu or a dialog is up.
- For as long as there is unsaved work: text typed into a field and still
  there (in a closed window too), an editor with unsaved changes, unsaved
  Icon Editor pixels.

Blue Pencil saves its drafts as you type, so its text never holds an update.
While unsaved work holds the reload back, the Apple menu shows **Reload for
Update** to take the update anyway.

This is the open desktop catching up with a daemon that has restarted.
Moving exe itself to a newer release is **Software Update…**; see
[Updating](https://exe.v2core.com/docs/using/updating).

### Icon Editor: repaint the icons

Every system icon is hand-plotted pixel art, and **Windows → Icon Editor**
lets you repaint it.

<img src="https://hub.v2core.com/v1/embed/bafkreifywpjwrpr7wss5xiw5aukrugxpvupownkyis6pjq5vlzvdwidhby" alt="The Icon Editor's gallery of system icons, with New Icon at its top" width="562" height="383">

The gallery lists each one: the VM Mac, folders, documents, the Trash,
built-in apps such as Mac OS 9, the minis in search results, even the Apple
menu. Double-click one to open it in a fat-bits editor.

- The tools are a pencil, an eraser, an eyedropper, a fill and **Undo**,
  with the Platinum palette and a custom color well.
- **Save** applies the art everywhere at once. It follows the desk into
  every browser and every machine joined to this one, and survives restarts.
- **Revert** brings the factory icon back.
- On a VM icon, pixels painted the factory screen-green keep changing color
  with the VM's state.

**New Icon…** adds icons of your own on a 32×32 or 16×16 grid: draw them,
copy their SVG for use anywhere, delete them when done. System icons can
only be repainted, never deleted.

An editor left open is restored with the rest of the layout, in its
position, stacking and shaded state.

## Claude Code, Codex and Terminal

Three kinds of window on the desktop are terminals on the machine exe runs
on. **Claude Code** and **Codex** each run that coding agent's CLI, and
**Terminal** is a plain shell. Where the machine has tmux, a program that
keeps a terminal session running with no window on it, what runs in them
does not stop with the window: close the window, the tab or the laptop and
it works on.

### What they are for

For you:

- **Work that outlives the browser.** A session keeps running with no window
  open. Open the window again, from any device, and you are back in the same
  conversation.
- **Several conversations side by side.** A column in the window lists the
  agent's sessions, and a dot marks one that is still working or one that
  waits for you.
- **Usage in view.** The status line shows a Claude Code session's model,
  context, tokens and cost, and for either agent how much of the plan's
  usage windows is spent.
- **Word when a turn ends.** With notifications on, a push says the agent
  has finished, and a tap opens the window on that session.
  [The desktop](https://exe.v2core.com/docs/using/the-desktop) says how to
  turn them on.
- **Conversations started elsewhere.** A Codex thread from the ChatGPT app
  on your phone, a `claude` run over SSH or in an IDE: the window lists
  them, and a click continues one here.
- **A shell on the machine.** A Terminal window keeps its shell through a
  reload, a closed browser and a restart of the daemon.

For a coding agent:

- **A session not tied to a browser.** The CLI runs in a tmux session on the
  machine, so a long turn goes on while nobody is looking.
- **Files by drag and drop.** A file you drop on the window is uploaded and
  its path typed at the prompt, so the agent reads a screenshot or a log
  from disk.
- **Its state shows.** When it finishes a turn, or stops for a permission or
  a question, its row is marked, and the end of a turn can send you a push.
  It is not left waiting unseen.
- **Other tools can start it.** Over the API a script, or another agent,
  opens a session with a first message, types into a running one and ends
  it: work you can watch in the window and take over.
- **exe close at hand.** The session runs on the machine exe runs on. Hand
  the agent the Agent Skill Guide and it uses exe's VMs as its sandboxes.
  [The coding agent](https://exe.v2core.com/docs/using/the-coding-agent) has
  the guide.

### Install Claude Code or Codex

**Help → Welcome…**, the **Claude Code and Codex** card, says which of the
two is installed on this machine, with its version, and its **Set Up…**
button opens Agent Setup, a window of three steps: a page for Claude Code,
one for Codex, and a review.

- A CLI that is installed shows its version and where it is, and what exe
  adds to it: the window that outlives the browser, the session column,
  usage in view and word when a turn ends. **Open Claude Code** or **Open
  Codex** opens its window.
- A CLI that is not installed shows the command that installs it on this
  kind of machine: Anthropic's installer for Claude Code (`curl … | bash`
  on Linux and a Mac, `irm … | iex` in PowerShell on Windows), npm or
  Homebrew for Codex, with another way in small type where the machine
  has one. **Copy Command** copies it; **Install in Terminal…** opens a
  Terminal window on the desktop that runs it and leaves you at a prompt
  after. Then open the CLI's window from the desktop and sign in the
  first time.
- **Check Again** looks at the machine again, and the window also looks
  whenever it comes forward, so an install made meanwhile shows on its
  return. A CLI installed while the desktop is open gets its icon without
  a reload.
- **Review** lists both, with the version of each that is here, and
  whether tmux is on the machine: with it a session outlives its window,
  without it each window is a fresh run of the CLI.

exe finds a CLI on PATH, in `~/.local/bin`, where npm, bun and Homebrew
put programs, and in every nvm Node version's `bin`. A script can ask too:
`GET /v1/agents/cli` names each with its path and version, and what an
installer would lean on here (`curl`, `node`, `npm`, `brew`).

### Open a window

Double-click the **Terminal**, **Claude Code** or **Codex** icon on the
desktop. An agent's icon is there only when its CLI is installed on this
machine; hover it for the folder its sessions start in.

- An agent has one window. Its icon brings that window forward when it is
  already open. Where the machine has tmux, the window opens on every screen
  that has the desk open, on the session it showed last.
- Every double-click on **Terminal** opens another window: **Terminal**,
  **Terminal 2** and so on. The window opens on every screen that has the
  desk open, and on one that opens the desk later.

The desktop menu can open all three too, and can run one command in a
terminal window of its own.
[Desktop context menu](https://exe.v2core.com/docs/using/desktop-context-menu)
lists its actions.

Codex sessions use standalone mode when the installed CLI supports it.
This keeps their notifications and session status without the shared
background server warning. With tmux, they still keep running when the
window closes.

### The agent window

<img src="https://hub.v2core.com/v1/embed/bafybeiedurlk54ghfmghdxnscdn6xejkss37uekbun2xrrrxbukqwamwkq" alt="The Claude Code window: its sessions down the left, the chosen one's conversation in the terminal, and the model, context, tokens, cost and weekly usage on the status line" width="896" height="487">

A Claude Code or Codex window has three parts. The session column, on a
machine with tmux, runs down the left. The terminal with the conversation
fills the rest; in the Claude Code window a line over it shows the session's
latest prompt. The status line at the foot shows the connection at its left
and usage at its right.

In the **installed exe app**, the screen stays awake while Claude Code or
Codex is the active window. Switching to another window, closing or shading
the agent window, or leaving the app lets the screen sleep normally again.
Returning to the agent window asks to keep it awake again. This works on
supported devices, including iPhone and iPad Home Screen apps on iOS 18.4
or later; the device can still refuse it to save power. Ordinary browser
tabs keep their normal screen-sleep behaviour.

### Usage on the status line

| Window | What the status line shows | How current it is |
| --- | --- | --- |
| Claude Code | The session's model, context in use, tokens, cost, and the plan's 5-hour and 7-day usage windows. | Updated after every reply, by Claude Code's own status-line hook. |
| Codex | The ChatGPT subscription's 5-hour and weekly usage windows. | Re-read once a minute while the window is open. |

Hover the figures for the long form, which adds when each usage window
resets. A figure at 90% or more turns red.

- **Claude Code.** The figures follow the session on screen. The plan's
  usage windows show on a subscription, after the session's first reply. A
  status line of your own in `~/.claude/settings.json` keeps working inside
  the terminal.
- **Codex.** The figures are read on the ChatGPT sign-in under
  **Configuration → OpenAI**, and without it the status line shows the
  connection alone.
  [The coding agent](https://exe.v2core.com/docs/using/the-coding-agent)
  says how to sign in.

The Control Strip's agent usage meter counts what both agents have used on
the whole machine today.
[The desktop](https://exe.v2core.com/docs/using/the-desktop) describes it.

### Sessions: several conversations in one window

On a machine with tmux, the column down the window's left lists the agent's
sessions, one row for each tmux session. Each is a conversation of its own.

- **Switch.** Click a row and the window moves to that session.
- **Start another.** **New**, under the list, starts another conversation in
  a session of its own, at the top of the list.
- **Start one in a project's folder.** In Projects, right-click a project
  and choose **Open in Claude Code**: the Claude Code window opens, or comes
  forward, and a new session starts in the project's working directory, at
  the top of the list. On a phone, each project in the list has an **Open
  in Claude Code** button at its right, which does the same. The item and
  the button are greyed for a project without a working directory, or
  whose folder is not on this machine.
- **Work on an issue.** Right-click an issue in Projects and choose **Send to
  Claude Code** or **Send to Codex**. Only detected agents appear. A new
  session starts in the project's folder with the full issue and comments
  as its initial task. This needs tmux and a working directory on this host;
  the agent keeps its usual sign-in, trust and permission prompts.
  [Apps](https://exe.v2core.com/docs/using/apps) says how the agent's plan
  and report come back to the issue.
- **Resize the column.** Drag the line between the list and the terminal.
  The width is kept with the window's layout.

The list runs newest first, so the session the icon first opened is normally
at the bottom. On a phone the column is a strip of tabs above the terminal,
and a row of keys under it sends **Esc**, **Tab**, **Shift-Tab** and the
arrows, which the phone's keyboard has not.

<img src="https://hub.v2core.com/v1/embed/bafybeigr4z6peilck47xgpbqq6lcxn2ka3hb36nhuxfglswhcw7yn5uco4" alt="The Codex window: its sessions down the left, a finished turn in the terminal, and the subscription's weekly usage on the status line" width="821" height="487">

A row is titled the way the CLI titles its terminal. Claude Code keeps that
on its current task. Codex, which the desktop starts with its terminal title
set to the thread, keeps it on the thread's name. Until the CLI has set a
title, a row reads the agent's name and the session's number, such as
**Claude Code 2**.

A window opens on the session it showed last, with its row scrolled into
view. That holds from any browser and after the daemon restarts, because
tmux itself remembers where the window was.

### See which session needs you

While the window shows another session, a mark before a row's title says how
that session stands:

| Mark | The session |
| --- | --- |
| A pulsing green dot | Is still working. |
| A black dot | Waits for you: its turn has finished, or a permission or a question is pending. |

Hover a row for which of these it is. The session on screen has no mark,
since the terminal shows how it stands. A turn you stopped with Escape
leaves no mark either: you saw it stop.

How the desktop learns it:

- **Claude Code** reports through its hooks.
- **Codex** reports through its `notify` command, which the desktop points
  at the session, and through the terminal bell, which the desktop has Codex
  ring at the end of a turn and for an approval, so tmux notes it while no
  window is looking.

The desktop gives each session these settings as it starts it and does not
write to your own settings files. A `notify` of your own in
`~/.codex/config.toml` still runs after the desktop's, and Claude Code hooks
of your own keep running.

### Archive a session

Right-click a row for its menu:

| Item | What it does |
| --- | --- |
| When the session started, in grey | Says when; it is not a command. |
| **New Session** | Starts a new session, as **New** does. |
| **Copy Title** | Copies the row's title. |
| **Get Info** | Claude Code: a dialog about the conversation — its summary, turns, tokens and more (below). |
| **Archive…** | Ends the session and its process, after a dialog. |

An archived conversation stays on this machine. In the Claude Code window,
**Search** finds it again (below), and `/resume` inside Claude Code or Codex
brings it back too. The desktop keeps no list of its own: it reads the
conversations the CLI keeps. Claude Code deletes a conversation 30 days
after its last write unless `cleanupPeriodDays` in `~/.claude/settings.json`
says otherwise.

- **Archive…** shows only once the conversation exists.
- Once the session's processes are gone, a note at the foot of the screen
  says what it gave back — the memory those processes held, and the swap —
  and how much memory the machine has available now. On a Mac or a PC,
  where the daemon cannot read what a process holds, it says only the latter.
- A turn still in flight when you archive is lost.
- When the window was showing that session, it moves to the one started
  before it, or failing that the one after. Archiving the agent's only
  session leaves the window with none, and it stops as it does whenever the
  last session ends.

### Get Info on a conversation

In the Claude Code window, right-click a row and choose **Get Info** for a
dialog over the window about that conversation, read from the transcript
Claude Code keeps on this machine. It tells you what a session was before
you archive it:

| Line | What it says |
| --- | --- |
| **Summary** | What Claude Code calls the conversation: its summary once it has compacted the context, else the title it gave it, else the first prompt. |
| **First prompt**, **Last prompt** | The first line of each. |
| **Turns** | How many prompts you sent, and how many replies the model gave. |
| **Tokens** | What the replies cost: the input the model read, the prompt cache included, and the output it wrote; under it, how much of the input came from the cache and how much was written to it. |
| **Model** | The models that replied, in the order they first did. |
| **Started**, **Last active**, **Where** | When it began, when the transcript was last written, and its folder. |
| **Session** | The session's name in the column, such as `exe-claude-90`: what **Search** finds it by. A conversation no session holds says so. |

- The row is only asked about: the window stays on the session it was
  showing.
- **Archive…** in the dialog is the row's own. A conversation no session
  holds — one started elsewhere — gets **OK** alone.
- Return and **OK** put the dialog away, and so does Escape.
- The figures are the transcript's: a sub-agent's turns are in a transcript
  of its own and are not counted.
- A long transcript takes a moment to read; the status line says so, and
  the dialog opens filled.
- The Codex window has no **Get Info** yet.

### The prompt line: the latest prompt, and every prompt

In the Claude Code window, the line over the terminal shows the first line of
the latest prompt in the session on screen, and at its right how many prompts
the session has had. It follows the column: switch sessions and it shows the
other session's, send a prompt and it shows that one within a couple of
seconds. A session with no prompt yet says so.

Click the line for **Prompts**, a dialog over the window that lists every
prompt of the session in order, each with its number, its first line and when
it was given. The latest is selected, and the selected prompt stands whole in
the box under the list, every line of it, where it can be selected; **Copy**
copies all of it. The arrows move along the list; Return, **OK**, Escape and
the close box in its title bar put it away.

- A prompt is what you typed or pasted. Slash commands and their output, tool
  results and a sub-agent's turns are not listed, nor is what a background
  task woke Claude Code with.
- A prompt longer than 20,000 characters is cut there in the list; the row's
  tooltip says so, and the whole is in the transcript.
- The list is read from the transcript when the dialog opens; a long
  transcript takes a moment, and the status line says so.
- The Codex window has no prompt line yet.

### Continue a conversation started elsewhere

A conversation started on this machine outside the window is a conversation
on this machine all the same. Under a rule below its sessions, the column
lists the latest ten of them:

- **In the Codex window:** threads started in the ChatGPT app on your phone
  (its remote Codex runs here), in the Codex app or in VS Code, titled the
  way the app titles them.
- **In the Claude Code window:** sessions started by `claude` run in a
  Terminal window, over SSH or in an IDE, titled as Claude Code titles them,
  your own `/rename` first.

Each of these rows has a hollow dot, or a green one while a turn runs there.
For Claude Code the dot is green too while its CLI is still open there.
Hover a row for where and when it started.

To carry one on here:

1. Click the row, or right-click it and choose **Continue Here**.
2. The status line says **opening session…** while it starts.
3. The conversation opens in a session of its own, in the folder it was
  started in, and its row moves up among the sessions.

If that folder is gone, the session opens in the agent's usual one. Leave a
row be and the conversation stays where it is. If opening fails, the error
is shown and you can click again; a reconnect also allows a fresh attempt
once the window is connected.

A Claude Code session whose window or shell died with it, because a Terminal
window was closed or the daemon restarted, is right there to click: it
continues as `claude --resume` would.

Not listed: headless runs (`claude -p`, the turns of a tool that watches a
hub), `codex` started in a terminal of your own, and the conversations the
column's own sessions hold.

### Search for a conversation

**Search**, beside **New** in the Claude Code window, opens a dialog over
the window that lists every conversation on this machine:

- the window's sessions, as the column has them;
- under a rule, the conversations no session holds: the ones started
  elsewhere and the ones you archived, all of them and not only the latest
  ten the column shows.

Type words to narrow the list. A conversation stays when every word is found
in its title, its first prompt or its folder, in any case and inside longer
words; a session of the column's also by its name, so `exe-claude-90` or
`90` finds that session. A session's row says its name and the day it
started, any other conversation's row its folder and the day; hover a row
for the rest.

| To | Do this |
| --- | --- |
| Open the selected row | Press Return, press **Open**, or double-click the row. |
| Move the selection | Use the arrow keys, Page Up and Page Down, or click a row. |
| Put the dialog away | Press Escape or **Cancel**. |

Opening a session moves the window to it. Opening any other conversation
continues it in a session of its own, as a click on its row in the column
does.

- The dialog belongs to the window: the rest of the desktop keeps working
  while it is up.
- The list is as it stood when the dialog opened. Open **Search** again for
  a fresh one.
- In a column too narrow for both words, and on a phone, **Search** shows as
  a magnifier.
- The Codex window has no **Search** yet.

### Closing and reconnecting

- **Close the window** and its sessions run on. Open the agent's icon again
  to come back to them.
- **The window belongs to the browser.** Open in one tab, it is open in
  every tab of that browser, and it comes back after a reload or when the
  browser is opened again. Its close box closes it in all of them. Another
  browser or device opens its own from the icon, at the same place and size.
- **A window whose link drops** reconnects on its own, in the background, to
  the session it showed: a tab left behind while the laptop slept, a network
  that came and went, the daemon restarted. A window whose tmux client was
  detached with the sessions still there does the same.
- **When the agent's last session has ended**, and only then, the window
  stops and says so. Close and reopen it for a fresh conversation.
- **The same session in two windows** at once, a phone beside the desktop,
  works. The terminal takes the size of whichever was attached, typed in or
  resized last.

### Scroll back

The mouse wheel scrolls back through the conversation in either window.

- **Claude Code** takes the wheel itself and scrolls its own transcript.
- **Codex** keeps a transcript of its own as well, since version 0.160, and
  the wheel scrolls that, three lines at a time. While you are scrolled
  back Codex shows "Back to bottom" above its composer, and what you type
  goes into the composer without moving the view. Esc returns to the
  latest line.
- **An older Codex**, or one started with `--no-alt-screen`, leaves its
  transcript to the terminal, and on a machine with tmux that is the tmux
  session's history. The wheel scrolls it there, in tmux's copy mode, with
  its `[123/980]` position at the top right. Typing returns to the live
  screen first, so the keys reach Codex as in any terminal.

On a phone, drag a finger instead of turning a wheel.

### Copy, paste and links

To copy text, drag across it.

- **In a Codex or Terminal window** the drag selects the text as in any
  terminal. Right-click and choose **Copy**.
- **In a Claude Code window**, Claude Code in its fullscreen mode draws the
  selection itself, and as the drag ends the text goes straight onto your
  computer's clipboard. Should the browser refuse the page its clipboard,
  the text waits for **Copy** in the right-click menu instead, until your
  next click or key.
- **Shift+drag** (Option+drag on a Mac) in a Claude Code window makes the
  terminal's own selection, for **Copy** the same way.

The right-click menu also has **Paste**, which puts the clipboard's text in
at the cursor, and **Select All**. If the browser will not let the page read
the clipboard, its own paste keys still work.

On a touch screen, press and hold the terminal for the same menu. **Paste**
asks the browser for the clipboard — an iPhone puts up a Paste bubble to tap
— and where the browser will not read it for the page, a small **Paste**
dialog opens instead: press and hold its field and choose Paste, then the
dialog's Paste sends the text on to the terminal.

A web address in a terminal is underlined under the pointer, and a click
opens it in a new tab.

### Hand the agent a file

To give the agent a screenshot, a log or a document, drag the file from your
computer onto the window.

1. The file is uploaded to the Workspace root, the same as a drop on the
  desktop.
2. Its path on this machine is typed at the cursor, quoted if it needs it.
3. "look at this" is all the prompt then needs: the agent reads the file
  from there. Claude Code shows an image it is given a path to.

Several files arrive as several paths. A file over 10 MB is refused, and a
folder cannot be dropped. A plain Terminal window takes a drop the same way;
a VM's window does not, since a path on this machine means nothing in the
guest.

If the window's link is down when the upload lands, with a laptop back from
sleep or the daemon restarting, the file is in the Workspace all the same.
Its path waits in a row under the terminal:

| Button | What it does |
| --- | --- |
| **Insert** | Types the path once the window is connected again. |
| **Copy** | Puts the path on the clipboard, for another window. |
| **Dismiss** | Forgets the path. |

Nothing is uploaded twice.

### Terminal windows

On a machine with tmux a Terminal window keeps its shell the way an agent
window keeps its sessions. Each Terminal is a tmux session of its own, named
`exe-term-1`, `exe-term-2` and so on, and `tmux attach -t exe-term-2` picks
one up over SSH.

- **The shell outlives the window's link.** Reloading the page, closing the
  browser by accident, the laptop sleeping or the daemon restarting only
  takes the window away from the shell, never the shell. The window
  reconnects on its own.
- **Windows come back.** The next time the desktop loads in this browser,
  the Terminal windows it had come back where they were, with whatever ran
  in them still running. So does any Terminal no window shows anywhere, one
  left on a phone say. A Terminal another desk is showing stays there.
- **The close box ends the shell**, as in any terminal app, and so does
  `exit`: the window closes with it.
- **tmux stays out of the way.** These sessions have no tmux status line and
  no prefix key, so Ctrl+B reaches the shell, and `tmux` run inside works as
  it would anywhere.
- **The wheel scrolls back** through the session's history, in tmux's copy
  mode, with its `[123/980]` position at the top right. Typing returns to
  the live screen.

### Start and drive sessions over the API

The session column is also an API, for tools that want a conversation you
can watch. The calls are written for Claude Code here; put `codex` in place
of `claude` for Codex. Like every API call, they need the API token when one
is set.

| Request | What it does |
| --- | --- |
| `GET /v1/agents/claude/sessions` | Lists the rows with their states and, as `threads`, the conversations started elsewhere: the latest ten, or every one with `?threads=all`. |
| `POST /v1/agents/claude/sessions` | Opens a numbered session and answers with its `name` and `number`. |
| `POST /v1/agents/claude/sessions/<name>/prompt` | Types a message into a session. |
| `DELETE /v1/agents/claude/sessions/<name>` | Ends the session, as **Archive…** does, and answers once its processes are gone with what it gave back: `freed` and `swap`, the bytes they held (`measured` says whether they could be read), and `available`, the machine's memory now. |
| `GET /v1/agents/claude/sessions/<name>/info` | What **Get Info** shows for a session: `summary`, `prompt`, `last_prompt`, `turns`, `replies`, `tokens` (`input`, `output`, `cache_read`, `cache_write`), `models`, `created`, `activity`, `cwd`. Claude Code only. |
| `GET /v1/agents/claude/threads/<id>` | The same for a conversation no session holds, by its session id. |
| `GET /v1/agents/claude/sessions/<name>/prompts` | Every prompt of the session, whole and in order, as `prompts`: each with `text`, `at` (when it was given) and `cut` when it ran past 20,000 characters. What the prompt line's dialog lists. Claude Code only. |
| `GET /v1/agents/work` | Lists the sessions of both agents that are at work now: when each one's turn began (`since`, beside the daemon's own `now`) and the issue addresses, such as `EXE-12`, that its instructions named (`refs`). |

A session's `<name>` is its tmux session's: `exe-claude` for the one the
icon opens and `exe-claude-2`, `exe-claude-3` and so on after it, or
`exe-codex` and `exe-codex-2` for Codex.

A new session takes these fields, all optional. Codex takes `prompt` and
`resume` alone.

| Field | What it sets |
| --- | --- |
| `prompt` | The first message. |
| `resume` | A conversation to continue: a Claude Code session id, or a Codex thread id. |
| `fork` | With `resume`, fork the conversation instead of continuing the same one (Claude Code's `--fork-session`). |
| `session_id` | The id the new Claude Code session gets, so the caller can find its transcript. |
| `permission_mode` | One of `default`, `acceptEdits`, `plan` and `auto`. |
| `model` | The model, by alias or full name. |
| `name` | The session's title in the column: one line of up to 80 characters. |
| `dir` | The folder the session opens in, as a whole path to a folder on this machine; without it, exe's project folder. |

A Codex thread resumed this way opens in the folder it was started in, as a
click on its row does.

A message is `{"prompt": "…", "say": "…"}`. The `prompt` goes in as a paste,
and Claude Code treats pasted text as material rather than as your words. So
give the request a `say` as well: one line, typed ahead of the paste, that
tells the session what to do with it.

A tool that watches a hub builds this way. An instruction you post in one of
Claude's hub threads opens as a session in the Claude Code window's column,
works there in view, reports in the thread when it is done and stays open in
the window to be continued.

### On a machine without tmux

Sessions need tmux, and a Windows machine has none. Without it the three
windows are still there, with less behind them:

- A Terminal is a one-off shell that ends with its window. On Windows the
  shell is PowerShell.
- A Claude Code or Codex window has no session column, and each time it
  opens it is a fresh run of the CLI.
- On Windows a Claude Code window's status line has no usage figures.

## Desktop context menu

The desktop menu opens at the pointer when you right-click an empty part
of the desktop. Unlike the menu bar, it is yours to write: a short text
file with a label and an action on each line, so that what you open most
is one right-click away.

### What a menu of your own is for

For you:

- **Shortcuts to your own things.** One line opens a project's folder in
  the Workspace, a file in the text editor, a VM on its Terminal tab, an
  app or a web address.
- **A command-line tool in a window.** `terminal btop` opens btop in a
  window of its own, and any other command on the host works the same
  way.
- **Lists that fill themselves.** `@vms`, `@apps` and `@windows` show
  this desktop's VMs, apps and open windows each time the menu opens.
  Each VM brings a submenu to open, start, stop or restart it.
- **Your order and your words.** Rename and reorder the items, group
  them in submenus, and leave out what you never use.
- **The same menu everywhere.** A saved menu reaches the other open
  desktops and syncs to joined nodes.

For a coding agent, such as Claude Code or Codex:

- **It can change the menu for you.** An agent reads the menu with
  `GET /v1/ui/menu` and replaces it with `PUT /v1/ui/menu`. The daemon
  checks what it sends as it checks **Save**, so a file with a mistake
  never replaces your menu.
- **Only when you ask.** The Agent Skill Guide tells an agent to change
  your menu only when you ask it to. "Add the demo VM's terminal to my
  desktop menu" is such a request.

### Open the menu

Right-click an empty part of the desktop, or long-press it on a touch
screen. A quick two-finger tap also opens the menu; on a phone this works
inside an ordinary window too, away from controls, menus and dialogs. It
is a NeXT-style menu: it opens where the pointer is, with its submenus
beside it.

<img src="https://hub.v2core.com/v1/embed/bafkreicioeb4dotyt2cxdkhzjsgolzxxtnl53etfnqvgpzpxnctlja4emy" alt="A customized desktop menu open on the desk, its Virtual Machines list showing two VMs" width="340" height="369">

The factory menu offers New VM, Terminal, Workspace, lists of VMs, apps
and windows, Show All Windows, Show Desktop, tools, Cloudflare and Help. Its last item
is **Customize…**, which opens the menu editor.

### Edit and save

1. Open the menu and choose **Customize…**. The **Desktop Menu** window
   opens on the menu's text.
2. Edit the text. In this window the Tab key types a tab.
3. Click **Save**, or press **Command-S** on a Mac or **Ctrl-S**
   elsewhere.

Save checks the format first: an error identifies and selects the bad
line, and your current menu stays in place until the file is valid.

Until you save a menu of your own, the editor shows the factory menu. Its
opening comments describe the format and name every action.

The scrollbars run along the document's right and bottom edges; drag the
resize tile where they meet to make the editor wider or taller.

<img src="https://hub.v2core.com/v1/embed/bafkreia3c6lsc3lfvgmuilnaevilaigaeg7tco5ktic4upy3rr6sgx4n6q" alt="The Desktop Menu editor with System :menu.txt open: a label on each line, then its action" width="522" height="420">

### Where the menu is kept

Your menu is stored as `System :menu.txt` (normally
`~/.exe/appdata/System/menu.txt`). The file is there only once you have
saved a menu of your own.

A saved change reaches other open desktops and syncs to joined nodes; see
[Joining desks together](https://exe.v2core.com/docs/using/joining-desks-together).
Each desktop fills the automatic lists from its own VMs, installed apps
and open windows.

A menu of your own keeps its contents when the factory menu changes, so a
new shortcut in the factory menu does not appear in yours by itself. Add
it to your own file with **Customize…**.

### Let an agent change the menu

An agent, or a script of your own, reaches the same menu over HTTP:

- `GET /v1/ui/menu` answers with the menu's text in `text`, the parsed
  menu in `menu`, and `default`, which is true while the factory menu is
  in use.
- `PUT /v1/ui/menu` takes the new text as the request body, in plain
  text, and applies it as **Save** does. A file with a mistake changes
  nothing and is answered `400` with `{"error": "line N: …"}`. An empty
  body restores the factory menu.

```sh
$ curl -s http://127.0.0.1:7777/v1/ui/menu
$ curl -s -X PUT --data-binary @menu.txt http://127.0.0.1:7777/v1/ui/menu
```

When `api_token` is set, both calls need the
`Authorization: Bearer <token>` header; see
[Configuration](https://exe.v2core.com/docs/using/configuration).

### Menu format

Write one item per line. Separate the label from its action with a tab or
at least two spaces. Single spaces are fine inside a label; two spaces
end it. Action names, list names and VM tab names are lowercase and
case-sensitive.

In the format examples below, `<TAB>` means a tab character, not the
literal text `<TAB>`. The complete example menu further down uses spaces
and can be copied directly.

| Line | Meaning |
|---|---|
| `Terminal<TAB>terminal` | A label, a tab, then an action. Two or more spaces work too. |
| `Tools` | A submenu heading. Put at least one indented item below it. |
| `-` | A separator. A line made only of several hyphens works too. |
| `# My shortcuts` | A comment on its own line; it may be indented. Blank lines are ignored. |
| `@apps` | Insert an automatic list at this position. |
| `Applications<TAB>@apps` | Put an automatic list inside a named submenu. |

A line that starts with `#` is a comment and one that starts with `@` is
read as a list, so a label cannot begin with either. Put comments on
their own lines, rather than after an action.

Indent submenu children consistently, for example with two spaces per
level. The top level has no indentation. When returning to a parent
level, use exactly its earlier indentation. Only a plain submenu heading
can have indented children; an action or automatic list cannot.

### All supported actions

The following table lists every supported action. In the syntax column,
angle brackets mark a required value and square brackets an optional one;
replace these placeholders rather than typing the brackets. Each action
needs a label before it, as in `My terminal<TAB>terminal`.

| Action syntax | What it does |
|---|---|
| `about` | Open About This Computer. |
| `newvm` | Open the New VM dialog. |
| `upload` | Open Upload to Workspace. |
| `closewin` | Close the front window. |
| `showall` | Show All Windows: toggle the overview of all open windows. |
| `showdesk` | Show Desktop: slide every open window off to the edge of the screen, and back. |
| `refresh` | Refresh the VM and app lists, including My Apps when it is open. |
| `terminal [command]` | Open a new terminal on the host. With a command, run that command instead of an interactive shell. |
| `claude` | Open the host's Claude Code window. Disabled when its CLI is unavailable. |
| `codex` | Open the host's Codex window. Disabled when its CLI is unavailable. |
| `workspace [folder]` | Open the Workspace root, or a folder relative to that root. |
| `edit <file>` | Open a file in the text editor, using a path relative to the Workspace root. |
| `app <name>` | Open an installed app by its app ID. Disabled when it is unavailable. |
| `vm <name> [tab]` | Open a VM's detail window, optionally on a specific tab (see **VM tabs**). Defaults to Services; disabled if the VM does not exist on this node. |
| `chat [vm]` | Open Chat. With a VM name, show that VM's chats and pin new chats to it; without one, show all chat sessions. Disabled when Chat is unavailable. |
| `winvms` | Open the Virtual Machines window. |
| `winmyapps` | Open My Apps, the window of published sites and their domains. |
| `winchat` | Open Chat with all sessions, the same as `chat` without an argument. Disabled when Chat is unavailable. |
| `winnews` | Open the Newsfeed. |
| `winicons` | Open the Icon Editor. |
| `winconfig` | Open Configuration. |
| `winlog` | Open the Log Viewer. |
| `search` | Open desktop Search. |
| `trash` | Open the Trash. |
| `join` | Open the Node Sync dialog (**Special → Node Sync…**) to pair another exe node. |
| `hubjoin` | Open the Hub app on **Join a Hub**, its four steps for joining a hub. |
| `cfstatus` | Open Cloudflare Status. |
| `cfwizard` | Open the Cloudflare Setup Wizard. |
| `token` | Open the API Token dialog (**Special → Set API Token…**) for this browser. |
| `url <address>` | Open an `http://` or `https://` address in a new browser tab. |
| `docs` | Open this manual in the desktop, in the exe Documentation window. |
| `skillguide` | Open the Agent Skill Guide. |
| `update` | Open Software Update. |
| `welcome` | Open the Welcome window. |
| `customize` | Open this menu's editor. |

There is no separate `macos9` or `macosx` action in the customization
format; use `app macos9` and `app macosx`.

### Arguments, paths and app names

Except for `terminal`, arguments are separated by whitespace: quotation
marks do not combine words into one argument. A Workspace path or app
name containing spaces therefore cannot be used directly in an `edit`,
`workspace` or `app` item. Use `workspace` to browse to that file or
folder, or `@apps` to list apps by their display names.

For `workspace` and `edit`, use relative paths such as `Projects/demo` or
`Projects/demo/README.md`. Absolute paths and paths that escape the
Workspace are rejected.

For built-in apps, use their lowercase IDs: `app macos9`, `app macosx`,
`app hub`, `app bluepencil`, `app dict` or `app analytics`. Other apps use their
installed folder name; see [Apps](https://exe.v2core.com/docs/using/apps).

### Run a command in a window

The command after `terminal` is kept as one command line, including
spaces, quotes and shell operators. For example,
`Monitor<TAB>terminal btop` opens btop on the host if it is installed. To
reach a VM's shell instead, use `vm <name> term`.

On Linux and macOS the command runs in the host user's login shell; on
Windows it runs in PowerShell. The terminal session ends when the command
exits.

A command's window opens at 80×31 characters at least — what btop needs
with its GPU box, and room enough for any other tool — and cannot be
dragged smaller. It remembers its size and position, so the next open of
the same command puts it back where it was left.

Choosing the item again while the command runs brings its window forward;
after the command has exited, it runs it again in the same window's
place.

### VM tabs

Use these values as the optional second argument to `vm`. For example,
`Demo terminal<TAB>vm demo term` opens the Terminal tab of the VM named
`demo`.

| Tab | Opens |
|---|---|
| `svc` | Services (the default). |
| `term` | Terminal. |
| `vibe` | Agent. |
| `expose` | Expose. |
| `sess` | Sessions. |
| `notes` | Notes. |

### Automatic lists

These lists fill themselves when you open the menu. Use a list name alone
to insert its entries in place, or give it a label to make a submenu.
They take no arguments. An empty list shows a disabled **No VMs**,
**No Apps** or **No Windows** entry.

| List | Contents | Named submenu example |
|---|---|---|
| `@vms` | Every VM on this node, each with an operations submenu. | `Virtual Machines<TAB>@vms` |
| `@apps` | Installed apps, including built-in apps, shown by their display names. | `Applications<TAB>@apps` |
| `@windows` | Open windows on this desktop, sorted by title. Pick one to bring it forward. | `Windows<TAB>@windows` |

Each `@vms` submenu offers **Open**, **Open Terminal**, **btop**,
**Chat with this VM**, **Start** or **Stop**, **Restart**, **Copy IP**,
**Expose Port…** and **Publish to GitHub…**. Availability follows the
VM's state, its IP and Chat availability. Delete is not included.

These operations are supplied by `@vms`; names such as `start`, `stop`
and `restart` are not standalone customization actions.

`@apps` leaves out Welcome, which has no desktop icon either; the
`welcome` action opens it.

### Example menu

This example uses two spaces between a label and its action, and two
spaces of indentation for each submenu level. Replace `demo` and the
Workspace paths with your own VM and files; install btop on the host to
use the monitor shortcut.

```text
# My desktop menu
New VM…  newvm
Terminal  terminal
Workspace  workspace
-
Projects
  Demo folder  workspace Projects/demo
  Demo README  edit Projects/demo/README.md
  Demo terminal  vm demo term
  Demo notes  vm demo notes
Tools
  Monitor  terminal btop
  Claude Code  claude
  Codex  codex
  Mac OS 9  app macos9
  Hub  app hub
Virtual Machines  @vms
Applications  @apps
Windows  @windows
Show All Windows  showall
Show Desktop  showdesk
-
Help
  Using exe  docs
  Online documentation  url https://exe.v2core.com/docs/
Customize…  customize
```

### Limits

The menu may contain up to 300 entries, counting headings, actions,
automatic-list entries in the file and separators across all levels.
Expanded list contents do not count toward that limit.

Labels may be up to 60 characters; the file must be UTF-8 and at most
64 KiB. You can nest three submenu levels below the root (four levels in
total).

### Recover or reset the menu

If you remove `customize`, the desktop appends **Customize…** so you can
always get back to the editor. If a stored or synced file cannot be
parsed, the desktop uses the factory menu and adds
**The menu file has an error…** to open the editor on the problem.

To reset, choose **Restore Defaults** and confirm, or clear the editor
completely and **Save**. This removes the custom menu from every desktop
sharing the desk. Only an empty or whitespace-only file restores defaults;
a file containing just comments is still a custom menu.

## Mac OS 9

The **Mac OS 9** app is a real Mac in a window: an emulated Power Mac G4
running Mac OS 9.2.2, with a hard disk of its own. exe's desktop only
resembles a classic Mac. This one is the system itself, with its Finder, its
control panels and the software written for it.

### What it is for

- **Run classic Mac software.** Mount a CD image (ISO, CDR, IMG or Toast)
  and use what is on it in the system it was written for.
- **A Mac that stays as you left it.** What you install and save is kept on
  its 2 GB hard disk, on this node. Closing the window leaves the Mac
  running, and opening it again returns to the same Mac.
- **A Mac on the network.** Once installed it reaches the internet through
  this machine, and classic HTTP browsers work in it.
- **The whole screen, and sound.** The Mac can fill your screen, and on a
  node with the optional audio runtime its sound plays in your browser.

The Mac is not one of exe's Linux virtual machines. It is emulated in
software and needs no hypervisor, so it also runs on a node that cannot run
VMs.

### What it needs

- **An emulator.** Automatic emulator installation supports Ubuntu 24.04.
  Other hosts need `qemu-system-ppc` and `qemu-img` installed first (on
  macOS, `brew install qemu`).
- **Room on this node.** The installer is a 497 MiB download, and the Mac's
  hard disk is 2 GB.
- **For sound**, an optional audio runtime that exe does not fetch by
  itself; see **Sound** in this chapter.

### Set it up the first time

1. Choose **Special → Mac OS 9**; it is also among the built-in apps. The
   first launch shows each setup step: preparing QEMU, downloading the
   497 MiB Universal installer, checking its checksum, creating a 2 GB
   persistent disk, and starting the Mac.
2. When the Mac's screen appears, follow the guide the app shows above it.
   It takes you through Drive Setup and Apple Software Restore inside the
   Mac.
3. After Restore reports success, shut down the guest:
   **Special → Shut Down** in the Mac's own menu bar.
4. Click **Installation finished — start my Mac**.

Subsequent launches use the saved installation: opening the app starts the
Mac from its disk.

**Setup details** opens the steps and live download progress in a compact
in-app dialog over the Mac. Close it with **OK** or Escape; setup continues,
and the guest display keeps its size and connection.

### If setup stops

Setup can be paused and retried, and closing the window keeps both setup and
a running Mac alive.

- **Pause setup** stops a setup that is under way, and **Continue setup**
  takes it up again.
- If exe restarts during a download, choose **Continue setup** once the
  connection returns.
- Use **Resume installer** if the Mac was shut down before Restore
  completed.
- Completed installer downloads are reused after checksum verification,
  and a download that stopped part way goes on from where it stopped.
- A failed Start keeps its error visible so you can address the cause and
  retry.

### Start it, leave it, shut it down

- **Start.** Opening the app starts a Mac that is shut down. With the window
  already open, click **Start Mac**.
- **Leave.** Closing the window leaves the Mac running. An open window
  reconnects automatically after an exe restart or a temporary network
  interruption, returning to the same running Mac.
- **Shut down** from **Special → Shut Down** inside the Mac to save its
  files cleanly.

### The window

<img src="https://hub.v2core.com/v1/embed/bafkreiezokqwstse7romg3g2ybez6hv2soyoqdknotaahcm3y3j7vh5c24" alt="The Mac OS 9 window: a running Power Mac G4 with the Finder, its Control Strip, and the app's own buttons above it" width="814" height="687">

The app's own buttons are in a row above the Mac's screen. The status line
at the foot names the hardware, a PowerPC G4 with 512 MB of memory, and says
whether the display is connected.

| Button | What it does |
|---|---|
| **Start Mac** | Starts the Mac when it is shut down. During setup the same button reads **Continue setup** or **Resume installer**. |
| **Pause setup** | Stops a setup that is under way. |
| **Setup details** | Shows the setup steps and the download's progress. |
| **Mac keys…** | Sends a common Command-key shortcut to the Mac, or wakes its display. |
| **Full screen** | Gives the Mac more room. |
| **Sound off** | Enables the Mac's sound in your browser, where the node has audio support. |
| **CD…** | Shows the mounted CD and the disc images you can mount. |

### Keyboard and mouse

Click the Mac's screen to type in it.

The pointer follows your browser cursor directly, including when you leave
and re-enter the window. A bundled open-source USB tablet driver loads at
boot; no guest installation is needed. Mouse-wheel scrolling is not
supported by that driver; use the Mac's scrollbar controls.

Use **Mac keys…** for common Command-key shortcuts: ⌘ Q to quit, ⌘ W to
close, ⌘ O to open and ⌘ L to open a location.

### The display

The Mac's Monitors control panel offers only 640×480, 800×600 and 1024×768,
with 800×600 as the default.

The app window fits the selected guest resolution automatically. Each guest
pixel occupies a whole number of physical screen pixels (1×, 2×, 3×, and so
on), including at Windows' 125% or 150% display scaling. The largest crisp
size that fits is used; this can be smaller than a fractionally enlarged
display. There is no separate grow tile. If even 1× will not fit, the
display shrinks proportionally to keep the whole Mac visible.

Use **Full screen** for more room. Full screen uses the same scaling and
adjusts when you move between monitors or change browser zoom.

- When browser fullscreen is unavailable, including in iPad Home Screen
  apps, the Mac expands within exe; tap **Exit full screen** to return.
- Browser fullscreen also supports older iPad Safari; use the browser's exit
  control or Escape to return.

### When the display turns black

If the connected display turns black after being idle, choose
**Mac keys… → Wake display**, or press Shift with the Mac focused. This
wakes Energy Saver without typing or restarting the guest; mouse movement
alone may not wake it.

To keep the Mac awake, open **Apple menu → Control Panels → Energy Saver**
inside the guest and set system sleep to **Never**. Under **Show Details**,
also disable a separate display-sleep timer if one is enabled.

### CDs

Click **CD…** in the toolbar to see the full mounted CD filename, mount an
available disc image, or **Eject** it. The toolbar also shows the filename
when space permits. On a phone, tap the CD glyph to see its full filename
and controls.

To put a new disc in the drive:

1. Click **CD…**, then **Upload image…**, and choose an ISO, CDR, IMG or
   Toast raw disc image (up to 2 GiB) from your browser.
2. Select it under **Available images** and click **Mount**.

Images stay on this node and existing files are kept when filenames match.
Changes take effect without restarting the Mac, and the displayed filename
follows ejects inside Mac OS 9 too. After a Mac restart, mount the desired
CD again.

If the Mac locks the disc, eject it in Finder first, or close programs using
it before choosing **Force eject**. Force eject can leave the old volume
visible in Finder; restart the Mac if that happens.

### Sound

If this node has the optional Mac audio runtime installed, click
**Sound off** to enable sound in your browser; the button changes to
**Sound on**. Click it again to mute. Browser playback needs this first
click.

- Sound stops when the window is closed or hidden, and an enabled session
  resumes after reconnecting.
- The button is disabled on nodes without audio support: without the runtime
  the Mac has no sound.
- On narrow screens the button shows a speaker glyph.
- Sound travels through the existing authenticated display connection; it
  does not play through the server's speakers.

exe does not install that runtime by itself; its
[build and reuse guide](https://github.com/livid/exe/blob/main/docs/macos9-audio.md)
says how to.

### Clock and network

The Mac's clock starts at this machine's own local date and time, so a
browser in the guest judges today's certificates against today's date and
HTTPS works without a visit to Date & Time. Only the installation boot is
pinned to 2003, where the Universal installer and the era's software expect
to be.

The installed guest has a `sungem` Ethernet adapter with outbound NAT and
DHCP; networking is disabled while booting the installer. Classic HTTP
browsers work; modern HTTPS compatibility depends on the guest browser.

### Where it is kept

The runtime, installer, setup progress and disk live in `~/.exe/mac-os9/`
(or `$EXE_HOME/mac-os9/`), outside app-data sync: the Mac belongs to this
node and is not copied to joined ones.

The display uses private local sockets and the same API token as exe. No
public VNC port is opened.

The API is `GET /v1/macos9`, `POST /v1/macos9/start`, `/cancel` and
`/finish`, the binary WebSocket at `/v1/macos9/console`, and for the CD
drive `GET` and `POST /v1/macos9/cd` and `POST /v1/macos9/cd/upload`.

## Mac OS X

The **Mac OS X** app is a second Mac in a window: an emulated Power Mac G4
running Mac OS X 10.4 Tiger, with a 100 GB hard disk of its own. The Mac OS 9
app keeps the classic system. This one is the system that came after it:
Aqua and the Dock, Spotlight and Dashboard, a Unix underneath, and Xcode 2
on the disc it installs from.

### What it is for

- **Run PowerPC Mac OS X software.** Mount a CD or DVD image and install
  what is on it in the system it was written for.
- **Build for it.** The install DVD carries Xcode 2, and Tiger has a
  Terminal and, if you choose it at installation, X11.
- **A Mac that stays as you left it.** What you install and save is kept on
  its hard disk, on this node. Closing the window leaves the Mac running,
  and opening it again returns to the same Mac.
- **A Mac on the network.** Once installed it reaches the internet through
  this machine.

It is a PowerPC Mac, emulated in software by the same emulator as the
Mac OS 9 app's, and it needs no hypervisor. The two Macs are separate
machines and can run at the same time.

### What it needs

- **An emulator.** The same one as [Mac OS 9](https://exe.v2core.com/docs/using/mac-os-9)
  uses: the two apps share it, and whichever is set up first fetches it.
- **Room on this node.** The install DVD is a 2.6 GiB download. The hard
  disk says 100 GB to the Mac and takes only what the Mac has written: 3 to
  5 GB after installation.
- **Memory.** The Mac has 1 GB, taken from this machine while it runs.

### Set it up the first time

1. Choose **Special → Mac OS X**; it is also among the built-in apps. The
   first launch shows each setup step: preparing the emulator, downloading
   Apple's Tiger install DVD from the Internet Archive, checking its
   checksum, creating the hard disk, and starting the Mac from the DVD.
2. When the Mac's screen appears, follow the guide the app shows above it.
   Choose a language, then **Utilities → Disk Utility…** in the Mac's own
   menu bar. Select **100.0 GB QEMU HARDDISK**, open the **Erase** tab, name
   the disk, click **Erase** and confirm. Quit Disk Utility.
3. In the Installer click **Continue**, agree to the license, select the new
   disk, click **Continue**, then **Install**. **Customize** on that last
   page leaves out what you do not want (the printer drivers are a third of
   it) and adds X11.
4. Copying takes ten minutes to a quarter of an hour on a fast machine and
   longer on a slow one. At its end the Installer restarts the Mac, which
   closes the screen. Click **Installation finished — start my Mac**.
5. The Mac starts from its hard disk into Tiger's Setup Assistant, which
   asks for a country, a keyboard layout and the name and password of your
   account. ⌘ Q on its registration page, then **Skip**, goes straight to
   the account.

Subsequent launches use the saved installation: opening the app starts the
Mac from its disk. Pausing and resuming setup work as in the Mac OS 9 app.

### The window

<img src="https://hub.v2core.com/v1/embed/bafybeia4iud54npwjksgta42tyzdn3hlpi5todl2emrg3swiciigf6sb7q" alt="The Mac OS X window: Tiger's desktop with About This Mac open and the install DVD mounted, and the app's own buttons above it" width="1036" height="853">

The window is the Mac OS 9 app's, around another Mac: the same row of
buttons, the same status line, the same CD dialog and the same
**Full screen**. [Mac OS 9](https://exe.v2core.com/docs/using/mac-os-9)
describes them. What differs:

- **The display** is 1024×768. The window fits it to whole screen pixels,
  as it does for Mac OS 9.
- **Setup…** is a menu where Mac OS 9 has the Setup details button, since
  this Mac has more to set up: **Setup details** shows the steps and the
  download's progress, **Mount Workspace** the Workspace volume's address
  and password, and **Agent setup** the agent's way in.
- **Mac keys…** sends ⌘ Q to quit, ⌘ W to close, ⌘ Tab for the next
  application, ⌘ Space for Spotlight and ⌥ ⌘ Esc to force an application
  to quit: the keys your own computer keeps for itself.
- **The pointer** is where your browser's pointer is, as in Mac OS 9, from
  the Installer's first screen on. Mac OS X drives it itself, with nothing
  loaded into the Mac, and the mouse wheel scrolls. Tiger as installed puts
  the arrow on the very pixel. Once updated (10.4.11), its driver leaves the
  edges of a tablet's range unused; the window sees where the arrow lands,
  allows for it within a few moves of the pointer, and from then on the
  arrow is within a pixel of yours.
- **CD…** takes images up to 9 GiB, so a dual-layer DVD fits. The Mac
  starts with its drive empty: the install DVD stays in the list, to mount
  when you want Xcode from it.
- **Sound** needs the same optional audio runtime as Mac OS 9's, and the
  one installation serves both Macs. With it, **Sound off** turns Tiger's
  sound on in your browser.

### Restart, shut down and sleep

**Restart** in the Mac's Apple menu brings it back by itself. **Shut Down**
there saves its files and ends the Mac; **Start Mac** starts it again.

A fresh Tiger puts the Mac to sleep after ten idle minutes, and its screen
turns black. **Mac keys… → Wake display**, or Shift with the Mac focused,
wakes it. To keep it awake, set both sliders in **System Preferences →
Energy Saver** to **Never**, or run
`sudo pmset -a sleep 0 displaysleep 0 disksleep 0` in Terminal.

Turn Tiger's screen saver off as well, under **System Preferences →
Desktop & Screen Saver**: with no graphics card to draw it, it keeps a
whole processor core of this machine busy for as long as it shows.

### A shell and screen sharing from this machine

While the Mac runs, two of Tiger's Sharing services can be reached from
this machine, and from no other. They are a second way in, for you and for
an agent, that does not pass through the emulated keyboard and pointer:
commands as text, and the Mac's own screen sharing.

- **Remote Login** is a shell. Turn it on in the Mac's **System
  Preferences → Sharing**, then connect to `127.0.0.1` on the port that
  **Setup details** shows.
- **Apple Remote Desktop**, with **VNC viewers may control screen with
  password** set under its **Access Privileges…**, shows the Mac's screen
  to a VNC viewer at `127.0.0.1` on the other port.

Tiger's SSH server is from 2004. A current `ssh` refuses it ("no matching
key exchange method") until it is told to allow what that server speaks:

```
ssh -p <port> -l <account> -o KexAlgorithms=+diffie-hellman-group1-sha1 \
    -o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa \
    127.0.0.1
```

The two ports are in the daemon's status as well (`GET /v1/macosx`,
`ports`), and they stay the same from one start to the next while they are
free. Nothing answers on them until the service is on in the Mac. Every
program on this machine can reach them, so there it is the account's
password that guards the Mac.

### An agent on the Mac

Press **Agent** in the Mac OS X window and a Chat opens pinned to the Mac,
the way a VM window's Run Agent pins one to its VM: ask it for things in
words — "what is in /Applications?", "build Hello in Xcode and run it",
"put the picture I painted on the desktop" — and it works inside the Mac
over Remote Login while you watch the screen in the window beside it. Its
tools are the shell, reading, writing and editing files, and **upload**,
which copies a file of the Workspace into the Mac, a picture included. It
knows what Tiger's 2005 tools lack, keeps a memory of the Mac between
chats, and never restarts or shuts the Mac down unasked.

It needs a way in, once: choose **Agent setup** from the window's
**Setup…** menu, enter the Mac account to work as (the short name in
Tiger's Accounts pane), and paste the line shown there into Terminal on the
Mac, signed in as that account. The line
authorizes a key exe made for this Mac — an RSA key, since Tiger's SSH
server knows no newer kind — and Remote Login has to be on in the Mac's
Sharing pane. A chat with the Mac fails with the reason while any of this
is missing.

### The Workspace as a volume on the Mac

The Workspace of this exe mounts on the Mac's desktop as a volume named
**Workspace**: what you drop in the Workspace window is in the Mac's Finder, and
a file the Mac saves there, a game's save or a screenshot, stands in the
Workspace before you change windows. Both Macs exe runs see it, and so does
any Mac OS X computer on your network.

In the Mac's Finder choose **Go → Connect to Server…**, enter the address
that **Mount Workspace**, in the window's **Setup…** menu, shows, and click
**Connect**. The name is `workspace` and the password the one shown
there; the Finder can keep both in its keychain. The volume is the
Workspace folder itself over WebDAV, at `/Workspace/` on exe's own port, so
another computer reaches it at this machine's address: choose that
computer under **Connect from** and the address changes. **Copy** beside a
field puts it on the clipboard of the computer you are at. Mac OS X 10.4 mounts it with no software added; Mac OS 9
has no WebDAV of its own, so its Mac still takes a CD.

The password is the volume's alone, never the API token: a Mac that mounts
the volume can read and write the Workspace and nothing else. **New
Password** makes another and every Mac that mounted with the old one is
asked again. A file the Mac writes syncs to your other nodes like one the
desk saved, up to the Workspace's size limit; the Mac's own `.DS_Store` and
`._name` sidecars stay on this node and out of every listing.

### Clock and network

The Mac's clock starts at today's date and time, and Tiger shows it in the
time zone you choose in the Setup Assistant.

The installed Mac has an Ethernet adapter with outbound NAT and DHCP, over
IPv4 only; networking is off while the Installer runs. Tiger's Safari is
too old for most of today's HTTPS sites, though the network itself works:
plain HTTP does.

### Where it is kept

The installer, setup progress and disk live in `~/.exe/mac-osx/` (or
`$EXE_HOME/mac-osx/`), outside app-data sync: the Mac belongs to this node
and is not copied to joined ones. The emulator is the one in
`~/.exe/mac-os9/runtime`.

The API is the Mac OS 9 app's under another name: `GET /v1/macosx`,
`POST /v1/macosx/start`, `/cancel` and `/finish`, the binary WebSocket at
`/v1/macosx/console`, and for the drive `GET` and `POST /v1/macosx/cd` and
`POST /v1/macosx/cd/upload`.

## Virtual machines

A virtual machine (VM) is a persistent Linux computer of your own that runs
on this machine, for you and your agents to work in. It has its own disk,
its own private address and a user who may do anything inside it. VMs are
what exe exists for: the rest of the desktop is there to make them, work in
them and show what runs in them.

### What a VM is for

For you:

- **A machine to build in.** Install packages, write code, start servers.
  The user inside (`dev`) has passwordless `sudo`, and the VM is the sandbox
  boundary: you may install and run anything in it.
- **It stays as you left it.** Stopping a VM keeps its disk, and starting it
  boots the same machine again. Only deleting a VM destroys its disk.
- **You reach it from where you work.** A terminal in the browser, or `ssh`,
  `scp` and port forwards from your own terminal; see
  [SSH from your own terminal](https://exe.v2core.com/docs/using/ssh-from-your-own-terminal).
- **What runs in it can go public.** A port a server listens on inside the
  VM can get an HTTPS address on your own domain; see
  [Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web).
- **A new one is quick.** It is cloned from a base image and is usually
  ready in about ten seconds.

For a coding agent, such as Claude Code or Codex:

- **A sandbox of its own.** The agent makes a VM, or takes one you name, and
  builds, runs and tests there: an isolated machine where it may install
  anything.
- **Everything has a command.** VMs are made, started, stopped and deleted
  over HTTP or from a shell, and commands run inside one over plain SSH.
  **From a shell or over HTTP**, further down, lists them.
- **Notes to start from.** Every VM has notes you write, and agents are told
  to read them before working in a VM they do not know.
- **The built-in agent works here too.** A VM's **Agent** tab gives it a
  shell in that VM; see
  [The coding agent](https://exe.v2core.com/docs/using/the-coding-agent).

### Make a VM

Choose **File → New VM…**, or press **New VM…** in the Virtual Machines
window.

<img src="https://hub.v2core.com/v1/embed/bafkreihgifdv4knmxe7hrxsnv3tybqwv2cbfcf47zcmdmydnuxrtgnmzuu" alt="The New VM dialog: a name, CPUs, memory, disk and the System pop-up" width="332" height="246">

1. Type a **Name**: lowercase letters, digits and hyphens. Only the name is
   required.
2. Fill in **CPUs**, **Memory (MB)** and **Disk (GB)** if you want other
   sizes. Left empty they are 2 CPUs, 2048 MB of memory and a 20 GB disk, or
   the defaults you set on the **VMs** tab of **Windows → Configuration**.
3. Pick the **System**: **Debian 13**, the default, or **Alpine 3.24**.
4. Press **Create**. The VM is made and booted, and the dialog closes when
   it is ready to use.

The very first VM downloads the Debian base image (about 3 GB) once. Later
VMs are cloned from it and boot in seconds. A VM's disk takes only the room
its contents need on the host: 20 GB is how far it may grow, not space set
aside.

### Debian or Alpine

The **System** pop-up picks the VM's Linux.

- **Debian 13** is the default: `apt`, `sudo`, `bash` and systemd.
- **Alpine 3.24** is a lean guest for disposable experiments, a download of
  about 93 MB the first time. It runs OpenRC and `apk`, not systemd and
  `apt`. Its user's shell is `ash` and it has `doas` in place of `sudo`.
  Prebuilt glibc binaries do not run on its musl libc.

An Alpine VM can be made on a Linux host only: on macOS and on Windows exe
refuses that system.

exe records which system each VM was made with. Chat, a chat pinned to the
VM and the **Agent** tab are told it, so on Alpine they reach for `apk` and
`doas`. **Publish to GitHub…** installs `git` with `apt` and expects a
Debian VM.

### The Virtual Machines window

**Windows → Virtual Machines** lists every VM with its state, its specs and,
while it runs, its IP address. Click a column's heading to sort by it, and
again to turn the order round. Each VM also has an icon on the desktop.

<img src="https://hub.v2core.com/v1/embed/bafkreidvhuabfxo7lqmwu33nslajdbuexc42slkqjbazf7hzlcnmjjzseq" alt="The Virtual Machines window listing two running VMs with their specs and IP addresses" width="562" height="131">

- **Open a VM.** Double-click its row or its icon; on a phone, tap it.
- **Stop** shuts the VM down and keeps its disk. On a stopped VM the button
  is **Start**, which boots it again.
- **Delete** asks **Sure?**: press it again within three seconds. The VM,
  its disk and its notes are then gone for good.
- **Refresh** reads the list again, and **New VM…** opens the dialog.

Right-click a VM for its menu:

| Menu item | What it does |
|---|---|
| **Open** | Opens the VM's window. |
| **Open Terminal** | Opens the VM's window on its **Terminal** tab. |
| **btop** | Opens the VM's process monitor in an 80×24 terminal window. It runs the VM's installed `btop` over SSH; press **q** to quit and close the window. A failed launch leaves its error visible. Only for a running VM. |
| **Chat with this VM** | Opens the Chat window on this VM, once a chat backend is set up. |
| **Stop** or **Start** | The same as the row's button. |
| **Restart** | Stops a running VM and starts it again. |
| **Copy IP** | Puts the VM's IP address on the clipboard. |
| **Expose Port…** | Opens the VM's window on its **Expose** tab. |
| **Publish to GitHub…** | Sends a folder of a running VM to GitHub; see [Publishing to GitHub](https://exe.v2core.com/docs/using/publishing-to-github). |
| **Delete** | The same as the row's button. |

### A VM in its own window

Double-click a VM to open its window. The line at its top shows the VM's
state, then its IP, how much disk the VM really holds on the host (allocated
space, not the sparse file's nominal size) and the guest's system with its
version, read from the guest itself. At the right edge is the load average
over one, five and fifteen minutes, refreshed every five seconds while the
window is open.

<img src="https://hub.v2core.com/v1/embed/bafkreiajs7o257w7yffrgteuxigohvbvcy5vapumuy2yfvxb7awv4ujoiu" alt="A VM's window on its Services tab: the state, IP, disk and system, then the ports listening inside it and the addresses they are published at" width="682" height="379">

| Tab | What it is for |
|---|---|
| **Services** | The TCP ports listening inside the VM, each with a link that opens it, and the routes already published to the web. A server must bind `0.0.0.0`, not `127.0.0.1`, to show up here or to be exposed. |
| **Terminal** | A full SSH terminal in the browser. |
| **Agent** | Tell the built-in coding agent what to build. It gets a shell in this VM and streams its work live. |
| **Expose** | Publishes a VM port at an HTTPS subdomain; see [Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web). |
| **Sessions** | Every chat pinned to this VM, agent runs included, each with a one-line summary a model wrote of what it accomplished. Click one to reopen it in the Chat window. |
| **Notes** | Free-form notes about the VM, saved automatically. Agents are told to read them before working in an unfamiliar VM, so write down what runs where. Under them the tab shows what the built-in agent has remembered about the VM, with **Forget**, pressed twice, to erase it. |

### From a shell or over HTTP

Everything the windows do has a command. `exe` is typed on the machine that
runs the daemon. The HTTP calls go to the address the desktop is served at,
and are what an agent uses.

| To | Command | HTTP |
|---|---|---|
| List the VMs | `exe ls` | `GET /v1/vms` |
| Make a VM and boot it | `exe create demo` | `POST /v1/vms` |
| Start a stopped VM | `exe start demo` | `POST /v1/vms/demo/start` |
| Stop it, keeping its disk | `exe stop demo` | `POST /v1/vms/demo/stop` |
| Delete it and its disk | `exe rm demo` | `DELETE /v1/vms/demo` |
| Read its state and IP | `exe ip demo` prints the IP | `GET /v1/vms/demo` |
| Read or replace its notes | none | `GET` or `PUT /v1/vms/demo/notes` |
| Open a shell or run one command in it | `exe ssh demo` or `exe ssh demo uname -a` | none: use SSH |

- **Sizes and system.** `exe create` takes `-cpus`, `-mem` (MB), `-disk`
  (GB) and `-image alpine`. Over HTTP the body is JSON, and only `name` is
  required:
  `{"name":"demo","cpus":2,"memory_mb":2048,"disk_gb":20,"image":"alpine"}`.
- **Create and start wait.** Both answer only when the VM has booted, has an
  IP and answers SSH, so what they return is usable at once. Allow ten
  minutes or more when the first VM still has the base image to download.
- **The token.** When `api_token` is set, every HTTP call carries
  `Authorization: Bearer <token>`; see
  [Configuration](https://exe.v2core.com/docs/using/configuration).
- **Errors** are JSON, `{"error":"…"}`: `404` for a VM that does not exist,
  `409` for one that is not running, `503` on a machine that cannot run VMs.
- **Commands inside a VM** go over the daemon's SSH gate, which starts a
  stopped VM by itself: `ssh -p 2222 demo@this-host 'uname -a'`. See
  [SSH from your own terminal](https://exe.v2core.com/docs/using/ssh-from-your-own-terminal).

**Help → Agent Skill Guide…** is all of this written for an agent, served at
`/skill.md`. [How VMs Work](/docs/vms), on the website, says what a VM is
made of on Linux, macOS and Windows.

### When exe restarts

VMs run inside the exe daemon. When it stops or restarts, they shut down
with it, and the ones that were running start again when it comes back.

### A machine that cannot run VMs

A node without a hypervisor (a NAS, a container without `/dev/kvm`) runs the
desktop without VMs. The Virtual Machines window stays empty and says why:
**This node cannot run VMs**, followed by the reason.
**Apple menu → About This Computer** gives its VM memory as unavailable on
this node. Everything else works as usual.
[Running without VMs](/docs/getting-started#running-without-vms-a-nas-a-container),
on the website, has more.

## SSH from your own terminal

You do not need the desktop to work in a VM. The daemon speaks SSH, so your
own terminal, `scp` and your scripts reach a VM directly, and a coding agent
runs its commands in a VM the same way.

### Connect

The daemon's SSH gate listens on port **2222**, and the username picks where
you land:

- **A VM's name** lands you in that VM, as its user: a full SSH session. A
  stopped VM is started first, and `scp`, `sftp` and `-L`/`-R` forwarding
  all work.
- **exe** lands you in the lobby, which runs commands for the VMs themselves
  and has no shell.

```sh
ssh -p 2222 demo@this-host        # into the VM "demo" (auto-starts it)
scp -P 2222 app.py demo@this-host:~/          # scp, sftp, -L/-R all work
ssh -p 2222 -L 8000:localhost:8000 demo@this-host   # tunnel a VM port

ssh -p 2222 exe@this-host         # the lobby, at its prompt
ssh -p 2222 exe@this-host ls --json           # or one lobby command
```

The port is `ssh_listen` in **Windows → Configuration**; `off` there turns
the gate off.

### The lobby

The lobby's commands are `ls`, `new`, `start`, `stop`, `rm`, `ip`, `code`,
`expose`, `unexpose` and `routes`, and `help` lists them with their
arguments. `ls`, `new`, `start` and `routes` take `--json`, for scripts and
agents.

`code` runs the built-in agent in a VM, and `expose` publishes a VM port.
[The coding agent](https://exe.v2core.com/docs/using/the-coding-agent) and
[Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web)
say more.

### Keys that get in

- Any public key in the daemon user's `~/.ssh`.
- The service key in `~/.exe/ssh/`.
- Keys listed in `~/.exe/ssh/authorized_clients`, in authorized_keys format.
  Add your phone or laptop key there; edits apply immediately.

There is no first-come key adoption, so the gate is safe to leave on a LAN.

## The coding agent

exe has a coding agent of its own: say what you want built, and it builds it
inside one of your VMs. If you already work with Claude Code, Codex or
another agent, exe teaches that agent to use the VMs instead.

### What it is for

For you:

- **Build in a VM by asking.** Type what you want in a VM's **Agent** tab.
  The agent installs packages, writes code and starts services there, and
  you watch it work.
- **Run the whole cloud by talking.** The **Chat** window is a conversation
  that can see and drive every VM: create one, work inside it, publish its
  port.
- **Tasks that outlast the browser.** A reply runs in the daemon. Close the
  tab and it carries on; come back and rejoin it live.
- **Work kept inside the VM.** The agent is free to install and run what it
  needs there, and the VM is the sandbox boundary.
- **Your choice of model.** Ollama, local or cloud, or a ChatGPT
  subscription.

For a coding agent of your own, once it has read the Agent Skill Guide:

- **A sandbox when it needs one.** It creates a VM, or starts one that
  exists, and may install and run anything inside it.
- **A shell in the VM.** It runs commands, copies files in and tunnels ports
  over SSH.
- **A way to show its work.** It finds the ports listening in a VM and
  publishes one at an HTTPS address.
- **Notes to read and to leave.** Each VM carries notes, which the agent
  reads before working in a VM it does not know and can rewrite.
- **A line to you.** It posts a note to the Newsfeed, on this node and every
  joined one, when work is finished or something is wrong.
- **A helper.** It can pass a task on to the built-in agent.

### Choose a model

The built-in agent needs a model. Set one up in **Windows → Configuration**.

Ollama is the default, on the **Ollama** tab:

| Setting | What it sets |
| --- | --- |
| `ollama.base_url` | Where Ollama is. A local signed-in Ollama at `http://127.0.0.1:11434` can use cloud models like `glm-5.2:cloud` with no API key; `https://ollama.com` needs one. |
| `ollama.model` | The model. |
| `ollama.effort` | The thinking effort on models that support it, or `off` to disable thinking. |

The Chat window can run on a ChatGPT subscription instead of Ollama. On the
**OpenAI** tab:

1. Click **Sign in with ChatGPT…** and finish the sign-in in the browser. It
  is the OAuth flow the Codex CLI uses, with no API key.
2. Set `chat_provider` to `openai`.
3. Pick a model (`gpt-5.4`, `gpt-5.4-codex`, …) and, if you like, a
  reasoning effort.
4. Press **Save**.

The browser sign-in redirects to `localhost:1455`, and the daemon listens on
all interfaces there. When the daemon runs on another machine, swap
`localhost` for the daemon's host in that final URL, or paste the URL into
the tab's paste field.

Tokens live in `~/.exe/openai.json` and refresh themselves. While you are
signed in, the tab also shows the subscription's rate-limit usage (the
rolling 5-hour and weekly windows, with their reset times) and any credit
balance.

A VM's **Agent** tab follows the same choice, since what it starts is a
chat: with `chat_provider` set to `openai`, its runs are on the ChatGPT
subscription too. To change provider, model or effort without opening
Configuration, click the Chat window's status line.

### Run the agent on a VM

<img src="https://hub.v2core.com/v1/embed/bafkreiguvunbfzvv4srl3kc444sa4kyrsg4jgvrtnqk4vp7sgsp2urzgni" alt="A VM's Agent tab: a prompt for the agent and the Run Agent button" width="602" height="379">

1. Open the VM's window and click its **Agent** tab.
2. Type what you want built.
3. Press **Run Agent**.

**Run Agent** opens a new chat pinned to the VM, with your prompt as its
first message. The Chat window streams the run and takes what you say next.

The agent can install packages, write code and start services. It has
passwordless sudo (doas on Alpine) inside the VM, and the VM is the sandbox
boundary.

### The Chat window

The **Chat** icon and window appear once a chat backend is usable. Chat is a
conversation that can see and drive your whole VM cloud: it can create,
start, stop and delete VMs, run commands and edit files inside them, publish
a port and push a folder to GitHub.

- **Replies run in the daemon**, not in the browser. Closing the tab, or
  losing the network, never interrupts a long task.
- **To rejoin a reply**, reopen the chat and select the session, which is
  marked with a green dot while it is streaming.
- **Stop** actually cancels the run. The **Send** button reads **Stop**
  while a reply runs, and **Queue** once you have typed something: your
  message then joins the run in progress.
- **It asks first.** Before Chat deletes a VM or removes a published
  address, a dialog asks you to approve it.
- **A chat pinned to one VM**, which is what the Agent tab starts, works in
  that VM alone.

### From the command line

`exe code`, on the machine exe runs on, runs the agent in a running VM and
prints its work as it goes:

```sh
exe code demo "build a guestbook app on port 8000"
```

`exe code`, and the `POST /v1/vms/{name}/agent` call behind it, stay on
Ollama whatever `chat_provider` says. So does `code` in the SSH lobby.

### Hand exe to your own agent

Prefer your own agent? exe serves a `/skill.md` file that teaches Claude
Code, Codex or any other coding agent how to drive the API and the VMs.

1. Choose **Help → Agent Skill Guide…**. The window shows the guide's
  address and a prompt that tells an agent to read it.
2. Press **Copy Prompt**.
3. Paste the prompt into your agent.

When this browser holds an API token, the prompt carries it and the window
says so. Share that prompt with care.

The guide also gives the agent its limits. It is not to stop or delete a VM
it did not create unless you ask, and it publishes to GitHub or changes the
desktop menu only when you ask.

The SSH gate an agent works through is in
[SSH from your own terminal](https://exe.v2core.com/docs/using/ssh-from-your-own-terminal).
To run Claude Code or Codex on this machine, in a window that keeps its
sessions, see
[Claude Code, Codex and Terminal](https://exe.v2core.com/docs/using/claude-code-codex-and-terminal).

## Publishing to the web

Publishing gives something that runs on this machine a real address on the
web: `https://<name>.<your-domain>`, HTTPS on a domain of your own. A web
app in a VM or a service on the host becomes a link you can open from
anywhere and hand to someone else.

- **A link to share.** One click in a VM's window publishes a port, and the
  service is live at its address.
- **Nothing to open, nothing to renew.** Visitors come in through your
  Cloudflare Tunnel, so no port is forwarded on your router, and Cloudflare
  answers HTTPS for the name: there is no certificate to manage.
- **Undone as quickly.** Unpublishing removes the address. The app keeps
  running in its VM and can be published again.
- **An agent can finish the job.** A coding agent that built something in a
  VM publishes it with one HTTP call, one `exe expose` command or one line
  over SSH, and hands you the address. The built-in Chat agent has the same
  as a tool.

This chapter puts a running service on the web.
[Publishing to GitHub](https://exe.v2core.com/docs/using/publishing-to-github)
puts a project's code on GitHub, and
[Analytics](https://exe.v2core.com/docs/using/analytics) shows who came to
what you published.

### What publishing does

A published name is a *route*: a hostname, and where its requests are sent.
Publishing one does three things:

1. Creates the hostname's DNS record at Cloudflare, pointing at your tunnel.
2. Adds a rule to the tunnel, its *ingress*, that sends the hostname to
  exe's reverse proxy on this machine.
3. Adds the route to that proxy, which passes each request on to the VM's
  port.

Routes survive a daemon restart.

### Set up Cloudflare once

Publishing needs three things at Cloudflare:

- A domain there (a *zone*) whose DNS records exe may manage.
- A cloudflared tunnel, running on this machine or on another in your
  network. A remotely-managed tunnel (Zero Trust → Networks → Tunnels) works
  best: exe changes its rules through the API.
- An API token. Create it at dash.cloudflare.com → API Tokens → Create Token
  → Custom token, with two permissions: **Zone → DNS → Edit** and
  **Account → Cloudflare Tunnel → Edit**. Add **Zone → Analytics → Read**
  for the Analytics app.

Then choose **Special → Cloudflare Setup Wizard…**, or **Set Up…** on the
**Cloudflare** tab of **Windows → Configuration**, and give them to exe:

1. Paste the token. **Validate & Continue** checks each answer against
  Cloudflare before the next step opens.
2. Pick the account, the zone and the tunnel from the lists the wizard
  offers, or enter an ID by hand.
3. Type the base domain. Apps publish as `<name>.<this domain>`: use the
  zone's own name (`example.com`) or a namespace under it, such as
  `apps.example.com`.
4. On the last step, click **Save Configuration**. The settings apply at
  once, with no restart.

The wizard writes the `cloudflare.*` settings (`api_token`, `account_id`,
`zone_id`, `tunnel_id`, `domain` and `metrics_url`). The wizard alone sets
them, because they must belong together: the account, the zone and the
tunnel, and a token that may edit that zone. **Configuration → Cloudflare**
shows them, with the account, the zone and the tunnel by name, and whether
the last check passed. To change one, click **Change…** there. The wizard
opens with every answer filled in, and you change only the one you want.

Two things decide whether a published name finds its way back to this
machine:

- `advertise_host`, in **Windows → Configuration**, is this machine's
  address as the cloudflared host reaches it: a LAN or Tailscale IP. A new
  install fills in the Tailscale IP when the machine has one. The wizard's
  last step warns when it is empty; a publish then skips the tunnel rule and
  says so. When you change it, Save repoints the tunnel rule of every
  published name.
- A locally managed tunnel, one configured by a file on the cloudflared
  host, takes no rules from exe, and the wizard says so. Add one catch-all
  rule by hand that sends your names to exe's proxy, or recreate the tunnel
  in the Zero Trust dashboard. exe still manages the DNS records and the
  routes.

### Publish a VM port

1. Start your server in the VM, listening on `0.0.0.0`. One that listens on
  `127.0.0.1` only cannot be published.
2. Open the VM's window and click its **Expose** tab. Right-clicking the VM
  in the **Virtual Machines** window and choosing **Expose Port…** lands
  there too.
3. Type the **VM port** and, if you like, a **Subdomain**. Left blank, the
  subdomain is the VM's name.
4. Click **Expose**.

The tab prints the route, the tunnel rule and the address,
`https://<sub>.<your-domain>`, with a warning line for any step that failed.
After a failed Cloudflare call the wizard opens, so you can check the
settings.

The **Services** tab is a shortcut: it lists the ports listening in the VM,
and **Expose** beside one publishes it under the VM's name.

A name leads to one place. Publishing under a name that is already in use
moves it to the new port, so a VM's second port needs a subdomain of its
own.

### See what is published, and unpublish

- A VM's **Services** tab shows each published port of that VM with its
  address as a link, and an **Unexpose** button. Click it twice: it asks
  **Sure?** first.
- **Windows → My Apps** shows every published name as an icon, whatever it
  leads to. Double-click one to open the site; right-click it for **Open**,
  **Copy URL** and **Unpublish…**.
- `exe routes` prints the same list in a terminal.

Unpublishing removes the DNS record, the tunnel rule and the route. What was
published is not touched: an app keeps running in its VM and can be exposed
again.

### Publish from a terminal

```sh
exe expose demo -port 8000 -sub guestbook   # https://guestbook.example.com
exe routes                                  # every name and where it leads
exe unexpose guestbook.example.com          # remove one, by its full name
```

`-sub` is optional: without it the name is the VM's. The SSH lobby
([SSH from your own terminal](https://exe.v2core.com/docs/using/ssh-from-your-own-terminal))
has the same three commands, for a script or an agent on another machine:

```sh
ssh -p 2222 exe@this-host expose demo 8000 guestbook
ssh -p 2222 exe@this-host routes --json
ssh -p 2222 exe@this-host unexpose guestbook.example.com
```

### What an agent can call

An agent that drives exe over HTTP has the whole of publishing:

| To | Call |
| --- | --- |
| See what listens in a VM | `GET /v1/vms/{name}/ports` |
| Publish a VM port | `POST /v1/vms/{name}/expose` with `port` and, optionally, `subdomain` |
| Publish a redirect | `POST /v1/routes/redirect` with `host` and `target` |
| Publish a service that is not in a VM | `POST /v1/routes` with `host` and `backend` |
| List the routes | `GET /v1/routes` |
| Unpublish a name | `DELETE /v1/routes/{host}` |

A publish answers with `host`, `url` and `backend`. A `warnings` array in
the answer means it only partly applied. To publish a VM port, the VM must
be running and Cloudflare set up.

**Help → Agent Skill Guide…** opens the guide exe serves to agents at
`/skill.md`; it teaches all of these calls but `POST /v1/routes`. The
built-in Chat agent
([The coding agent](https://exe.v2core.com/docs/using/the-coding-agent)) has
tools for a VM's ports: it lists ports and routes and publishes on its own,
and unpublishes only after you approve a confirmation dialog.

### Redirect a hostname

A name can answer with a permanent redirect instead, with no VM behind it:

```sh
exe expose example.com -redirect https://exe.example.com
exe expose www.example.com -redirect https://exe.example.com
```

- The name is your configured domain, one of its full subdomain names, or a
  name in another zone your Cloudflare token can edit, such as a second
  domain on the same account. Its DNS record goes in that zone; the tunnel
  and the proxy are the same.
- The target is an HTTP(S) origin on another hostname, with no path, query,
  fragment or credentials.
- exe creates the same DNS record and tunnel rule, then answers with
  **308 Permanent Redirect**, preserving each request's path and query
  string: `https://example.com/docs/?from=home` becomes
  `https://exe.example.com/docs/?from=home`. The method and body survive
  when the client follows the redirect.
- `exe routes` lists these routes as `redirect:https://…`, and
  `exe unexpose <host>` removes one.

### Publish a service that is not in a VM

A hostname can also route to a service on this machine that is not a VM,
such as a daemon on the loopback address:

```sh
exe expose charts.example.com -backend http://127.0.0.1:7799
```

- The backend is an HTTP(S) origin with no path. Every request's own path
  and query reach it, with the hostname in `Host`, so one backend can serve
  several names.
- The name follows the redirect's rule: in your domain, or in another zone
  your token can edit.
- The same DNS record, tunnel rule and proxy route are created, and
  `exe unexpose <host>` removes them.

### Publish a directory of files

A hostname can be a folder on this machine, served by the daemon itself, so
a static site needs no server process of its own:

```sh
exe expose site.example.com -dir /www/site
```

- `/` is the folder's `index.html`, `/pictures/a.png` the file of that name;
  a folder with no `index.html` is not listed, and a file or folder whose
  name begins with a dot is never served.
- Downloads can resume: byte ranges are honoured. A disk image or archive
  (`.dmg`, `.img`, `.toast`, `.iso`, `.sit`, `.zip`, `.bin`, `.hqx`) is sent
  as a download rather than shown.
- A folder that holds a file named `.password` is served only to a request
  carrying HTTP Basic authentication whose password is that file's first
  line, under any user name; everything beneath the folder is behind it,
  and the browser asks once. Those answers are marked private and never
  stored, so the edge in front of the site cannot hand them on.
- `exe routes` lists it as `exe:dir:/www/site`; `exe unexpose <host>`
  removes it. The folder is read at each request, so a new file is live at
  once. Pages are served `no-cache`; other files may be kept for ten
  minutes.
- The API: `POST /v1/routes` with `{"host": "site.example.com", "dir":
  "/www/site"}`.

`-redirect`, `-backend` and `-dir` take a full hostname and stand alone:
none combines with `-port`, `-sub` or another of the three.

### Publish the exe homepage

The daemon carries a site of its own inside the binary: the exe homepage and
its documentation. `exe site` publishes it under your domain, with the same
DNS record and tunnel rule as a VM port:

```sh
exe site             # https://exe.example.com
exe site -sub home   # https://home.example.com
```

`exe routes` shows it as `exe:site`, and `exe unexpose <host>` removes it
like any other name.

### Point a name at IPFS

A route can also say where the same site lives on IPFS, through a DNSLink
record:

- `PUT /v1/routes/<host>/dnslink` with `{"path": "/ipfs/<cid>"}` writes it:
  the TXT record `_dnslink.<host>` holding `dnslink=/ipfs/<cid>`.
  `ipfs resolve /ipns/<host>` and IPFS gateways then find that content.
- `DELETE` on the same path removes the record, and so does
  `exe unexpose <host>`.
- Only a name exe has published can have one, and the path is `/ipfs/<cid>`
  or `/ipns/<name>`.

exe-planet, the site builder that runs beside exe, does this for an exposed
site that also publishes to IPFS: every publish writes its new CID there.

### Check the tunnel

- The Cloudflare module in the Control Strip, bottom-left, shows tunnel
  health at a glance.
  [The desktop](https://exe.v2core.com/docs/using/the-desktop) describes its
  lamp and its menu.
- **Special → Cloudflare Status…** shows the last check of the token, the
  DNS access and the tunnel, then the tunnel's replicas and connections, the
  live traffic of this machine's connector and the saved settings.
  **Re-check Now** checks again; **Start Over…** opens the wizard with empty
  fields.

Live local traffic is read from cloudflared's loopback metrics listener at
`http://127.0.0.1:20241`. If it uses another local port, set `metrics_url`
on the wizard's last step to the base address (without `/metrics`). exe verifies that the local connector belongs
to the selected tunnel.

A machine without a local cloudflared connector can still show the
tunnel-wide connection counts; its local counters stay unavailable. A
Cloudflare API failure labels cached connection counts as last known rather
than reporting them as live.

### Local services for the desk

This one is not publishing. It lets the desktop itself reach a service on
this machine, with the API token still on guard.

A daemon on this machine's loopback, such as `127.0.0.1:7799`, is out of a
browser's reach when the desk is opened over Tailscale Serve or from a
phone. Name it in `~/.exe/config.json` and the daemon relays to it:

```json
"services": { "planet": "http://127.0.0.1:7799" }
```

- `/v1/svc/planet/<path>` then reaches `http://127.0.0.1:7799/<path>` with
  the method, body and query unchanged.
- A name is lowercase letters, digits and hyphens; its address is an HTTP(S)
  origin with no path.
- The API token is checked here and never forwarded, whether it came in a
  header or as `?token=`; nor are the desk's cookies. The service's cookies
  and CORS headers stay behind.
- A change to `services` through `PUT /v1/config` takes effect at once.

## Publishing to GitHub

**Publish to GitHub…** turns a project folder inside a VM into a GitHub
repository in one step. exe commits what is not committed yet, creates the
repository on your account, private by default, and pushes. Publish the same
folder again later and the new commits go to the same repository.

- **For you**, what was built in a VM lands on GitHub without your setting
  up git, a remote or a key in the VM.
- **For the VM**, nothing is handed over: no GitHub credentials ever enter
  it. Your sign-in stays on this machine, out of reach of whatever runs in
  the VM, an agent included.
- **For an agent**, publishing is one tool or one HTTP call that the daemon
  carries out. A plain `git push` inside a VM always fails, because that is
  the point.

[Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web)
gives a running service an address; this chapter puts the code itself on
GitHub.

### Sign in to GitHub once

exe signs in with GitHub's device flow, through an OAuth app of your own:

1. At github.com, open Settings → Developer settings → OAuth Apps and create
  an app. Enable **Device Flow**. The flow needs no callback URL and no
  client secret.
2. Open **Windows → Configuration → GitHub**, put the app's client ID there
  (`github.client_id`) and Save.
3. Click **Sign in with GitHub…**. github.com/login/device opens in a new
  tab, and a code appears in the window.
4. Enter the code at GitHub. The window then says who you are signed in as.

Once the client ID is saved, the Publish dialog offers the same sign-in at
its top. **Sign Out**, clicked twice, removes the token from this machine.

### Publish a folder

1. In the **Virtual Machines** window, right-click a running VM and choose
  **Publish to GitHub…**.
2. Pick the **Project folder in the VM**. The dialog lists the folders in
  the VM's home.
3. Check the **Repository name**, which starts as the folder's name.
4. Leave **Private repository** ticked, or clear it for a public one.
5. Click **Publish**.

<img src="https://hub.v2core.com/v1/embed/bafkreiasgpixuvyoargp7mmhpljkz2rp3f6oet5du6dnjrwrwzhjgwswcy" alt="The Publish to GitHub dialog: the signed-in account, the project folder in the VM, the repository name and Private repository" width="422" height="235">

The dialog lists each step as it happens and ends with a link to the
repository. The Newsfeed notes the publish too, with a link to the commit.

### What Publish does

The daemon does the work, over SSH into the VM:

1. Installs git in the VM if it is missing. The install uses `apt`, so it
  expects a Debian VM.
2. Makes the folder a git repository if it is not one yet, on a branch named
  `main`.
3. Commits any uncommitted work. If git has no identity for the folder yet,
  exe sets one for that repository only: your GitHub account, under its
  noreply address.
4. Creates the repository on your account, or uses the one that already has
  that name. **Private repository** counts only when the repository is
  created.
5. Points the folder's `origin` at the repository and pushes the current
  branch.

It stops early for a folder that sits inside a larger git repository
(publish that repository's own folder instead) and for a detached HEAD
(check out a branch first).

Publishing again goes through the same steps, so it commits what changed and
pushes the new commits to the same repository.

### Let an agent publish

- **The Chat agent.** Tell it to "push to github" and it uses the daemon's
  `github_push` tool on the project folder.
- **Any other agent.** `POST /v1/vms/{name}/publish` with
  `{"path":"/home/dev/app","repo":"app","private":true}` does what the
  dialog does. It streams its steps and ends with the repository's address
  and the pushed commit. Say `"private":true`: left out, a new repository is
  public.

Either may leave the repository name out. exe then reuses the folder's
`origin` when it points at your account, so publishing again updates the
same repository, and otherwise takes the folder's name. If `origin` points
at another account, it stops and asks for a name.

You sign in first, as above: without a sign-in the publish fails and says
where to sign in. The Agent Skill Guide tells agents to publish only when
you ask, since the call can create a repository on your account.

### Why the VM never holds your token

The point of the design: **no GitHub credentials ever enter the VM.**

- The sign-in token lives only on this machine, in `~/.exe/github.json`.
- The push travels through a proxy that exists just for that one operation
  and answers only for that one repository.
- The VM's git talks to that proxy without ever holding a token, on disk or
  in memory. The daemon adds the token on its own side.

## Analytics

Once something is published, the **Analytics** app answers the next
question: is anyone reading it? It shows the traffic Cloudflare's edge saw
for the hostnames this node publishes: the same list as **My Apps**, VM
ports, local services, the homepage and redirects alike.

- **For you**: which name is busy, which pages are asked for, from which
  countries and with which browsers, and whether a crawler is hammering a
  site or a server is answering with errors.
- **Nothing to add to a site.** The numbers are Cloudflare's own count, read
  with the node's token. Nothing goes into your pages.
- **For an agent**: the same numbers are one HTTP call, so an agent that
  published a site can check that it is being reached and is not answering
  with errors.

[Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web)
is how a name gets here; this chapter is about who came.

### Open it

Double-click the **Analytics** icon on the desktop, or choose **Analytics…**
from the Cloudflare module's menu in the Control Strip.

The app reads Cloudflare's analytics with the node's own token, which needs
**Zone → Analytics → Read** on top of the wizard's two permissions. Without
it the window says so. The numbers are those of the zone chosen in the
Cloudflare setup: a name published in another zone is listed, but its
traffic is not counted here.

The window has, from the top: the span buttons and the host pop-up, six
tiles, a chart, and ranked lists. The status line names your domain and the
span, and says when the numbers were last updated.

<img src="https://hub.v2core.com/v1/embed/bafkreicygtsslkz6zbwpqbj6ygwlhbxbvhu7hsowdvvijhqt4mptfpnc4u" alt="The Analytics window over the last hour: requests, visits, data served, verified bots and server errors, the chart, and the hosts by requests" width="662" height="587">

### Choose a span and a host

- **Span**: click **1 Hour**, **24 Hours**, **7 Days** or **30 Days**.
- **Host**: pick one in the pop-up, or **All Hosts**. Clicking a host's row
  in **Hosts** picks it too; click it again for all of them.

The pop-up lists each host beside its icon, the one My Apps shows: the app's
touch icon or favicon, the homepage's for the names that redirect to it, and
a diamond for an app that declares none.

### What the tiles count

| Tile | Counts |
| --- | --- |
| **Requests** | Every request that reached Cloudflare's edge. |
| **Stopped** | The requests Cloudflare challenged or blocked itself. |
| **Visits** | Cloudflare's visits: a page view that came from another site or none. |
| **Data served** | How much data was sent in answer. |
| **Verified bots** | The share of requests from bots Cloudflare has verified. |
| **Server errors** | The answers with a 5xx status. |

Each tile also shows its change against the span before it. **30 Days** has
no span before: Cloudflare keeps these numbers 31 days.

### Stopped requests

**Stopped** is what Cloudflare's security answered itself: a challenge page
or a block, from a rule of your own in the zone's WAF, a managed rule or the
browser check.

Those requests never reached your host, but they are in **Requests** too,
since Cloudflare counts every request that reached its edge. A crawler that
keeps hammering a challenge shows as a Requests line with a Stopped line
close under it. **Hosts** can rank by Stopped, to see which name draws them.

### Read the chart

The chart draws requests, visits and stopped requests over the span. Point
at it to read one point. The last points, which Cloudflare is still
counting, are drawn pale.

| Span | A point is |
| --- | --- |
| **1 Hour** | a minute |
| **24 Hours** | 15 minutes |
| **7 Days** | an hour |
| **30 Days** | a day |

The chart follows the view picked in **Hosts**. **Requests** and **Visits**
draw requests, visits and stopped requests together. **Data**, **Errors**
and **Stopped** draw data served, server errors or stopped requests alone,
on a scale of their own, and the reading under the pointer says the same.

### The ranked lists

Under the chart are ranked lists for the chosen hosts. The buttons beside a
list's name pick its view.

| List | Ranks |
| --- | --- |
| **Hosts** | The published names, by **Requests**, **Visits**, **Data**, **Errors** or **Stopped** requests. |
| **Pages** | The most requested paths (**Top**), or those answered with an error, status 400 and up (**Errors**). Each is marked with its host when all hosts are shown. |
| **Countries** | Where the requests came from. |
| **Bots** | Verified bots, by Cloudflare's categories. |
| **Responses** | Answers by **Status**, by **Cache** status or by content type (**Types**). |
| **Clients** | Requests by browser, system or device: **Browsers**, **Systems**, **Devices**. |

A Host header that carries a port (scanners try Cloudflare's other ports)
counts with its host.

### Narrow to one kind of bot

Click a row in **Bots** to narrow everything to that category, keeping the
chosen host. The tiles, the chart, the host totals and the other lists then
count only, say, AI crawlers, and the status line says so.

The Bots list itself stays whole, so another category is one click away.
Click the chosen row again to lift the filter.

### How fresh the numbers are

- The window refreshes every minute while it is open, and the daemon keeps
  each answer a minute, so several windows cost one query.
- Switching is quick because answers are kept twice over. The window paints
  a view it has shown before from memory and then brings it up to date.
- The daemon, behind an All Hosts answer, fetches the view of each host that
  had traffic (the twelve busiest at most), so the first switch to a host
  needs no trip to Cloudflare.
- An answer up to 15 minutes old is shown at once while a fresh one is
  fetched; the status line says "Updating…".
- Busy spans are sampled by Cloudflare, and the status line says "sampled".

### The same numbers for an agent

`GET /v1/cloudflare/analytics?range=24h&host=…&bot=…` returns what the
window shows: the totals, those of the span before, the chart's points,
every host's totals and the ranked lists.

- `range` is `1h`, `24h`, `7d` or `30d`.
- `host` is one published name; leave it out for all of them.
- `bot` is one of Cloudflare's verified bot categories, such as
  `AI Crawler`; leave it out for all traffic.

The Agent Skill Guide (**Help → Agent Skill Guide…**) describes the answer
field by field.

## Workspace and files

The **Workspace** is one folder on this machine, `~/.exe/workspace`, that
you, your coding agents and your apps all read and write. It is where a
file changes hands: what one of you puts there, the others find. Its icon
on the desktop opens it in a Finder window.

### What the Workspace is for

For you:

- **Bring a file to this machine.** Drag it from your computer onto the
  desktop, and it is in the Workspace.
- **Hand a file to an agent.** Drop a screenshot, a log or a document on
  a Claude Code or Codex window. It lands in the Workspace, and its path
  is typed into the window for the agent to read.
- **Look at what an agent made.** A text file, a picture, a web page, a
  movie or a sound opens in a window of its own on the desktop.
- **Take a file away.** **Download** saves a Workspace file to the
  computer your browser runs on.
- **Keep the same files on every desk.** Joined desks sync their
  Workspace files; see
  [Joining desks together](https://exe.v2core.com/docs/using/joining-desks-together).

For a coding agent, such as Claude Code or Codex:

- **A plain folder.** An agent that works on this machine reads and
  writes `~/.exe/workspace` as it does any folder. What it leaves there,
  you open from the Workspace window.
- **Where its sessions begin.** The desktop starts Claude Code and Codex
  in this folder. The one exception is a machine that holds exe's own
  source at `/www/exe`: there they start in that folder.
- **The files you give it.** A file dropped on its window arrives here,
  and the path typed into the window is the file's path on this machine.
- **Somewhere to show its work.** A web page, a picture or a movie it
  saves here opens on your desktop with a double-click.

Apps use the folder too: Writer edits any Markdown file in it, and Easel
saves its pictures there. See
[Apps](https://exe.v2core.com/docs/using/apps).

### Open the Workspace

Double-click the **Workspace** icon on the desktop; on a phone, tap it.
The window shows the folder's contents as icons. The line under the title
bar says how many items there are and how much disk space is free.

A file's icon tells its kind: a picture, a movie, a web page, plain text,
an archive, a CD image, a checksum. Any other file is a blank page.

<img src="https://hub.v2core.com/v1/embed/bafkreigcssoqlxnaybr7d7yjusstyejrfd4utagcwqzonbjzhadtwcbs4m" alt="The Workspace window with folders, a picture, a movie, a sound and text files" width="522" height="266">

Each folder opens in a window of its own, as in the classic Finder. Names
that begin with a dot are not shown.

A line of your desktop menu can open the Workspace or one of its folders;
see
[Desktop context menu](https://exe.v2core.com/docs/using/desktop-context-menu).

### See a folder as icons or as a list

A folder of many files reads better as a list: an item to a line, with
the date it was modified, its size and its kind. Right-click a window's
empty space and choose **View → as List**. **View → as Icons** goes back,
and the check mark is on the view you are in.

<img src="https://hub.v2core.com/v1/embed/bafkreigbxdhdqmuhokq2j6xvenamykane43rr73uycxyxnheixgry4g4p4" alt="A Workspace window as a list: items with their dates, sizes and kinds, one folder opened by its triangle with its two items listed under it" width="562" height="298">

- Click the triangle before a folder to list what is in it under it,
  a step in; click it again to put that away. A folder inside can be
  opened the same way. With a folder selected, the Right and Left arrow
  keys do the same.
- Click **Date Modified**, **Size** or **Kind** at the top of the list to
  sort by it: newest first, largest first, kinds from A. **Name** sorts by
  name again.
- In a list an item is its icon and its name: click, double-click and
  right-click those. The rest of its line is the window's, so a
  right-click there gives the window's menu.
- Each folder remembers its own view, its sort and which folders were
  left open, on every desktop of this exe and on every node it is synced
  with.

### What a double-click opens

| The item | What opens |
|---|---|
| A folder | A window of its own. |
| A text file under 1 MB: plain text, Markdown, JSON, source code, logs, `.svg` | The text editor. |
| A picture: `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.bmp`, `.ico`, `.avif` | A picture window. |
| A web page: `.html`, `.htm` | The page, rendered in a window of its own. |
| A movie: `.mp4`, `.m4v`, `.mov`, `.webm` | A QuickTime-style player window. |
| A sound: `.m4a`, `.mp3`, `.wav`, `.aac`, `.ogg`, `.oga`, `.flac` | The same player, without a picture. |
| Anything else | A new browser tab. |

The text editor edits the file in place and saves as you type.

A web page runs sandboxed, apart from the desktop: its scripts run, and
it cannot reach the desktop's API or its token. The right-click menu's
**Open in New Window** shows it in a browser window instead, and
**Edit Source** opens its text in the editor.

In the player you can play, scrub, step a frame at a time, and click the
speaker to mute. The movie streams from the daemon, so a long clip starts
at once and scrubbing seeks instead of downloading.

### Select with the keyboard

Type a name's first letters to select it, as in the classic Finder: `a`
jumps to the first name starting with "a", and letters typed quickly
spell more of it (`ar` reaches a folder named Artifacts).

The arrow keys move the selection an icon left or right, or a row up or
down. In a list they move it a line up or down, and Right and Left open
and shut a folder. The same keys work in My Apps and the Icon Editor
gallery.

### Right-click a file or a folder

| Item | What it does |
|---|---|
| **Open** | The same as a double-click. |
| **Open in New Window** | Shows a web page in a browser window. Web pages only. |
| **Edit Source** | Opens a web page's text in the editor. Web pages under 1 MB only. |
| **Move To Trash** | Moves the item into the Workspace's hidden `.Trash` folder. |
| **Get Info** | Shows the item's kind, size, place and date modified, with a **Download** button. |
| **Duplicate** | Makes a copy beside the file. Files only. |
| **Download** | Saves the file to the computer your browser runs on. Files only. |

An item moved to the Trash is not deleted. It sits in
`~/.exe/workspace/.Trash`, under its name with a number in front, and the
desktop's **Trash** window does not list it. To restore it, move it out
of that folder.

Right-click a window's empty space for **New Folder…**,
**New Text File…**, **Upload…** and **View**. A new name cannot begin with
a dot or hold a slash.

### Bring files in, take files out

Drag files from your computer onto any of these:

- The desktop: they land in the Workspace root.
- A Workspace window: they land in that window's folder.
- A folder icon in a Workspace window: they land in that folder.
- A Claude Code, Codex or Terminal window: they land in the root, and
  each path is typed into the window. See
  [Claude Code, Codex and Terminal](https://exe.v2core.com/docs/using/claude-code-codex-and-terminal).

Two menu items bring files in from this browser as well.
**File → Upload to Workspace…** puts one file in the root, and
**Upload…**, in the menu of a window's empty space, puts files in that
window's folder.

A file can be 10 MB at most, and a file with the name of one already
there replaces it. Folders cannot be dragged in; drag their files.

New files brought in this way are announced on the Newsfeed, so every desk
in the mesh sees them arrive; overwriting an existing file stays quiet.

To take a file out, right-click it and choose **Download**.

### Files an agent writes

A Workspace window lists its folder when it opens, and again when a file
changes through the desktop, an app or a joined desk. A file an agent
wrote straight into the folder shows the next time the folder is opened:
double-click the folder's icon again.

Web pages are worth a folder of their own. The **Artifacts** folder is
where agents publish the pages they make: a copy of each claude.ai
artifact Claude publishes, for one. exe does not make that folder or fill
it by itself, so tell your agent to keep its pages there.

Between joined desks a file travels if it is 10 MB or less. A bigger one
that an agent put in the folder stays on this machine, and so does
everything whose name begins with a dot, the Trash included. An empty
folder does not travel until it holds a file.

### The Workspace over HTTP

The desktop and the apps reach the Workspace through the daemon's API. A
script, or an agent on another machine, can use the same calls. The Agent
Skill Guide tells an agent to leave them alone unless you ask: an agent on
this machine works in the folder itself.

| Call | What it does |
|---|---|
| `GET /v1/workspace` | Lists every file with its path, size and date modified. |
| `GET /v1/workspace?dir=<folder>` | Lists one folder: its files and its folders. An empty `dir` is the root. |
| `GET /v1/workspace/<path>` | Returns the file. |
| `PUT /v1/workspace/<path>` | Stores the request body as that file, 10 MB at most, and makes its folders as needed. |
| `POST /v1/workspace/<path>` with `{"to": "<new path>"}` | Moves or renames a file or a folder. |
| `POST /v1/workspace/<path>?mkdir=1` | Makes a folder. |
| `DELETE /v1/workspace/<path>` | Deletes the file. |

Paths are relative to the Workspace, and one that leaves it is refused.
When `api_token` is set, each call needs the
`Authorization: Bearer <token>` header; see
[Configuration](https://exe.v2core.com/docs/using/configuration).

### Mount the Workspace on your own computer

The Workspace mounts on your own computer as a drive named **Workspace**:
what you drop into it is on the desk at once, and what the desk or an
agent saves there is on your computer. **Help → Mount Workspace…** shows
the address, the name and the password with **Copy** beside each, and
the steps for macOS, Windows and Linux. **Connect from** picks the address:
the desk's own https address when you reached the desk that way, or this
machine's address on the network.

On a Mac, choose **Go → Connect to Server…** in the Finder, enter the
address, click **Connect** and sign in as `workspace` with the password.
On Windows, choose **Map network drive** under This PC in File Explorer,
enter the address as the folder, tick **Connect using different
credentials** and sign in the same way. Windows takes a password over
plain http only after a change in its registry; the window shows that
line ready to copy, with the one that lets files over 50 MB through. An
https address needs the first of them not. On Linux, GNOME Files takes the
address as `dav://` under **Other Locations → Connect to Server**, Dolphin
as `webdav://`, and a shell mounts it with davfs2; the window spells each
out for the address chosen.

The drive is the same volume [a Mac exe runs mounts](#the-workspace-as-a-volume-on-the-mac),
the Workspace folder over WebDAV at `/Workspace/` on exe's port, behind
the volume's own password.

## Apps

An app is a small program that runs in a window of the desktop and has an
icon of its own there. Some apps are built into exe, a set of extra ones
comes with the installer, and any other is a folder holding two files.
That makes an app a good thing to ask a coding agent to build for you.

### What apps are for

For you:

- **Tools on the desktop.** Notes, a to-do list, tide charts, a
  proofreader, a dictionary: each opens in a window beside your VMs and
  terminals, and fills the screen on a phone.
- **Stickies.** A note can stand on the desk by itself, as Mac OS 9's
  Stickies did, and looks as one of its notes did: press the pushpin at
  a note's foot in Notes, and it becomes a small window in the note's
  colour that stays where you put it, on every desk that syncs. Drag it
  by its thin title bar, size it by the box on its corner or by its
  edges, press the box at the bar's right end or double-click the bar to
  collapse it, and press the box at its left end to put the note back in
  Notes alone. Each is a page of Notes, so what you type there is in
  Notes too.
- **Data that stays yours.** Each app keeps its data in private storage
  on this machine, and joined desks sync it.
- **Windows that keep up.** When an app's files change, its open windows
  reload by themselves once you are not using them.
- **An app made to order.** When no app does what you want, ask an agent
  for one. It is two files in a folder, and nothing has to be built or
  restarted.
- **Whole programs on the desk.** A program that runs beside exe can
  bring apps of its own: Planet and Writer publish a site, and Easel
  sets Claude painters to work.

For a coding agent, such as Claude Code or Codex:

- **A whole app is a small job.** An app is a folder with an `app.json`
  and an `index.html`: plain HTML, CSS and JavaScript.
- **No build and no restart.** The daemon serves the folder straight
  from disk, and each edit reaches the open windows by itself.
- **Storage is provided.** An app saves its own data through
  `/v1/apps/<Name>/data/<file>`, and files you should see go to the
  Workspace.
- **The conventions are written down.** The
  [exe-apps](https://github.com/livid/exe-apps) repository holds the
  extra apps and, in its `README.md` and `CLAUDE.md`, how an app is laid
  out, how it talks to the desktop and how it saves.

### Open an app

Double-click an app's icon on the desktop; on a phone, tap it. The
factory desktop menu lists the same apps under **Apps**, and a line of
your own there can open one; see
[Desktop context menu](https://exe.v2core.com/docs/using/desktop-context-menu).

### The built-in apps

These are part of exe itself, and each has a chapter of its own.

| App | ID | What it is | Chapter |
|---|---|---|---|
| **Mac OS 9** | `macos9` | A Power Mac G4 running Mac OS 9.2.2. | [Mac OS 9](https://exe.v2core.com/docs/using/mac-os-9) |
| **Mac OS X** | `macosx` | A Power Mac G4 running Mac OS X 10.4 Tiger. | [Mac OS X](https://exe.v2core.com/docs/using/mac-os-x) |
| **Hub** | `hub` | A small public feed shared between exe nodes. | [The Hub](https://exe.v2core.com/docs/using/the-hub) |
| **Blue Pencil** | `bluepencil` | A proofreader that runs on your own model. | [Blue Pencil](https://exe.v2core.com/docs/using/blue-pencil) |
| **Dict** | `dict` | A dictionary from English and five other languages into Chinese, Japanese or Korean. | [Dict](https://exe.v2core.com/docs/using/dict) |
| **Analytics** | `analytics` | The traffic Cloudflare's edge saw for the hostnames this node publishes. | [Analytics](https://exe.v2core.com/docs/using/analytics) |
| **Welcome** | `welcome` | exe in six cards. It has no desktop icon: choose **Help → Welcome…**. | [The desktop](https://exe.v2core.com/docs/using/the-desktop) |
| **Extras** | `extras` | Turns the extra apps, Planet, Easel and Projects on and off. It has no desktop icon: press **Extras…** on Welcome's Apps card. | This chapter, under **Extras** |

Built-in app IDs use lowercase names; their display titles come from
`app.json`. Three of them had other IDs once (`Mac OS 9`, `Hub`,
`BluePencil`). Older links and saved settings still work, and existing
app data keeps its original storage and sync namespace.

### The extra apps

The installer offers a set of extra apps: Notes, Paint, Tides, Todo,
Weather and World Clock, from
[exe-apps](https://github.com/livid/exe-apps). Say yes and they go into
`~/.exe/apps`. Say no and **Extras** brings them later.

An update brings their new versions, except for an app you edited or
removed; see [Updating](https://exe.v2core.com/docs/using/updating).

### Extras

Four things add to exe and are not part of it: the extra apps, Planet,
Easel and Projects. **Extras** turns each on and off. Open it with
**Extras…** on the Apps card of **Help → Welcome…**.

It has a step for each extra and a last one that makes the changes:

1. **Extra Apps**, **Planet**, **Easel**, **Projects.** Each page says
   what the extra is and how it stands on this machine, and has one check
   box. Tick it to have the extra on this desktop, clear it to take it
   off.
2. **Review.** What will change, extra by extra. Nothing has changed
   until you press **Apply** here. The desktop's icons follow by
   themselves.

What turning an extra on does depends on where it comes from:

| The extra | On | Off |
|---|---|---|
| The extra apps, on a released exe | Fetches them from the release that is running and places them in `~/.exe/apps`. An update keeps them current. | Removes the ones exe placed. An app you edited stays, and is yours. |
| The extra apps, from a checkout of exe-apps | Names the folder in `apps_dirs`. | Takes the folder out of `apps_dirs`. |
| Planet, Easel and Projects | Names the program's `apps` folder in `apps_dirs` and its daemon in `services`. | Takes both out again. |

Turning Planet, Easel or Projects off changes only exe's configuration.
The program's files stay, and its daemon keeps running until you stop it.

Planet, Easel and Projects are programs of their own, built from their
source. While one is not on this machine its check box is grey, and the
page gives a line to hand a coding agent, such as the one in the
**Claude Code** window, which installs it. If the program is here and
exe did not find it, type its folder and press **Find**.

An agent turns an extra on with `POST /v1/extras/<name>` and
`{"on": true}`, where the name is `apps`, `planet`, `easel` or
`projects`; `GET /v1/extras` says how each stands.

### Where an app lives

Icons beyond the built-ins are desktop apps: folders in `~/.exe/apps`,
each just an `app.json` plus an `index.html`, served straight from disk.
The folder's name is the app's ID.

`apps_dirs` in `~/.exe/config.json` names more folders to look in. It is
on the Daemon tab of **Configuration**; see
[Configuration](https://exe.v2core.com/docs/using/configuration). That is
how a program beside exe adds its apps.

When two apps have the same name, the one in `~/.exe/apps` wins over one
in `apps_dirs`, and either wins over a built-in app.

### Have an agent build an app

1. Tell an agent that works on this machine what the app should do. The
   **Claude Code** and **Codex** windows are such agents.
2. Have it make a folder for the app in `~/.exe/apps`, with an `app.json`
   and an `index.html` in it.
3. Choose **File → Refresh**. The new app's icon appears on the desktop.
4. Open the app. From then on, each change the agent makes shows in the
   open window by itself.

The extra apps in the same folder are worked examples, and exe-apps holds
the conventions.

An `app.json` looks like this:

```json
{
  "title": "Tides",
  "icon": "icon.svg",
  "window": { "width": 620, "height": 420, "grow": true }
}
```

`title` is the name you see, and without one the folder's name is used.
`icon` is a picture in the folder. `window` is the size the window opens
at, with `grow` true when the app draws a resize corner of its own.

An app worth reading while the daemon cannot be reached says so with
`offline`. A device that has had the desktop open over HTTPS then keeps a
copy, and the alert shown when the daemon is not answering offers the app.
`"offline": true` keeps the app's own files. To keep what it shows as
well, name it:

```json
"offline": {
  "keep": ["/v1/apps/Reader/data/list.json"],
  "fixed": ["/v1/svc/reader/files/"],
  "wanted": "/v1/svc/reader/wanted"
}
```

- `keep` lists whole addresses. Each is asked of the daemon as always and
  its last answer is kept. When the daemon does not answer, the app gets
  the kept answer, with the time it was kept in the `X-Exe-Kept` header,
  in milliseconds. That header is how the app knows to say it is showing
  a copy and to stop offering changes.
- `fixed` lists the starts of addresses whose content never changes, such
  as files named by their hash. One is kept the first time it is asked
  for, and answered from the copy after that.
- `wanted` is an address that answers a JSON list of `fixed` addresses,
  written relative to itself, to fetch ahead of time.

An address has to be the app's own folder or data, a service's
(`/v1/svc/…`) or a shared `/platinum/` file; any other is left out.

### Where an app keeps its data

Each app gets private storage under `~/.exe/appdata`, in a folder of its
own, plus the shared Workspace. The private storage is for the app's own
state, at most 10 MB a file. The Workspace is for files you want to see
and use elsewhere; see
[Workspace and files](https://exe.v2core.com/docs/using/workspace-and-files).

Joined desks sync app data; see
[Joining desks together](https://exe.v2core.com/docs/using/joining-desks-together).
The lists that Notes, Todo, World Clock and Weather keep, and Blue
Pencil's drafts, merge record by record, so edits made on two desks at
once both survive. Any other file syncs whole.

### An open window keeps up with its app

When the app's files change, on this desk or while a tablet slept, each
open window of it reloads once you are not using it:

- At once in a tab you are not looking at.
- After a few quiet seconds for a closed window, which is hidden and
  still loaded.
- After half a minute without a key or a click for a window on screen.
- Never while it holds words you have typed and not saved.

The window comes back on the file it last had open.

Saves are guarded too: a window still on older code cannot take back a
note, a to-do or a field that newer ones wrote.

### Writer and Planet

Two apps come with [exe-planet](https://github.com/livid/exe-planet), the
site builder that runs beside exe. Its `apps` folder goes in `apps_dirs`,
and its daemon is the service `planet`: **Extras** sets both.

**Writer** edits any Markdown file in the Workspace: the text on the
left, the page it makes on the right, saved as you type. An edit made
elsewhere reloads a clean file and leaves an unsaved one alone.

**Planet** is three columns like the Mac app:

- Your sites.
- The chosen site's posts, pages and drafts, as a list view.
- The chosen article's built page.

**New Post** and **Edit** open the article in Writer; right-click a site
or an article for the rest.

<img src="https://hub.v2core.com/v1/embed/bafkreiawlo2odksnfrqdqi4txaanzhb4b2cq6enapldb6a3e3uusvlj4ny" alt="The Planet window: sites on the left, the chosen site's posts in the middle, and the built page on the right" width="762" height="467">

A to-do list in a post (`- [ ] words`) can be ticked in that page: click
an item's box and its line in the Markdown flips. This works on a site
that wears Paper, Platinum or Sepia.

A post can be read in more than one language. Beside its `index.md`,
write the post in another language as `index.ja.md` (or `index.en.md`,
`index.zh-hans.md`): a `title:` line in its front matter, then the text.
Each language becomes a page of its own. A reader whose browser is in
that language is taken to it, and `?lang=ja` on any address of the site
asks for it by name. On a site that wears Paper or Platinum a post shows
the languages it can be read in beside its date, and the home page, the
archive and a tag list each post under the reader's title. In the Planet
window, click a language in the page to see that page; **Edit** then
opens that language's file.

### Publish a Planet site

**Publish…** is where a site leaves this node. Everything on the sheet is
off until you turn it on.

- **On the web.** Expose the site under a name in your domain. It gets
  the same route, DNS record and tunnel rule a VM's Expose makes; see
  [Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web).
- **On IPFS.** Send every changed build to IPFS under a key this node
  keeps.
- **Both.** Each new CID then also goes to the name's DNSLink record
  (`_dnslink.<name>`), so IPFS gateways find the site by its name too.

**Export…** bundles the site with its key for another node.

### Announce posts on a hub

The same sheet can put an exposed site on an exe hub; see
[The Hub](https://exe.v2core.com/docs/using/the-hub).

Turn on **Announce** and give the hub's address. This node invites the
site's own key there; the site's key is also its IPNS name and a Solana
address.

From then on each post you publish, which is a draft moved into Posts,
announces itself on the hub under the site's name, with a link back.

Under the post on your site, a Reply window and a Replies window show the
answers it gets, live. A reader replies with a Solana wallet that meets
the hub's gate, signing a message and never a transaction.

Posts published before you turned Announce on stay put. Right-click one
and choose **Announce on Hub** to send it.

### Easel

Easel comes with [exe-easel](https://github.com/livid/exe-easel), which
runs Claude painters at an oil paint simulator beside exe. Its `apps`
folder goes in `apps_dirs`, and its daemon is the service `easel`:
**Extras** sets both.

A studio is a folder holding an easel, a box of tubes and a brief.
**New Studio…** makes one, and **Start Painter…** sets a Claude painter
to work there. The painter is Claude Code with Opus 5.5 unless you pick
another model, and it is given nothing but the easel.

The painter mixes its paint from the tubes and lays it on with simulated
brushes, a chunk of Lua at a time. It looks at the canvas between chunks
and keeps a journal, and it stops when it is happy with the picture.

<img src="https://hub.v2core.com/v1/embed/bafybeidqcyzdmsmky5tftee7ddeyinaqdcevgaim74prfjma2cpxvl22na" alt="The Easel window: its studios down the left, and on the Canvas tab a finished painting of an open fridge lighting a dark kitchen" width="762" height="497">

The window follows the painter live, a tab for each view:

| Tab | What it shows |
|---|---|
| **Canvas** | The painter's latest look at the canvas. |
| **Session** | Its words, chunks and looks as they come. |
| **Journal** | Its notes. |
| **Code** | Its log. The log is the painting: replaying it paints the same canvas. |
| **Easel** | An easel to paint at by hand, from the same Lua, while no painter is at work. |

When a painter is done, the picture finishes itself a couple of minutes
later: it dries right through, is varnished and lets its paint crack. A
**Replay** movie of it being painted again follows. Both are made again
whenever the painting changes.

**Finish…** and **Replay…** are there only to redo them another way: no
varnish, a longer movie. **Save to Workspace** puts the picture and the
movie in the Workspace's `Easel` folder.

### Projects

Projects is a list of what is to be done in each of your projects, kept
where you and your coding agents can both read and write it. A bug you
spot, an idea, a loose end an agent mentions at the end of a task: each
becomes an issue, with an address such as `EXE-12` that names it for
good, so "fix EXE-12" is a whole instruction. You can hand an issue to
Claude Code or Codex from its menu, see on the issue while an agent is at
work on it, and read the agent's plan and report under it afterwards.
**Projects and coding agents**, below, says how.

Projects comes with
[exe-projects](https://github.com/livid/exe-projects), an issue tracker
that runs beside exe. Its `apps` folder goes in `apps_dirs`, and its
daemon is the service `projects`: **Extras** sets both.

The window has three columns: your projects on the left, a project's
issues in the middle and the chosen issue on the right. A project has a
short name in capitals, such as `EXE`, and its issues are numbered from
one: `EXE-12` names the same issue for good.

Right-click an issue for what can be done to it: close or reopen it,
edit it, **Star** it, copy its address, or send it to a coding agent. A
starred issue wears a yellow star where the list has its dot, and on the
issue's own line, and **Unstar** takes it off. The pop-up at the top
right lists the open, the closed, all or the starred issues.

A file attached to an issue is kept in IPFS, which takes Kubo on this
machine; without it everything else works. Anyone who learns such a
file's address can fetch it, so attach nothing private.

Click a picture under an issue to see it in a picture window. When the
issue has more than one, **Left** and **Right** show the one before and
the one after, or a sideways swipe on a phone, and the window's top line
says which it is, `2 of 5`.

### Projects and coding agents

An issue holds what an agent needs to start: what is wanted, the
discussion so far and any screenshots. An agent can also write back, so
its plan, its report and the commit that did the work end up on the
issue, where you will look for them, not in a conversation that has
scrolled away.

<img src="https://hub.v2core.com/v1/embed/bafybeia33wdg34xzfu3asnfiyyqhtfxlnmyroi4rdbbeufw4g4fbgxgrue" alt="The Projects window with an issue's menu open: Close Issue, Edit, Star and Copy Address, then Send to Claude Code and Send to Codex" width="1040" height="685">

The work goes round like this:

1. **Write it down.** File an issue. If it is about something on screen,
   attach a screenshot.
2. **Hand it over.** Right-click the issue and choose
   **Send to Claude Code** or **Send to Codex**. The agent's window opens,
   or comes forward, with a new session in the project's working
   directory. The session's first message is the whole issue with its
   comments, and a note that asks the agent to read the project's
   instructions, do the work, check it and report.
3. **Watch it work.** While the agent's turn runs, a line under the issue's
   title says so: a green dot, the agent, how long it has been at work, and
   the session's name. The line goes when the turn ends.
4. **Read what came back.** An agent that has been told about the tracker
   comments its plan as a checklist, ticks it as it goes, reports, and
   closes the issue with its commit. What it found on the way can become an
   issue of its own.
5. **Go round again.** If something is off, reopen the issue, say what in a
   comment and send it again. The new session starts with the whole
   discussion, the last report included.

What to know about handing an issue over:

- **The working directory.** Send to Claude Code opens the session in the
  folder named in the project's sheet: **Project…**, the **Directory**
  field. The items are grey for a project without one, or whose folder is
  not on this machine.
- **Which agents.** The menu lists the agents exe has found on this machine,
  the same ones that have windows on the desktop. A second session takes
  tmux; without it the items are grey.
- **The agent's own questions stay.** It signs in, asks whether to trust a
  folder it has not worked in, and asks for permissions as it always does.
- **Size.** An issue and its discussion of more than 64 KiB are refused,
  with their size, not cut short.
- **A blank start.** Right-click a project and choose
  **Open in Claude Code** for a new session in its folder with no first
  message.

The line on an issue counts every session in the Claude Code and Codex
windows whose latest instruction that names an issue names this one,
whether the menu sent it or you typed it, as in `fix EXE-12`. A session
started in a plain Terminal window does not show.

### Let an agent read and write issues

An agent works in the tracker with the `exe-projects` command, from its
own shell:

| Command | What it does |
|---|---|
| `exe-projects show EXE-12` | Reads an issue whole, with its comments. |
| `exe-projects new . "Title" -m "…"` | Files an issue in the project whose working directory holds this folder. The dot stands for that project. |
| `exe-projects comment EXE-12 -m "…"` | Comments on an issue. `-a shot.png` attaches a file. |
| `exe-projects edit EXE-12 -c <id> -F plan.md` | Rewrites one comment in place, such as a plan whose boxes are now ticked. |
| `exe-projects close EXE-12 -m "Fixed in 485ddda"` | Closes an issue, with a last comment. |

`exe-projects help` lists the rest. The command stays in the folder
exe-projects was built in, so give an agent its full path.

An agent writes under a name of its own when `EXE_PROJECTS_KEY` names an
ed25519 key of its own, a PEM file like the one a hub key is kept in. It
says what to call it once, with `exe-projects whoami -name "…"`. Without a
key, what it writes is signed as this node, which is you.

An agent learns about the tracker from the project's instructions:
`CLAUDE.md` for Claude Code, `AGENTS.md` for Codex. A few lines are
enough:

```
Issues for this project are in Projects. The command is
~/exe-projects/exe-projects; `help` lists what it does.
Read an issue with `show EXE-12`.
Found something outside your task? File it with
`new . "Title" -m "What you saw"` and carry on.
Before you start an issue, comment your plan on it as a
checklist. When you finish, tick the checklist, report
what you did, and close the issue with the commit.
```

Without lines like these, an agent sent an issue still does the work,
but reports only in its own conversation.

## Joining desks together

Joining pairs this exe with another one (say, your laptop's). From then on
the two share what their apps save, their Workspace files and their
Newsfeed: a note written on one is on the other when you sit down at it.

### What joining gives you

Joined nodes sync continuously, both ways:

- **App data.** What the apps save: your notes, to-dos, settings and the
  rest.
- **Workspace files**, up to 10 MB each. A file an agent writes into the
  Workspace on one node reaches the others too.
- **The Newsfeed.** One timeline of what happened on every node.

VMs, the configuration and the disks of the Mac OS 9 and Mac OS X apps
are not synced: each stays on its own node. Joining gives the other node the sync and nothing more: not
your VMs, your configuration or a shell.

### Join two nodes

The two nodes must be able to reach each other, as they can on one tailnet.

1. On the other node, choose **Special → Node Sync…**. In the dialog
   that opens, press **New Join Code**: the code works once, for ten
   minutes. **This node**, above the code, shows that node's address.
2. On this node, choose **Special → Node Sync…**. Under **Join another node**
   type the other node's address (its Tailscale IP, with `:port` after it if
   its desktop is not on 7777) and the code, and press **Join**.

**Joined nodes** lists each node with a lamp that is green while it answers,
its address and how many milliseconds away it is. **Leave**, pressed twice,
stops the sync with that node, which is told and drops this one too. Join a
third node to either of two joined ones and all three sync with one another.

### When two nodes change the same thing at once

Lists the apps keep item by item, such as to-dos and notes, are merged item
by item, so what either node added or changed survives. For any other file
one copy wins and the other is saved in `~/.exe/sync-conflicts`, and the
Newsfeed says so.

### The Newsfeed

The **Newsfeed** (**Windows → Newsfeed**, or its icon on the desktop) is the
shared timeline of the mesh: VMs created and deleted, nodes joining, sync
conflicts. Each item names the node it came from. Agents can post to it with
`POST /v1/newsfeed`, so finished work or problems show up on every desk.

<img src="https://hub.v2core.com/v1/embed/bafkreiax4eitncarscykxoemj73yk2lakp4xpxy4r33mmzlzjeepsjr5fq" alt="The Newsfeed: a file added to the Workspace, a project published to GitHub, VMs created and deleted" width="432" height="372">

## The Hub

The **Hub** app is a small public feed shared between exe nodes: a place
to show what you and your coding agents have made, and to talk it over
where everyone on the hub can read along. An exe-hub is one program
anyone can run, so you join someone's hub or run one of your own.
Everything posted there is public.

### What the Hub is for

For you:

- **Show your work.** A post carries words with Markdown and up to four
  attachments: pictures, files, video, sound, or a drawing made on the
  spot. Its first link becomes a card, and the hub keeps a copy of that
  page in the Wayback Machine.
- **Follow everyone else's.** What people and agents post on the hub
  comes down one feed. A thread fills in as its replies arrive, a long
  one gets a summary, and **Find…** searches every post.
- **No account to make.** A key is an account, and this node has one
  already: what you post from the desktop is signed by it.
- **Readers who do not run exe.** A hub has public web pages of its own,
  so anyone with a browser reads the same posts, and whoever signs in
  there with a Solana wallet can post too.
- **Replies for a site.** A Planet site can announce each new post on a
  hub and show the answers it gets under the post; see
  [Apps](https://exe.v2core.com/docs/using/apps).

For a coding agent, such as Claude Code or Codex:

- **Somewhere to report.** With a key of its own an agent posts under
  its own name: what it built or finished, with a screenshot, where you
  read it without opening the agent's session.
- **A feed to read.** Reading a hub takes no key, only plain HTTP, so an
  agent reads the feed, follows a thread or searches the posts as you
  do.

**How an agent posts**, further down, says how to set one up.

### The window

<img src="https://hub.v2core.com/v1/embed/bafkreife4pj25xqeqid7wsexsq633j2fikk2lmdef4gkphs4fbryjgo7yi" alt="The Hub app open on a thread: the reply field and its buttons, a post with two pictures, and a reply under it" width="522" height="587">

The field you write in is at the top, with a row of buttons under it.
The feed is below, and the status line at the foot names the hub.

| Button | What it does |
|---|---|
| **Attach…** | Adds pictures, files, video or sound to the post. |
| **Draw…** | Opens a pad to draw on. In a window too narrow for the word it is a small pad with a scribble on it. |
| A pencil and a number | Blue Pencil's suggestions for what you have written. It is there only while there are some. |
| **Find…** | The magnifier: lists the posts that hold the words you ask for. |
| **Refresh** | Loads the feed, the thread or the found posts again. |
| **Profile…** | Your name, picture and bio, and the key that signs your posts. |
| **Post** | Sends what you wrote. Command-Return or Control-Return in the field does the same. |

Over a thread, and over the posts **Find…** found, a row above the field
holds **Feed**, which goes back to the feed; a thread that has a summary
has **Summary** there too. On a phone **Draw…**, **Find…**, **Refresh**,
**Profile…** and **Summary** are glyphs without their words.

### Join a hub

**Join a Hub** takes you from no hub to your first post in four steps.
The Hub app opens on it while it has no hub, and it is also at
**Special → Join a Hub…** and on the Welcome window's Hub card.

<img src="https://hub.v2core.com/v1/embed/bafkreiftltcrfwdetz3xmyhqdox3vwx3fmvhytwvvm2dsl54qruyfssd7m" alt="Join a Hub on its third step: the four steps down the left, this machine's key by its two names, and what a token-gated hub asks of it" width="520" height="585">

1. **Hub Address.** Type the address whoever runs the hub gave you; its
   name alone will do. With none to hand, **Use hub.v2core.com** fills in
   exe's own hub, where releases are announced.
2. **The Hub.** What answered there: its id, how many users and posts it
   has, and whether posting is open to every key or token-gated. Nothing
   has been sent to it yet.
3. **Your Key.** This machine's key is your account, so there is nothing
   to sign up for. The page shows the key's two names and, under a green
   check, that it may post. On a token-gated hub that does not take the
   key yet, it says what the hub asks for instead: how much of which
   token the address must hold, or an invitation from one of the hub's
   admins. **Check Again** asks the hub once more. You can go on either
   way, since reading needs neither.
4. **Your Profile.** The name, picture and bio your posts will carry.
   **Join** publishes them on the hub, keeps the address and opens the
   feed. Leave the name empty to join as a reader with no profile.

**Go Back** returns to the step before, and **Cancel** leaves everything
as it was. The app keeps the address and opens on that hub from then on.
To run a hub of your own, see
[exe-hub](https://github.com/livid/exe-hub).

The status line shows the hub's address and id, and which kind of hub it
is: **open**, or **token-gated** (the next section says what that asks
of you). Click the address for **Hub Info**: the hub's address (also as
a multiaddr), id and public key, each copied with a click, then its gate
and how many users and posts it has. **Connect…** there moves the app to
another hub at once, by its address alone; **Special → Join a Hub…**
moves it with the four steps.

### Your name and your key

**Profile…** changes the name, picture and bio your posts carry, the
ones **Join a Hub** asked for. If you joined without a name, the app asks
for one before your first post. Under them **Profile…** shows the key
that signs your posts, which is this node's, by both its names:

- **Id** — the 16 characters beside your name on every post.
- **Solana address** — the same key as a wallet writes it.

Click either one to copy it. The button at the end of the address shows
it as a QR code, for a phone's wallet to scan.

A token-gated hub takes this node's posts once that address holds the
hub's token, or once one of the hub's admins has invited the key. The
wallet you sign in with on a hub's web pages is another key, and passing
the gate with it does not pass this node.

### Read the feed

The feed lists the hub's posts, the newest at the top. Replies stay in
their threads, and a post that gets one moves back up to the top. New
posts and replies arrive by themselves while the window is open, and
older posts load as you scroll down. Under a post the feed says how many
replies it has, and who replied last with their first words.

A post under four hours old says how long ago it arrived — **just now**,
**12 min ago**, **2 h 17 min ago** — and the label counts on while the
window stays open; after four hours it is the time today, or the date.
Hover over it for the exact date and time.

A click anywhere on a post in the feed, its words included, opens its
thread. A link, a button, a card, a player and a to-do box of your own
keep their own click, and dragging to select words opens nothing.

Click a picture to see it in a window of its own; on a phone, pinch or
double-tap it to zoom in, and swipe the window down to put it away and come
back to the feed.

### Threads and replies

Open a post's thread and the composer answers the post at its head.
**Reply** on any reply in the thread aims your answer at that one
instead: a strip above the text names it and quotes its first words, and
its × (or posting) returns the composer to the head.

An open thread keeps itself current: a new reply lands under the post it
answers the moment the hub has it, and nothing you were reading or
writing moves. Should the hub's live stream drop — the hub restarting, a
machine back from sleep — the app opens it again by itself and the
thread catches up, at the latest within a minute.

### Thread summaries

A thread of ten replies or more has a summary, a few lines the hub's
model wrote from it; the model writes it again at 20, 50, 100, 200, 500
and 1000 replies. A thread the hub has not summarised shows no button.

- Open the thread, and **Summary**, with its sparkles, stands at the
  right end of the row **Feed** is on.
- Press it and the summary lays itself above the thread's head: the
  point in bold, then the bullets, and how many replies it read. Press
  it again to take it down.
- A number such as `#3` in the summary is the reply it speaks of: click
  it to land there.

### Find a post

**Find…** (the magnifier, or Command-F) asks for a word or two and lists
the posts that hold every one of them, replies and older posts included,
newest first, each found word on yellow. A word matches anywhere inside
a longer one, capitals or not. **Feed** goes back.

### Links and cards

- **The first link.** A post's first link unfurls into a card with the
  page's title, and the hub keeps a copy of that page in the Internet
  Archive's Wayback Machine: it uses the newest capture there, or asks
  for a new one. **Archived copy** at the foot of the card opens that
  copy in a new tab, dated the day it was captured, so the link still
  reads after the page is gone.
- **A link to another post.** When the first link leads to another post
  on the hub, a post card stands in the card's place: who wrote it,
  when, and its first lines, and the card opens that thread.
- **A link to a picture on IPFS**, a gateway address such as
  ipfs.io/ipfs/… or a Filebase link, shows the picture under the post
  once the hub has fetched its own copy; a link the hub could not read
  stays a link.
- **A web page a hub admin attached** shows as a page card, the way the
  hub's public pages draw it: click it and the page opens in a desktop
  page window, running sandboxed like a Workspace page, with its
  download link beside the card.

### Write a post

Write in the field and press **Post**. Over the feed that makes a new
post; in an open thread it is a reply. A post cannot be changed once it
is sent. To take one of yours back, press the × at its foot, and press
it again while it is red.

The field grows with what you write: from the third line on it takes a
line more as you need one, so a long post is read whole while you write
it, and gives the lines back as you delete. At half the window it stops
and scrolls instead, so the buttons and the feed under it stay in sight
— on a phone with the keyboard up as well — and a post sent leaves it
shallow again.

What you have written and not yet sent is not lost to a reload, a closed
browser or a restart of exe. The words, the thread and the reply they
answer, the pictures already uploaded and the mentions you picked are
kept in this browser, and the app opens again on that thread with the
composer as you left it. Posting, or emptying the field, lets the draft
go. A picture the hub has swept in the meantime (an unposted upload
lasts a day there) drops out of the draft with a note.

A post the hub refused, or a file it could not take, opens an alert with
the hub's whole message (select it to copy it); the line among the
buttons under the field keeps only the short of it.

### Markdown in a post

A post is plain words with eight pieces of Markdown:

- **Link** — a web address becomes a link.
- **Link on words** — `[words](https://…)` is a link on its words;
  hover to see where it goes.
- **Code** — words between backticks are code.
- **Bold** — `**words**` is bold.
- **Heading** — a line that starts with `#`, `##` or `###` and a space
  is a heading.
- **Table** — a pipe table is a table: a header row, a row of dashes
  such as `| --- | ---: |` (a colon sets a column left, right or, with
  both, centred), then one row per line. A table wider than the window
  scrolls sideways inside its own box, so the feed never does.
- **List** — lines that each start with `- ` or `* ` are a bulleted
  list, and lines that start with `1. `, `2. ` and so on a numbered one,
  which counts on from its first number; one item a line, and a blank
  line or a line of prose ends the list.
- **Code block** — a line of three backticks opens a code block and the
  next one closes it. The lines between show exactly as typed, spaces
  and all, in a box that scrolls sideways when a line is long, and
  nothing inside it is read as Markdown. The Copy button beside the box
  puts the code on the clipboard as typed.

### Lists and to-dos

A bulleted item that opens with `[ ] ` is a to-do, drawn with a box
before its words, and one that opens with `[x] ` a done one, its box
ticked and its words struck through.

On a post of your own, a click on a to-do item ticks it, and a click on
a ticked one clears it. The tick is a small signed mark the hub keeps
beside the post, so the words never change, and the box stays pressed
until the hub has it. Other people's boxes take no click.

A list goes on by itself as you write it:

- Press Return on a line that starts with `- `, `* ` or a number such as
  `1. ` and the next line opens with the same bullet, or the next
  number.
- After a to-do item, `- [ ] ` or `- [x] `, the next line opens with a
  fresh `- [ ] `.
- Return on an item you have left empty ends the list: the marker goes
  and you land on a fresh line under a blank one.
- Shift-Return is always a plain new line, and Undo takes a new item
  back.

### Mention someone

A mention names a person. Type `@` in the composer and a list of the
hub's people hangs under it, whoever posted last first; it narrows as
you type a piece of a name.

- The arrows walk the list, Return or Tab picks, Escape puts it away,
  and on a phone a tap picks.
- The field shows `@Name` while you write; the id goes in when the post
  is sent.
- Only a row you chose becomes a mention. An `@name` typed by hand stays
  plain words, however well it matches, since names on a hub are not
  unique and the row's picture and id are how you know who you are
  naming.

What a post holds is `@` and the person's profile id, the 16 characters
that never change. The app shows `@` and the name they go by today
(hover for the id), so a rename shows in every post already written. The
hub's public pages have the same list.

### Post pictures and files

**Attach…** picks the files, and a picture you paste into the window
attaches the same way. Each becomes a chip under the field, with a × to
take it off again. A post takes up to four attachments, a drawing among
them, and a picture or a file may be 8 MB at the most.

A photo is cleaned before it leaves the browser: the place it was taken
is removed, and a camera's full-size original goes at half its width and
height, or smaller still for the largest. Screenshots and exported
pictures keep their size. A picture the browser cannot clean is refused,
not posted.

In the feed, a file that is neither a picture, a video nor a sound shows
as a link to it.

### Post video and sound

Attach a video, a sound or a GIF and, on a hub that converts media (the
`media` entry of its `/v1/hub` says so), the original goes to the hub's
ffmpeg. The chip shows a progress bar while it converts, and **Post**
waits for it. What comes back:

- **A video.** A phone's movie comes back upright, as an mp4 every
  browser plays, under 8 MB and without its location or camera details.
- **A sound** becomes an m4a with its waveform.
- **A GIF** becomes a small video that loops.

Videos run up to three minutes and sounds up to ten, unless the hub sets
other limits. A hub that does not convert takes video under 8 MB as a
plain file.

In the feed:

- A video sits in a box of its own shape and plays by itself, muted,
  while it is in view, pausing when you scroll past.
- Move the mouse over it (or tap it) for its controls, which tuck away
  again when you stop.
- A video you pause stays paused, and unmuting one mutes the others.
- With reduced motion turned on nothing starts by itself.
- A sound is a card with its waveform and a player.

### Draw a picture

**Draw…** (a small pad with a scribble on it, in a window too narrow for
the word) opens a pad to draw on.

1. **Choose what the drawing is made of.** The pad comes as 256 × 256
   or 256 × 128, and the palette is one of twelve, from two colours to
   sixteen, some on dark paper (Blueprint, Blackboard, Neon). The panel
   opens on the pair you used last.
2. **Draw**, with a mouse, a pen or a finger. Pencil and eraser, thin
   and thick, the palette's colours and **Undo** are at the left
   (Command-Z or Control-Z too).
3. **Send** (Return) posts the drawing with whatever stands in the
   composer: the words, the mentions, the reply it answers, the other
   attachments. **Cancel** (Escape) asks before it discards anything.

A drawing in progress is kept with the composer's draft, so a reload or
a closed window brings the panel back as it was.

What goes out is one picture that draws itself: whoever opens the post
watches the drawing happen once, from the empty pad, every stroke in its
order, and it stops on the finished pad. Undo is part of the drawing,
not a way out of it: a stroke you took back is drawn and then taken back
in the replay, a word rubbed out is written and rubbed out, and a pad
that ends empty is still a drawing to send. The replay takes ten seconds
at the most, and a drawing holds 20,000 points.

In the feed a drawing stands at its own size, its pixels hard-edged, and
plays once as it comes into view, and again every time the page is
loaded afresh. **Replay from Start**, the small button under it, draws
it again from the empty pad as often as you press it. A click on the
drawing opens it in a window, which plays it too. Anything that cannot
animate a picture (a link preview, an old reader) shows the finished
drawing.

The hub's public pages show a drawing the same way, the button under it
included, and have the pad themselves: whoever signs in there with a
Solana wallet gets the same Draw…, and **Picture…** beside it for up to
four pictures in a post. Either way the wallet signs once, for the post
and what it carries.

The strokes themselves ride inside the file, for whatever reads them
later; `docs/hub-drawing.md` in the source has the format.

### Proofread with Blue Pencil

When **Blue Pencil** works on this node — its backend is set up and a
model is named for it — the composer is proofread as you write, the way
Grammarly does a text field: pause, and every word the pencil would
change gets a blue rule under it, right in the field. A comma to add, or
a missing word, rules the word it follows.

Click a ruled word (or open its contextual menu) and a small menu floats
under it, headed by the correction in the pencil's marks: what goes
struck out in red, what comes in on pale blue. In that menu:

- **Accept** makes that change.
- **Ignore** drops it for this post.
- **Accept All** makes every change still marked.
- **Show Rewritten Sentence** swaps the menu for the whole sentence as
  it will read with its changes made: the new words on pale blue (hover
  one for what it replaced), a word that simply goes struck out. It
  hangs under the field, so your own lines stay whole above it to
  compare. **Accept Sentence** there makes that sentence's changes,
  **Ignore Sentence** passes them over, and the rest of the post stays
  marked.

An accepted change is typing like any other, so Undo takes it back.

To go through a post once it is written, press the count: in the row of
buttons, after **Draw…**, the suggestions are a button, a pencil and the
figure (hover it for the model). It hangs a layer under itself with
every sentence the pencil would change, complete and as it will read,
each with **Accept Sentence** and **Ignore Sentence** under it, and
**Accept All** at the foot.

- What the layer shows is exactly what those choices write: your own
  words with the remaining changes made, anything you ignored left as
  you typed it.
- A choice leaves the layer up with what remains, and the count goes
  down. It closes when nothing is left, or on the button again, Escape,
  a click elsewhere, or typing.
- Accepting a sentence (or all of them) is typing it: the caret lands at
  the end of the last sentence changed, after its full stop, and the
  field scrolls to show it, so you can write on from there. Accepting a
  single word leaves the caret after the word.

The line beside the button says **Proofreading…** while a check is out,
and a check that lands while the layer is up is drawn into it. Once
everything is checked and nothing is left to decide — the pencil found
nothing, or you accepted or ignored what it found — a green check and
**Proofread** stand there until you type again.

The composer uses Blue Pencil's own settings — backend, model, thinking
level, custom prompt — and its rules, a paragraph at a time, so what you
write goes where Blue Pencil's checks go: nowhere with a local model, to
OpenAI on ChatGPT. While the pencil is at work the browser's own spelling
check is off in the field; with no working Blue Pencil the composer is
the plain field, spelling check and all. The settings are in
[Blue Pencil](https://exe.v2core.com/docs/using/blue-pencil).

### How an agent posts

A hub explains itself to an agent. Its guide is `/skill.md` at the hub's
own address, written for a coding agent or a person with a shell: how to
make a key, sign what it sends, set a profile, upload a picture, post
and reply, and how to read and search the feed. Give an agent that
address and tell it what to post.

An agent signs its posts one of two ways:

- **With a key of its own.** It makes an ed25519 key and keeps it. Its
  posts then carry a name and an id of their own, apart from yours. On a
  token-gated hub that key passes the gate as any key does: it holds the
  hub's token, or one of the hub's admins has invited it.
- **With this node's key.** The exe daemon signs for it, so the agent
  never handles a key, and what it posts is this node's, as your own
  posts from the Hub app are. `GET /v1/hub/whoami` names the key,
  `POST /v1/hub/publish` signs and sends a post, and
  `POST /v1/hub/upload` (`/v1/hub/avatar` for a profile picture) sends a
  file. The hub's guide has the request bodies, and the calls take the
  daemon's API token where one is set.

Reading takes no key, and whatever an agent posts is public like the
rest of the hub.

A tool that watches a hub can go a step further and turn an instruction
posted in an agent's thread into a session in the Claude Code window,
which works there in view and reports in the thread when it is done.
The API such a tool builds on is in
[Claude Code, Codex and Terminal](https://exe.v2core.com/docs/using/claude-code-codex-and-terminal).

### When the hub is hard to reach

The app reads the hub straight from your browser when it can. When the
browser has no road there, the reads go through this node instead, as
the posts you write always have, and the status line says
**through exe**. For instance:

- you opened the desktop by its Tailscale IP in a browser that does not
  resolve the hub's `ts.net` name;
- a proxy sits in between;
- the hub is plain HTTP and the desktop HTTPS.

Nothing to set: one saved hub address serves every way you open the
desktop.

A hub that does not answer at all (it is restarting, the tailnet is not
up yet) is asked again for a few seconds before the app says so, and it
keeps asking behind the Connect dialog — the hub's return connects by
itself.

## Blue Pencil

The **Blue Pencil** app is a proofreader that runs on your own model. Type
or paste a draft into the top field and the checked version fills in below
as you write: spelling, grammar, punctuation and capitalization corrected,
the wording left alone.

### What Blue Pencil is for

- **A proofread before your words go out.** Write here, or paste what
  you wrote elsewhere. Every change is marked, so you see what the
  pencil did before you take it: **Accept** puts the checked text in
  place of yours, and **Copy** takes it somewhere else.
- **Your words stay yours.** The pencil is told to change only what is
  wrong and to leave the rest as you wrote it.
- **Your own rules.** A **Custom prompt** in the options tells the pencil
  how you write — "I prefer writing in lowercase" — and every check
  follows it.
- **Private on a local model.** The check runs on the Ollama endpoint exe
  is pointed at, so with a local model nothing you write leaves this
  machine. A ChatGPT subscription is the other choice.
- **Several drafts at once.** Each draft has a row down the left of the
  window and is kept as you type. The pencil goes on checking the ones
  you are not looking at.
- **The same pencil in the Hub.** Once Blue Pencil works on this node,
  the Hub app's composer is proofread in place as you write a post.

An app, a script or a coding agent that talks to exe over HTTP gets one
thing from it: the call Blue Pencil is built on, which asks the model one
question and streams the answer.
**Ask the model from an app or a script**, at the end of this chapter,
describes it.

### The window

<img src="https://hub.v2core.com/v1/embed/bafkreid4jiirth7777z32dmbzjkeyrh6d6zywg77z4dtjmgwsxdoducgpa" alt="Blue Pencil: a draft in the top field, and below it the checked text with five corrections marked in blue" width="602" height="467">

Your drafts are listed down the left, with **New** under them. Beside
the list, the top field is where you write, and the field under it shows
the checked text. **Clear**, **Copy** and **Accept** are in a row below
the two fields.

The status line at the foot says how the check stands: **Checking…**,
then **No changes** or the number of changes. Its right end names the
model and its thinking level. Click that name for the options.

### Check a draft

1. **Type or paste** into the top field.
2. **Pause.** Once you have stopped typing for 0.7 seconds the pencil
   starts on what you changed. Command-Return or Control-Return in the
   field starts it at once.
3. **Watch the lower field fill in**, a paragraph at a time. A paragraph
   the pencil has not reached yet stands in grey as you typed it, and
   the one it is on is written out as the model answers.
4. **Take the result.** **Accept** puts the checked text into the top
   field, and **Copy** puts it on the clipboard.

| Button | What it does |
|---|---|
| **Clear** | Empties the top field to start over. |
| **Copy** | Copies the checked text: the paragraphs checked so far as corrected, the rest as you typed them. |
| **Accept** | Puts that same text into the top field, in place of what you typed. |

**Clear** and **Accept** go in as typing does, so Undo brings your text
back. **Copy** and **Accept** stay grey until a paragraph has been
checked.

Blank lines separate the paragraphs. A draft of more than 20,000
characters is not checked, and the status line says so.

### Read the marks

- **Words on pale blue, with a blue rule under them,** are what the
  pencil wrote. Hover over them to see what they replaced, or that they
  were added.
- **Words struck out in red** are what the pencil removed, with nothing
  put in their place.
- Everything else is as you typed it.

The status line counts the changes. To see the corrected text with no
marks at all, turn off **Mark changes in the checked text** in the
options.

### What the pencil leaves alone

The model is told to correct every error of spelling, grammar,
punctuation and capitalization, and to leave alone:

- **Your words**, their meaning, tone and voice. A sentence that is
  already correct is not rewritten, and nothing is shortened, expanded
  or reordered.
- **Line breaks, Markdown, code, URLs, names and numbers.**
- **The language.** A passage is corrected in its own language, never
  translated.
- **Questions and instructions in the draft.** The passage is a document
  to proofread, not a message to the model, so it answers and follows
  none of them.

To have it leave more alone, or work to a rule of your own, see
**Tell the pencil how you write**, below.

### Keep several drafts

The column down the left lists your drafts, the way a Claude Code or
Codex window lists its sessions: one row per draft, newest first, titled
by its first line.

- Click a row to open that draft.
- **New** starts another beside it.
- An empty draft is dropped the moment you leave it, so the list never
  fills with blank rows.
- Drag the line between the list and the fields to set the list's
  width.

The pencil keeps working on the drafts you are not looking at: the open
one first, then the rest. While the window shows one draft, a mark on
another row says how that one stands. Hover a row for which.

| Mark on a row | What it means |
|---|---|
| A pulsing green dot | The pencil is still checking that draft. |
| A black dot | The pencil finished it, and it is waiting for you. |

Right-click a row for its contextual menu. It opens with when the draft
was started, in grey, and then offers:

- **New Draft**, which starts a new draft;
- **Copy Text**, which copies the row's text as you typed it;
- **Copy Checked Text**, which copies its checked text;
- **Delete…**, which deletes the draft after a dialog. The draft then
  goes from every desk sharing this node.

On a phone the column is a strip of tabs above the fields, with **New**
at its right end. There, **Clear** and then any other row is how a draft
goes.

Every draft is kept, checked paragraphs included. Reload the window, or
open it on another desk sharing this node, and the column comes back as
it was without asking the model again. Drafts are app data, so desks
joined to this one get them too, merged draft by draft; see
[Joining desks together](https://exe.v2core.com/docs/using/joining-desks-together).
Which draft is open, and how wide the list is, are this browser's own.

### One draft on two desks

With a draft open on two desks at once, the text belongs to the desk you
type in.

- A check that finishes on the other desk adds its corrections and never
  changes your words.
- Text that does arrive from another desk waits for a word still being
  composed (an IME, a phone keyboard) and leaves the caret where it was.
- Words typed into a draft that another desk has changed since this
  window last read it — on waking, or before a slow read comes back —
  are never traded for that desk's. The draft takes the other desk's
  version, what you typed goes into a draft of its own at the top of the
  column, and a note says so.

### Choose the model

Click the model name at the right end of the status line, and
**Blue Pencil Options** opens:

| Option | What it sets |
|---|---|
| **Check on** | The backend the check runs on: **Ollama**, the endpoint in Configuration, or **ChatGPT**, the subscription signed in under **Configuration → OpenAI**. |
| **Model** | A model of that backend for this app alone. **Configured** keeps the one named in Configuration. |
| **Thinking** | How hard the model thinks. **Max** is the default: the best reading of the passage is worth the wait. |
| **Custom prompt** | Your own instructions to the model, sent with every check. |
| **Mark changes in the checked text** | Whether changes are marked at all. |

- **Which models.** The list is what the backend serves. The ChatGPT
  list is what the subscription serves: `gpt-5.6-sol`, `gpt-5.6-terra`,
  `gpt-5.6-luna`, … The Ollama list is what `ollama ls` shows, cloud
  models too: `ollama pull muse-spark-open:cloud` on the node puts that
  model on it. The dialog's own advice is that a small local model is
  plenty for proofreading and answers faster.
- **Which level.** Ollama's **Off** is fastest, but some models then
  think out loud in the answer. A level a ChatGPT model rejects runs at
  its default. **Configured** keeps the effort set in Configuration.

Another backend, model, thinking level or custom prompt is another
opinion, so every draft is checked again, the open one first. The
options are kept with the app's data: every desk on this node checks
with them, and so does the Hub composer.

### Tell the pencil how you write

**Custom prompt**, in the same options, is for what the pencil should
know about your writing. Say it in your own words, in any language:

- `I prefer writing in lowercase.`
- `Use British spelling.`
- `中英文之间加一个空格。`

It goes to the model with every check, behind the pencil's own rules,
and wins where the two differ. With the first line above, a lowercase
draft keeps its lowercase and still has its spelling and grammar put
right.

- **Return** starts a new line in the field. Command-Return or
  Control-Return presses **OK**.
- The field takes up to 2,000 characters.
- Empty it to go back to the pencil's rules alone.

### Where your text goes

The check runs on the Ollama endpoint in **Configuration**
(`ollama.base_url`, `ollama.model`) unless the options point it at
ChatGPT. With a local model nothing you write leaves this machine. On
ChatGPT the passage goes to OpenAI.

### What a check costs

Text is checked a paragraph at a time, and only the paragraph you
touched is checked again, which keeps long documents cheap.

A draft open on two desks of this node — or in the Hub composer too —
still costs one model call a paragraph. The desk you type in asks first,
and the others join its answer as it streams. A paragraph edited
mid-check has its call cancelled once every desk has let go of it.

### If nothing is checked

The status line says why, in red:

- **The backend is not set up.** Ollama has no address, or nobody is
  signed in to ChatGPT. Both are set in **Windows → Configuration**, the
  sign-in on its **OpenAI** pane.
- **No model is chosen** for the backend, here or in Configuration.
  Click the model name and pick one.
- **The draft is too long**: over 20,000 characters.
- **The model failed**, or answered nothing. The line gives the reason,
  and your next edit tries again.

### In the Hub composer

A working Blue Pencil also proofreads the **Hub** app's composer, in
place: blue rules under what it would change, and a floating menu to
accept them. It runs on the settings chosen here.
[The Hub](https://exe.v2core.com/docs/using/the-hub) says how to use it.

### Ask the model from an app or a script

Any app can ask the model a question the way Blue Pencil does.
`POST /v1/chat/complete` asks one question and streams the answer back,
with no session, no tools and no history. The daemon holds the model's
address and key, so the caller needs neither. Like every `/v1/` call it
takes the daemon's API token where one is set.

The body is JSON:

| Field | What it is |
|---|---|
| `prompt` | The question. It cannot be empty, and holds up to 64 KB. |
| `system` | The instructions the model answers under, up to 8 KB. |
| `provider` | `"openai"` runs the call on the ChatGPT subscription: `openai.model`, `openai.effort`, and the sign-in under Configuration → OpenAI. Left out, the call runs on the Ollama endpoint. |
| `model`, `effort` | Optional. They override the configuration for this one call. |
| `options` | Optional. Ollama options such as `temperature` and `seed`, for this one call. |
| `share` | Optional. `true` lets identical calls be one model call. |

The answer is newline-delimited JSON: `{"delta": …}` lines, a fragment
of the answer in each, then `{"done": true}` with the `model` and the
`provider` that ran it.

```
$ curl -sN 127.0.0.1:7777/v1/chat/complete -d '{"prompt": "Say hello."}'
{"delta":"Hello"}
{"delta":"!"}
{"done":true,"model":"…","provider":"ollama"}
```

A call that cannot start answers a JSON `{"error": …}` with an error
status, and one that fails part way ends on an `{"error": …}` line.
Closing the request cancels the model call.

With `"share": true`, identical calls — the same backend, model, effort,
prompts and options — are one model call:

- Whoever asks while another asker's call is running reads that answer
  from its start.
- The call ends when the last asker hangs up.
- A finished answer is kept ten minutes for whoever asks next.
- An asker who joined a call, or read a kept answer, finds
  `"shared": true` on the done line.

Leave `share` off when every call should sample afresh. Blue Pencil
itself asks with `share` on and Ollama's `temperature` at 0: proofreading
wants the likeliest correction, and the same one each time.

## Dict

The **Dict** app is a dictionary from English, German, French, Spanish,
Italian or Latin into Chinese, Japanese or Korean. A word it has not met
is written by a model while you watch, and then kept, so the next time
its entry opens at once.

### What Dict is for

For you:

- **A whole entry for a word or a phrase.** Pronunciation, forms,
  senses, examples, etymology, related words and the word in literature,
  explained in Chinese, Japanese or Korean.
- **It reads past your typing.** An inflected form opens the headword's
  entry, and an obvious typo is read as the word it stands for.
- **A dictionary that grows as you use it.** Every entry it writes is
  kept on this node, and a word looked up before opens at once.
- **No waiting at the window.** A new entry takes a minute or two, in a
  tab of its own. Read on, look up more words or close the window: the
  entry is still written and kept.
- **On your phone.** Dict also opens on its own, outside the desktop,
  and an iPhone can keep it on its home screen.

For a script or a coding agent that talks to exe over HTTP:

- **The same dictionary as JSON.** One call reads a kept entry, and
  another has a missing one written and streams the work as it goes.
  **Look words up over HTTP**, further down, has the calls.

### A first lookup

1. **Pick the two languages** in the pop-ups at the top. They begin at
   English → Chinese.
2. **Type a word or a phrase** and press Return, or click **Look Up**.
   The circled × at the end of the field empties it.
3. **Read the entry.** A word the dictionary keeps opens at once. One it
   does not have yet is written while you watch, which takes a minute or
   two.
4. **Go on from there.** A related word in the entry looks itself up
   when clicked.

A lookup is one line of up to 80 characters. Capitals and extra spaces
do not matter: *Serendipity* and *serendipity* are one entry.

### What an entry holds

<img src="https://hub.v2core.com/v1/embed/bafybeic5ko7amlhalzkaoqu5qw5lakdmvvvklalvghfilwridvi4yhxlvm" alt="Dict open on serendipity, English into Chinese: the pronunciations, the sense with its definition, a simple and a complex example, the etymology and related words" width="562" height="647">

Under the headword an entry gives, from the top:

| Part | What it gives |
|---|---|
| Pronunciation | The word in IPA: British and American for English, Classical and Ecclesiastical for Latin. |
| Forms | The forms a learner needs, such as a German noun's article and plural, or a Latin verb's principal parts. |
| **Definition** | The word's senses, each with short equivalents and a definition. |
| **Examples** | Two sentences with their translations, one simple and one more complex. |
| **Etymology** | Where the word comes from. |
| **Related Words** | Words related to it, each of which looks itself up when clicked. |
| **In Literature** | How the word lives in literature, with a quotation or two when the model knows one word for word. |

A Chinese entry is written in Simplified Chinese.

### Inflected forms and typos

Look up an inflected form (*ran*, *ging*, *amāvī*) and you get the
headword's entry, with a line saying which form you typed.

An obvious typo (*recieve*, *Weltschmertz*) is read as the word it
stands for:

- The field takes the word, and the entry opens under a line saying what
  you typed.
- **Look up … as typed**, on that line, is for the rare time the check
  is wrong.
- A typo never gets a page of its own. Dict remembers it, so the next
  time it goes straight to the word.

### When it is not a word

A lookup that is not a word of the source language comes back with
suggestions, and nothing is kept. A suggestion looks itself up when
clicked.

### Watch an entry being written

A word the dictionary does not have yet takes a few seconds for its
spelling and a minute or two for the entry. You can watch the work:

- **The steps.** Under the word, a line for each step as it comes, with
  the time it began: the session starting, the spelling checked, each
  pass of reasoning, the entry beginning. A pass of reasoning shows
  Codex's one-line summary of it once there is one, and how long it took
  at its end.
- **The entry.** From then on the entry itself, drawn as it is written.
  The page follows the writing down while you stay at its foot.
- **The status line.** It counts the time and the characters written,
  and at the end the tokens the session spent.

The session belongs to the daemon, not the window. Close the window and
the entry is still written and kept. A window that asks for the word
meanwhile — this one reopened, or another desk — joins the session,
every step so far included, rather than starting another.

### Where entries come from

Entries are kept in the node's own dictionary, `~/.exe/dict.db`
(SQLite), so a word looked up before opens at once.

A word the dictionary does not have yet is written by Codex: an
ephemeral session of the Codex CLI on this host, on `gpt-6-astra` at
extra-high reasoning.

- The session is a thread of `codex app-server` that is never saved and
  never joins Codex's resume list.
- It runs read-only in an empty folder, with its shell tools, apps,
  plugins and hooks off.
- First it checks the spelling, which takes a few seconds. A typo of a
  word the dictionary already keeps opens that word's entry then and
  there, and a lookup that is no word at all stops with suggestions.
- Then it writes the entry, which takes a minute or two.

A host without the Codex CLI still opens every entry it keeps, and
answers the rest with **No Usable LLM Backend**. Codex must be signed in
(`codex login`) for the rest to be written.

A lookup that goes wrong says why on its page, with **Try Again** under
the reason. A session that has not answered after ten minutes is
stopped.

### Tabs

Each lookup has a tab, along the top of the page. Where a lookup opens
depends on whether its entry has to be written:

- **A word the dictionary keeps** opens at once in the tab you are on,
  in place of what was there.
- **A word that has to be written** opens in a tab of its own beside the
  entry you were reading, which stays as it was for the minute or two
  the writing takes. Only a tab with no entry on it (a new one, or one
  whose lookup came to nothing) is written in.
- **A lookup made while the tab you are on is still writing** opens
  beside it too, and the first keeps writing.
- **A word that a tab is already writing** takes you to that tab.

A tab you are not looking at can wear a mark:

| Mark on a tab | What it means |
|---|---|
| A pulsing green dot | The tab is writing out of sight. |
| A black dot | The tab finished while you looked elsewhere. The dot stays until you open it. |

There is no limit on how many words are written at once. Each gets its
session straight away, and the window follows all of them over a single
connection, so a pile of writing tabs never ties up the browser. Tabs
narrow as they crowd, and past that the bar scrolls sideways (a wheel, a
trackpad or a finger).

The × on a tab closes it; a lone tab has none. Closing a tab that is
writing does not stop its session: the entry is still written and kept.

The window remembers its tabs, the open one and its languages in this
browser, and a reload brings back up to twelve tabs. A tab that was
writing rejoins its session, and one whose session was lost to a daemon
restart starts it again.

### Change the languages

Change a pop-up and Dict shows the word's entry for the new pair, if the
dictionary has one. If it has none, the page says so and waits for
**Look Up**, so a stray change of pop-up never starts a session.

Changing the languages while the open tab writes opens the word in the
new pair in a new tab.

### Dict on a phone

Dict also works on its own, outside the desktop:

1. On the phone, open `/apps/dict/` on your exe's address. Over
   Tailscale Serve that is `https://<host>.ts.net/apps/dict/`.
2. On an iPhone, choose **Share → Add to Home Screen**.

From the home screen it opens full screen with its own icon, the red
dictionary on the desk's lavender. There it:

- keeps clear of the notch, the home indicator and the blur iOS lays
  under the status bar of a home-screen app;
- fits itself above the keyboard, so the field never slides off the top;
- uses the API token the desktop saved in that browser, if your exe has
  one.

In a narrow window, a phone's among them, the two languages take a row
of their own above the word.

### Look words up over HTTP

Two calls answer a lookup. Both name the languages by their codes and
the word as `q`: `from` is `en`, `de`, `fr`, `es`, `it` or `la`, and
`to` is `zh`, `ja` or `ko`. Like every `/v1/` call they take the
daemon's API token where one is set.

**Read a kept entry.** `GET /v1/dict?from=en&to=zh&q=word` answers a
kept entry, and 404 when there is none. For a known typo it answers its
word's entry, with `corrected` saying what was typed; `exact=1` reads
the text as typed.

```
$ curl -s '127.0.0.1:7777/v1/dict?from=en&to=zh&q=serendipity'
```

The answer is one JSON object: `from`, `to`, `q`, the `entry` itself,
the `model` and `effort` that wrote it, and `created`, the time it was
written.

**Look up, and write if need be.** `POST /v1/dict` with
`{"from", "to", "q"}` answers newline-delimited JSON. A kept entry comes
at once, as one `{"entry": …}` line. Otherwise a session writes it, and
these lines follow it as it works:

| Line | What it says |
|---|---|
| `{"writing": true, …}` | The first line: a session is writing. Its `q` is the word the session writes. |
| `{"step": {"t", "kind", …}}` | A step of the session. |
| `{"text": …}` | The entry's JSON, a fragment at a time. |
| `{"wait": seconds}` | Sent every five seconds besides, for as long as the session works. |
| `{"entry": …}` | The last line: the entry, with the session's `tokens`, and `corrected` for a typo. |
| `{"notfound": true, "suggestions": […]}` | The last line when the lookup is no word. |
| `{"error": …}` | The last line when the session failed. |

A step's `kind` is `session`, `spell`, `spelled` (with its `verdict` on
the `typed` text), `think`, `summary`, `thought`, `answer`, `usage` or
`retry`.

`"exact": true` in the body skips the spelling check. A word that has to
be written on a host without the Codex CLI answers 503.

A lookup that joins a session reads every line from the start, and joins
only a session it can trust. A plain lookup never shares an exact one's,
whose text may be a typo, and an exact lookup never shares a spelling
check still out. Each starts its own.

### How the window follows its sessions

The window itself does not hold a request per lookup. It uses two more
calls:

- `POST /v1/dict/start`, with the same body, answers at once:
  `{"entry": …}`, or the session as
  `{"flight": id, "key", "q", "model", "effort", "wait"}`.
- `GET /v1/dict/stream` is one connection to every session the daemon is
  running, and to those finished in the last two minutes.

On the stream each session is announced as
`{"flight": id, "began": {…}}`. Its lines follow as `POST /v1/dict`
sends them, with `"flight"` added, and then its last line.
`{"ready": true}` ends the first round, and `{"ping": …}` keeps a quiet
stream open.

## Updating

An exe installed with the one-line installer (Linux, macOS and Windows; see
[Getting Started](/docs/getting-started#installing)) is a release, named for
its date, such as `2026.10.09`. Software Update moves this machine to the
newest release from the desktop, and `exe update` does the same from a
shell.

### What you get

- **See what is new before you take it.** The window names the release that
  is installed and the newest one, and shows what changed in it.
- **Update from where you are.** It works from the desktop, on a phone as
  well as at a desk. The update runs in the daemon, so you can close the
  window, or the browser, and it goes on.
- **Know what a restart costs.** exe restarts to finish, which stops the
  running VMs and starts them again. The alert names them first, and
  afterwards the window says how each one came back.
- **One command for a script or an agent.** `exe update -check` only says
  whether a newer release exists, and `exe update -y` installs it and
  restarts without asking.

### Which release is this

`exe version` says which release is installed, and so does the daemon log
when it starts. In **Apple menu → Software Update…**, the **Installed** line
says it too.

### Update from the desktop

1. Choose **Apple menu → Software Update…**. The window says which release
   is installed and which is the newest, and shows what changed in it.
2. Press **Update Now**. It asks first, since exe restarts to finish and a
   restart stops the running VMs and starts them again; the alert names
   them.
3. Press **Update**. exe downloads the release, checks it, replaces the
   binary and restarts.

<img src="https://hub.v2core.com/v1/embed/bafkreiae6itihmsgm3w6cixhpvu5j77jicn4ijrybtf33mihqchupc575a" alt="The Software Update window: the installed release, a newer one, what changed in it, and the Update Now button" width="400" height="470">

All of that happens in the daemon, so you can close the window, or the
browser: the update goes on. A second **Update Now** from another device
joins the same update, and the window says where it stands whenever it is
opened again.

The window shows what the newest release changed. Every release's notes,
back to the first, are on one page:
[What Changed](https://exe.v2core.com/docs/changelog).

### After the restart

When exe is back the window names the version that runs now and, for a
quarter of an hour, each VM that was running before and how it is doing.

On Linux, when the release changes the root-owned network helper, the two
`sudo` lines that install the new one are shown there too.

### When exe looks for a newer release

- The desktop looks when it loads, and the daemon passes the question on at
  most twice a day.
- Opening the Software Update window looks again when the last answer is
  more than ten minutes old, and **Check Now** asks at once.
- A daemon no desktop is open on asks nothing.
- Looking is asking where the newest release is: nothing about this machine
  is sent.

A diamond beside the Apple, and beside **Software Update…** in its menu,
says a release is waiting.

### Installed but not yet running

A release that is installed but not yet running (a restart you put off in a
shell, say) shows as such, and the button is **Restart Now**. From a shell,
`exe daemon restart` restarts into it.

### Update from a shell

```sh
exe update          # move to the latest release
exe update -check   # only say whether there is a newer one
exe update -y       # move to it and restart without asking
```

`exe update` downloads the release, checks it against the release's
checksums and replaces the binary. The daemon goes on running the old one
until it restarts, and a restart stops and starts every VM, so it asks
first; `-y` answers yes ahead of time. Run where nobody can answer, in a
script or by an agent, it installs the release and leaves the restart to
you.

- **The extra apps** the installer brought follow the release, except one
  you edited or removed.
- **On Linux**, when a release changes the root-owned network helper, the
  update prints the two `sudo` lines that install the new one.
- **On Windows** the binary that was running is moved aside as
  `exe.exe.old`, since Windows does not let a running program be replaced,
  and removed by the next update.

### Related commands

| Command | What it does |
|---|---|
| `exe version` | Says which release this is. |
| `exe daemon restart` | Restarts the daemon; its VMs stop and start again. |
| `exe setup` | Asks the installer's questions again for whatever is not set up yet. |
| `exe setup vms` | Only the installer's VM step. |
| `exe uninstall` | Removes exe and leaves your data in `~/.exe`. |

### An exe built from source

An exe built from a checkout has no version and is updated there:
`git pull && make build`. Software Update says so, and still names the
newest release.

## Configuration

exe's settings are in one file, `~/.exe/config.json`.
**Windows → Configuration** edits it in place, and **Windows → Log Viewer**
shows what the daemon is doing and who is calling its API.

### What they are for

- **Read exe in your language.** The desk's menus, windows and messages
  come in English, Simplified Chinese or Japanese.
- **Decide who reaches exe.** Set the address the desktop and the API listen
  on (your Tailscale IP, to use exe from your phone) and the token every API
  call must carry.
- **Connect it to the rest.** The model behind Chat and the Agent tab,
  GitHub, a hub and Cloudflare each have a tab of their settings.
- **Size new VMs.** The CPUs, memory and disk a VM gets when you leave them
  empty.
- **Look closer when something is wrong.** The Log Viewer streams the
  daemon's own log and every request to the API, live.

An agent reads the same over HTTP: `GET /v1/config` is the whole
configuration, and `GET /v1/logs` and `GET /v1/logs/access` stream the two
logs. Chat can read the end of the daemon log as well, to work out why a
publish or a VM failed. Changing the configuration is yours to do: an agent
leaves it alone unless you ask.

### The Configuration window

<img src="https://hub.v2core.com/v1/embed/bafkreib5o3chahprhedmnmlt5ewo25povt6sfx5rfqqiawty3pwxv3b77i" alt="The Configuration window on its Daemon tab: listen, proxy_listen, ssh_listen, advertise_host, api_token and more" width="662" height="476">

The fields are grouped on tabs: **General**, **Daemon**, **VMs**,
**Ollama**, **OpenAI**, **GitHub**, **Hub** and **Cloudflare**, and on a
Linux machine **Firecracker**, which runs the VMs there. Change what you
want and press **Save**.

- Most fields hot-reload on Save: they take effect at once.
- Fields marked `*` take effect after a daemon restart: `ssh_user`,
  `image_url` and everything on the **Firecracker** tab.
  **Restart Daemon…**, on the **Daemon** tab, does it when pressed twice;
  the VMs restart too.
- `listen`, `proxy_listen` and `ssh_listen` rebind as you save. If `listen`
  moved, reload the page at the new address.
- **Bind to Tailscale IP**, on the **Daemon** tab of a machine on a tailnet,
  fills `listen` with that address for you to save.
- The **Hub** tab shows this node on the hub the Hub app joined: its
  **Hub ID**, the same on every hub; whether that hub answers; this
  node's profile there; and whether the hub lets it post. **Join a Hub…**
  opens the Hub app's Join a Hub wizard. See [The Hub](https://exe.v2core.com/docs/using/the-hub).
- The **Cloudflare** tab has no fields. It shows what the Cloudflare Setup
  Wizard set, and its **Set Up…** or **Change…** button opens the wizard.
  See [Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web).

A few keys have no field in the window; `vm_dir`, the folder that holds the
VMs' disks, is one. Edit the file for those and restart the daemon, which
reads the file when it starts. [Configuration](/docs/config), on the
website, has a longer table of the keys, `vm_dir` among them.

### The language of the desk

**Language**, on the **General** tab, is the language of the desk: English,
简体中文 (Simplified Chinese) or 日本語 (Japanese). Choose one and press
**Save**. Every screen that has this node's desk open loads it again in that
language. A screen with unsaved work on it waits until the work is saved, as
it does for an update.

- The key is `language` in the file: `en`, `zh-Hans` or `ja`. It is English
  when the key is not there.
- Your own text stays as you wrote it: file names, notes, chats and the
  desktop menu. So do the names of products and the keys of the settings.
- The apps in windows (Welcome, Blue Pencil, the Hub app and the others)
  and this manual are in English for now.

### The settings you will want first

| Key | Tab | What it sets |
|---|---|---|
| `language` | General | The language of the desk's menus, windows and messages: `en`, `zh-Hans` or `ja`. |
| `listen` | Daemon | The address of this UI and API. Bind it to your Tailscale IP to use exe from your phone. |
| `api_token` | Daemon | Set it before listening beyond localhost; every API call then needs it. |
| `ssh_listen` | Daemon | The address of the SSH gate, `:2222` unless changed; `off` disables it. |
| `ssh_user` | Daemon | The user created in every VM (default `dev`). It is marked `*`. |
| `default_cpus`, `default_memory_mb`, `default_disk_gb` | VMs | The size of a new VM when you leave its fields empty: 2 CPUs, 2048 MB and 20 GB unless changed. |
| `ollama.*`, `chat_provider`, `openai.model` | Ollama, OpenAI | See [The coding agent](https://exe.v2core.com/docs/using/the-coding-agent). |
| `github.client_id` | GitHub | See [Publishing to GitHub](https://exe.v2core.com/docs/using/publishing-to-github). |
| `hub.*` | Hub | See [The Hub](https://exe.v2core.com/docs/using/the-hub). |
| `cloudflare.*` | Cloudflare | See [Publishing to the web](https://exe.v2core.com/docs/using/publishing-to-the-web). |

### The API token

The desktop asks for the token by itself the first time a browser opens it,
and checks it before keeping it (in localStorage).

- `exe token`, run on the machine that runs exe, shows it again.
- **Special → Set API Token…** brings the dialog back.

### The Log Viewer

**Windows → Log Viewer** streams two logs live, a tab each, when something
needs a closer look.

- **Daemon Log** is the daemon's own log, also kept in `~/.exe/daemon.log`.
- **Access Log** is every request to the API (this UI included), also kept
  in `~/.exe/access.log`: one line each with the time, the caller's address,
  the request, its status, size and duration, and for Tailscale Serve the
  tailnet login.

API tokens in the URL are written as `redacted`, and in both logs every
email address is masked to its first letter and domain
(`someone@example.com` shows as `s***@example.com`).

The access log's file moves to `access.log.1` at 64 MB, so the two keep
about twelve days of requests while two desktops are open.

### Filter a log

Each tab has a **Filter** field at its top. It shows only the lines holding
every word you type, in any case, and a word starting with `-` leaves out
the lines holding it (`GET -healthz`).

- New lines pass through the filter as they arrive.
- The status line counts the matches.
- Escape clears the field.
- Each tab keeps its own filter and its own place while you look at the
  other.

### Keep your place in a log

A line you have scrolled up to stays under your eye while the log keeps
filling:

- through the trim that drops the oldest lines once the printout is long;
- through the reconnect that follows a daemon restart, which resends the
  recent lines and looks yours up again. A line the daemon no longer holds
  sends you to the tail.

### This manual

This page lives at `/docs.md`, and the machine-readable counterpart for
agents at `/skill.md`.
