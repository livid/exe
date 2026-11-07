package macos9

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tests never fetch exe's emulator from the internet; those that want it serve their own.
func TestMain(m *testing.M) {
	emulatorBase, intelEmulatorBase = "", ""
	os.Exit(m.Run())
}

// a build of exe's emulator: POSIX scripts that answer as QEMU does to the startup check
func fakeEmulator(t *testing.T, version string) []byte {
	t.Helper()
	return tarGz(t, map[string]string{
		"EXE-QEMU.json":                     `{"version": "` + version + `", "features": ["screamer", "vnc-pacing"]}`,
		"usr/bin/qemu-system-ppc":           "#!/bin/sh\nexit 0\n",
		"usr/bin/qemu-img":                  "#!/bin/sh\nexit 0\n",
		"usr/share/qemu/openbios-ppc":       "screamer openbios",
		"usr/share/qemu/vgabios-stdvga.bin": "vga",
	})
}

// a build of exe's x86 emulator, the same way
func fakeIntelEmulator(t *testing.T, version string) []byte {
	t.Helper()
	return tarGz(t, map[string]string{
		"EXE-QEMU.json":                     `{"version": "` + version + `", "features": ["kvm", "qegl", "ovmf-ia32"]}`,
		"usr/bin/qemu-system-x86_64":        "#!/bin/sh\nexit 0\n",
		"usr/bin/qemu-img":                  "#!/bin/sh\nexit 0\n",
		"usr/share/OVMF/" + ovmfCode:        "exe's ovmf code",
		"usr/share/OVMF/" + ovmfVars:        "exe's ovmf vars",
		"usr/share/qemu/vgabios-stdvga.bin": "vga",
		"usr/share/qemu/keymaps/en-us":      "keymap",
		"usr/share/doc/ovmf-ia32/copyright": "edk2",
	})
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, text := range files {
		mode := int64(0644)
		if strings.HasPrefix(name, "usr/bin/") {
			mode = 0755
		}
		tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(text)), Typeflag: tar.TypeReg})
		tw.Write([]byte(text))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func serveEmulator(t *testing.T, archive []byte, pinned []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(pinned)
	plat := runtime.GOOS + "-" + runtime.GOARCH
	saved, savedBase := emulatorBuilds[plat], emulatorBase
	emulatorBuilds[plat] = emulatorBuild{hex.EncodeToString(sum[:]), int64(len(pinned))}
	emulatorBase = srv.URL + "/"
	t.Cleanup(func() { emulatorBuilds[plat], emulatorBase = saved, savedBase })
}

// serveIntelEmulator serves archive as exe's x86 emulator, pinned to pinned's checksum, on a
// computer taken to be one the Intel Mac runs on; it returns the paths asked for.
func serveIntelEmulator(t *testing.T, archive []byte, pinned []byte) *[]string {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(pinned)
	saved, savedBase, savedHost := intelEmulatorBuild, intelEmulatorBase, intelHost
	intelEmulatorBuild = emulatorBuild{hex.EncodeToString(sum[:]), int64(len(pinned))}
	intelEmulatorBase, intelHost = srv.URL+"/", true
	t.Cleanup(func() { intelEmulatorBuild, intelEmulatorBase, intelHost = saved, savedBase, savedHost })
	return &asked
}

// ubuntuIntelRuntime is the Intel Mac's runtime as Ubuntu's packages make it
func ubuntuIntelRuntime(t *testing.T, m *Manager) {
	t.Helper()
	dir := m.path("runtime")
	for _, name := range []string{"qemu-system-x86_64", "qemu-img"} {
		writeFixture(t, filepath.Join(dir, "usr/bin", name), "#!/bin/sh\nexit 0\n", 0700)
	}
	writeFixture(t, filepath.Join(dir, "usr/share/OVMF", ovmfCode), "ubuntu's ovmf code", 0600)
	writeFixture(t, filepath.Join(dir, "usr/share/OVMF", ovmfVars), "ubuntu's ovmf vars", 0600)
	writeFixture(t, filepath.Join(dir, "usr/share/seabios/vgabios-stdvga.bin"), "vga", 0600)
}

// The Intel Mac's runtime of Ubuntu's packages gives way to exe's x86 emulator, from the
// x86 builds' own place, and the Mac then runs its QEMU and its 32-bit firmware.
func TestIntelRuntimeBecomesExesX86Emulator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX runtime fixture")
	}
	m := NewMacOSXIntel(t.TempDir())
	ubuntuIntelRuntime(t, m)
	archive := fakeIntelEmulator(t, intelEmulatorVersion)
	asked := serveIntelEmulator(t, archive, archive)
	if err := m.ensureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "/" + intelEmulatorVersion + "/qemu-x86-" + intelEmulatorVersion + "-linux-amd64.tar.gz"
	if len(*asked) != 1 || (*asked)[0] != want {
		t.Fatalf("fetched %q, want %q", *asked, want)
	}
	dir := m.path("runtime")
	if e := m.ownEmulator(); e == nil || e.Version != intelEmulatorVersion || !e.has("qegl") {
		t.Fatalf("runtime after: %+v", e)
	}
	if b := m.binary("qemu-system-x86_64"); b != filepath.Join(dir, "usr/bin/qemu-system-x86_64") {
		t.Fatalf("the Mac runs %s", b)
	}
	if b, err := os.ReadFile(m.ovmf(ovmfCode)); err != nil || string(b) != "exe's ovmf code" {
		t.Fatalf("firmware %s: %q %v", m.ovmf(ovmfCode), b, err)
	}
	args := " " + strings.Join(m.intelLaunchArgs(false), " ") + " "
	if !strings.Contains(args, " -L "+filepath.Join(dir, "usr/share/qemu")+" ") ||
		!strings.Contains(args, "file="+filepath.Join(dir, "usr/share/OVMF", ovmfCode)+" ") {
		t.Fatalf("launch without the emulator's firmware: %s", args)
	}
	// the next start finds it in place and fetches nothing
	intelEmulatorBase = "http://127.0.0.1:1/"
	if err := m.ensureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A download of the x86 emulator that does not match its pin is not installed: a runtime that
