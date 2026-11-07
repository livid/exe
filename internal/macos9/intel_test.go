package macos9

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// The Mac OS X Intel app's Mac is an x86 one of its own: a q35 board under
// KVM with OVMF's 32-bit firmware and its own NVRAM, OpenCore on a bus
// Tiger cannot see, the Apple SMC with its key, the USB tablet as pointer,
// and the install DVD as the disk image it is.
func TestMacOSXIntelIsAKVMMacOfItsOwn(t *testing.T) {
	root := t.TempDir()
	m := NewMacOSXIntel(root)
	for _, installer := range []bool{false, true} {
		args := m.launchArgs(installer)
		pairs := argPairs(args)
		for flag, want := range map[string]string{
			"-machine": "q35", "-accel": "kvm", "-cpu": intelCPU, "-smp": "2", "-m": "2048",
			"-name": "exe-mac-osx-intel", "-vga": "std", "-rtc": "base=utc,clock=host",
			"-vnc": "unix:" + filepath.Join(root, "vnc.sock"),
			"-qmp": "unix:" + filepath.Join(root, "qmp.sock") + ",server=on,wait=off",
		} {
			if len(pairs[flag]) != 1 || pairs[flag][0] != want {
				t.Errorf("installer=%v: %s is %q, want %q", installer, flag, pairs[flag], want)
			}
		}
		joined := " " + strings.Join(args, " ") + " "
		for _, want := range []string{
			" if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF32_CODE_4M.fd ",
			" if=pflash,format=raw,file=" + filepath.Join(root, "efivars.fd") + " ",
			" isa-applesmc,osk=ourhardworkbythesewordsguardedpleasedontsteal(c)AppleComputerInc ",
			" -usb -device usb-kbd -device usb-tablet ",
			" if=none,id=opencore,format=raw,readonly=on,file=fat:16:" + filepath.Join(root, "opencore") + " ",
			" virtio-blk-pci,drive=opencore,bootindex=0 ",
			" if=none,id=disk,format=qcow2,file=" + filepath.Join(root, "macosx-intel.qcow2") + " ",
			" ide-cd,bus=ide.0,drive=cd ",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("installer=%v: launch lacks %q", installer, want)
			}
		}
		if strings.Contains(joined, "usb-mouse") || strings.Contains(joined, "qemu-system-ppc") || strings.Contains(joined, "-cdrom") {
			t.Errorf("installer=%v: a PowerPC Mac's part in %q", installer, joined)
		}
		if installer {
			for _, want := range []string{" if=none,id=cd,format=dmg,readonly=on,file=" + filepath.Join(root, "media/MacOSX10.4.11-Intel-Install.dmg") + " ", " -nic none ", " -no-reboot "} {
				if !strings.Contains(joined, want) {
					t.Errorf("the install boot lacks %q", want)
				}
			}
			if strings.Contains(joined, "-netdev") {
				t.Error("the install boot has a network")
			}
		} else {
			for _, want := range []string{" if=none,id=cd,media=cdrom ", " -netdev user,id=net0,ipv6=off ", " e1000-82545em,netdev=net0,mac=52:54:00:10:04:11,romfile= "} {
				if !strings.Contains(joined, want) {
					t.Errorf("a normal boot lacks %q in %q", want, joined)
				}
			}
			if strings.Contains(joined, " -no-reboot ") || strings.Contains(joined, ".dmg") {
				t.Errorf("a normal boot is %q", joined)
			}
		}
	}
	st := m.Status()
	if st.Steps[1].Title != "Download Mac OS X" || st.Steps[5].Title != "Install Mac OS X" {
		t.Fatalf("steps name the wrong system: %+v", st.Steps)
	}
	// 10.4.11 trims the tablet from the Installer on: the viewer begins there.
	if st.Trim != 0.15 {
		t.Fatalf("trim %v before any viewer said, want 0.15", st.Trim)
	}
	// Remote Login is forwarded, but the Chat's agent works in the PowerPC Mac.
	if a, err := m.Agent(); a != nil || err != nil {
		t.Fatalf("the Intel Mac has an agent: %+v %v", a, err)
	}
	if a, err := NewMacOSX(t.TempDir(), t.TempDir()).Agent(); a == nil || err != nil {
		t.Fatalf("the PowerPC Mac lost its agent: %v", err)
	}
	if m.MaxCDSize() != 9<<30 || m.sys.cdDrive != "cd" || macOSX.cdDrive != "ide1-cd0" {
		t.Fatal("the CD drives changed")
	}
}

