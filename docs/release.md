# Releases

exe is released for Linux and macOS, x86-64 and ARM64 each, and for
Windows on x86-64. A release is what the one-line installer and
`exe update` fetch:

```sh
curl -fsSL https://exe.v2core.com/install.sh | sh    # Linux, macOS
irm https://exe.v2core.com/install.ps1 | iex         # Windows, in PowerShell
```

Windows is in every release since 2026.10.10.2; the first, 2026.10.10, has
no Windows build.

- **Where.** GitHub Releases of `livid/exe` holds the files and says which
  release is the latest. Nothing is pinned in Kubo; the hub only carries
  the announcement.
- **When.** Only when Livid says "release a new version". Publishing
  pushes `main` and a tag and cannot be taken back: the repository has
  immutable releases on, so a published release's files and tag are
  locked. A bad release is followed by a new one.
- **Version.** The UTC date, `2026.10.09`; a second release that day is
  `2026.10.09.2`. The tag is the version. A binary built from a checkout
  has no version, and `exe update`, `exe setup` and `exe uninstall` refuse
  to act on it.

## What a release is

Nine files, with the same names in every release, so
`…/releases/latest/download/<name>` always works:

| File | Holds |
|---|---|
| `exe-linux-amd64.tar.gz`, `exe-linux-arm64.tar.gz` | `exe` and `exe-net-helper`, static |
| `exe-darwin-amd64.tar.gz`, `exe-darwin-arm64.tar.gz` | `exe`, signed for macOS 13 and later |
| `exe-windows-amd64.tar.gz` | `exe.exe`, static, not signed |
| `exe-apps.tar.gz` | the bundles of `/www/exe-apps` at its HEAD |
| `install.sh`, `install.ps1` | the installers, as the daemon serves them at `/install.sh` and `/install.ps1` |
| `SHA256SUMS` | the checksums of the eight |

A tenth file, `NOTES.md`, says what changed. It is words to show, so it is
not in `SHA256SUMS`; see "The notes" below.

Neither the installer nor `exe update` calls `api.github.com` (60 requests
an hour for an address). The latest version is read from where
`/releases/latest` redirects to, and files come from
`/releases/download/<tag>/<name>`.

## The notes

`NOTES.md`, at the top of the repository, is what the next release says
changed. Software Update shows it under "What changed in <version>" to
somebody deciding whether to press Update Now, in a box six lines high
and some fifty characters wide. So it is short, and written for them:

- **When.** A change that a user of a release will notice gets its line
  in the commit that ships it. Written at release time from the commit
  log, the notes come out as the log: long, and in the builder's words.
- **What.** Something new to use, something that now works differently,
  a fix for a fault a user could have met. Not a refactor, a test, a
  change to the docs, or anything only a checkout sees.
- **How.** One item (`- …`) a change: what the user gets and where it is,
  with the name of the thing to click in bold. The commit subject is not
  it. "**Join a Hub…** in the Hub app: four steps, from a hub's address to
  your profile." is.
- **How much.** At most eight items, of at most 110 characters each,
  which is two lines of the box. When a ninth turns up, the one a user
  would miss least goes. Details belong in the manual, and a release with
  more to say says it on its GitHub page (`publish`, below).

```sh
deploy/release.sh notes   # the next release's notes, or what is wrong with them
```

It refuses a file that is missing or names no change, more than eight
items, a line over 110 characters, and an item the last release already
had. `build` asks the same of the commit before it compiles anything, and
puts the notes into the release without the file's comment.

A release's notes cannot be changed once it is published, so they are
read where they will be shown, in the Software Update window of a machine
under test (below), before `publish`. To change them then: edit
`NOTES.md`, commit, build again.

After publishing, `publish` adds the notes to `CHANGELOG.md`, where every
release's are kept, newest first, under the release's name. Then it empties
`NOTES.md` to its comment and commits the two, so that what is written in
it from then on is the next release's. A release's section of
`CHANGELOG.md` is written by `publish` alone: a line for the next release
goes in `NOTES.md`.

