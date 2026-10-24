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
	emulatorBase = ""
	os.Exit(m.Run())
}

// a build of exe's emulator: POSIX scripts that answer as QEMU does to the startup check
func fakeEmulator(t *testing.T, version string) []byte {
	t.Helper()
	files := map[string]string{
		"EXE-QEMU.json":                     `{"version": "` + version + `", "features": ["screamer", "vnc-pacing"]}`,
		"usr/bin/qemu-system-ppc":           "#!/bin/sh\nexit 0\n",
		"usr/bin/qemu-img":                  "#!/bin/sh\nexit 0\n",
		"usr/share/qemu/openbios-ppc":       "screamer openbios",
		"usr/share/qemu/vgabios-stdvga.bin": "vga",
	}
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
