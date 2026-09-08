# Build and reuse the Mac OS 9 sound runtime

This is the recipe behind the working Mac OS 9 sound setup on Spark, verified
on **2026-09-08**. Use the pinned source and matching firmware below to reproduce
it. The tested host is **Ubuntu 24.04, Linux aarch64/arm64**. Native builds on
other Linux architectures use the same PowerPC guest target, but still need
verification on that host.

For another compatible Ubuntu 24.04 ARM64 machine, the fastest route is to copy
the prepared runtime archive instead of compiling QEMU again. For an x86-64
machine, build an x86-64 executable; the ARM64 executable cannot run there
natively. The PowerPC firmware is the same in both cases.

## What has to match

| Component | Working version or setting |
|---|---|
| exe | Commit `e8dbce9` or a descendant containing its audio support |
| QEMU source | `https://github.com/mcayland/qemu.git` |
| Source branch used | `screamer-v9.1.0` |
| Exact source commit | `1c47688ed6557fae9e5bfe4f4b4082d570c94a70` |
| Reported QEMU version | `9.1.0` |
| Firmware | `pc-bios/openbios-ppc` from **that exact checkout** |
| Emulated Mac | `mac99`, `G4`, 512 MiB RAM, TCG |
| Audio device | `screamer` |
| Host audio backend | `none` for browser playback; `wav` for diagnosis |
| Browser transport | QEMU's VNC audio extension over exe's existing console WebSocket |
| Stream | Signed 16-bit little-endian stereo PCM, 44,100 Hz |

The stock emulator originally used on Spark did not emulate the required Mac
sound hardware. Adding a host sound driver to that executable was insufficient.
The working setup required the Screamer fork, its OpenBIOS, **and** the browser
playback changes in exe. A QEMU version string alone does not prove Screamer
support; check `-device screamer,help` too.

