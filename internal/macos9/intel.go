package macos9

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Mac OS X 10.4.11 for Intel Macs, on this computer's own processor: QEMU's
// x86 machine (q35) under KVM, where the other two Macs are PowerPC ones
// emulated in software. It runs where exe runs on Linux on x86-64; Tiger for
// Intel does not start under QEMU's software emulation, so a computer of
// another kind has no use for it.
//
// What makes the virtual machine a Mac to Tiger:
//   - OVMF's 32-bit build as firmware: Tiger's boot.efi runs only on 32-bit
//     EFI, as the 2006 Macs had.
//   - OpenCore (acidanthera, BSD-licensed), fetched at setup and served to
//     the firmware from a folder of the Mac's as a FAT disk on virtio, a bus
//     Tiger has no driver for, so the Mac never sees it. Its config
//     (opencore/config.plist) gives Tiger an i386 kernel, a Mac Pro's
//     SMBIOS and the Data Hub boot.efi reads, and an HFS+ driver.
//   - QEMU's Apple SMC with Apple's key, which Tiger's "Don't Steal Mac OS
//     X" driver asks for before it runs the Finder, Dock and the rest.
//
// The install DVD is Apple's 10.4.11 (8S2167) for Intel Macs, whose
// Installer refuses only old PowerPC models. QEMU reads the disk image as
// it is. Its pointer is the USB tablet, as on the PowerPC Macs, and 10.4.11
// leaves 15% of its range unused from the Installer on (trims).
var macOSXIntel = &system{
	name: "Mac OS X", version: "10.4.11", process: "exe-mac-osx-intel",
	mediaURL:    "https://archive.org/download/tiger-10.4.11-intel/Tiger_8S2167_018-2986-35.dmg",
	mediaSHA1:   "23bd74189b33af12ad36594d7f639128e9672d52",
	mediaSize:   2344029364,
	media:       "MacOSX10.4.11-Intel-Install.dmg",
	downloading: "Downloading the 2.2 GiB Tiger install DVD for Intel Macs from Internet Archive…",
	maxCD:       9 << 30,
	disk:        "macosx-intel.qcow2", diskSize: "100G",
	diskReady: "Your 100 GB hard disk is saved on this computer.",
	install:   "Use Disk Utility and the Installer in the Mac screen below.",
	shutDown:  "Wait until the Installer restarts the Mac, then try again.",
	setup:     6 * time.Hour,
	network:   "user,id=net0,ipv6=off",
	forwards:  []forward{{"ssh", 22}, {"vnc", 5900}},
	ethernet:  "52:54:00:10:04:11",
	trims:     []float64{0.15, 0},
	gpu:       true,
	x86:       true,
	emulator:  "qemu-system-x86_64",
	checking:  "Checking for KVM and an x86 emulator…",
	ready:     "KVM and the x86 emulator are ready.",
	starting:  "Starting your Intel Mac…",
	cdDrive:   "cd",
}

// NewMacOSXIntel is the Mac that runs Mac OS X for Intel. Its runtime is
// its own, in its root: exe's emulator is a PowerPC one.
func NewMacOSXIntel(root string) *Manager { return newManager(macOSXIntel, root, root) }

// IntelMacHere says whether this computer can run the Intel Mac: Linux on
// x86-64, where KVM is. Whether KVM is open to exe, setup finds out.
func IntelMacHere() bool { return intelHost }

var intelHost = runtime.GOOS == "linux" && runtime.GOARCH == "amd64"

// appleSMCKey is what an Intel Mac's SMC answers when Mac OS X asks for its
// OSK0 and OSK1 keys; QEMU's isa-applesmc gives it to the guest.
const appleSMCKey = "ourhardworkbythesewordsguardedpleasedontsteal(c)AppleComputerInc"

