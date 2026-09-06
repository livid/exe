# Mac OS 9 VGA driver

`qemu_vga.upstream.ndrv` is the unmodified driver distributed with QEMU 8.2.2:
https://github.com/qemu/qemu/blob/v8.2.2/pc-bios/qemu_vga.ndrv

Its source is QemuMacDrivers commit
`90c488d5f4a407342247b9ea869df1c2d9c8e266`:
https://gitlab.com/qemu-project/QemuMacDrivers/-/tree/90c488d5f4a407342247b9ea869df1c2d9c8e266

The corresponding source archive and GPLv2 license are included here.
Upstream binary SHA-256: `d8697354bde8a7ac9e6fe3a5fbca03698de623e95433a59c248e8e09b882c6b8`.

QEMU's firmware search path loads this NDRV into Open Firmware; no guest-disk
installation is needed. Some Linux QEMU distributions omit this driver.
It reads the VGA device's EDID and exposes exactly our three modes when
`xres=800,yres=600,xmax=1024,ymax=768` is used. Keep EDID enabled.

`qemu_vga.ndrv` adds one local fix: GraphicsCoreGetModeTiming marks only mode
ID 2 (800x600 in our restricted, sorted list) as kModeDefault. Upstream marks
every mode as default, making Mac OS 9 select 640x480 during startup despite
QEMU's boot resolution. The modified corresponding source is DriverQDCalls.c;
all other sources are unchanged in source.tar.gz.

Run `python3 patch-driver.py` to reproduce the shipped binary exactly from the
included upstream driver. The script checks its input hash and PEF layout,
adds a six-instruction conditional stub, and adjusts the code size and data
file offset without changing existing code/data addresses or relocations.
The local fix was verified on a fresh CD boot and through native mode switches.
