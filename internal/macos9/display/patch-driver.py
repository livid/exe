#!/usr/bin/env python3
"""Reproduce the narrow default-mode fix in QEMU 8.2.2's PPC PEF driver.

The original GraphicsCoreGetModeTiming marks every mode as default. Mac OS 9
then chooses 640x480. With our restricted, sorted EDID list, mode ID 2 is the
800x600 default. Keep all three modes valid and safe, but prefer only mode 2.
The matching C source change is supplied as DriverQDCalls.c.

No existing code/data addresses or relocations change: append a register-only
stub to the code section, move the data's file offset, and branch back to the
original store. The loader section is before the code and stays untouched.
"""
import hashlib
from pathlib import Path
import struct

ROOT = Path(__file__).resolve().parent
original = (ROOT / 'qemu_vga.upstream.ndrv').read_bytes()
assert hashlib.sha256(original).hexdigest() == 'd8697354bde8a7ac9e6fe3a5fbca03698de623e95433a59c248e8e09b882c6b8'
b = bytearray(original)
assert b[:12] == b'Joy!peffpwpc'
code = struct.unpack('>6I4B', b[40:68])
data = struct.unpack('>6I4B', b[68:96])
assert code[2:7] == (12520, 12520, 12520, 912, 0)
assert data[5:7] == (13440, 1)
code_end = code[5] + code[4]
patch = 0x1a58
assert b[patch:patch + 8] == bytes.fromhex('38000007901e0010')  # li r0,7; stw r0,16(r30)

def branch(source, target):
    assert (target - source) % 4 == 0 and -(1 << 25) <= target - source < (1 << 25)
    return 0x48000000 | ((target - source) & 0x03fffffc)

stub = struct.pack('>6I',
    0x807e0000,  # lwz r3,0(r30): timingInfo->csTimingMode
    0x2c030002,  # cmpwi r3,2: 800x600 in our EDID mode list
    0x38000003,  # li r0,3: kModeValid | kModeSafe
    0x40820008,  # bne +8: skip default flag for other modes
    0x60000004,  # ori r0,r0,4: kModeDefault
    branch(code_end + 20, patch + 4))
b[patch:patch + 4] = struct.pack('>I', branch(patch, code_end))
b = b[:code_end] + stub + b[data[5]:]
for offset in (48, 52, 56):  # code total, unpacked, and packed sizes
    struct.pack_into('>I', b, offset, code[4] + len(stub))
struct.pack_into('>I', b, 88, code_end + len(stub))  # data containerOffset
assert (code_end + len(stub)) % (1 << data[8]) == 0
(ROOT / 'qemu_vga.ndrv').write_bytes(b)
print(hashlib.sha256(b).hexdigest())
