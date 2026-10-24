# exe's PowerPC emulator

The Mac OS 9 and Mac OS X apps run on an emulator of exe's own: QEMU 9.1.0 with

- **Screamer**, the Power Mac's sound device (Mark Cave-Ayland's `screamer-v9.1.0` tree, the
  one [macos9-audio.md](macos9-audio.md) built by hand before), so both Macs have sound;
- **a VNC server paced for the browser viewer**: a refresh every 16 ms instead of 30, no
  slowing down while the guest draws, a request answered at once when changes wait, and each
  request counted, so several frames can be on the way;
- **JPEG** in the Tight encoding;
- **the qegl GPU device**, on Linux only (it loads EGL): OpenGL passed through to the host's
  GPU, which Mac OS X's Quartz Extreme driver and Mac OS 9's QEGL Engine use.

The daemon fetches the build for its system when a Mac prepares its emulator
(`internal/macos9/emulator.go`), checks its size and SHA-256 against the values pinned there,
and unpacks it as the Macs' runtime. It replaces a runtime of the system's QEMU, or an older
build, at the next start; when the download fails, a runtime that works stays. Where no build
exists, the system's QEMU is used as before. `EXE_EMULATOR_URL` points the daemon elsewhere
(a mirror, ending in `/`); an empty value turns the builds off.

The daemon also paces the screen on its side, for any QEMU (`internal/vncpace`): it keeps one
update request waiting at the emulator and takes the viewer's requests as credits, so the
viewer's network round trip does not limit the frame rate. `internal/vncpace/vncbench`
measures it.

## 9.1.0-exe.1

Published on https://exe-assets.v2core.com/qemu/9.1.0-exe.1/ with its README, `SHA256SUMS` and
the source (`qemu-9.1.0-exe.1-source.tar.xz`, 31.7 MB, its build scripts in `exe-release/`). The macOS programs are signed with the
Developer ID and notarized; macOS 11 or later. Linux builds need glibc 2.35 or later.

| System | Archive | SHA-256 | IPFS |
|---|---|---|---|
| Linux ARM64 | [`qemu-9.1.0-exe.1-linux-arm64.tar.gz`](https://exe-assets.v2core.com/qemu/9.1.0-exe.1/qemu-9.1.0-exe.1-linux-arm64.tar.gz) | `3c6951b08ca0f3026865187753cd92186031f6c7bd73d5c57b545f05e5011621` | `bafybeidegs7wnmz2qom5jfe4zirefdt7snjfprqeave46jm4nsf65zihni` |
| Linux x86-64 | [`qemu-9.1.0-exe.1-linux-amd64.tar.gz`](https://exe-assets.v2core.com/qemu/9.1.0-exe.1/qemu-9.1.0-exe.1-linux-amd64.tar.gz) | `518c126163b5feb813d275c415fb6d13a6c2aa4921eb07ce75f9dbe5afcc1dc7` | `bafybeia5iyvlbbnqbs5he6ne54uesallsbd3nrhao74wnnr5nxuldpuzui` |
| macOS, Apple silicon | [`qemu-9.1.0-exe.1-darwin-arm64.tar.gz`](https://exe-assets.v2core.com/qemu/9.1.0-exe.1/qemu-9.1.0-exe.1-darwin-arm64.tar.gz) | `65fb41424425a618752d525146fc34c13bd213e9a0bb909486e1592ff471e24a` | `bafybeicohse73pzwa4azh3fuxfyz5ikf5jqjchzc6dy2camq7rwyznjoce` |
| macOS, Intel | [`qemu-9.1.0-exe.1-darwin-amd64.tar.gz`](https://exe-assets.v2core.com/qemu/9.1.0-exe.1/qemu-9.1.0-exe.1-darwin-amd64.tar.gz) | `ba41022d863124f103d33a8b10a7e473f5f5b9e92f21b321151c82e3acfdb139` | `bafybeicvgqy23ljcvif2glk2lgus54heztg74ijzwrenptuynscraqy4i4` |
| Windows x86-64 | [`qemu-9.1.0-exe.1-windows-amd64.tar.gz`](https://exe-assets.v2core.com/qemu/9.1.0-exe.1/qemu-9.1.0-exe.1-windows-amd64.tar.gz) | `a60744dac254cba5f26306830ae781b2dda42af59f9b4e90eb5995d31b93ef51` | `bafybeiapbolwbkvtorrksjk6tbvvq2iguzfw7vjgrfwk4aiii2oexqv7hy` |

Tested before publishing: Linux ARM64 (spark, Mac OS 9 booted and measured), Linux x86-64
(precision), macOS ARM64 (planet; birdie, macOS 26.5), macOS Intel (birdie, under Rosetta),
Windows (world). A scratch daemon on Linux, macOS and Windows fetched it, started a Mac and
relayed its screen.

## A new version

1. In exe-qegl (Livid's repository of the qegl work; the scripts are also in each source
   tarball's `exe-release/`): change `release/VERSION`, run `release/prepare-source.sh`, the Docker
   builds (`release/build-linux.sh arm64` and `amd64` in the `exe-qemu-linux` image,
   `release/build-windows.sh` in `exe-qemu-windows`), and `release/build-macos.sh build` and
   `sign` for `arm64` and `x86_64` on the Mac that signs exe's releases; then
   `release/package.sh` (reproducible archives).
2. Copy `build/release/dist/VERSION/` to `/www/exe-assets/public/qemu/VERSION/`, pin the
   archives in IPFS, and add the version's table here.
3. Set `emulatorVersion` and `emulatorBuilds` in `internal/macos9/emulator.go`. A published
   version never changes: Cloudflare and browsers keep its files for a year.
