package macos9

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func argPairs(args []string) map[string][]string {
	pairs := map[string][]string{}
	for i := 0; i < len(args)-1; i++ {
		if strings.HasPrefix(args[i], "-") {
			pairs[args[i]] = append(pairs[args[i]], args[i+1])
		}
	}
	return pairs
}

// The Mac OS X app's Mac is a PowerPC one of its own: a G4 on the PMU board
// with ADB input, where the USB tablet is the only USB pointer (beside the
// plain PMU board's USB mouse the arrow did not follow the browser's), its
// own disk and sockets, and no part of what the Mac OS 9 guest needs in its
// ROM.
func TestMacOSXLaunchIsAPowerPCMacOfItsOwn(t *testing.T) {
	root, shared := t.TempDir(), t.TempDir()
	m := NewMacOSX(root, shared)
	for _, installer := range []bool{false, true} {
		args := m.launchArgs(installer)
		pairs := argPairs(args)
		for flag, want := range map[string]string{
			"-machine": "mac99,via=pmu-adb", "-cpu": "G4", "-m": "1024", "-g": "1024x768x32",
			"-name": "exe-mac-osx", "-rtc": "base=utc,clock=vm",
			"-drive": "file=" + filepath.Join(root, "macosx.qcow2") + ",format=qcow2,media=disk",
			"-vnc":   "unix:" + filepath.Join(root, "vnc.sock"),
			"-qmp":   "unix:" + filepath.Join(root, "qmp.sock") + ",server=on,wait=off",
		} {
			if len(pairs[flag]) != 1 || pairs[flag][0] != want {
				t.Errorf("installer=%v: %s is %q, want %q", installer, flag, pairs[flag], want)
			}
		}
		if got := strings.Join(pairs["-device"], " "); !strings.Contains(got, "usb-tablet") || !strings.Contains(got, "xres=1024,yres=768") {
			t.Errorf("installer=%v: devices %q", installer, got)
		}
		// The tablet is the pointer, in the Installer and after: the arrow is
		// then where the browser's is. It needs the USB bus, and a board
		// that builds no USB mouse beside it (see macOSXMachine).
		if !strings.Contains(" "+strings.Join(args, " ")+" ", " -usb -device usb-tablet ") || strings.Contains(strings.Join(args, " "), "usb-mouse") {
			t.Errorf("installer=%v: the tablet is not the Mac's one USB pointer", installer)
		}
		if strings.Contains(strings.Join(args, " "), "nvramrc") {
			t.Errorf("installer=%v: Mac OS X needs no tablet driver in the ROM", installer)
		}
		joined := " " + strings.Join(args, " ") + " "
		if installer {
			for _, want := range []string{" -no-reboot ", " -boot d ", " -nic none ", " -cdrom " + filepath.Join(root, "media/MacOSX10.4-Tiger-Install.toast") + " "} {
				if !strings.Contains(joined, want) {
					t.Errorf("the install boot lacks %q", want)
				}
			}
		} else {
			if strings.Contains(joined, " -no-reboot ") || !strings.Contains(joined, " -boot c ") || !strings.Contains(joined, " -netdev user,id=net0,ipv6=off ") || !strings.Contains(joined, "sungem,netdev=net0,mac=52:54:00:10:04:00") {
				t.Errorf("a normal boot is %q", joined)
			}
		}
	}
	if got := NewMacOSX(root, shared).Status().Steps; got[1].Title != "Download Mac OS X" || got[5].Title != "Install Mac OS X" {
		t.Fatalf("steps name the wrong system: %+v", got)
	}
	nine := New(root)
	if nine.launchArgs(false)[3] != "exe-mac-os9" || !strings.Contains(strings.Join(nine.launchArgs(false), " "), " -netdev user,id=net0 -device") || m.MaxCDSize() <= nine.MaxCDSize() {
		t.Fatal("the Mac OS 9 Mac changed, or a DVD does not fit Mac OS X's library")
	}
}