// The runtime is the Intel Mac's own, in its root: QEMU and the firmware
// unpacked there come first, the system's otherwise.
func TestMacOSXIntelRunsItsOwnRuntime(t *testing.T) {
	root := t.TempDir()
	m := NewMacOSXIntel(root)
	if m.binary(m.sys.emulator) != "qemu-system-x86_64" {
		t.Fatal("found an emulator where none is unpacked")
	}
	writeFixture(t, filepath.Join(root, "runtime/usr/bin/qemu-system-x86_64"), "emulator", 0700)
	writeFixture(t, filepath.Join(root, "runtime/usr/share/OVMF", ovmfCode), "firmware", 0600)
	writeFixture(t, filepath.Join(root, "runtime/usr/share/OVMF", ovmfVars), "nvram", 0600)
	writeFixture(t, filepath.Join(root, "runtime/usr/share/qemu/vgabios-stdvga.bin"), "vga", 0600)
	if got := m.binary(m.sys.emulator); got != filepath.Join(root, "runtime/usr/bin/qemu-system-x86_64") {
		t.Fatalf("emulator is %q", got)
	}
	args := m.launchArgs(false)
	if l := argPairs(args)["-L"]; len(l) != 1 || l[0] != filepath.Join(root, "runtime/usr/share/qemu") {
		t.Fatalf("firmware search path is %q", l)
	}
	if !strings.Contains(strings.Join(args, " "), "readonly=on,file="+filepath.Join(root, "runtime/usr/share/OVMF", ovmfCode)) {
		t.Fatal("the firmware is not the runtime's")
	}
	// Its NVRAM starts as the firmware's blank one and is then the Mac's.
	if err := m.ensureEFIVars(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "efivars.fd"), []byte("startup disk chosen"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureEFIVars(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "efivars.fd")); string(b) != "startup disk chosen" {
		t.Fatalf("the Mac's NVRAM was replaced: %q", b)
	}
}

