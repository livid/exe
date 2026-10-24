package macos9

import (
	"strings"
	"time"
)

// system is what tells one of this package's Macs from the other: the
// installer it is set up from, its hard disk, the machine QEMU builds for it
// and the words setup says. The setup steps, the emulator runtime, the CD
// drive and the display are the Manager's, the same for both.
type system struct {
	name    string // "Mac OS 9", as setup and its errors say it
	version string // installed.json's
	process string // QEMU's -name

	mediaURL, mediaSHA1 string
	mediaSize           int64
	media               string // the installer's file in media/
	downloading         string // step 1, while it runs
	maxCD               int64  // the largest image the CD library takes

	disk, diskSize string // the hard disk's file, and qemu-img's size for a new one
	diskReady      string // step 3, done

	install  string        // step 5, the user's turn
	shutDown string        // Finish, refused while the Mac still runs
	setup    time.Duration // how long one run of setup may take, download included

	// machine is the Mac itself: board, memory, display, pointer and clock.
	machine  func(installer bool) []string
	network  string    // QEMU's -netdev for an installed Mac
	forwards []forward // services of the Mac this machine can reach
	ethernet string    // the address of its network card
	// trims are the shares of an absolute pointer's range the system may
	// leave unused, split between the two edges: what the viewer has to
	// allow for to put the arrow where the browser's pointer is. Tiger as
	// installed (10.4.0) uses the whole range; from its updates on (seen in
	// 10.4.11) the HID driver drops 15%, 7.5% at each edge, and has no
	// setting for it. The viewer finds out which and tells the daemon.
	trims []float64
	// gpu: the Mac gets the qegl device when the emulator has it, OpenGL
	// passed through to this machine's GPU. With the guest's drivers Tiger
	// composites with Quartz Extreme and runs Core Image there, and Mac OS 9's
	// OpenGL gets a renderer on the GPU from the QEGL Engine extension;
	// without them the device sits unused.
	gpu bool
}

// A forward is a service inside the Mac that this machine, and nothing
// beyond it, can reach: QEMU listens on a loopback port of the host and
// hands each connection to the guest's port. Nobody answers there until the
// Mac's own Sharing preferences turn the service on. It is a second way
// into the guest, one that does not pass through the emulated keyboard and
// pointer: a shell for commands, and the Mac's own screen sharing.
type forward struct {
	name  string // as Status names its port: "ssh", "vnc"
	guest int
}

var macOS9 = &system{
	name: "Mac OS 9", version: "9.2.2", process: "exe-mac-os9",
	mediaURL:    "https://archive.org/download/mac-os-9.2.2-universal-inst/Mac%20OS%209.2.2%20Universal%20Inst.iso",
	mediaSHA1:   "b7390d0444b9ea637c5081c1655c1bf0ca864a3b",
	mediaSize:   521435136,
	media:       "MacOS9.2.2-Universal.iso",
	downloading: "Downloading the 497 MiB Universal installer from Internet Archive…",
	maxCD:       2 << 30,
	disk:        "macos9.qcow2", diskSize: "2G",
	diskReady: "Your 2 GB hard disk is saved on this computer.",
	install:   "Use Drive Setup and Apple Software Restore in the Mac screen below.",
	shutDown:  "Shut down the Mac from its Special menu first, then try again.",
	setup:     45 * time.Minute,
	machine:   macOS9Machine,
	network:   "user,id=net0",
	ethernet:  "52:54:00:09:02:22",
	gpu:       true,
}