// One emulator runtime serves both Macs: the Mac OS X Mac finds QEMU, its
// firmware and the optional sound build in the root it was told, and keeps
// everything else in its own.
func TestMacOSXRunsTheSharedRuntime(t *testing.T) {
	root, shared := t.TempDir(), t.TempDir()
	m := NewMacOSX(root, shared)
	if m.binary("qemu-system-ppc") != "qemu-system-ppc" {
		t.Fatal("found an emulator where none is unpacked")
	}
	writeFixture(t, filepath.Join(shared, "runtime/usr/bin/qemu-system-ppc"), "emulator", 0700)
	writeFixture(t, filepath.Join(shared, "runtime/usr/share/qemu/openbios-ppc"), "firmware", 0600)
	if got := m.binary("qemu-system-ppc"); got != filepath.Join(shared, "runtime/usr/bin/qemu-system-ppc") {
		t.Fatalf("emulator is %q", got)
	}
	firmware := argPairs(m.launchArgs(false))["-L"]
	if len(firmware) != 2 || firmware[0] != filepath.Join(root, "firmware") || firmware[1] != filepath.Join(shared, "runtime/usr/share/qemu") {
		t.Fatalf("firmware search path is %q", firmware)
	}
	if strings.Contains(strings.Join(m.launchArgs(false), " "), "audiodev") {
		t.Fatal("sound without a sound build")
	}
	if !strings.Contains(strings.Join(m.env(), "\n"), "LD_LIBRARY_PATH="+filepath.Join(shared, "runtime/usr/lib")) {
		t.Fatal("the emulator's libraries are not the shared runtime's")
	}
	// Tiger plays through the Screamer build as Mac OS 9 does (its speech
	// was recorded from that device on 2026-10-11), so a node that has the
	// build gives both Macs sound.
	writeFixture(t, filepath.Join(shared, "audio/qemu-system-ppc"), "emulator", 0700)
	writeFixture(t, filepath.Join(shared, "audio/openbios-ppc"), "firmware", 0600)
	if got := m.binary("qemu-system-ppc"); got != filepath.Join(shared, "audio/qemu-system-ppc") {
		t.Fatalf("with a sound build the emulator is %q", got)
	}
	sound := strings.Join(m.launchArgs(false), " ")
	for _, want := range []string{"-bios " + filepath.Join(shared, "audio/openbios-ppc"), "-audiodev none,id=mac-audio", "-global screamer.audiodev=mac-audio", filepath.Join(root, "vnc.sock") + ",audiodev=mac-audio"} {
		if !strings.Contains(sound, want) {
			t.Errorf("sound launch lacks %q", want)
		}
	}
}

// An emulator with the qegl device (OpenGL on this machine's GPU) gives it
// to Mac OS X's installed system: the guest's drivers make Quartz Extreme
// and Core Image of it. Not to the install boot, not to Mac OS 9, and not
// from an emulator that lacks it.
func TestMacOSXGetsTheGPUDeviceWhenTheEmulatorHasIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX emulator fixture")
	}
	root, shared := t.TempDir(), t.TempDir()
	m := NewMacOSX(root, shared)
	writeFixture(t, filepath.Join(shared, "audio/openbios-ppc"), "firmware", 0600)
	writeFixture(t, filepath.Join(shared, "audio/qemu-system-ppc"), "#!/bin/sh\necho 'screamer options:'\n", 0700)
	if strings.Contains(strings.Join(m.launchArgs(false), " "), "qegl") {
		t.Fatal("the GPU device from an emulator without it")
	}
	// a new emulator file is asked again
	writeFixture(t, filepath.Join(shared, "audio/qemu-system-ppc"), "#!/bin/sh\n[ \"$2\" = qegl,help ] && echo 'qegl options:' && echo '  apps=<uint32>'\n", 0700)
	if !strings.Contains(" "+strings.Join(m.launchArgs(false), " ")+" ", " -device qegl ") {
		t.Fatalf("an installed Mac OS X lacks the GPU device: %q", m.launchArgs(false))
	}
	if strings.Contains(strings.Join(m.launchArgs(true), " "), "qegl") {
		t.Fatal("the install boot has the GPU device")
	}
	if strings.Contains(strings.Join(New(shared).launchArgs(false), " "), "qegl") {
		t.Fatal("Mac OS 9 has the GPU device")
	}
	// an emulator whose device can log: to the Mac's folder
	writeFixture(t, filepath.Join(shared, "audio/qemu-system-ppc"), "#!/bin/sh\n[ \"$2\" = qegl,help ] && echo 'qegl options:' && echo '  logfile=<str>'\n", 0700)
	if want := " -device qegl,logfile=" + filepath.Join(root, "qegl.log") + " "; !strings.Contains(" "+strings.Join(m.launchArgs(false), " ")+" ", want) {
		t.Fatalf("the GPU device does not log to the Mac's folder: %q", m.launchArgs(false))
	}
}