`CHANGELOG.md` is in the binary too (`changelog.go`, at the top of the
repository because `go:embed` reaches no higher than its own folder), and
the site shows it as a page, https://exe.v2core.com/docs/changelog, without
the comment that speaks to its editors. The file is therefore written in
what both GitHub and the site's renderer read: headings, paragraphs, lists,
`code`, **bold**, and links whose address is whole. `build` adds the
release it is building to the copy of the file it compiles, the way
`publish` then adds it to the checkout's, so that a release's own page
begins with that release and not with the one before it.

## Build

```sh
deploy/release.sh build              # today's version, from HEAD
deploy/release.sh build 2026.10.09.2 # a version by name
```

This writes `dist/release/<version>/` and touches nothing else here. It
builds a clean export of the commit, so uncommitted files in the working
tree are not in it. It starts with the commit's `NOTES.md` and stops
there if the notes will not do ("The notes" above); a `--worktree` build
takes the file as it is, or goes without. The Linux, Windows and apps
tarballs are the same bytes whenever they are built from the same commits
with the same Go; the macOS ones carry the time they were signed.

The Windows binary is cross-built here like the Linux ones (no cgo, so no
Windows machine is needed to build it) and packed as a `.tar.gz` like the
rest: Windows has had `tar.exe` since 10 version 1803. It carries no
Authenticode signature. What that costs is under "Windows" below.

### The macOS binaries

They are built on a Mac, over ssh, because they link Apple's frameworks
through cgo (Virtualization.framework, the menu bar) and because their
signature is made there. The signature is not optional: Apple silicon
runs no unsigned code, and the entitlement that lets exe run VMs
(`vz.entitlements`) rides in it. One Apple silicon Mac builds both
processors.

`deploy/release.env` (not in the repository — it names machines) says
which Mac: `EXE_MAC_BUILDER=user@host`. That account needs:

- Go in `~/sdk/go`, and Xcode for `codesign`, `notarytool` and `clang`.
- `~/.exe-signing/pass`: the password of its login keychain, mode 600. A
  session over ssh has no unlocked keychain, and an unlock lasts only for
  the ssh command it is in.
- A Developer ID Application identity whose private key lets `codesign`
  sign without asking. A key made in Keychain Access or by Xcode asks at
  the screen for every new program, which an ssh session cannot answer
  (`errSecInternalComponent`): approve `codesign` once at the screen with
  Always Allow, or import the identity with `security import … -T
  /usr/bin/codesign`, and run `security set-key-partition-list -S
  apple-tool:,apple:,codesign: -s -k <password> <keychain>` once.
- Where "Developer ID Application" does not pick out one identity:
  `~/.exe-signing/identity` (its SHA-1) and `~/.exe-signing/keychain`
  (the keychain it is in).
- A notarytool profile named `exe-notary`
  (`xcrun notarytool store-credentials exe-notary`).

A build from a commit signs both binaries with the Developer ID, with the
hardened runtime and Apple's timestamp, and has Apple notarize them — one
zip, a few minutes. A bare binary cannot carry the notarization ticket
(there is nothing to staple it to), so a Mac that has to check one asks
Apple. It rarely has to: `curl` leaves no quarantine mark on what it
downloads, so an install through the one-liner is never checked; a tarball
downloaded with a browser is, and passes because of the notarization.

A `--worktree` build gives them an ad-hoc signature instead
(`EXE_MAC_SIGN=adhoc|developer-id` says otherwise). That runs on any Mac
through the installer and is what tests use; `publish` refuses it.
`EXE_MAC_BUILDER=none` leaves macOS out of a test build.

## Test it before it is published

`deploy/release-mirror.py` serves `dist/release/` in GitHub's layout, and
`EXE_RELEASE_URL` points the installer and `exe update` at it:

```sh
deploy/release-mirror.py --bind <this machine's tailnet address> --port 7796

# on the machine under test
export EXE_RELEASE_URL=http://<this machine>:7796/releases
curl -fsSL http://<this machine>:7796/install.sh | sh
exe update
```

