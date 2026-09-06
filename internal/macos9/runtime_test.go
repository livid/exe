package macos9

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedRuntimeRepairsFirmware(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX runtime fixture")
	}
	m := New(t.TempDir())
	dir := m.path("runtime")
	for _, name := range []string{"qemu-system-ppc", "qemu-img"} {
		writeFixture(t, filepath.Join(dir, "usr/bin", name), "#!/bin/sh\nexit 0\n", 0700)
	}
	writeFixture(t, filepath.Join(dir, "usr/share/qemu/openbios-ppc"), "openbios fixture", 0600)
	writeFixture(t, filepath.Join(dir, "usr/share/seabios/vgabios-stdvga.bin"), "vga fixture", 0600)
	// Both binaries answer --version, but the final firmware copy never happened.
	if err := m.ensureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "usr/share/qemu/vgabios-stdvga.bin"))
	if err != nil || string(b) != "vga fixture" {
		t.Fatalf("firmware not repaired: %q %v", b, err)
	}
	// A later incomplete extraction must not pass readiness checks either.
	os.Remove(filepath.Join(dir, "usr/share/qemu/openbios-ppc"))
	if err = validateRuntime(context.Background(), dir); err == nil {
		t.Fatal("accepted runtime without OpenBIOS")
	}
}

func TestRuntimeSwapRecovery(t *testing.T) {
	m := New(t.TempDir())
	writeFixture(t, m.path("runtime/sentinel"), "old runtime", 0600)
	if err := m.activateRuntime(m.path("missing-staging")); err == nil {
		t.Fatal("activated nonexistent staging")
	}
	if b, err := os.ReadFile(m.path("runtime/sentinel")); err != nil || string(b) != "old runtime" {
		t.Fatal("lost prior runtime after failed activation")
	}
	if err := os.Rename(m.path("runtime"), m.path("runtime.previous")); err != nil {
		t.Fatal(err)
	}
	// Recover a daemon exit between moving the old runtime and activating the new.
	if err := m.recoverRuntime(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(m.path("runtime/sentinel")); err != nil || string(b) != "old runtime" {
		t.Fatal("did not recover interrupted swap")
	}
	writeFixture(t, m.path("runtime.staging/sentinel"), "new runtime", 0600)
	if err := m.activateRuntime(m.path("runtime.staging")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(m.path("runtime/sentinel")); err != nil || string(b) != "new runtime" {
		t.Fatal("new runtime not activated")
	}
}

func TestMacLaunchUsesIntegratedPointerAndDefaultResolution(t *testing.T) {
	m := New(t.TempDir())
	for _, installer := range []bool{false, true} {
		args := m.launchArgs(installer)
		pairs := map[string][]string{}
		for i := 0; i < len(args)-1; i++ {
			if strings.HasPrefix(args[i], "-") {
				pairs[args[i]] = append(pairs[args[i]], args[i+1])
			}
		}
		if pairs["-device"][0] != "VGA,edid=on,xres=800,yres=600,xmax=1024,ymax=768" || pairs["-vga"][0] != "none" {
			t.Fatal("guest must advertise only the three supported resolutions")
		}
		if pairs["-L"][0] != m.path("firmware") {
			t.Fatal("bundled native display driver must take precedence")
		}
		if pairs["-g"][0] != "800x600x32" {
			t.Fatal("unexpected default resolution")
		}
		if pairs["-machine"][0] != "mac99" {
			t.Fatal("tablet driver requires CUDA, not PMU")
		}
		tablet, loader := false, false
		for _, v := range pairs["-device"] {
			if v == "usb-tablet" {
				tablet = true
			}
		}
		for _, v := range pairs["-prom-env"] {
			if strings.HasPrefix(v, "nvramrc=: b64 ") {
				loader = true
			}
		}
		if !tablet || !loader {
			t.Fatal("guest would boot without absolute pointer integration")
		}
	}
}

func TestDisplayDriverRepairsMissingOrIncompleteFile(t *testing.T) {
	m := New(t.TempDir())
	path := m.path("firmware/qemu_vga.ndrv")
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			if err := os.WriteFile(path, []byte("interrupted"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := m.ensureDisplayDriver(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != string(vgaDriver) {
			t.Fatal("native display driver not restored")
		}
	}
}