// Setup fetches OpenCore once, checks it, takes the 32-bit build's files
// out of it and writes exe's config beside them; a config of an older exe
// is replaced, and the archive is not fetched again.
func TestOpenCoreIsFetchedOnceAndConfigured(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range append([]string{"X64/EFI/OC/OpenCore.efi", "IA32/EFI/OC/Drivers/AudioDxe.efi"}, prefixed("IA32/", openCoreFiles)...) {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("bytes of " + name))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(archive)
	}))
	defer srv.Close()
	sum := sha256.Sum256(archive)
	saved := openCore
	defer func() { openCore = saved }()
	openCore.url, openCore.sha256, openCore.size = srv.URL, hex.EncodeToString(sum[:]), int64(len(archive))

	root := t.TempDir()
	m := NewMacOSXIntel(root)
	if err := m.ensureOpenCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "opencore")
	for _, name := range openCoreFiles {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != "bytes of IA32/"+name {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	for _, absent := range []string{"EFI/OC/Drivers/AudioDxe.efi", "X64"} {
		if _, err := os.Stat(filepath.Join(dir, absent)); err == nil {
			t.Errorf("%s was unpacked", absent)
		}
	}
	config := filepath.Join(dir, "EFI/OC/config.plist")
	if b, _ := os.ReadFile(config); !bytes.Equal(b, openCoreConfig) {
		t.Fatal("the config is not exe's")
	}
	if err := os.WriteFile(config, []byte("an older exe's"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureOpenCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(config); !bytes.Equal(b, openCoreConfig) || hits.Load() != 1 {
		t.Fatalf("config not renewed, or OpenCore fetched %d times", hits.Load())
	}
	// A new OpenCore unpacks from a new archive; a wrong download is refused.
	openCore.version, openCore.sha256 = "9.9.9", strings.Repeat("0", 64)
	if err := m.ensureOpenCore(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a download that does not match: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "media/OpenCore-9.9.9-RELEASE.zip")); err == nil {
		t.Fatal("kept a download that does not match")
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatal("a failed update took the working OpenCore away")
	}
}

// The config is OpenCore's for Tiger on this machine: what each part of it
// is there for is said in it; here, that it is what boots.
func TestOpenCoreConfigBootsTiger(t *testing.T) {
	c := string(openCoreConfig)
	for _, want := range []string{
		"<key>ConnectDrivers</key><true/>",
		"<key>KernelArch</key><string>i386</string>",
		"<key>ScanPolicy</key><integer>1114114</integer>",
		"<key>Vault</key><string>Optional</string>",
		"<key>SecureBootModel</key><string>Disabled</string>",
		"<key>Resolution</key><string>1024x768</string>",
		"<key>UgaPassThrough</key><true/>",
		"<key>SystemProductName</key><string>MacPro1,1</string>",
		"<key>ShowPicker</key><false/>",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("config lacks %s", want)
		}
	}
	for _, f := range openCoreFiles {
		if strings.HasPrefix(f, "EFI/OC/Drivers/") && !strings.Contains(c, "<string>"+filepath.Base(f)+"</string>") {
			t.Errorf("config does not load %s", f)
		}
	}
}

// The config carries the one fix Tiger's kernel needs on OVMF: ml_static_mfree
// rewritten to free only pages the pmap manages (config.plist says why). A
// patch OpenCore cannot apply is skipped without a word, and Tiger then
// panics in pmap within minutes, so its shape is checked here: the same
// length found and replaced, for the i386 kernel of 10.4 only, and the new
// loop asks pmap_valid_page (call rel32 0xffffaeea from 0x19e1a5 =
// 0x199094) where the old one did not.
func TestOpenCoreConfigPatchesTigersKernel(t *testing.T) {
	c := string(openCoreConfig)
	start, end := strings.Index(c, "<key>Patch</key>"), strings.Index(c, "<key>Emulate</key>")
	if start < 0 || end < start {
		t.Fatal("config has no Kernel Patch array")
	}
	patches := strings.Split(c[start:end], "<dict>")[1:]
	data := func(p, key string) []byte {
		m := regexp.MustCompile(`<key>` + key + `</key><data>([^<]*)</data>`).FindStringSubmatch(p)
		if m == nil {
			t.Fatalf("patch has no %s", key)
		}
		b, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return b
	}
	// Each patch, by its length and what its replacement adds or takes out.
	want := []struct {
		comment    string
		size       int
		added, cut []byte
	}{
		{"ml_static_mfree", 124, []byte{0xe8, 0xea, 0xae, 0xff, 0xff}, nil},            // call pmap_valid_page
		{"i386_vm_init", 44, []byte{0x3b, 0x45, 0xc4, 0x72}, []byte{0x3b, 0x48, 0x08}}, // cmp fap, jb in; cmp alloc out
		{"machine_idle_cstate", 37, []byte{0xfb, 0xf4}, nil},                           // sti; hlt
		{"ml_init_lock_timeout", 28, []byte{0x00, 0x94, 0x35, 0x77}, nil},              // 2e9
		{"lck_mtx_interlock_spin", 20, []byte{0xeb, 0x1b}, []byte{0x39, 0xd6}},         // jmp over the deadline
		{"pmap_flush_tlbs", 18, []byte{0xeb, 0x22}, []byte{0x39, 0xd6}},                // jmp over the deadline
	}
	if len(patches) != len(want) {
		t.Fatalf("config has %d kernel patches, want %d", len(patches), len(want))
	}
	for i, w := range want {
		p := patches[i]
		if !strings.Contains(p, "<key>Comment</key><string>"+w.comment+":") {
			t.Errorf("patch %d is not %s's", i, w.comment)
			continue
		}
		find, repl := data(p, "Find"), data(p, "Replace")
		if len(find) != w.size || len(repl) != len(find) {
			t.Errorf("%s: Find is %d bytes, Replace %d", w.comment, len(find), len(repl))
		}
		if bytes.Contains(find, w.added) || !bytes.Contains(repl, w.added) {
			t.Errorf("%s: the patch does not add % x", w.comment, w.added)
		}
		if w.cut != nil && (!bytes.Contains(find, w.cut) || bytes.Contains(repl, w.cut)) {
			t.Errorf("%s: the patch does not take out % x", w.comment, w.cut)
		}
		for _, s := range []string{
			"<key>Arch</key><string>i386</string>",
			"<key>Identifier</key><string>kernel</string>",
			"<key>Enabled</key><true/>",
			"<key>Count</key><integer>1</integer>",
			"<key>MinKernel</key><string>8.0.0</string>",
			"<key>MaxKernel</key><string>8.11.99</string>",
		} {
			if !strings.Contains(p, s) {
				t.Errorf("%s: patch lacks %s", w.comment, s)
			}
		}
	}
}

func prefixed(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}