The tailnet address is enough: every machine under test is on the tailnet,
a VM on this machine reaches it through its gateway, and the LAN is not
served an unpublished release.

On a Mac reached over SSH, give the installer's shell a locale the Mac
knows — `… | LC_ALL=en_US.UTF-8 sh`. SSH carries this machine's
`LANG=en_US.utf8` across, macOS has no locale of that spelling and falls
back to C, and a Mac's `sh` (bash 3.2) reads a script differently there
than in the UTF-8 locale every Terminal window has: both releases of
2026-10-10 passed on `birdie` that way while `echo "… $version…"` ended
the install for anyone at a Mac's own keyboard ("version?: unbound
variable" — the first byte of the ellipsis read as a letter of the name).

To test an update, build two versions and put the older one's name in
`dist/release/LATEST` before installing; change the file to move "the
latest release" on. `deploy/release.sh build --worktree <version>` builds
the files on disk rather than a commit, for testing something not yet
committed; `publish` refuses such a build.

**Software Update** (the desktop's Apple menu) is `exe update` run by the
daemon, so it reads the release address from the daemon's environment, not
from a shell's. To try it against the mirror, give the service the
variable and restart it — on Linux a drop-in:

```sh
mkdir -p ~/.config/systemd/user/exe.service.d
printf '[Service]\nEnvironment=EXE_RELEASE_URL=http://<this machine>:7796/releases\n' \
    > ~/.config/systemd/user/exe.service.d/mirror.conf
systemctl --user daemon-reload && systemctl --user restart exe
```

On a Mac the service is the launchd agent, and `launchctl setenv` does not
reach it: add the variable to `EnvironmentVariables` in
`~/Library/LaunchAgents/com.v2core.exe.plist` (keep the comment that marks
the file as the installer's), then `launchctl bootout gui/$(id -u)/com.v2core.exe`
and `launchctl bootstrap gui/$(id -u) <the file>`, which is how the
installer reloads a changed agent. Running the installer again afterwards
puts the file back. Give the mirror's Tailscale address there, not a LAN
one: a launchd-started exe that reaches for the local network before it
has been allowed to raises macOS's Local Network alert on the Mac's
screen and gets "no route to host" until somebody answers it. A VM needs
that permission in any case, so say so to whoever sits at the Mac before
creating one. And a Mac's exe that listens on its Tailscale address must
not be restarted while Tailscale is stopped there.

A daemon keeps its answer about the newest release for twelve hours, so
after moving `LATEST` on press Check Now (or ask `/v1/update?check=1`).

Then move `dist/release/LATEST` on and open the machine's desktop (through
`ssh -L` when it listens on itself only): the Apple wears a diamond, the
window names the new release with what its `NOTES.md` says, and Update Now
installs and restarts. The case to run: press Update, close the tab during
the download, open the desktop again while exe restarts, and find one
`update: exe <version> is installed` line in `~/.exe/daemon.log`, the new
`exe version`, and each VM that was running back. Then the other way in: a
shell's `exe update` without `-y` and without a terminal installs and
leaves the daemon alone, and the window offers Restart Now. Remove the
drop-in afterwards, and `~/.exe/update.json`.

To have the window install the very files that will be published, give
the machine a second build of the same commit under a lower number first:
`EXE_MAC_BUILDER=none deploy/release.sh build --worktree 2026.10.11.90`
before a 2026.10.12, that name in `LATEST`, a shell's `exe update -y` on
the machine, and `LATEST` removed again. The window then offers the
release itself, and the machine ends on what GitHub will serve. A release
before 2026.10.12 has no Software Update, so this is also how a machine on
one gets a window to press.

On Windows the daemon has the environment of the sign-in that started it,
and a restart hands it on. `setx EXE_RELEASE_URL …` makes it a variable of
the user; then stop the daemon the way a sign-out does (a task in the
desktop session that runs `taskkill /PID <daemon>` without `/F`) and run
`exe daemon start`, whose task starts it with the user's environment as it
now is. Afterwards `reg delete HKCU\Environment /v EXE_RELEASE_URL /f` and
the same stop and start.

With a VM, look inside it as well as at the window. "Running again" is
the daemon's word for a VM it has started; whether the guest was shut
down or had its power cut shows in the guest: `sudo journalctl -b -1 | tail -1`
ends in "Journal stopped" after a shutdown, and after a power cut the next
boot's journal says `EXT4-fs (vda1): recovery required`. This is how the
Mac's restart was found to cut the power (2026-10-10, fixed the same day):
the window was right that the VM had come back once and stayed stopped
the second time, and the guest said why.

`NOTES.md` is in the build, so the window under test shows the notes the
release will carry. Read them there: it is the last look before they are
published for good. A build without the file (a `--worktree` build of a
tree that has none) makes the window link the release's page instead.

The machines: `lab` (an exe VM: Debian 13, ARM64, no `/dev/kvm`) for the
installer, the service, `exe update` and `exe uninstall`; `precision`
(Ubuntu 24.04, x86-64, KVM, Tailscale) for the x86-64 build and the VM
step; `birdie` (a Mac, Apple silicon, someone logged in at its screen) for
macOS — the launchd agent, the menu-bar item and a VM. The Intel Mac build
runs on `birdie` under Rosetta, which shows its installer and desk but not
its VMs: those need a real Intel Mac, and none has tried them. `world`
(Windows 11 Pro, x86-64, someone signed in at its screen) for Windows;
how to drive it is under "Windows" below. Since 2026.10.10.2 `world` runs
exe for real, as a node with its own data: it is updated like `precision`,
never uninstalled, and a new install on Windows needs Livid's say-so or
another PC. Never run a released build's
`exe setup` on spark: it would install beside the checkout's own daemon.

## Publish

```sh
deploy/release.sh publish 2026.10.09 [page.md]
```

It publishes the files `build` made, and refuses if they changed, if the
commit is not on `main`, if the tag exists, or if the macOS binaries are
not Developer ID signed and notarized. It tags the commit, pushes
`main` and the tag, uploads the nine files to a draft, publishes the draft
as the latest release, and checks that GitHub's `latest` now serves the
checksums that were built. Then it adds the release's notes to
`CHANGELOG.md`, empties `NOTES.md` in the checkout and commits both,
without pushing them.

The notes go up twice. As a tenth file, `NOTES.md`, the one `build` made:
the desktop's Software Update reads it the way every other file of a
release is read (no GitHub API, and a mirror serves it), and `publish`
refuses a build that has none. And in the release's text on GitHub, under
the install lines, which whoever reads `NOTES.md` has no use for: they
have exe installed. A release with more to say there — what was not tried,
what to know before installing — gives `publish` a file to use as that
text instead; `NOTES.md` stays as it was built. The two releases before
this, 2026.10.10 and 2026.10.10.2, have no `NOTES.md`, and Software Update
says of them that they came without notes; `CHANGELOG.md` has a few lines
for each, written afterwards from their pages.

Afterwards: rebuild and restart the daemon on spark (it serves
`/install.sh`, `/install.ps1` and the homepage), run the real one-liner
once on `lab`, check that `/install.ps1` is served as the release has it
(`world` runs exe for real, and is not installed over), move the machines
that run a release to it with `exe update`, and post the release on the
hub.

The first release, 2026.10.10, went out this way: built from a commit,
installed from the mirror on `lab` and by `exe update` on `precision`, its
Mac binaries checked with `spctl` ("Notarized Developer ID"), then
published — GitHub marked it immutable — and installed through the real
one-liner on `lab` and on a Mac.

The second, 2026.10.10.2, was the first with a Windows build and the first
that anything was updated to: `exe update` took `lab` and `precision` from
2026.10.10 to it through the mirror, a VM was made on `precision`, the Mac
binaries passed `spctl`, `birdie` installed it and was left clean, and
`world` installed it with the VM step and ran a VM. Once it was published,
the real lines ran on `lab` and on `world`, and `exe update` moved the Mac
node from GitHub.

The third, 2026.10.12, was the first with Software Update and with notes.
`exe update` took `lab` and the Mac node from 2026.10.10.2 to it through
the mirror, and `precision` and `world` to a lower-numbered build of the
same commit, from which the window's Update Now installed the release
itself, a VM running on each and the tab closed during the download: one
`update: exe 2026.10.12 is installed` line in each log, and each guest's
journal ending in "Journal stopped". That was the window's first run on
Windows. The Mac node restarted with a VM running and its guest was shut
down too, which 2026.10.10.2 did not do. `birdie` and a new VM on this
machine installed it with the questions answered at a terminal; the Intel
build said its version under Rosetta. Not repeated: a new install on
Windows.

The fourth, 2026.10.15, was the first that every machine under test met
already running a release with Software Update, so no lower-numbered
build was needed. The update the window starts, `POST /v1/update` with
`restart`, was sent on `precision`, `world` and the Mac node by a script
that read each machine's token there and nowhere else. Each moved from
2026.10.12 with a VM running, in 34 s, 9 s and 4 s, with one `update: exe
2026.10.15 is installed` line in its log and its guest's journal ending in
"Journal stopped". `lab` moved by a shell's `exe update -y`; `birdie` and a
new VM on this machine installed it with the questions answered at a
terminal; the Intel build said its version under Rosetta. The window's
button was not pressed in a browser, as its code had not changed since
2026.10.12. The notes were read in the window of that new VM, its daemon
pointed at the mirror. Before publishing, `exe-projects` and `exe-planet`
were pushed: Extras… installs them from GitHub, and a line of the notes
needed their newest commits. Not tried: a new install on Windows, and the
iPhone lines of the notes on a device.

Rolling back is moving GitHub's "latest" mark to the release before
(`gh release edit <older> --latest`); whether that is allowed with
immutable releases on has not been tried — publishing the next version is
the sure way.

## What the installer does

`install.sh` and `install.ps1` only download, check and unpack;
`exe setup -from <dir>` (`cmd/exe/install.go`) asks the questions and does
the work, so it is tested in Go and can be run again as `exe setup`. The
one source serves all three systems: what differs is chosen by the host's
`OS` field, so the Mac's and the PC's paths are tested on Linux as well.
What only the real system can answer is behind a few functions
(`install_unix.go`, `install_windows.go`), and on Windows has tests of its
own that run there (`install_windows_test.go`).

- Asks, in order: where to listen (this machine, all interfaces, or
  Tailscale when the machine has it — the answer goes for the desk, the
  proxy and the SSH gate alike); whether to require an API token (not
  asked for all interfaces, where one is always generated); whether to
  install the extra apps; whether to set the machine up for VMs (Linux
  with sudo, where `/dev/kvm` exists; Windows as an administrator, where
  something is missing — it lists its commands first, and a Mac needs no
  setup for VMs and is never asked); and, on Windows, which drive the VMs
  go on.
- Asks nothing with nobody at a keyboard, and takes its answers from
  `EXE_INSTALL_LISTEN`, `EXE_INSTALL_TOKEN`, `EXE_INSTALL_APPS`,
  `EXE_INSTALL_VMS`, `EXE_INSTALL_VM_DRIVE` and `EXE_API_TOKEN`.
- Writes nothing until the last answer; keeps a `config.json` that is
  already there; never touches a service it did not write.
- Puts the binary in `~/.local/bin` (Windows:
  `%LOCALAPPDATA%\Programs\exe`, added to the user's `PATH`), the staged
  helper and its own records in `~/.exe/release/`, the apps in
  `~/.exe/apps`, and the service where the system keeps a user's own: a
  systemd user unit, `~/.config/systemd/user/exe.service`; on a Mac a
  launchd agent, `~/Library/LaunchAgents/com.v2core.exe.plist`; on Windows
  the value `exe` in the user's `Run` key.

On a Mac the agent runs in the user's login session — that is where the
menu bar is — so exe starts at login, not at boot, and an install over ssh
with nobody at the screen only leaves the agent for the next login. The
agent comes back by itself only after a failure, so that Quit in the menu
bar stays quit; a restart is `launchctl kickstart -k
gui/<uid>/com.v2core.exe`, which the daemon runs on itself when the agent
started it (`EXE_LAUNCHD`, `server.RestartDaemon`).

`exe update` checks the download and runs its `version`, copies the new
binary beside the old one, stages the helper, refreshes the apps it
installed (not one that was edited or removed), and only then renames the
binary into place. That rename is the commit: the old binary does the
whole update, so an update that fails before it is simply run again. Were
the binary replaced first, the retry would be the new binary, which finds
itself up to date and repairs nothing. The installer keeps the same order.
`exe update` says so when the root-owned network helper has changed, and
restarts the daemon only on a yes or `-y`.

The apps' record is `~/.exe/release/apps.json`, saved at the end of a run.
What a run is about to place is written first, to `apps.json.pending`, and
removed once the record is saved, so a run that stops halfway — an error,
or a killed process — is recognised by the next one: a bundle that stands
there with exactly the contents the plan named is the installer's, and
anything else without a record is its owner's (`internal/release/apps.go`).

## Windows

Nothing the installer does there needs an administrator except the VM
step, and it installs for the one user: the binary in
`%LOCALAPPDATA%\Programs\exe`, the data in `%USERPROFILE%\.exe`.

**Starting and stopping.** Windows has no service of a user's own, so exe
starts from the `Run` key when its user signs in:
`conhost.exe --headless <exe> daemon start`. `exe daemon start` starts
the daemon and is done; it is under `conhost --headless` only so that it
opens no window on its way. The daemon itself gets a console of its own
that is hidden (`CREATE_NEW_CONSOLE` and a hidden window), and that is not
a detail:

- Windows tells a program that its user is signing out, or the PC shutting
  down, through its console. The daemon's stop writes down which VMs were
  running (`~/.exe/autostart`), and they come back at the next start. A
  daemon with no console (`DETACHED_PROCESS`) is simply ended, and its VMs
  stay off. Go hears the event as SIGTERM as long as the program has not
  loaded `user32.dll` or `gdi32.dll`, and the daemon loads neither.
- `conhost --headless` cannot hold the daemon itself. Started from another
  program it reads the end of that program's standard input as its
  terminal going away and closes, with the daemon inside.
- From an ssh session (session 0, no desktop) what is started ends with
  the connection, so `exe daemon start` and the installer start the daemon
  through a task that runs once in the session of the signed-in user
  (`schtasks /IT`) and is deleted. With nobody signed in they say so, and
  exe starts at the next sign-in. The task runs `exe daemon start`, not
  the daemon: a task's process is ended when the task's time is up, and
  the daemon is started out of the task's job
  (`CREATE_BREAKAWAY_FROM_JOB`).
- A restart is the daemon starting its successor and exiting
  (`server.RestartDaemon`), the same way: a hidden console, out of the
  job. `exe daemon restart` asks for one.

**Updating.** Windows does not let a running program be overwritten but
lets it be renamed, so `exe update` moves `exe.exe` aside to `exe.exe.old`
and puts the new one in its place (`replaceFile`). The old file goes at
the next update, or with `exe uninstall`, which also ends a daemon still
running from it.

**VMs.** The VM step runs what is missing of: `dism /Online
/Enable-Feature` for the Windows Hypervisor Platform and the Virtual
Machine Platform (a reboot is asked for when Windows says 3010), and
`winget install SoftwareFreedomConservancy.QEMU`. From a terminal that is
not elevated they go through one UAC prompt. Then the installer asks which
drive holds the VMs: the fixed NTFS drives with their free space and what
they are — an SSD, a hard disk, or a disk that is somewhere else on the
network, which Windows also calls fixed (iSCSI, or a Storage Space made of
one) and which is never the default. Any drive but the home one is written
to `config.json` as `vm_dir` (`X:\exe`); the setting works on every
system.

**Not signed.** `exe.exe` has no Authenticode signature. Fetched by
PowerShell it carries no mark of the web, so SmartScreen never looks at it,
and Defender scanned it and ran it without a word on `world`. A browser
download of the tarball would be marked.

Defender does judge command lines. `irm … | iex` typed into PowerShell is
what the homepage will say, and is what every such installer says. The
same thing passed to a new process — `cmd /c powershell -ExecutionPolicy
Bypass -Command "…; irm http://<address>/install.ps1 | iex"` — was removed
as `Trojan:Win32/Commando.A!ml`, with a notification on the screen. So a
test does not launch it that way: the lines go in a file there, and
PowerShell is asked to evaluate the file.

**Testing on `world`.** An ssh session there is elevated, runs in session
0, and has no terminal unless asked for one (`ssh -tt`, for the
questions). PowerShell's execution policy is Restricted, so a script is
not run as a file but read and evaluated:

```sh
scp try.ps1 user@world:exe-try.ps1     # $env:EXE_RELEASE_URL = '…'; irm …/install.ps1 | iex
ssh -tt user@world 'powershell -NoProfile -NoLogo -Command "iex (Get-Content -Raw $env:USERPROFILE\exe-try.ps1)"'
```

The tests that need a real Windows are in the test binary:
`GOOS=windows go test -c -o exe-cmd.test.exe ./cmd/exe`, copy it over, and
run it with `-test.run OnRealWindows -test.v`. It replaces a program that
is running, reads the real drives, writes and removes a sign-in entry and
a `PATH` entry under names of its own, and checks that a program started
the way the daemon is has a console and hears an event sent to it.

Run there before the Windows build was first committed, each on the real
PC: the one-liner with its questions answered at a terminal; the VM step
installing QEMU; a VM under WHPX with its disk on the chosen drive; `exe
daemon start` from ssh; `exe daemon restart` and `exe update -y` with the
VM running (the daemon came back as the new version, the VM with it); the
daemon's console closed the way a sign-out closes it (stopped in under
three seconds, the VM recorded and back at the next start, which was the
sign-in entry's own command line run as a task); `exe uninstall`. Two
things on a Windows of its own behave unlike a terminal: `exe ssh <vm>`
inherits standard input, so a script gives it `< NUL`, and the Windows
`ssh.exe` stalls when its output is captured into a PowerShell variable
with no console around — send it to a file.

`exe ssh <vm>` lost its key on any PC where Codex had run, in 2026.10.10.2
and 2026.10.12 (found on `world`, 2026-10-12). Codex's Windows sandbox
gives a group of its own, `CodexSandboxUsers`, the right to read most
folders at the top of the home folder (39 of 51 on `world`; `.ssh` is not
one), and to ones made after it was set up, so `.exe` got it at the first
Codex run after exe was installed. The key inherited that, and Windows'
OpenSSH will not use a private key anyone else can read ("UNPROTECTED
PRIVATE KEY FILE"). The daemon reads the key itself and reached its VMs
all the same; only the command failed, and only once Codex had run, which
is why the test of 2026-10-10 passed.

The mode exe writes a key with (0600) says nothing on Windows, where a new
file takes its folder's permissions. So a private key now gets a
permission list of its own, with inheritance off: its user, SYSTEM and the
Administrators (`keys.Protect`). That is done when a key is written, at
every daemon start for a key that is already there, and by `exe ssh` before
it runs `ssh`, so an install from before the fix is put right by the first
of the two it meets. The test is Windows' own judge:
`GOOS=windows go test -c -o exe-keys.test.exe ./internal/keys`, run there
with `-test.run OnRealWindows -test.v`, gives a folder a permission for
`Users`, and asks `ssh-keygen -y -f` whether it will read a new key, the
same key written the plain way (it must not), and that key after the next
start. `ssh-keygen` names the principal it objects to; send its output to a
file, since read through a pipe in a session without a console it does not
come back.

Not tried: a real sign-out and sign-in (that needs the person at the PC);
the VM step turning the hypervisor features on, and its UAC prompt, since
`world` had the features and its ssh session is elevated; Windows on ARM,
which has no build — the VM backend is x86-64 only.