// The CPU Tiger sees: a Penryn Core 2, with the TSC's frequency in the
// hypervisor leaf for OpenCore. Two of them (intelCPUs), which takes the
// i386_vm_init fix in exe's OpenCore config: without it Tiger faults at
// boot on more than one.
const intelCPU = "Penryn,vendor=GenuineIntel,+invtsc,vmware-cpuid-freq=on"

const intelCPUs = "2"

// OpenCore, pinned: the release archive and the files of it the Mac boots.
var openCore = struct {
	version, url, sha256 string
	size                 int64
}{"1.0.8", "https://github.com/acidanthera/OpenCorePkg/releases/download/1.0.8/OpenCore-1.0.8-RELEASE.zip",
	"2011e8b7216ecb2645d97ea710df965947edd406846e92050b9bc4190be6d27b", 10544084}

var openCoreFiles = []string{
	"EFI/BOOT/BOOTIA32.efi",
	"EFI/OC/OpenCore.efi",
	"EFI/OC/Drivers/OpenHfsPlus.efi",
	"EFI/OC/Drivers/OpenPartitionDxe.efi",
	"EFI/OC/Drivers/OpenRuntime.efi",
}

//go:embed opencore/config.plist
var openCoreConfig []byte

// The Intel Mac's runtime, from Ubuntu's packages: QEMU for x86 and OVMF's
// 32-bit firmware, with the libraries QEMU needs that a desktop may lack.
var intelPackages = []string{"qemu-system-x86", "qemu-system-common", "qemu-system-data", "qemu-utils", "ovmf-ia32", "seabios",
	"libfdt1", "libslirp0", "liburing2", "libpmem1", "libndctl6", "libdaxctl1", "librdmacm1t64", "libibverbs1"}

const ovmfCode, ovmfVars = "OVMF32_CODE_4M.fd", "OVMF32_VARS_4M.fd"

// kvmUsable: KVM is there and exe may use it.
func kvmUsable() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("The Intel Mac runs on this computer's processor through KVM, and exe cannot use it: %v. Turn on virtualization in the firmware settings, or add this account to the kvm group, then Continue.", err)
	}
	return f.Close()
}

// ovmf is a file of the 32-bit firmware: the runtime's, else the system's.
func (m *Manager) ovmf(name string) string {
	local := m.runtimePath(filepath.Join("runtime/usr/share/OVMF", name))
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return filepath.Join("/usr/share/OVMF", name)
}

