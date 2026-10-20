# Configuration

Everything exe knows about this machine is in `~/.exe/config.json`,
written by `exe init` and editable from the desktop's Configuration
window. Most of it is hot-reloaded; the fields marked `*` in the window
need a restart.

| key | meaning |
|---|---|
| `listen` | API address. Defaults to your Tailscale IP when one is detected (e.g. `100.120.160.126:7777`) so you can drive it from your phone, otherwise `127.0.0.1:7777`. When bound to a specific non-loopback IP, the API also stays on `127.0.0.1:<port>` |
| `proxy_listen` | reverse-proxy address the tunnel forwards to (default `:8090`) |
| `ssh_listen` | SSH gate address (default `:2222`): `ssh -p 2222 exe@mac` = lobby, `ssh -p 2222 <vm>@mac` = the VM. `"off"` disables |
| `advertise_host` | this Mac as reachable **from the cloudflared host** — LAN IP (e.g. `192.168.1.131`) or Tailscale IP (pre-filled with the Tailscale IP when detected) |
| `api_token` | if set, every API call needs `Authorization: Bearer <token>`. Set it before binding beyond localhost. The desktop asks for it once in each browser; `exe token` prints it |
| `vm_dir` | where the VMs' disks and the base images are kept: its `vms` and `images` folders. Empty (the default) is the state folder, `~/.exe`. Set it to put the one large part of exe on another drive: `/data/exe`, or on Windows `G:\\exe` (the backslash doubled, as JSON wants). Read at start; to move VMs that exist, stop exe and move the two folders there |
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

Secrets can also come from `OLLAMA_API_KEY`, `CLOUDFLARE_API_TOKEN`, `EXE_API_TOKEN`.

# Cloudflare setup (one time)

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

To redirect the zone apex or a full subdomain without a VM:

```sh
exe expose example.com -redirect https://exe.example.com
exe expose www.example.com -redirect https://exe.example.com
```

This publishes DNS and tunnel ingress the same way, but the daemon answers
with **308 Permanent Redirect**. Paths and query strings are preserved;
the target must be an HTTP(S) origin without a path, query, fragment or
credentials. The source must be the configured domain or a subdomain.
Use `exe routes` to inspect redirects and `exe unexpose <host>` to remove one.