Source references: [pinned QEMU tree](https://github.com/mcayland/qemu/tree/1c47688ed6557fae9e5bfe4f4b4082d570c94a70),
[QEMU build system](https://www.qemu.org/docs/master/devel/build-system.html).
The commands here are pinned to the tested revision; current upstream build
instructions may describe a different revision.

## 1. Prepare exe and identify its state directory

First install/update exe and use its **Mac OS 9** app to obtain a working normal
installation. This supplies the disk, other firmware, normal QEMU runtime and
`qemu-img`. On Ubuntu 24.04, exe can prepare its normal runtime automatically.
The sound runtime is an optional additional installation; exe does not download
this fork automatically.

Run the commands in **Bash**, as the same account that runs exe. Keep the shell
open between sections. Change the first two paths if needed:

```bash
set -euo pipefail
export MAC9_ROOT="$HOME/.exe/mac-os9"
export MAC9_WORK="$HOME/.cache/exe-macos9-audio"
export MAC9_REV=1c47688ed6557fae9e5bfe4f4b4082d570c94a70
export MAC9_SRC="$MAC9_WORK/qemu-src"
export MAC9_BUILD="$MAC9_WORK/qemu-build"
export MAC9_CANDIDATE="$MAC9_WORK/audio"
mkdir -p "$MAC9_WORK"
```

`MAC9_ROOT` is the Mac directory under the **daemon account's** exe state, not
necessarily the home directory of the person connected over SSH. On Spark,
`~/.exe/mac-os9` points to `/www/exe/output/mac-os9`. That symlink and the
`/www/exe` checkout layout are specific to Spark, not installation requirements.

The optional installed layout is:

```text
mac-os9/
  macos9.qcow2
  firmware/                   existing exe firmware
  runtime/                    existing normal runtime, when locally installed
  audio/
    qemu-system-ppc            executable built for this host architecture
    openbios-ppc              PowerPC guest firmware from the pinned checkout
    lib/                      bundled Linux libraries, if needed
    COPYING
```

exe selects the sound executable only when **both** `audio/qemu-system-ppc` and
`audio/openbios-ppc` exist as nonempty regular files. It still uses its normal
`qemu-img`; this recipe does not replace that tool.

## 2. Fast route: reuse the prepared ARM64 archive

The prepared runtime is pinned in Kubo/IPFS. Download
[macos9-audio-ubuntu24.04-aarch64-qemu9.1.0.tar.gz](https://ipfs.io/ipfs/bafybeich3pv53zqr5r4e5kw3ianduwfoywkyh3oiv42y23ijvodne4yqsi)
through the public IPFS gateway, use the
[hub download](http://100.116.32.57:7788/v1/embed/bafybeich3pv53zqr5r4e5kw3ianduwfoywkyh3oiv42y23ijvodne4yqsi),
or retrieve it by CID with an IPFS client:

```text
bafybeich3pv53zqr5r4e5kw3ianduwfoywkyh3oiv42y23ijvodne4yqsi
```

Both download URLs were verified against the original archive on **2026-09-08**.
Only the hub URL requires access to Spark's Tailscale network. The CID identifies
the same archive independently of either URL. The archive is **4,995,810 bytes**
(about 4.8 MiB). Its SHA-256 is:

```text
35e5e778fb59117593a304e119c1dcedb43a1059875cdebdcb37b2317f44154d
```

It contains the sound runtime, its checksums, dependency license notices and
build metadata. It contains no Mac OS installer, VM disk, credentials or saved
games. The original local copy remains under `output/mac-os9/releases/`, outside
Git; installation on another machine does not require access to that directory.

On the destination, after setting the variables in section 1, download it:

```bash
export MAC9_CID=bafybeich3pv53zqr5r4e5kw3ianduwfoywkyh3oiv42y23ijvodne4yqsi
export MAC9_ARCHIVE=macos9-audio-ubuntu24.04-aarch64-qemu9.1.0.tar.gz
curl --fail --location --show-error \
  "https://ipfs.io/ipfs/$MAC9_CID" \
  --output "$MAC9_WORK/$MAC9_ARCHIVE"
```

If the public gateway is unavailable and you can reach the hub, change the URL
to `http://100.116.32.57:7788/v1/embed/$MAC9_CID`.
Alternatively, with a running Kubo node, replace the `curl` command with
`ipfs cat "$MAC9_CID" > "$MAC9_WORK/$MAC9_ARCHIVE"`. Then verify the archive
against the checksum recorded here **before extracting**, and check its contents:

```bash
(cd "$MAC9_WORK" && printf '%s  %s\n' \
  35e5e778fb59117593a304e119c1dcedb43a1059875cdebdcb37b2317f44154d \
  "$MAC9_ARCHIVE" | sha256sum -c -)
mkdir -p "$MAC9_WORK/reuse"
tar -xzf "$MAC9_WORK/$MAC9_ARCHIVE" -C "$MAC9_WORK/reuse"
export MAC9_CANDIDATE="$MAC9_WORK/reuse/audio"
(cd "$MAC9_CANDIDATE" && sha256sum -c SHA256SUMS)
file "$MAC9_CANDIDATE/qemu-system-ppc"
ldd "$MAC9_CANDIDATE/qemu-system-ppc"
"$MAC9_CANDIDATE/qemu-system-ppc" --version
"$MAC9_CANDIDATE/qemu-system-ppc" -device screamer,help
```

Use this archive on a compatible **aarch64 Linux** host. It was built with
Ubuntu 24.04's glibc 2.39; it is not a universal Linux, macOS or Windows binary.
All `ldd` entries must resolve. The archive bundles GLib, Pixman and libslirp,
but still uses the host's loader, libc, libm, zlib and PCRE2. On Ubuntu 24.04,
missing zlib/PCRE2 runtime packages are `zlib1g` and `libpcre2-8-0`.

Continue at **section 5**. On a different architecture or incompatible Linux
release, use the native build below instead of copying foreign system libraries.

## 3. Native build dependencies

### Ordinary Ubuntu build

This is the simpler repeat-install route when you can install development
packages. The original Spark build used locally extracted packages instead;
that exact approach is recorded in the next subsection.

```bash
sudo apt-get update
sudo apt-get install --no-install-recommends \
  build-essential git pkg-config python3 python3-venv ninja-build \
  libglib2.0-dev libpixman-1-dev libslirp-dev zlib1g-dev libfdt-dev \
  patchelf binutils
```

Do not install a newer Meson just to build this revision. QEMU's configure step
creates `pyvenv` and can use its bundled Meson 1.2.3 wheel. The original build
used that version. Permit network access while configuring: Meson can fetch
source subprojects named in the checkout's pinned `.wrap` files.

### How Spark was built without installing system packages

Spark already had GCC/G++, Python 3.12, `pkg-config`, Git, zlib headers and the
normal exe QEMU runtime. The remaining packages were downloaded and unpacked
under the build workspace. This is **not** a complete compiler bootstrap for a
bare machine; use the ordinary dependency installation above if those base
tools are absent.

```bash
export MAC9_DEPS="$MAC9_WORK/deps"
export MAC9_PACKAGES="$MAC9_WORK/packages"
export MAC9_TRIPLET="$(gcc -dumpmachine)"
mkdir -p "$MAC9_DEPS" "$MAC9_PACKAGES"
(
  cd "$MAC9_PACKAGES"
  apt-get download \
    ninja-build patchelf libglib2.0-dev libglib2.0-dev-bin libglib2.0-0t64 \
    libpixman-1-dev libpixman-1-0 libslirp-dev libslirp0 \
    libfdt-dev libfdt1 libffi-dev libpcre2-dev \
    libpcre2-8-0 libpcre2-16-0 libpcre2-32-0
)
for package in "$MAC9_PACKAGES"/*.deb; do
  dpkg-deb -x "$package" "$MAC9_DEPS"
done

# Extracted .pc files must point at the extracted headers/libraries, not /usr.
python3 - "$MAC9_DEPS" <<'PY'
import pathlib, re, sys
root = pathlib.Path(sys.argv[1]).resolve()
for path in root.rglob('*.pc'):
    text = path.read_text()
    text = re.sub(r'^prefix=/usr$', 'prefix=' + str(root / 'usr'),
                  text, flags=re.MULTILINE)
    path.write_text(text)
PY
export PATH="$MAC9_DEPS/usr/bin:$PATH"
export PKG_CONFIG_PATH="$MAC9_DEPS/usr/lib/$MAC9_TRIPLET/pkgconfig"
export LD_LIBRARY_PATH="$MAC9_DEPS/usr/lib/$MAC9_TRIPLET:$MAC9_ROOT/runtime/usr/lib/$MAC9_TRIPLET"
pkg-config --modversion glib-2.0 pixman-1 slirp zlib
```

The original extracted directory was `output/mac-os9/audio-build-deps`; its
wrapper is `output/mac-os9/audio-build-env.sh`. That wrapper has Spark-specific
absolute paths. Use the variables above on another machine.

Recorded build versions:

| Dependency | Spark version |
|---|---|
| GCC | Ubuntu GCC 13.3.0 |
| Python | 3.12.3 |
| Meson | 1.2.3 |
| Ninja | 1.11.1 |
| pkg-config | 1.8.1 |
| GLib | 2.80.0 (`2.80.0-6ubuntu3.8` packages) |
| Pixman | 0.42.2 |
| libslirp | 4.7.0 (`4.7.0-1ubuntu3.1` packages) |
| zlib | 1.3 |
| patchelf | 0.18.0 |
| device-tree library | internal DTC subproject in the successful build |

APT may serve newer security updates later; the package list is a recipe, not
a permanent package-version lock. The exact `.deb` files used on Spark remain
in `output/mac-os9/audio-build-packages` for same-platform reproduction.

## 4. Fetch, build and stage the runtime

Fetch the exact commit, not whichever commit the branch points to in the future:

```bash
test ! -e "$MAC9_SRC"
git init "$MAC9_SRC"
git -C "$MAC9_SRC" remote add origin https://github.com/mcayland/qemu.git
git -C "$MAC9_SRC" fetch --depth 1 origin "$MAC9_REV"
git -C "$MAC9_SRC" checkout --detach FETCH_HEAD
test "$(git -C "$MAC9_SRC" rev-parse HEAD)" = "$MAC9_REV"
mkdir -p "$MAC9_BUILD"
cd "$MAC9_BUILD"
"$MAC9_SRC/configure" \
  --target-list=ppc-softmmu \
  --disable-docs --disable-werror --without-default-features \
  --enable-tcg --enable-vnc --enable-slirp --enable-pixman --enable-fdt \
  --audio-drv-list= --disable-guest-agent --disable-tools \
  --prefix="$MAC9_WORK/screamer-runtime"
ninja -j 12 qemu-system-ppc
```

Those are the successful configure options from Spark's `config.log`, with
portable paths substituted. `-j 12` is the parallelism used there; reduce it
on smaller machines. No measured first-build duration was retained, so there
is no promised build-time benchmark.

`ppc-softmmu` describes the **guest** target, even when the host is ARM64 or
x86-64. Build on the destination architecture; do not substitute `aarch64-softmmu`
or `x86_64-softmmu`. `--without-default-features` avoids unrelated GUI/audio
backends, and `--disable-tools` keeps the existing `qemu-img` in use. Empty
`--audio-drv-list=` still leaves QEMU's `none` and `wav` backends available.

The build does not need every firmware Git submodule. In particular, the
working setup used the **already built** `pc-bios/openbios-ppc` in the checkout;
it did not rebuild OpenBIOS. Its SHA-256 is:

```text
51e6d5e98985db5c7523dfea817cbacc2cdc883e6fc20e39567ba5ab101cda98
```

Stage only what exe needs; `make install` is unnecessary:

```bash
test ! -e "$MAC9_CANDIDATE"
install -d -m 700 "$MAC9_CANDIDATE/lib"
install -m 755 "$MAC9_BUILD/qemu-system-ppc" "$MAC9_CANDIDATE/qemu-system-ppc"
strip "$MAC9_CANDIDATE/qemu-system-ppc"
install -m 644 "$MAC9_SRC/pc-bios/openbios-ppc" "$MAC9_CANDIDATE/openbios-ppc"
install -m 644 "$MAC9_SRC/COPYING" "$MAC9_CANDIDATE/COPYING"

# These are the three shared libraries bundled on Spark. pkg-config selects
# either the normal system libraries or the locally extracted packages.
cp -L "$(pkg-config --variable=libdir glib-2.0)/libglib-2.0.so.0" "$MAC9_CANDIDATE/lib/"
cp -L "$(pkg-config --variable=libdir pixman-1)/libpixman-1.so.0" "$MAC9_CANDIDATE/lib/"
cp -L "$(pkg-config --variable=libdir slirp)/libslirp.so.0" "$MAC9_CANDIDATE/lib/"
patchelf --set-rpath '$ORIGIN/lib' "$MAC9_CANDIDATE/qemu-system-ppc"

# Check without the build workspace's LD_LIBRARY_PATH hiding missing libraries.
env -u LD_LIBRARY_PATH "$MAC9_CANDIDATE/qemu-system-ppc" --version
env -u LD_LIBRARY_PATH "$MAC9_CANDIDATE/qemu-system-ppc" -device screamer,help
env -u LD_LIBRARY_PATH "$MAC9_CANDIDATE/qemu-system-ppc" -audiodev help
env -u LD_LIBRARY_PATH ldd "$MAC9_CANDIDATE/qemu-system-ppc"
sha256sum "$MAC9_CANDIDATE/openbios-ppc"
```

Expect QEMU 9.1.0, a `screamer` device with an `audiodev` property, and both
`none` and `wav` audio backends. Every `ldd` dependency must resolve. A native
build may link additional libraries, such as external libfdt; satisfy those
on the destination too. Do not bundle a foreign libc or dynamic loader to
force an incompatible build to run.

The stripped/patched **Spark ARM64 binary** is 15,293,224 bytes with SHA-256
`7cabb57f9eb2ad1b6999ef271141b3f6abb5bc460feccf2cd9add7c7b4807b9f`.
A rebuild with another compiler, dependency set or host architecture need not
have that binary checksum. The pinned firmware checksum should match.

## 5. Check exe's actual runtime environment

exe supplies its normal runtime library path when launching the optional binary.
On Linux, `LD_LIBRARY_PATH` takes precedence over the binary's `RUNPATH`, so test
that environment as well as the standalone executable:

```bash
MAC9_RUNTIME_LIBS=""
for dir in "$MAC9_ROOT"/runtime/usr/lib/*-linux-gnu "$MAC9_ROOT/runtime/usr/lib"; do
  test -d "$dir" || continue
  MAC9_RUNTIME_LIBS="${MAC9_RUNTIME_LIBS:+$MAC9_RUNTIME_LIBS:}$dir"
done
export MAC9_RUNTIME_LIBS
LD_LIBRARY_PATH="$MAC9_RUNTIME_LIBS" "$MAC9_CANDIDATE/qemu-system-ppc" --version
LD_LIBRARY_PATH="$MAC9_RUNTIME_LIBS" "$MAC9_CANDIDATE/qemu-system-ppc" -device screamer,help
LD_LIBRARY_PATH="$MAC9_RUNTIME_LIBS" ldd "$MAC9_CANDIDATE/qemu-system-ppc"
```

If these fail while the standalone checks pass, inspect the conflicting library
under the normal `runtime/`. Use compatible libraries or rebuild for that host;
do not delete the normal runtime merely to make the error disappear.

## 6. Back up, activate, and start the Mac

On Spark, the new emulator was first tested on a separate copy of an offline
Mac disk. The test used separate QMP/VNC sockets and PID files, no network, and
the `wav` backend to record a Sound control-panel alert. The existing script
`output/mac-os9/audio-test/start.py` records those exact launch arguments;
its paths are machine-specific. Do not run two QEMU processes against one
writable disk image.

For a repeat installation, test on an expendable Mac installation first when
possible. Before switching an existing Mac, quit guest applications and choose
**Special → Shut Down inside Mac OS 9**. Closing the browser or restarting exe
does not establish that the guest disk is offline. Wait until the Mac is stopped
and its QEMU process has exited, then make the backup and install the candidate:

```bash
# Run only after confirming the guest has shut down cleanly.
# QEMU normally removes its PID file on exit; stop here if one remains.
test ! -e "$MAC9_ROOT/qemu.pid"
MAC9_STAMP="$(date +%Y%m%d-%H%M%S)"
cp --reflink=auto --sparse=always "$MAC9_ROOT/macos9.qcow2" \
  "$MAC9_ROOT/macos9-before-audio-$MAC9_STAMP.qcow2"
MAC9_IMG="$MAC9_ROOT/runtime/usr/bin/qemu-img"
if test ! -x "$MAC9_IMG"; then MAC9_IMG="$(command -v qemu-img)"; fi
LD_LIBRARY_PATH="$MAC9_RUNTIME_LIBS" "$MAC9_IMG" check \
  "$MAC9_ROOT/macos9-before-audio-$MAC9_STAMP.qcow2"

# Copy into a separate directory before making the complete pair visible.
MAC9_STAGE="$MAC9_ROOT/audio.new-$MAC9_STAMP"
mkdir "$MAC9_STAGE"
cp -a "$MAC9_CANDIDATE/." "$MAC9_STAGE/"
if test -e "$MAC9_ROOT/audio"; then
  mv "$MAC9_ROOT/audio" "$MAC9_ROOT/audio.previous-$MAC9_STAMP"
fi
mv "$MAC9_STAGE" "$MAC9_ROOT/audio"
```

If a PID file remains, investigate whether it is stale; do not just remove it
and assume the disk is safe. Keep the normal runtime and the backup. The Spark
backup is `output/mac-os9/macos9-before-audio-20260908.qcow2` and passed
`qemu-img check` before the first live sound boot.

Click **Start Mac** in the app. Installing this optional pair does not require
an exe rebuild/restart when the running exe already includes the audio changes.
It **does** require a new guest QEMU process; changing files does not upgrade
an already-running emulator. If exe itself needed upgrading, deploy that update
using the normal host-specific exe procedure first.

## 7. Launch settings exe supplies

The manager retains its existing Mac configuration and adds:

```text
-bios <MAC9_ROOT>/audio/openbios-ppc
-audiodev none,id=mac-audio
-global screamer.audiodev=mac-audio
-vnc unix:<MAC9_ROOT>/vnc.sock,audiodev=mac-audio
```

Keep `mac99`, `-cpu G4`, `-m 512`, and `-accel tcg,tb-size=128`. exe also supplies
the VGA settings, tablet driver in NVRAM, disk, firmware search path, networking,
and QMP socket. Prefer starting through the app to reconstructing those by hand.
Do not add a second Screamer device; the machine supplies the device to which
the `-global` option applies.

`none` is intentional: QEMU still provides captured PCM to VNC, and the browser
plays it. No ALSA/PulseAudio/PipeWire service or host speakers are needed.
For a separate diagnostic VM, replace just the audio backend with
`-audiodev wav,id=mac-audio,path=/absolute/path/to/sound.wav`; leave the same
Screamer and VNC audiodev links in place. Shut that VM down before inspecting
the completed WAV.

To confirm what the running Linux guest process actually uses:

```bash
python3 - "$MAC9_ROOT" <<'PY'
import pathlib, sys
root = pathlib.Path(sys.argv[1])
pid = (root / 'qemu.pid').read_text().strip()
args = pathlib.Path('/proc', pid, 'cmdline').read_bytes().decode().split('\0')
print('Executable:', args[0])
for i, arg in enumerate(args[:-1]):
    if arg in ('-bios', '-audiodev', '-global', '-vnc'):
        print(arg, args[i + 1])
PY
```

## 8. Verify playback, not just compilation

1. Reload exe after an exe upgrade; open **Mac OS 9** and wait for **Connected**.
2. Click **Sound off** once. It should become **Sound on**. The initial browser
   gesture is required; each new viewer session starts muted.
3. Open the Mac's **Sound** control panel, choose an alert and click **Play**.
   Confirm playback on the device running the browser.
4. Launch SimCity 2000. Check its intro with the CD mounted, then in-game music
   while the city is running. The game's Pause state also pauses its music.
   Disable **Options → Music** temporarily and check **Sound Effects** separately.
5. Click **Sound on** to mute. Test closing/reopening or hiding/showing the app,
   and a viewer reconnect. Playback must not replay an old accumulated queue.
6. Verify the keyboard, pointer and normal Mac display still work.

The original validation went beyond nonzero emulator samples: a browser analyser
and recording confirmed nonzero output from Web Audio. The live intro run had
no browser errors, peak amplitude about 0.314, and a maximum observed scheduled
buffer lead of about 0.237 seconds. That last value is **not** a measured
end-to-end latency guarantee. Music and isolated effects were checked separately.

Recorded evidence on Spark:

- `output/mac-os9/audio-test/browser-alert.webm`: guest alert at browser output.
- `output/mac-os9/simcity-browser-audio.webm`: live intro at browser output.
- `output/mac-os9/audio-live-verification.json`: live original-Mac measurements.
- `output/mac-os9/audio-game-verification.json`: music and isolated-effects checks.
- `output/mac-os9/audio-test/browser-verification.json`: playback/layout checks;
  mute, reconnect and hide/show were also exercised by the browser test.

From an exe checkout, the relevant automated checks are:

```bash
go test ./internal/macos9 ./internal/server
node internal/macos9/audio/audio_test.mjs
```

Those tests check runtime selection, PCM framing/negotiation and player behavior.
They complement the guest/browser playback test; they cannot prove that a new
host's emulator and firmware produce audible sound.

## Troubleshooting and rollback

| Symptom | Check |
|---|---|
| Sound button disabled | Wait for Connected; inspect the running executable and VNC audiodev arguments; check both optional files are present. |
| Stock emulator still running | Shut down and start the **Mac**, not just the browser. Check `/proc/<pid>/exe`. |
| Unknown `screamer` device/property | Wrong executable or source revision. `--version` alone is insufficient. |
| Sound device missing in the guest | Verify the pinned `openbios-ppc` is selected with `-bios`; the old firmware was insufficient. |
| `Exec format error` | Host executable architecture mismatch. Build natively for the destination. |
| Missing library or symbol-version error | Run `ldd` with and without exe's runtime library path; use compatible libraries or rebuild. |
| Sound on but no music | Check browser/OS volume, the Mac Sound panel, game sound settings, and whether SimCity is paused. |
| WAV has audio but browser is silent | Check exe version, reload cached assets, click Sound off, and verify VNC has `audiodev=mac-audio`. |
| Browser on plain HTTP | Expected: this player uses AudioBufferSourceNode and does not require AudioWorklet/HTTPS. |
| CD missing after a Mac restart | Use the CD toolbar to mount the image again; audio does not make CD mounts persistent. |

Screamer remains an experimental fork. The verified memory setting is **512 MiB**;
do not raise it to 1 GiB or more as part of this recipe. Microphone input, live
migration and VM memory snapshots are outside the verified integration. Do not
replace the pinned branch with a newer revision without repeating the checks.

To roll back, shut down the Mac cleanly again, then move the optional pair aside:

```bash
mv "$MAC9_ROOT/audio" "$MAC9_ROOT/audio.disabled-$(date +%Y%m%d-%H%M%S)"
```

Start the Mac through exe; it selects the normal emulator again. The guest disk
normally needs no restoration. Restore the saved disk only if you deliberately
want its entire earlier state, and only while the guest is stopped.

## Keep the next installation fast

After validating a native build on another Linux architecture, package that
candidate for the next compatible machine. Add the dependency license notices
from the package sources before sharing it, as in the prepared Spark archive.
The following creates a fresh archive without copying a guest disk:

```bash
MAC9_BUNDLE="$(mktemp -d "$MAC9_WORK/bundle.XXXXXX")"
cp -a "$MAC9_CANDIDATE" "$MAC9_BUNDLE/audio"
# Hash the executable, firmware and bundled libraries within the archive.
(cd "$MAC9_BUNDLE/audio" && sha256sum qemu-system-ppc openbios-ppc lib/* > SHA256SUMS)
MAC9_ARCH="$(uname -m)"
MAC9_ARCHIVE="macos9-audio-linux-$MAC9_ARCH-qemu9.1.0.tar.gz"
tar -czf "$MAC9_WORK/$MAC9_ARCHIVE" -C "$MAC9_BUNDLE" audio
(cd "$MAC9_WORK" && sha256sum "$MAC9_ARCHIVE" > "$MAC9_ARCHIVE.sha256")
```

Cache the **runtime archive** for each host OS/architecture you verify. Save its
SHA-256, the exact source revision, compiler/dependency versions and successful
playback result. Keep corresponding source and license files available alongside
shared binaries.

For publicly shareable artifacts **under 20 MB**, pin the archive in Kubo and
record its CID, a working download URL, size, host OS/architecture and SHA-256 in
this guide. Verify both the recursive pin and the checksum of a fresh download.
Keep binaries outside Git. With a local Kubo CLI, publishing a new archive is:

```bash
MAC9_PUBLISHED_CID="$(ipfs add --cid-version=1 --pin=true -Q "$MAC9_WORK/$MAC9_ARCHIVE")"
ipfs pin ls --type=recursive "$MAC9_PUBLISHED_CID"
ipfs cat "$MAC9_PUBLISHED_CID" | sha256sum
```

See the [Kubo CLI reference](https://docs.ipfs.tech/reference/kubo/cli/) and
[pinning guide](https://docs.ipfs.tech/how-to/pin-files/). A pin retains content
on that node; keep a provider online, or pin another copy on a second node, for
future downloads.

This prepared archive was uploaded through the hub's signed `/v1/upload` API,
which adds and recursively pins it in the hub's Kubo store. Its CID is attached
to [this hub reply](http://100.116.32.57:7788/p/d60af99950b27eb3dfedb335964943e89196942cc10b031fbd118c5dd9d76276)
so it is retained beyond the hub's 24-hour unreferenced-upload cleanup.
Hub uploads currently have an **8 MiB** limit; larger artifacts under
20 MB need the direct Kubo route above and a reachable IPFS gateway. A direct
Kubo upload alone does not register a hub `/v1/embed/` download URL.

The source and build trees on Spark are:

```text
output/mac-os9/qemu-screamer-src
output/mac-os9/qemu-screamer-build
output/mac-os9/audio-build-packages
output/mac-os9/audio-build-deps
```

The cached ARM64 archive is the quickest route for another compatible Spark-like
machine. The pinned native-build recipe is the route for a different Linux CPU
architecture. Native macOS and Windows builds are not validated by this guide;
do not copy the Linux archive to those hosts or treat the main exe platform
support as proof that this optional Mac OS 9 audio runtime has been tested there.
