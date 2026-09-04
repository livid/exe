# exe — a personal VM cloud

A single Go binary inspired by [exe.dev](https://exe.dev): create persistent Linux VMs on
macOS, Linux or Windows, vibecode inside them with models from Ollama Cloud, and publish
any VM port to a real HTTPS subdomain through your Cloudflare Tunnel. macOS
uses Virtualization.framework; Linux uses KVM through Firecracker; Windows
uses QEMU on the Windows Hypervisor Platform.

![The web UI — a Mac OS 9 Platinum desktop: sortable VM list and an SSH terminal into a VM](docs/screenshot.png)

```
phone/laptop ──► exe API (bind to Tailscale IP)
                    │
                    ├── VMs  (Virtualization.framework or Firecracker, cloud-init, SSH)
                    │     └── agent: Ollama (glm-5.2, …) drives bash/write/read over SSH
                    │
                    ├── SSH gate :2222  (ssh exe@mac = lobby, ssh <vm>@mac = the VM)
                    │
                    └── reverse proxy :8090  ◄── cloudflared tunnel (LAN)  ◄── https://app.your.domain
                              └── Host header ──► VM_IP:PORT
```

## Quick start

```sh
make build        # also builds exe-net-helper on Linux
                  # Windows: go build -o exe.exe ./cmd/exe
./exe init        # writes ~/.exe/config.json
./exe serve       # run the daemon

./exe create demo               # clone Debian 13, boot, cloud-init, SSH ready
./exe ssh demo                  # log in (key in ~/.exe/ssh/)
./exe code demo "build me a guestbook app on port 8000"
./exe expose demo -port 8000 -sub guestbook   # -> https://guestbook.<domain>
```

The first `create` downloads the Debian 13 `genericcloud` raw image (~3 GB) once
into `~/.exe/images/`. Linux also downloads the configured direct-boot kernel.

### Linux requirements

- An amd64 or arm64 host with hardware virtualization and `/dev/kvm`.
- Firecracker on `PATH`, plus `ip`, `iptables`, `debugfs`, and `resize2fs`.
- The daemon user must belong to the `kvm` group. Restrict the network helper's
  execute permission to root and the daemon user's group.
- The root-owned `exe-net-helper` must have only `CAP_NET_ADMIN`. It
  validates private /30 subnets, deterministic `exe-*` TAP names, the caller
  uid, and outbound interface names. Privileged child tools are resolved only
  from root-owned system directories and run with a minimal environment.

For the Supervisor deployment in this repository:

```sh
sudo usermod -aG kvm livid
make build
sudo install -o root -g livid -m 0750 exe-net-helper /usr/local/libexec/exe-net-helper
sudo setcap cap_net_admin=ep /usr/local/libexec/exe-net-helper
sudo install -m 0644 deploy/supervisor/exe.conf /etc/supervisor/conf.d/exe.conf
sudo supervisorctl reread
sudo supervisorctl update
```

Install Firecracker from its official release archive before starting the
service. Reapply `setcap` whenever the helper binary is replaced. The
checked-in Supervisor config is specific to `/www/exe` and
`/home/livid/.exe`.

At boot the daemon may come up before Tailscale has its address. It waits
up to five minutes for `listen` (likewise `proxy_listen` and `ssh_listen`)
to become bindable, and exits non-zero after that so Supervisor starts it
again.

### Windows requirements

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

## SSH as an interface (:2222)

Like [exe.dev](https://exe.dev) (`ssh exe.dev`), the daemon speaks SSH on
`:2222`. The SSH **username** picks where you land:

```sh
ssh -p 2222 exe@mac               # the lobby: an interactive REPL for VM lifecycle
ssh -p 2222 exe@mac ls --json     # one-shot commands, JSON for scripts/agents
ssh -p 2222 exe@mac new           # create + boot a VM (invents a name like fuzzy-otter)
ssh -p 2222 exe@mac code demo "add a /healthz endpoint"   # vibecode, streamed

ssh -p 2222 demo@mac              # full SSH *into* the VM (auto-starts it)
scp -P 2222 app.py demo@mac:~/    # scp, sftp, -L/-R port forwarding all pass through
ssh -p 2222 -L 8000:localhost:8000 demo@mac   # tunnel a VM port to your laptop
```

Lobby commands: `help`, `ls`, `new`, `start`, `stop`, `rm`, `ip`,
`code <vm> <prompt>`, `expose <vm> <port> [sub]`, `unexpose`, `routes` —
`--json` where it matters. The lobby is commands-only (no scp/sftp there);
`ssh <vm>@mac` is a transparent bridge to the VM's sshd, so everything works
there. The name `exe` is reserved for the lobby.

Who gets in: the service key (`~/.exe/ssh/id_ed25519`), any of the daemon
user's `~/.ssh/*.pub`, and keys listed in `~/.exe/ssh/authorized_clients`
(authorized_keys format — add your phone's key there; edits apply
immediately). The gate's host key lives at `~/.exe/ssh/host_ed25519`.
Configure the address with `ssh_listen` (`"off"` disables).

## Web UI

The daemon serves a single-page UI at `http://<listen>/` (default
http://127.0.0.1:7777). It can:

- list VMs with live state, and create / start / stop / delete them
- open a full SSH terminal in the browser (xterm.js over WebSocket, embedded in the binary)
- see web services listening inside a VM with one-click links
- vibe code inside a VM with streaming agent output
- browse every vibe-code transcript (CLI runs are recorded too, under
  `~/.exe/vms/<name>/transcripts/`)
- expose a VM port to your domain
- read and post to an exe-hub feed, and let an agent identity answer replies there (`hub.*`)
- edit the full configuration (saved to `~/.exe/config.json` and hot-reloaded;
  fields marked `*` need a daemon restart)

If `api_token` is set, paste it into the token field in the header (stored in
localStorage). Bind `listen` to your Tailscale IP to use the UI from your phone.

## Agent skill guide (/skill.md)

The daemon also serves `http://<listen>/skill.md`
([source](internal/server/skill.md)): a self-contained guide any coding agent
(Claude Code, Codex, opencode, …) can fetch to learn the API — VM lifecycle
over HTTP, running commands over the SSH gate, service discovery, and
exposing ports. Point an agent at that URL (plus the `api_token` if set) and
it can drive your VMs; the file also works dropped into a skills directory
as-is.

While `exe serve` runs it also puts an icon in the macOS menu bar: **Open Web
UI**, **Restart Daemon** (running VMs are brought back automatically), and
**Quit exe** (asks for confirmation, then shuts down running VMs and the
daemon). In headless sessions (ssh) the daemon runs without the icon.

## Configuration (~/.exe/config.json)

| key | meaning |
|---|---|
| `listen` | API address. Defaults to your Tailscale IP when one is detected (e.g. `100.120.160.126:7777`) so you can drive it from your phone, otherwise `127.0.0.1:7777`. When bound to a specific non-loopback IP, the API also stays on `127.0.0.1:<port>` |
| `proxy_listen` | reverse-proxy address the tunnel forwards to (default `:8090`) |
| `ssh_listen` | SSH gate address (default `:2222`): `ssh -p 2222 exe@mac` = lobby, `ssh -p 2222 <vm>@mac` = the VM. `"off"` disables |
| `advertise_host` | this Mac as reachable **from the cloudflared host** — LAN IP (e.g. `192.168.1.131`) or Tailscale IP (pre-filled with the Tailscale IP when detected) |
| `api_token` | if set, every API call needs `Authorization: Bearer <token>`. Set it before binding beyond localhost |
| `ssh_user` | user created in each VM (default `dev`, passwordless sudo) |
| `image_url` | base image; macOS accepts raw cloud images, while Linux accepts a raw ext4 filesystem or a GPT image containing an ext4 root partition |
| `firecracker.binary` | Linux Firecracker executable (default `firecracker` from `PATH`) |
| `firecracker.kernel_url` | Linux direct-boot kernel URL, selected for the host architecture |
| `firecracker.network_helper` | root-owned, capability-limited helper path |
| `firecracker.network_cidr` | private IPv4 pool divided into one /30 per VM (default `172.30.0.0/16`) |
| `firecracker.outbound_interface` | Linux egress interface; empty auto-detects the default IPv4 route |
| `qemu.binary` | Windows QEMU executable (default `qemu-system-x86_64` from `PATH`, then `C:\Program Files\qemu`) |
| `qemu.firmware_dir` | folder with `edk2-x86_64-code.fd` + `edk2-i386-vars.fd`; empty auto-detects QEMU's `share` dir |
| `qemu.network_cidr` | Windows in-process guest subnet (default `192.168.127.0/24`); the daemon is the gateway |
| `ollama.base_url` | `http://127.0.0.1:11434` to go through your signed-in local Ollama (cloud models like `glm-5.2:cloud` need no key), or `https://ollama.com` + `ollama.api_key` |
| `ollama.model` | default agent model, e.g. `glm-5.2:cloud` |
| `cloudflare.*` | see below |
| `hub.url` | the exe-hub this node's agent watches, e.g. `http://100.64.0.2:7788` |
| `hub.agent.key` | the agent's own ed25519 key (PKCS8 PEM) — a separate identity from the node's peer key; empty keeps the agent off |
| `hub.agent.answer` | hub profile ids whose replies the agent answers under its own posts; nobody else's text reaches the model |
| `hub.agent.model`, `hub.agent.repos`, `hub.agent.max_per_day`, `hub.agent.max_per_thread` | the Claude model that writes replies (run through the Claude Code CLI with every tool disabled), local checkouts whose commit subjects it may cite, and how much it may say |

Secrets can also come from `OLLAMA_API_KEY`, `CLOUDFLARE_API_TOKEN`, `EXE_API_TOKEN`.

## Cloudflare setup (one time)

1. Use a **remotely-managed** tunnel (created in Zero Trust → Networks → Tunnels).
   `exe expose` edits its ingress rules via API; a locally-managed tunnel
   (config.yml on the cloudflared host) can't be updated this way — for those,
   add one catch-all ingress `*.your.domain -> http://<advertise_host>:8090`
   by hand instead, and exe will still manage DNS + routing.
2. API token with **Zone → DNS → Edit** and **Account → Cloudflare Tunnel → Edit**.
3. Fill `cloudflare.api_token`, `account_id`, `zone_id`, `tunnel_id`, `domain`.

`exe expose <vm> -port N [-sub name]` then:
creates/updates the CNAME `<sub>.<domain>` → `<tunnel>.cfargotunnel.com`,
upserts a tunnel ingress rule `<sub>.<domain>` → `http://<advertise_host>:8090`,
and routes that hostname in the local proxy to `http://<vm_ip>:N`.

## How VMs work

The default image is Debian 13 `genericcloud`. Each VM gets a persistent sparse
disk, the `dev` user, and the service SSH key through cloud-init NoCloud.

On macOS, VMs use EFI and virtio disk/net/console/entropy through
Virtualization.framework. NAT comes from the shared macOS DHCP (`bootpd`);
IPs are discovered from `/var/db/dhcpd_leases`, matching by MAC or, for
DUID-identifying clients (Debian 13's dhcpcd), by lease name with a pre-boot
snapshot to skip stale entries.

On Linux, exe extracts the ext4 root partition from the default GPT cloud
image, resizes it offline, and uses it as Firecracker's single `/dev/vda` root
drive. It injects persistent systemd-networkd and NoCloud data directly into
that filesystem, then direct-boots the configured kernel and connects a per-VM
TAP to host NAT. Custom images must either be raw ext4 filesystems or GPT images
with an ext4 root partition, and must support the configured kernel. Firecracker
serial output is at `~/.exe/vms/<name>/console.log`.

On Windows, VMs are `qemu-system-x86_64 -accel whpx` child processes booting
the same EFI image as macOS (per-VM EFI variable store, virtio disk/net/rng,
seed.iso, serial to `console.log`, QEMU messages to `qemu.log`). The guest
network is a gvisor netstack inside the daemon: each VM's virtio NIC streams
ethernet frames to the daemon over loopback, a built-in DHCP server hands out
one deterministic address per VM (MACs are derived from the address), and the
daemon NATs guests outbound through the host sockets API. A kill-on-close job
object powers VMs off if the daemon dies. Custom images must be full GPT disk
images with an EFI partition.

VMs are children of `exe serve`. An exclusive state lock prevents two Linux
daemons from managing the same VM directory. Graceful daemon shutdown powers
VMs off; parent-death handling terminates Firecracker if the daemon crashes,
and the next daemon startup removes stale per-VM TAP and firewall state. Disks
persist and `exe start` boots them again.

## Security notes

- VMs have private host-local addresses; the proxy is what exposes their services.
- Set `api_token` before binding the API beyond localhost.
- The agent has passwordless sudo **inside the VM** — that's the sandbox boundary.
- On Linux, the daemon and Firecracker are unprivileged. Only the validated
  network helper has `CAP_NET_ADMIN`; it is root-owned and not daemon-writable.
- The SSH gate accepts only keys it already knows (service key, your
  `~/.ssh/*.pub`, `~/.exe/ssh/authorized_clients`) — there is no
  first-come key adoption, so it's safe to leave on `:2222` on a LAN.

## Roadmap / ideas

- `exe unexpose` currently leaves the Cloudflare DNS record + ingress rule in place.
- Snapshots (`SaveMachineStateToPath` is already in vz), memory ballooning, virtiofs shares.
- Auto-restart VMs that were running when the daemon exited.