// ensureIntelRuntime: a runtime of this Mac's that starts, else the
// system's QEMU and firmware, else Ubuntu's packages unpacked.
func (m *Manager) ensureIntelRuntime(ctx context.Context) error {
	dir := m.runtimePath("runtime")
	if _, err := os.Stat(dir); err == nil {
		if repairRuntimeFirmware(dir) == nil && validateIntelRuntime(ctx, dir) == nil {
			return nil
		}
	} else if os.IsNotExist(err) && m.command(ctx, m.sys.emulator, "--version") == nil && m.command(ctx, "qemu-img", "--version") == nil {
		if _, err := os.Stat(m.ovmf(ovmfCode)); err == nil {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	release, _ := os.ReadFile("/etc/os-release")
	if !strings.Contains(string(release), "ID=ubuntu") || !strings.Contains(string(release), "VERSION_ID=\"24.04\"") {
		return errors.New("Install QEMU for x86 with OVMF's 32-bit firmware (Ubuntu's qemu-system-x86 and ovmf-ia32) on this computer, then Continue. Automatic emulator setup supports Ubuntu 24.04.")
	}
	return m.unpackPackages(ctx, intelPackages, validateIntelRuntime)
}

// validateIntelRuntime starts the machine the Mac is, paused, without a
// disk: it needs KVM, the Apple SMC device and the USB tablet.
func validateIntelRuntime(ctx context.Context, dir string) error {
	for _, name := range []string{"usr/bin/qemu-system-x86_64", "usr/bin/qemu-img", "usr/share/OVMF/" + ovmfCode, "usr/share/OVMF/" + ovmfVars, "usr/share/qemu/vgabios-stdvga.bin"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("Incomplete emulator component: %s", name)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	img := runtimeCommand(ctx, dir, "usr/bin/qemu-img", "--version")
	if out, err := img.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img: %w: %.2000s", err, out)
	}
	cmd := runtimeCommand(ctx, dir, "usr/bin/qemu-system-x86_64", "-L", filepath.Join(dir, "usr/share/qemu"), "-machine", "q35", "-accel", "kvm",
		"-cpu", intelCPU, "-m", "64", "-drive", "if=pflash,format=raw,readonly=on,file="+strings.ReplaceAll(filepath.Join(dir, "usr/share/OVMF", ovmfCode), ",", ",,"),
		"-vga", "std", "-usb", "-device", "usb-tablet", "-device", "isa-applesmc",
		"-display", "none", "-nic", "none", "-S", "-qmp", "stdio")
	cmd.Stdin = strings.NewReader("{\"execute\":\"qmp_capabilities\"}\n{\"execute\":\"quit\"}\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Emulator startup check: %w: %.2000s", err, out)
	}
	return nil
}

// ensureOpenCore puts OpenCore's files and exe's config for it in the
// Mac's opencore folder, which QEMU shows the firmware as a disk. The
// release archive stays in media/; a changed config is written over the
// old one while the Mac is shut down.
func (m *Manager) ensureOpenCore(ctx context.Context) error {
	dir := m.path("opencore")
	if b, err := os.ReadFile(filepath.Join(dir, "VERSION")); err != nil || strings.TrimSpace(string(b)) != openCore.version {
		archive := m.path("media/OpenCore-" + openCore.version + "-RELEASE.zip")
		if verifySHA256(archive, openCore.sha256, openCore.size) != nil {
			if err := download(ctx, openCore.url, archive, openCore.sha256, openCore.size); err != nil {
				return fmt.Errorf("OpenCore: %w", err)
			}
		}
		staging := dir + ".staging"
		if err := os.RemoveAll(staging); err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		if err := unzipOpenCore(archive, staging); err != nil {
			return fmt.Errorf("OpenCore: %w", err)
		}
		if err := os.WriteFile(filepath.Join(staging, "VERSION"), []byte(openCore.version+"\n"), 0600); err != nil {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := os.Rename(staging, dir); err != nil {
			return err
		}
	}
	config := filepath.Join(dir, "EFI/OC/config.plist")
	if b, err := os.ReadFile(config); err == nil && bytes.Equal(b, openCoreConfig) {
		return nil
	}
	if err := os.WriteFile(config+".tmp", openCoreConfig, 0600); err != nil {
		return err
	}
	return os.Rename(config+".tmp", config)
}

// unzipOpenCore takes the 32-bit build's files out of the release archive.
func unzipOpenCore(archive, dst string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	want := map[string]string{}
	for _, f := range openCoreFiles {
		want["IA32/"+f] = f
	}
	for _, f := range z.File {
		name, ok := want[f.Name]
		if !ok {
			continue
		}
		delete(want, f.Name)
		r, err := f.Open()
		if err != nil {
			return err
		}
		path := filepath.Join(dst, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			var out *os.File
			if out, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
				_, err = io.Copy(out, io.LimitReader(r, 16<<20))
				if cerr := out.Close(); err == nil {
					err = cerr
				}
			}
		}
		r.Close()
		if err != nil {
			return err
		}
	}
	for name := range want {
		return fmt.Errorf("the archive lacks %s", name)
	}
	return nil
}

// ensureEFIVars gives the Mac its own NVRAM, from the firmware's blank one:
// the startup disk Tiger chooses is kept there.
func (m *Manager) ensureEFIVars() error {
	vars := m.path("efivars.fd")
	if _, err := os.Stat(vars); err == nil {
		return nil
	}
	b, err := os.ReadFile(m.ovmf(ovmfVars))
	if err != nil {
		return err
	}
	if err = os.WriteFile(vars+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(vars+".tmp", vars)
}

// intelLaunchArgs is the Intel Mac: a q35 board under KVM with OVMF and
// OpenCore, the Apple SMC, USB keyboard and tablet, the disk and the CD
// drive on the board's SATA controller (Tiger's AHCI driver takes both),
// and an Intel PRO/1000 Ethernet card, which Tiger's own driver for the Mac
// Pro's Ethernet takes. The install DVD is the disk image as it is.
func (m *Manager) intelLaunchArgs(installer bool) []string {
	esc := func(s string) string { return strings.ReplaceAll(s, ",", ",,") }
	var args []string
	if dir := m.runtimePath("runtime/usr/share/qemu"); exists(dir) {
		args = append(args, "-L", dir)
	}
	args = append(args, "-name", m.sys.process,
		"-machine", "q35", "-accel", "kvm", "-cpu", intelCPU, "-smp", intelCPUs, "-m", "2048",
		"-drive", "if=pflash,format=raw,readonly=on,file="+esc(m.ovmf(ovmfCode)),
		"-drive", "if=pflash,format=raw,file="+esc(m.path("efivars.fd")),
		"-device", "isa-applesmc,osk="+appleSMCKey,
		"-vga", "std", "-usb", "-device", "usb-kbd", "-device", "usb-tablet",
		"-drive", "if=none,id=opencore,format=raw,readonly=on,file=fat:16:"+esc(m.path("opencore")),
		"-device", "virtio-blk-pci,drive=opencore,bootindex=0",
		"-drive", "if=none,id=disk,format=qcow2,file="+esc(m.path(m.sys.disk)),
		"-device", "ide-hd,bus=ide.1,drive=disk",
		// Mac OS X keeps universal time in the Mac's clock.
		"-rtc", "base=utc,clock=host",
		// No sleep: a Tiger that sleeps here stops, its clock and screen
		// still, until a key or the mouse reaches it. Without S3 and S4 it
		// keeps running when Energy Saver's time comes.
		"-global", "ICH9-LPC.disable_s3=1", "-global", "ICH9-LPC.disable_s4=1",
		"-display", "none", "-vnc", "unix:"+m.path("vnc.sock"), "-qmp", "unix:"+m.path("qmp.sock")+",server=on,wait=off",
		"-monitor", "none", "-serial", "file:"+m.path("serial.log"), "-pidfile", m.path("qemu.pid"), "-daemonize")
	if installer {
		// The Installer ends by restarting the Mac, which would start the DVD
		// again: the restart ends the emulator, as for the PowerPC Mac.
		args = append(args, "-drive", "if=none,id=cd,format=dmg,readonly=on,file="+esc(m.path("media/"+m.sys.media)),
			"-device", "ide-cd,bus=ide.0,drive=cd", "-nic", "none", "-no-reboot")
	} else {
		args = append(args, "-drive", "if=none,id=cd,media=cdrom", "-device", "ide-cd,bus=ide.0,drive=cd",
			"-netdev", m.sys.network+m.hostForwards(), "-device", "e1000-82545em,netdev=net0,mac="+m.sys.ethernet+",romfile=")
		if dev := m.qeglDevice(); dev != "" {
			args = append(args, "-device", dev)
		}
	}
	return args
}

// runtimeCommand is a program of a runtime, with the runtime's libraries.
func runtimeCommand(ctx context.Context, dir, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, filepath.Join(dir, program), args...)
	cmd.Env = runtimeEnv(dir)
	return cmd
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// download fetches a small file whole and keeps it only when it is the one
// pinned.
func download(ctx context.Context, url, dst, digest string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d. Continue to retry.", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = verifySHA256(tmp, digest, size)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func verifySHA256(path, digest string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("the download does not match its published checksum. Continue to fetch it again.")
	}
	return nil
}
