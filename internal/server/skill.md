---
name: exe-vms
description: >
  Create and drive persistent Linux VMs on this host through the exe daemon:
  full VM lifecycle over HTTP, command execution and file transfer over SSH
  (port 2222), service discovery, and publishing VM ports to HTTPS subdomains.
  Use whenever you need an isolated Linux sandbox to build, run, or test
  something, or to work on an existing exe VM.
---

# exe — drive Linux VMs from any agent

exe is a single-binary personal VM cloud running on this host. It boots
persistent Debian VMs (Virtualization.framework on macOS, Firecracker/KVM on
Linux), gives each one SSH, and can publish any VM port to a real HTTPS
subdomain. You (an agent) can use it as an isolated sandbox: create a VM,
run commands inside it, install anything, start servers, and expose them.

**Base URL**: the scheme://host:port you fetched this file from
(default `http://127.0.0.1:7777`). Examples below use `$BASE` and, for SSH,
`$HOST` (the same host, without scheme/port).

## Auth

If the daemon has `api_token` set, every `/v1/*` request needs
`Authorization: Bearer <token>`. WebSocket endpoints also accept
`?token=<token>` as a query parameter. If no token is configured, calls work
without it. `GET /healthz` is always open and returns `ok`.

```sh
curl -s -H "Authorization: Bearer $TOKEN" $BASE/v1/vms
```

Errors are JSON: `{"error":"..."}`. `404` = no such VM, `409` = VM not in the
required state (usually: not running — start it first), `400` = bad request.

## The VM object

```json
{
  "name": "demo",
  "state": "running",        // "running" | "stopped"
  "cpus": 2,
  "memory_mb": 2048,
  "disk_gb": 20,
  "mac": "aa:bb:cc:dd:ee:ff",
  "ip": "172.30.0.2",        // present while running; host-local private IP
  "created_at": "2026-08-06T12:00:00Z"
}
```

Inside every VM: Debian 13, a user named `dev` (configurable as `ssh_user`,
check `GET /v1/config`) with passwordless sudo, and the daemon's SSH key
installed. The VM is the sandbox boundary — you may install packages and run
anything inside it.

## VM lifecycle

| Method & path | Body | Effect |
|---|---|---|
| `GET /v1/vms` | — | List all VMs (array of VM objects) |
| `POST /v1/vms` | `{"name":"demo","cpus":2,"memory_mb":2048,"disk_gb":20}` | Create **and boot**; only `name` is required (lowercase letters, digits, `-`), the rest defaults to 2 CPU / 2048 MB / 20 GB |
| `GET /v1/vms/{name}` | — | One VM |
| `POST /v1/vms/{name}/start` | — | Boot a stopped VM |
| `POST /v1/vms/{name}/stop` | — | Graceful power-off (disk persists) |
| `DELETE /v1/vms/{name}` | — | Delete VM **and its disk** |
| `GET /v1/vms/{name}/notes` | — | Free-form notes about the VM → `{"notes":"..."}` — read them before working in an unfamiliar VM |
| `PUT /v1/vms/{name}/notes` | `{"notes":"..."}` | Replace the notes (markdown welcome; also editable in the web UI) |

`POST /v1/vms` and `/start` are **synchronous**: they return only when the VM
is booted, has an IP, and SSH answers — the returned object is immediately
usable. Typical boot is seconds, but the **first create ever downloads a ~3 GB
base image**, so allow a generous client timeout (10+ minutes) or pre-check
that other VMs already exist.

```sh
curl -s -X POST $BASE/v1/vms -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"demo"}'                      # → 201 + VM object with .ip
```

Don't delete or stop VMs you didn't create unless the user asked.

## Run commands inside a VM (SSH gate, port 2222)

The daemon speaks SSH on port `2222` of the same host. The SSH **username
selects the destination**:

- `ssh -p 2222 <vm>@$HOST` — transparent bridge to that VM's sshd (lands as
  the `dev` user, auto-starts a stopped VM). Non-interactive commands, scp,
  sftp, and `-L`/`-R` port forwarding all work.
- `ssh -p 2222 exe@$HOST <cmd>` — the "lobby": VM lifecycle commands with
  `--json` output (`ls`, `new`, `start`, `stop`, `rm`, `ip`, `code`,
  `expose`, `routes`). Equivalent to the HTTP API; use whichever is easier.

```sh
ssh -p 2222 demo@$HOST 'uname -a'                      # run a command
ssh -p 2222 demo@$HOST 'sudo apt-get install -y jq'    # sudo just works
scp -P 2222 app.py demo@$HOST:~/                       # copy files in
ssh -p 2222 -L 8000:localhost:8000 demo@$HOST          # tunnel a VM port
ssh -p 2222 exe@$HOST ls --json                        # lobby, JSON list
```

Access requires your SSH public key to be known to the daemon: keys in the
daemon user's `~/.ssh/*.pub`, the service key `~/.exe/ssh/id_ed25519`, or any
key listed in `~/.exe/ssh/authorized_clients` (authorized_keys format; edits
apply immediately). If you run as the same user as the daemon, plain `ssh`
works out of the box. Use `-o StrictHostKeyChecking=accept-new` on first
contact.

