# Mac OS 9 absolute pointer driver

`nvramrc.fth` is the verbatim concatenated `nvramrc` argument from
https://github.com/elliotnunn/macos9-usb-tablet at commit
`170eb0103b927e6e2e546236920524fbd79250f9` (MIT license, included).
The original driver is by kanjitalk755; Elliot Nunn added the firmware loader.

QEMU loads it into the guest ROM on each boot, without modifying the disk.
It requires Mac OS 9.1 or later, `mac99` with its default CUDA controller
(not `via=pmu`), and `usb-tablet`. Right-click works; wheel scrolling is not
implemented by the upstream driver. Keep the payload unchanged when updating
our launch code; its compact encoding is constrained by Open Firmware.