// works stays, and without one the start fails. The PowerPC builds' pins are not the x86 one's.
func TestIntelEmulatorChecksumIsRequired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX runtime fixture")
	}
	good := fakeIntelEmulator(t, intelEmulatorVersion)
	serveIntelEmulator(t, fakeIntelEmulator(t, "9.1.0-exe.tampered"), good)
	m := NewMacOSXIntel(t.TempDir())
	if err := m.ensureRuntime(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a tampered download was accepted: %v", err)
	}
	if _, err := os.Stat(m.path("runtime")); !os.IsNotExist(err) {
		t.Fatal("a tampered download became the runtime")
	}
	m = NewMacOSXIntel(t.TempDir())
	ubuntuIntelRuntime(t, m)
	if err := m.ensureRuntime(context.Background()); err != nil {
		t.Fatalf("the runtime that works was not kept: %v", err)
	}
	if m.ownEmulator() != nil {
		t.Fatal("a tampered download became the runtime")
	}
	if b, _ := os.ReadFile(m.ovmf(ovmfCode)); string(b) != "ubuntu's ovmf code" {
		t.Fatalf("firmware now %q", b)
	}
}

// A runtime of the system's packages (no EXE-QEMU.json) gives way to exe's own emulator, which
// then runs both Macs with its Screamer, ahead of a locally built one in audio/.
func TestRuntimeBecomesExesOwnEmulator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX runtime fixture")
	}
	m := New(t.TempDir())
	dir := m.path("runtime")
	for _, name := range []string{"qemu-system-ppc", "qemu-img"} {
		writeFixture(t, filepath.Join(dir, "usr/bin", name), "#!/bin/sh\nexit 0\n", 0700)
	}
	writeFixture(t, filepath.Join(dir, "usr/share/qemu/openbios-ppc"), "stock openbios", 0600)
	writeFixture(t, filepath.Join(dir, "usr/share/qemu/vgabios-stdvga.bin"), "vga", 0600)
	writeFixture(t, m.path("audio/qemu-system-ppc"), "#!/bin/sh\nexit 0\n", 0700)
	writeFixture(t, m.path("audio/openbios-ppc"), "local screamer openbios", 0600)
	if !strings.Contains(m.binary("qemu-system-ppc"), "audio") {
		t.Fatal("before: the local Screamer build should run the Macs")
	}
	archive := fakeEmulator(t, emulatorVersion)
	serveEmulator(t, archive, archive)
	if err := m.ensureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e := m.ownEmulator(); e == nil || e.Version != emulatorVersion || !m.hasScreamer() {
		t.Fatalf("runtime after: %+v", e)
	}
	if b := m.binary("qemu-system-ppc"); b != filepath.Join(dir, "usr/bin/qemu-system-ppc") {
		t.Fatalf("the Macs run %s", b)
	}
	args := " " + strings.Join(m.launchArgs(false), " ") + " "
	if !strings.Contains(args, " -bios "+filepath.Join(dir, "usr/share/qemu/openbios-ppc")+" ") ||
		!strings.Contains(args, "screamer.audiodev=mac-audio") {
		t.Fatalf("launch without the emulator's own Screamer and OpenBIOS: %s", args)
	}
	// the next start finds it in place and fetches nothing
	emulatorBase = "http://127.0.0.1:1/"
	if err := m.ensureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A download that does not match its pinned checksum is not installed; the runtime that
// works stays, and without one the start fails.
func TestEmulatorChecksumIsRequired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX runtime fixture")
	}
	m := New(t.TempDir())
	good := fakeEmulator(t, emulatorVersion)
	serveEmulator(t, fakeEmulator(t, "9.1.0-exe.tampered"), good)
	if err := m.ensureRuntime(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a tampered download was accepted: %v", err)
	}
	if _, err := os.Stat(m.path("runtime")); !os.IsNotExist(err) {
		t.Fatal("a tampered download became the runtime")
	}
}

// An archive may not write outside the runtime
func TestEmulatorArchiveStaysInside(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	dir := t.TempDir()
	if err := untarGz(&buf, filepath.Join(dir, "staging")); err == nil {
		t.Fatal("unpacked a path outside the runtime")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatal("wrote outside the runtime")
	}
}
