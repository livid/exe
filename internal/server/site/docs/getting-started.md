# Getting Started

exe is one Go binary. Install it, open the desktop, and everything else —
a VM, an agent inside it, a public HTTPS address — follows from there.

**1. Open the desktop.** On Linux and on a Mac — x86-64 or ARM64, Intel or
Apple silicon — one line installs the latest release and starts it:

```sh
curl -fsSL https://exe.v2core.com/install.sh | sh
```

On Windows (x86-64), the same from PowerShell:

```powershell
irm https://exe.v2core.com/install.ps1 | iex
```

It asks where exe should listen and a few things more, then prints the
desktop's address; [Installing](#installing) says what it asks and where
it puts things. To work on exe itself, build it instead. That needs Go
1.25 or newer:

```sh
git clone https://github.com/livid/exe.git
cd exe
make build        # also builds exe-net-helper on Linux
                  # Windows: go build -o exe.exe ./cmd/exe
./exe init        # writes ~/.exe/config.json
./exe serve       # run the daemon; it keeps this terminal
```

Open http://127.0.0.1:7777: the desktop is the first thing that works, and it
needs nothing else. A new install opens on a **Welcome** window: six short
cards on what exe does, each saying whether this machine is ready for it
and where to go to set it up. A Linux machine without KVM or Firecracker still gets
it, with the apps, the Terminal and the Hub, and an empty VM list; see
[Running without VMs](#running-without-vms-a-nas-a-container).

The examples below say `./exe`, as in a checkout. An installed exe is on
your `PATH`: write `exe`.

**2. A VM, from a second terminal** (`serve` has the first). Linux and Windows
have [requirements](#linux-requirements) of [their own](#windows-requirements);
`exe code` talks to the Ollama named in the [configuration](/docs/config).

```sh
./exe create demo               # clone Debian 13, boot, cloud-init, SSH ready
./exe ssh demo                  # log in (key in ~/.exe/ssh/)
./exe code demo "build me a guestbook app on port 8000"
```

The first `create` downloads the Debian 13 `genericcloud` raw image (~3 GB) once
into `~/.exe/images/`. Linux also downloads the configured direct-boot kernel.

**3. A public URL.** Needs a Cloudflare tunnel and a token, set up once:
[Cloudflare setup](/docs/config#cloudflare-setup-one-time).

```sh
./exe expose demo -port 8000 -sub guestbook   # -> https://guestbook.<domain>
```

# Installing

```sh
curl -fsSL https://exe.v2core.com/install.sh | sh
```

For Linux, and for macOS 13 or later; Windows has a line and a section of
its own, [On Windows](#on-windows). The script downloads the latest
release from [GitHub](https://github.com/livid/exe/releases) for this
machine's system and processor, checks it against the release's
`SHA256SUMS`, and hands over to `exe setup`. That asks up to four
questions — three on a Mac — and writes nothing until the last one is
answered. Return on each of them is the careful install: this machine
only, and no sudo.

1. **Where should exe listen?** This machine only (`127.0.0.1`), all
   interfaces, or the machine's Tailscale address when it has one. The
   answer goes for all three listeners: the desktop and API (7777), the
   reverse proxy (8090) and the SSH gate (2222). A port something else
   holds is passed over for the next free one, and the installer says so.
2. **Require an API token?** Asked for this machine only (default no) and
   for Tailscale (default yes). On all interfaces there is no question: a
   token is generated and shown, because whoever reaches the API has this
   machine's Terminal. exe speaks plain HTTP, so all interfaces is for a
   network you trust. The desktop asks for the token the first time a
   browser opens it; `exe token` shows it again.
3. **Install the extra desktop apps?** Notes, Paint, Ping, Tides, Todo,
   Weather and World Clock, from [exe-apps](https://github.com/livid/exe-apps),
   into `~/.exe/apps`.
4. **Set this machine up to run VMs?** Linux only, asked where `/dev/kvm`
   exists, and the one step that uses sudo. It lists the commands it would
   run before you answer: adding you to the `kvm` group, installing
   Firecracker (a pinned version, checked against its checksum) and the
   root-owned network helper with `CAP_NET_ADMIN`, and with `apt` any of
   `iproute2`, `iptables`, `e2fsprogs` and `libcap2-bin` that are missing.
   Say no and exe runs the desktop without VMs; `exe setup vms` does this
   step later. A Mac is not asked: its VMs need nothing set up.

Then it starts exe as a service of your own user and prints the desktop's
address — and the token, if there is one. On Linux that is a systemd user
unit (`systemctl --user status exe`, `journalctl --user -u exe`), with
lingering turned on so it runs with nobody logged in and starts at boot.
On a Mac it is a launchd agent; see [On a Mac](#on-a-mac).

| What | Where |
|---|---|
| The binary | `~/.local/bin/exe` |
| Configuration, VMs, Workspace, app data | `~/.exe` |
| The service, Linux | `~/.config/systemd/user/exe.service` |
| The service, macOS | `~/Library/LaunchAgents/com.v2core.exe.plist` |
| The network helper (Linux VM step) | `/usr/local/libexec/exe-net-helper` |
| Firecracker (Linux VM step) | `/usr/local/bin/firecracker` |

Run again over an install, the installer keeps `~/.exe/config.json` as it
is and asks only about what is not there yet.

**With nobody at a keyboard** (a provisioning script, `ssh host 'curl … | sh'`)
it asks nothing and installs for this machine only, with the apps and
without the VM step. Each answer can be given ahead of time:

```sh
curl -fsSL https://exe.v2core.com/install.sh |
  EXE_INSTALL_LISTEN=tailscale EXE_INSTALL_TOKEN=yes EXE_INSTALL_VMS=yes sh
```

| Variable | Values |
|---|---|
| `EXE_INSTALL_LISTEN` | `local`, `all`, `tailscale` |
| `EXE_INSTALL_TOKEN` | `yes`, `no` (`all` always has one) |
| `EXE_INSTALL_APPS` | `yes`, `no` |
| `EXE_INSTALL_VMS` | `yes`, `no` (Linux, Windows) — without a keyboard, Linux needs sudo to work without a password |
| `EXE_INSTALL_VM_DRIVE` | a drive letter (Windows): the drive that holds the VMs |
| `EXE_API_TOKEN` | the token to use, instead of a generated one |

**Updating.** In the desktop, **Apple menu → Software Update…** shows the
newest release and what changed in it, and moves this machine to it; a
diamond by the Apple says when one is waiting. From a shell,
`exe update` moves an installed exe to the latest release:
it downloads it, checks it, and replaces the binary. The running daemon
goes on as it was until it restarts, and a restart stops and starts every
VM, so `exe update` asks first (`-y` does not ask; `-check` only says
whether a newer release exists). The extra apps follow the release, except
one you edited or removed. The network helper belongs to root, so when a
release changes it, `exe update` prints the two `sudo` lines that install
the new one. `exe version` says which release this is. A release is named
for its date: `2026.10.09`.

**Removing.** `exe uninstall` stops the service and removes the binary,
the service and the apps it installed. Everything in `~/.exe` that is
yours — the configuration, VMs, Workspace, app data — stays; delete the
folder to remove that too. On Linux it prints the `sudo rm` for the
network helper.

## On a Mac

The release is one binary for each processor, signed with a Developer ID
and notarized by Apple, so macOS runs it whether it came through the
one-liner or was downloaded by hand. Nothing in the install uses sudo.

- **It starts when you log in**, not at boot: the agent runs in your login
  session, which is where its menu-bar item is. Installed over SSH with
  nobody logged in at the screen, it starts at the next login.
- **The menu-bar item** opens the desktop, restarts the daemon and quits
  it. Quit stays quit until the next login, or
  `launchctl kickstart gui/$(id -u)/com.v2core.exe`.
- **The first VM brings an alert.** Since macOS 15 a program has to be
  allowed to reach the local network, and that is where exe's VMs are.
  The first time one starts, macOS asks to let exe find devices on your
  local network: choose Allow. Until then the VM runs but exe cannot
  reach it, and `exe create` says so. The switch is in System Settings →
  Privacy & Security → Local Network.
- **Its logs** are `~/.exe/daemon.log`, as everywhere, and
  `~/.exe/launchd.log` for anything it printed before it could log.
- **On an Intel Mac** the installer and the desktop are the same. Its VMs
  have not been tried yet: the Intel build was only run on Apple silicon,
  under Rosetta, where macOS offers no virtualization.

## On Windows

```powershell
irm https://exe.v2core.com/install.ps1 | iex
```

For Windows 11 on x86-64, typed into PowerShell; Windows 10 from version
1809 has what it uses, and has not been tried. It installs for you alone
and needs no administrator, except for the VM step. The first three
questions are the ones above. Two more follow:

- **Set this PC up to run VMs?** Asked when something is missing, and it
  lists what it would run: turning on the **Windows Hypervisor Platform**
  and the **Virtual Machine Platform**, and installing QEMU with `winget`.
  This is the one step that needs an administrator: from a terminal that
  is not one, Windows asks you to allow it. Windows may want a restart
  before the features work, and the installer says so. Say no and exe
  runs the desktop without VMs; `exe setup vms` does this step later.
- **Which drive should hold the VMs?** Asked on a PC that will run them
  and has more than one drive. It lists your fixed drives with their free
  space and what they are — an SSD, a hard disk, a disk that is somewhere
  else on the network — and suggests your home folder's drive when it has
  40 GB free, otherwise the drive in this PC with the most room. A base
  image is 3 GB and a VM takes up to its disk size, so a nearly full `C:`
  is a poor home for them. Any drive but your home folder's is written to
  the [configuration](/docs/config) as `vm_dir`: the VMs go to `X:\exe`
  and everything else stays in your home folder.

| What | Where |
|---|---|
| The binary | `%LOCALAPPDATA%\Programs\exe\exe.exe`, on your `PATH` in every terminal opened afterwards |
| Configuration, Workspace, app data | `%USERPROFILE%\.exe` |
| VMs and base images | `%USERPROFILE%\.exe`, or `X:\exe` on the drive you chose |
| What starts it | the value `exe` under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` |

- **It starts when you sign in**, with no window: Windows has no service
  of a user's own, so exe is a program of your session. Installed over
  SSH with nobody signed in at the PC, it starts at the next sign-in.
  `exe daemon start` starts it by hand, `exe daemon restart` restarts it.
- **Signing out stops it, and its VMs.** exe writes down which VMs were
  running, and they start again when you sign in.
- **The release is not signed.** Fetched by PowerShell, Windows runs it
  without a question, and Microsoft Defender found nothing in it.
- **Updating** is `exe update`, as everywhere. Windows does not let a
  running program be replaced, so the old binary is moved aside as
  `exe.exe.old` and removed by the next update.
- **Removing** is `exe uninstall`. The VMs stay where they are, and it
  says where.
- **With nobody at a keyboard** it asks nothing, as above;
  `EXE_INSTALL_VM_DRIVE` names the drive.
- **Its log** is `%USERPROFILE%\.exe\daemon.log`.
- Windows on ARM has no release: exe's VMs there are x86-64 only.

# Linux requirements

The installer's VM step does what this section describes; the rest of it
is for a checkout.


- An amd64 or arm64 host with hardware virtualization and `/dev/kvm`.
- Firecracker on `PATH`, plus `ip`, `iptables`, `debugfs`, and `resize2fs`.
- The daemon user must belong to the `kvm` group. Restrict the network helper's
  execute permission to root and the daemon user's group.
- The root-owned `exe-net-helper` must have only `CAP_NET_ADMIN`. It
  validates private /30 subnets, deterministic `exe-*` TAP names, the caller
  uid, and outbound interface names. Privileged child tools are resolved only
  from root-owned system directories and run with a minimal environment.

For the systemd deployment in this repository (a user unit, so the daemon
restarts, reports status and shows its journal without sudo):

```sh
sudo usermod -aG kvm livid
make build
sudo install -o root -g livid -m 0750 exe-net-helper /usr/local/libexec/exe-net-helper
sudo setcap cap_net_admin=ep /usr/local/libexec/exe-net-helper
sudo loginctl enable-linger livid
install -m 0644 deploy/systemd/exe.service ~/.config/systemd/user/exe.service
systemctl --user daemon-reload
systemctl --user enable --now exe
```

Install Firecracker from its official release archive before starting the
service. Reapply `setcap` whenever the helper binary is replaced. The
checked-in unit is specific to `/www/exe` and `/home/livid/.exe`.

After `make build`, `systemctl --user restart exe` runs the new binary;
`journalctl --user -u exe` is the log. The daemon stops its VMs on the way
down and starts them again on the way up, and the unit's `KillMode=process`
leaves the tmux servers behind the Terminal, Claude Code and Codex windows
running across the restart. The restart endpoint (`POST
/v1/daemon/restart`, the desktop's Restart item) does the same under
systemd: it exits and lets the manager start the new binary.

At boot the daemon may come up before Tailscale has its address. It waits
up to five minutes for `listen` (likewise `proxy_listen` and `ssh_listen`)
to become bindable, and exits non-zero after that so systemd starts it
again.

# Windows requirements

The installer's VM step does the first two of these for you
([On Windows](#on-windows)); a build from a checkout needs them done by
hand.

- An x86-64 host. Enable **Windows Hypervisor Platform** and **Virtual
  Machine Platform** under *Windows features* (available on Windows Home
  too), then reboot.
- QEMU: `winget install SoftwareFreedomConservancy.QEMU`, or set
  `qemu.binary` if it is not on `PATH` or in `C:\Program Files\qemu`.
- No admin rights, drivers or TAP devices: the guest network is a userspace
  TCP/IP stack inside the daemon. VM IPs (default `192.168.127.0/24`) exist
  only inside `exe serve` — the SSH gate, web terminal, agent and reverse
  proxy all reach them, and the Services tab publishes per-port
  `127.0.0.1` forwards for your browser.
- Consider excluding `%USERPROFILE%\.exe` from Microsoft Defender scanning;
  first boots are much faster.

# Running without VMs (a NAS, a container)

The daemon only needs a hypervisor for the VMs. When the Linux backend
cannot start because Firecracker, its network helper, `debugfs` or
`resize2fs` is missing (or the CPU is one Firecracker does not support),
`exe serve` logs why and runs without VMs: the desktop, Workspace, apps,
the Terminal, Claude Code and Codex windows, the Hub, Chat and Mac OS 9 all
work, the VM list is empty, About This Computer says why, and every VM call
answers `503` with the reason. A bad configuration or a busy state
directory still stops the daemon.

`make cross` builds static `dist/exe-linux-amd64` and `dist/exe-linux-arm64`
(plus the network helpers) with cgo off, so the binary runs on any Linux
userland with no Go installed. Point `EXE_HOME` at a writable directory
when the daemon user has no home. Synology DSM is one such target: the
Intel Plus and xs models are amd64, the Realtek models are arm64, and
Container Manager runs a plain Linux image on both; the x86 models that
run Synology's own Virtual Machine Manager have `/dev/kvm`, which a
container would need passed through (plus `/dev/net/tun` and
`CAP_NET_ADMIN`) before Firecracker could work there. That last step is
untested.
