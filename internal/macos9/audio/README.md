# Optional Mac OS 9 audio

For the complete, pinned build and installation procedure, dependency versions,
compatible-host archive reuse, playback checks and rollback, see
[Build and reuse the Mac OS 9 sound runtime](../../../docs/macos9-audio.md).

The upstream QEMU runtime remains the default. Mac audio needs the experimental
Screamer device and its matching OpenBIOS. The manager selects a local audio
runtime only when both files exist under its node-local Mac OS 9 state directory:

```
mac-os9/audio/qemu-system-ppc
mac-os9/audio/openbios-ppc
```

Build source: <https://github.com/mcayland/qemu/tree/screamer-v9.1.0>, pinned to
`1c47688ed6557fae9e5bfe4f4b4082d570c94a70` (QEMU 9.1.0). The firmware is
`pc-bios/openbios-ppc` from the same checkout. Preserve QEMU's GPLv2 source and
license when distributing binaries. This is a local optional installation;
exe does not download a replacement emulator automatically.

On Linux, build for the host with a C compiler, Python, Meson, Ninja, GLib,
Pixman, zlib, and libslirp development files. A minimal configuration is:

```sh
mkdir build
cd build
../configure --target-list=ppc-softmmu --without-default-features \
  --enable-tcg --enable-vnc --enable-slirp --enable-pixman --enable-fdt \
  --audio-drv-list= --disable-docs --disable-guest-agent --disable-tools
ninja qemu-system-ppc
```

Install the executable and firmware at the paths above only after testing on a
copy of the guest disk. Shut down the original Mac cleanly and back up its disk
before its first boot with a new emulator. Keep the normal runtime for rollback;
removing the optional pair selects it again. Never replace a running VM's disk.

The manager uses `-audiodev none,id=mac-audio`, assigns it to Screamer, and selects
it on the existing VNC socket. Audio travels through the existing authenticated
console WebSocket. It needs no new endpoint, exposed port, or host audio service.
The viewer negotiates QEMU encoding -259 and requests 44.1 kHz, signed 16-bit,
little-endian stereo. Audio is opt-in with the Sound button, stops on hide or
disconnect, and has a bounded playback queue. AudioBufferSourceNode supports
local HTTP origins where AudioWorklet is unavailable.

Screamer is experimental. Keep the guest below 1 GB RAM; the existing 512 MB
configuration is appropriate. Check boot, alert sounds, game audio, and repeated
rate changes before adopting a different revision. Microphone input, migration,
and VM memory snapshots are not supported or verified by this integration.