// An install DVD is gigabytes: a download that stopped goes on from the
// bytes it has, and only a file that fails its checksum is thrown away.
func TestInstallerDownloadResumes(t *testing.T) {
	data := []byte(strings.Repeat("Mac OS X Install DVD ", 4096))
	sum := sha1.Sum(data)
	var ranges []string
	failAt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		if failAt > 0 {
			// Promise the whole file, send part of it, and hang up.
			n := failAt
			failAt = 0
			w.Header().Set("Content-Length", "999999")
			w.Write(data[:n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "dvd.toast", time.Time{}, strings.NewReader(string(data)))
	}))
	defer srv.Close()
	sys := *macOSX
	sys.mediaURL, sys.mediaSHA1, sys.mediaSize, sys.media = srv.URL, hex.EncodeToString(sum[:]), int64(len(data)), "dvd.toast"
	root := t.TempDir()
	m := newManager(&sys, root, root)
	media, part := m.path("media/dvd.toast"), m.path("media/dvd.toast.part")

	failAt = 30000
	if err := m.download(context.Background()); err == nil {
		t.Fatal("a cut download passed for a whole one")
	}
	info, err := os.Stat(part)
	if err != nil || info.Size() != 30000 {
		t.Fatalf("the part was not kept: %v %v", info, err)
	}
	if err = m.download(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(media); string(got) != string(data) {
		t.Fatal("the resumed file is not the installer")
	}
	if len(ranges) != 2 || ranges[0] != "" || ranges[1] != "bytes=30000-" {
		t.Fatalf("requests asked for %q", ranges)
	}
	if _, err = os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("the part outlived the download")
	}

	// A saved installer is verified, not fetched again.
	ranges = nil
	if err = m.download(context.Background()); err != nil || len(ranges) != 0 {
		t.Fatalf("fetched a verified installer again: %v %q", err, ranges)
	}

	// A part that is not this file's beginning fails the checksum and goes.
	os.Remove(media)
	writeFixture(t, part, strings.Repeat("x", 30000), 0600)
	if err = m.download(context.Background()); err == nil {
		t.Fatal("accepted a file that begins with something else")
	}
	if _, err = os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("kept a part that failed its checksum")
	}
	if err = m.download(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A second way into the Mac that does not pass through the emulated keyboard
// and pointer: Tiger's Remote Login and its screen sharing, forwarded to
// loopback ports of this machine and to nothing the network can reach. The
// installer boot has no network and no forwards, the Mac OS 9 Mac has none,
// and the ports are written down for a daemon that starts later.
func TestMacOSXForwardsItsSharingServicesToLoopback(t *testing.T) {
	root := t.TempDir()
	m := NewMacOSX(root, root)
	ports, err := m.pickPorts(false)
	if err != nil || ports["ssh"] == 0 || ports["vnc"] == 0 || ports["ssh"] == ports["vnc"] {
		t.Fatalf("ports %v: %v", ports, err)
	}
	m.ports = ports
	netdev := argPairs(m.launchArgs(false))["-netdev"]
	want := fmt.Sprintf("user,id=net0,ipv6=off,hostfwd=tcp:127.0.0.1:%d-:22,hostfwd=tcp:127.0.0.1:%d-:5900", ports["ssh"], ports["vnc"])
	if len(netdev) != 1 || netdev[0] != want {
		t.Fatalf("netdev %q, want %q", netdev, want)
	}
	if args := strings.Join(m.launchArgs(true), " "); strings.Contains(args, "hostfwd") {
		t.Fatal("the installer boot forwards a port")
	}
	if again, _ := m.pickPorts(false); again["ssh"] != ports["ssh"] || again["vnc"] != ports["vnc"] {
		t.Fatalf("free ports were not taken again: %v then %v", ports, again)
	}
	// A port somebody else holds is passed over, not fought for.
	held, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ports["ssh"]))
	if err != nil {
		t.Skip(err)
	}
	defer held.Close()
	if other, err := m.pickPorts(false); err != nil || other["ssh"] == ports["ssh"] || other["ssh"] == 0 {
		t.Fatalf("took a port in use: %v %v", other, err)
	}
	// Status names the ports of a running, installed Mac, also to a daemon
	// that did not launch it; a stopped Mac has none to name.
	later := NewMacOSX(root, root)
	writeFixture(t, filepath.Join(root, "installed.json"), "{}", 0600)
	if s := later.Status(); s.Running || s.Ports != nil {
		t.Fatalf("a stopped Mac names ports: %+v", s.Ports)
	}
	ln, err := net.Listen("unix", filepath.Join(root, "qmp.sock"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	saved := later.savedPorts()
	if s := later.Status(); !s.Running || s.Ports["ssh"] != saved["ssh"] || s.Ports["vnc"] != saved["vnc"] || saved["ssh"] == 0 {
		t.Fatalf("a running Mac's ports: %+v, saved %v", s.Ports, saved)
	}
	if _, err := m.pickPorts(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ports.json")); !os.IsNotExist(err) {
		t.Fatal("the installer boot left ports written down")
	}
	nine := New(t.TempDir())
	if p, _ := nine.pickPorts(false); p != nil || strings.Contains(strings.Join(nine.launchArgs(false), " "), "hostfwd") {
		t.Fatal("the Mac OS 9 Mac forwards a port")
	}
}

// What a viewer found out about the system's pointer stays with the Mac: one
// of the corrections the system is known to need, and nothing else.
func TestPointerTrimIsKeptWithTheMac(t *testing.T) {
	root := t.TempDir()
	m := NewMacOSX(root, root)
	if got := m.Status().Trim; got != 0 {
		t.Fatalf("a new Mac's trim is %v", got)
	}
	if err := m.SetPointerTrim(0.15); err != nil {
		t.Fatal(err)
	}
	if got := NewMacOSX(root, root).Status().Trim; got != 0.15 {
		t.Fatalf("the trim did not stay with the Mac: %v", got)
	}
	for _, bad := range []float64{0.1, 0.5, -0.15, 15} {
		if err := m.SetPointerTrim(bad); err == nil {
			t.Fatalf("took %v for a correction", bad)
		}
	}
	if got := m.Status().Trim; got != 0.15 {
		t.Fatalf("a refused value changed the trim to %v", got)
	}
	writeFixture(t, filepath.Join(root, "pointer.json"), `{"trim":0.4}`, 0600)
	if got := m.Status().Trim; got != 0 {
		t.Fatalf("a value nobody could have set is reported: %v", got)
	}
	if err := m.SetPointerTrim(0); err != nil || m.Status().Trim != 0 {
		t.Fatal("the trim cannot be taken back")
	}
	// Mac OS 9's pointer is its own driver's and takes no correction.
	if err := New(t.TempDir()).SetPointerTrim(0.15); err == nil {
		t.Fatal("the Mac OS 9 Mac took a correction")
	}
}