VM IPs (`.ip`) are private host-local addresses: from **this host** you can
also `curl http://<vm_ip>:<port>` directly — useful for testing a server you
just started in the VM. Exception: on Windows hosts VM IPs live inside the
daemon process; use the per-port `local` addresses from
`GET /v1/vms/{name}/ports` (e.g. `curl http://127.0.0.1:53422`) instead.

## Built-in coding agent (vibecode)

`POST /v1/vms/{name}/agent` runs exe's own Ollama-backed coding agent inside
the VM (it gets bash/read/write tools over SSH) and **streams plain text**
until done. VM must be running.

```sh
curl -sN -X POST $BASE/v1/vms/demo/agent -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"build a guestbook app on port 8000","model":""}'
```

`model` empty = configured default. Closing the connection cancels the run.
Every run is recorded: `GET /v1/vms/{name}/transcripts` lists
`{id,prompt,model,status,...}`; `GET /v1/vms/{name}/transcripts/{id}` returns
`{meta, log}`. If you are a coding agent yourself, you usually want the SSH
gate instead and only reach for this to delegate.

## Discover services, publish to the web

- `GET /v1/vms/{name}/ports` → `{"ip":"...","ports":[{"port":8000,"process":"python3"}]}` —
  TCP ports listening on non-loopback addresses inside the VM (SSH excluded).
  Servers must bind `0.0.0.0`, not `127.0.0.1`, to be reachable/exposable.
  On Windows hosts each entry also carries `"local":"127.0.0.1:NNNN"`, a
  host-reachable forward to that port.
- `POST /v1/vms/{name}/expose` body `{"port":8000,"subdomain":"guestbook"}` →
  `{"host":"guestbook.<domain>","url":"https://...","backend":"http://<vm_ip>:8000"}`.
  Publishes the VM port through the daemon's reverse proxy and Cloudflare
  Tunnel. `subdomain` defaults to the VM name. Requires Cloudflare to be
  configured (`cloudflare.domain` in `GET /v1/config`); a `warnings` array in
  the response means it only partially applied.
- `GET /v1/routes` → `{"host": "backend-url", ...}` current published routes;
  `DELETE /v1/routes/{host}` unpublishes one.

## Interactive terminal (WebSocket)

`GET /v1/vms/{name}/terminal` upgrades to a WebSocket bridged to a shell in
the VM: **binary** frames carry terminal bytes both ways; **text** frames
carry `{"resize":[cols,rows]}`. Auth via `?token=` if needed. Prefer plain
`ssh -p 2222` unless you specifically need a browser-style PTY stream.

## Other endpoints

`GET /v1/config` (full daemon config, incl. `ssh_user` and cloudflare setup),
`GET /v1/logs` (streams daemon log).
`POST /v1/vms/{name}/publish` body `{"path":"/home/dev/app","repo":"app","private":true}`
publishes a VM folder to the signed-in user's GitHub; the daemon holds the
token and pushes for the VM. `repo` is optional: omitted, the daemon reuses
the folder's `origin` remote name when it points at the signed-in account
(so publishing again updates the same repo), else falls back to the folder
name; it errors if `origin` points at a different account. Streams NDJSON
events — `{"type":"step","text":"..."}` lines, ending in
`{"type":"done","repo":"...","url":"..."}` (the repo URL) or
`{"type":"error","error":"..."}`. **Only when the user asks** — it can
create a repository on their account.
`POST /v1/newsfeed` body `{"title":"...","body":"..."}` (optional `"kind"`,
default `"note"`) posts a note to the desktop Newsfeed of this node **and
every joined node** — good for announcing finished work or problems the user
should see.
Endpoints not listed in this file
(config PUT, daemon restart, chat, workspace, apps, ui state) back exe's own
web UI — leave them alone unless the user explicitly asks.

## Recipes

**Fresh sandbox, run a server, verify, publish:**

```sh
BASE=http://127.0.0.1:7777; HOST=127.0.0.1
AUTH=(-H "Authorization: Bearer $TOKEN")               # omit if no token

curl -s "${AUTH[@]}" -X POST $BASE/v1/vms -d '{"name":"scratch"}'
ssh -p 2222 scratch@$HOST 'sudo apt-get update -qq && sudo apt-get install -y -qq python3'
scp -P 2222 server.py scratch@$HOST:~/
ssh -p 2222 scratch@$HOST 'nohup python3 server.py >server.log 2>&1 & sleep 1; curl -s localhost:8000/healthz'
curl -s "${AUTH[@]}" $BASE/v1/vms/scratch/ports        # confirm 8000 is listening
curl -s "${AUTH[@]}" -X POST $BASE/v1/vms/scratch/expose -d '{"port":8000}'
```

**Reuse an existing VM:** `GET /v1/vms`, pick by name, `POST .../start` if
`"state":"stopped"` (synchronous), then SSH in.

**Clean up when the user is done:** `POST .../stop` keeps the disk;
`DELETE` destroys it — confirm with the user before deleting.