func macOS9Machine(installer bool) []string {
	// The Mac's time of day is seeded once, when the machine is created: the
	// CUDA clock this board carries never consults -rtc again, so whatever
	// base= says at boot is what OS 9 believes until someone opens Date &
	// Time. An install is pinned to 2003, where the Universal installer and
	// the era's software expect to be; a normal boot starts today, so that
	// current certificates are judged against a date that is in their window.
	// base=localtime, not utc, because OS 9 keeps local time in its clock.
	rtc := "base=localtime,clock=vm"
	if installer {
		rtc = "base=2003-06-01T12:00:00,clock=vm"
	}
	return []string{"-machine", "mac99", "-cpu", "G4", "-m", "512", "-accel", "tcg,tb-size=128", "-prom-env", "vga-ndrv?=true", "-g", "800x600x32", "-vga", "none", "-device", "VGA,edid=on,xres=800,yres=600,xmax=1024,ymax=768", "-usb", "-device", "usb-tablet", "-prom-env", "use-nvramrc?=true", "-prom-env", "nvramrc=" + strings.TrimSpace(tabletNVRAMRC), "-rtc", rtc}
}

// Mac OS X 10.4 Tiger, from Apple's retail install DVD for PowerPC
// (2Z691-5305-A, "Includes Xcode 2"). The Internet Archive publishes the
// image's SHA-1 in the item's metadata and its MD5 beside the image.
var macOSX = &system{
	name: "Mac OS X", version: "10.4", process: "exe-mac-osx",
	mediaURL:    "https://archive.org/download/691-5305-A2ZMac_OS_X_v10.4_Tiger._Includes_Xcode_2._Install_Disc_2005_DVD/691-5305-A%2C2Z%2CMac%20OS%20X%20v10.4%20Tiger.%20Includes%20Xcode%202.%20Install%20Disc_2005%20%28DVD%29.toast",
	mediaSHA1:   "4c84934eb70c723a0ea41a832a4e76de0c273124",
	mediaSize:   2832271360,
	media:       "MacOSX10.4-Tiger-Install.toast",
	downloading: "Downloading the 2.6 GiB Tiger install DVD from Internet Archive…",
	maxCD:       9 << 30, // a dual-layer DVD
	disk:        "macosx.qcow2", diskSize: "100G",
	diskReady: "Your 100 GB hard disk is saved on this computer.",
	install:   "Use Disk Utility and the Installer in the Mac screen below.",
	shutDown:  "Wait until the Installer restarts the Mac, then try again.",
	setup:     6 * time.Hour,
	machine:   macOSXMachine,
	// Tiger takes an IPv6 address from the emulator's router and tries it
	// first for every name; nothing answers there, and each connection
	// waited 24 seconds before it fell back (0.3 seconds with it off).
	network: "user,id=net0,ipv6=off",
	// Remote Login, and Apple Remote Desktop's VNC, in Tiger's Sharing pane.
	forwards: []forward{{"ssh", 22}, {"vnc", 5900}},
	ethernet: "52:54:00:10:04:00",
	trims:    []float64{0, 0.15},
	gpu:      true,
}

func macOSXMachine(installer bool) []string {
	// The pointer must be where the browser's is, as Mac OS 9's is: that is
	// the USB tablet, which Mac OS X's own driver takes with no loader in the
	// ROM. It has to be the only USB pointer. The plain PMU board (via=pmu)
	// builds a USB mouse of its own, and once the installed system had started
	// that mouse, not the tablet, was the pointer QEMU's display fed: the
	// arrow then followed the browser's by relative steps the Mac speeds up,
	// and landed nowhere near it. via=pmu-adb gives the board an ADB keyboard
	// and mouse instead, as Mac OS 9's CUDA board has, and keeps the PMU,
	// under which Restart brings the Mac back.
	// Mac OS X keeps universal time in the Mac's clock, where Mac OS 9 kept
	// local time, and an install needs no date of its own.
	args := []string{"-machine", "mac99,via=pmu-adb", "-cpu", "G4", "-m", "1024", "-accel", "tcg,tb-size=256", "-prom-env", "vga-ndrv?=true", "-g", "1024x768x32", "-vga", "none", "-device", "VGA,edid=on,xres=1024,yres=768,xmax=1024,ymax=768", "-usb", "-device", "usb-tablet", "-rtc", "base=utc,clock=vm"}
	if installer {
		// The Installer ends by restarting the Mac, which would start the DVD
		// again. The restart ends the emulator instead, and the app's
		// "Installation finished" button takes it from there.
		args = append(args, "-no-reboot")
	}
	return args
}
