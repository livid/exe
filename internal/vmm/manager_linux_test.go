//go:build linux

package vmm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTapNameIsStableAndFitsLinuxLimit(t *testing.T) {
	first := tapName("example-vm")
	if first != tapName("example-vm") {
		t.Fatal("tap name is not stable")
	}
	if first == tapName("another-vm") {
		t.Fatal("different VM names produced the same TAP name")
	}
	if len(first) > 15 {
		t.Fatalf("tap name %q exceeds Linux's 15-byte interface limit", first)
	}
}

func TestNetworkCIDR(t *testing.T) {
	got, err := networkCIDR("172.30.4.6", 30)
	if err != nil {
		t.Fatal(err)
	}
	if got != "172.30.4.4/30" {
		t.Fatalf("networkCIDR() = %q, want 172.30.4.4/30", got)
	}
}

func TestAllocateNetworkSkipsExistingVM(t *testing.T) {
	state := t.TempDir()
	if err := os.Mkdir(filepath.Join(state, "vms"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR("172.30.0.0/29")
	if err != nil {
		t.Fatal(err)
	}
	manager := &fcManager{
		opts:      Options{StateDir: state},
		network:   network,
		prefixLen: 29,
	}
	first, err := manager.allocateNetwork()
	if err != nil {
		t.Fatal(err)
	}
	if first.HostIP != "172.30.0.1" || first.GuestIP != "172.30.0.2" {
		t.Fatalf("first allocation = %#v", first)
	}

	dir := filepath.Join(state, "vms", "first")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := vmMeta{Spec: Spec{Name: "first"}, Network: first}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "vm.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := manager.allocateNetwork()
	if err != nil {
		t.Fatal(err)
	}
	if second.HostIP != "172.30.0.5" || second.GuestIP != "172.30.0.6" {
		t.Fatalf("second allocation = %#v", second)
	}

	dir = filepath.Join(state, "vms", "second")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta.Network = second
	data, _ = json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "vm.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.allocateNetwork(); err == nil {
		t.Fatal("expected exhausted network error")
	}
}

func TestSystemdNetworkConfig(t *testing.T) {
	network := &vmNetwork{HostIP: "172.30.0.1", GuestIP: "172.30.0.2", PrefixLen: 30}
	got := systemdNetworkConfig("02:00:00:00:00:01", network)
	for _, want := range []string{
		"MACAddress=02:00:00:00:00:01",
		"172.30.0.2/30",
		"Gateway=172.30.0.1",
		"1.1.1.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("systemd network config does not contain %q:\n%s", want, got)
		}
	}
}

func TestLinuxRootRegionDirectExt4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root.ext4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(4 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0x53, 0xef}, 1024+56); err != nil {
		t.Fatal(err)
	}
	file.Close()
	region, err := linuxRootRegion(path)
	if err != nil {
		t.Fatal(err)
	}
	if region.offset != 0 || region.size != 4<<20 {
		t.Fatalf("region = %#v", region)
	}
}

func TestLinuxRootRegionGPT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.raw")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(4 << 20); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 92)
	copy(header, "EFI PART")
	binary.LittleEndian.PutUint64(header[72:80], 2)
	binary.LittleEndian.PutUint32(header[80:84], 1)
	binary.LittleEndian.PutUint32(header[84:88], 128)
	if _, err := file.WriteAt(header, 512); err != nil {
		t.Fatal(err)
	}
	entry := make([]byte, 128)
	entry[0] = 1
	binary.LittleEndian.PutUint64(entry[32:40], 2048)
	binary.LittleEndian.PutUint64(entry[40:48], 4095)
	if _, err := file.WriteAt(entry, 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0x53, 0xef}, 2048*512+1024+56); err != nil {
		t.Fatal(err)
	}
	file.Close()
	region, err := linuxRootRegion(path)
	if err != nil {
		t.Fatal(err)
	}
	if region.offset != 1<<20 || region.size != 1<<20 {
		t.Fatalf("region = %#v", region)
	}
}

func TestCloneLinuxDiskKeepsContentAndSparseness(t *testing.T) {
	dir := t.TempDir()
	size := int64(4 << 20)
	content := make([]byte, size)
	content[1024+56] = 0x53
	content[1024+56+1] = 0xef
	copy(content[2<<20:], "data in the middle")
	src := filepath.Join(dir, "base.raw")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "disk.raw")
	if err := cloneLinuxDisk(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("cloned disk differs from the source region")
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := st.Sys().(*syscall.Stat_t).Blocks * 512; allocated >= size {
		t.Fatalf("clone is not sparse: %d bytes allocated for %d bytes of mostly zeros", allocated, size)
	}
}

func TestGuestFileWritesDetectDebugfsFailures(t *testing.T) {
	for _, tool := range []string{"mke2fs", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not available", tool)
		}
	}
	disk := filepath.Join(t.TempDir(), "root.ext4")
	file, err := os.Create(disk)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(8 << 20); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if output, err := exec.Command("mke2fs", "-q", "-F", "-t", "ext4", disk).CombinedOutput(); err != nil {
		t.Fatalf("mke2fs: %v: %s", err, output)
	}

	if err := writeExt4File(disk, "/missing/file", "data"); err == nil {
		t.Fatal("write into a missing directory unexpectedly succeeded")
	}
	if err := makeExt4Dir(disk, "/missing/child"); err == nil {
		t.Fatal("mkdir under a missing parent unexpectedly succeeded")
	}
	if err := makeExt4Dir(disk, "/seed"); err != nil {
		t.Fatal(err)
	}
	if err := makeExt4Dir(disk, "/seed"); err != nil {
		t.Fatalf("mkdir of an existing directory is not treated as success: %v", err)
	}
	if err := writeExt4File(disk, "/seed/user-data", "data"); err != nil {
		t.Fatal(err)
	}
	if err := writeExt4File(disk, "/seed/user-data", "changed"); err == nil {
		t.Fatal("overwrite of an existing guest file unexpectedly succeeded")
	}
}

func TestWriteFirecrackerConfig(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "vms", "test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := &fcManager{
		opts:     Options{StateDir: state},
		ownerUID: uint32(os.Geteuid()),
		ownerGID: uint32(os.Getegid()),
	}
	meta := &vmMeta{
		Spec:    Spec{Name: "test", CPUs: 2, MemoryMB: 512, DiskGB: 4},
		MAC:     "02:00:00:00:00:01",
		Network: &vmNetwork{Tap: "exe-test"},
	}
	path, err := manager.writeFirecrackerConfig("test", meta, "/kernel")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config fcConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.MachineConfig.VCPUCount != 2 || config.MachineConfig.MemSizeMiB != 512 {
		t.Fatalf("machine config = %#v", config.MachineConfig)
	}
	if len(config.Drives) != 1 || !config.Drives[0].IsRootDevice {
		t.Fatalf("drives = %#v", config.Drives)
	}
	for _, want := range []string{"root=/dev/vda", "ds=nocloud;s=file:///var/lib/exe-seed/", "ip="} {
		if !strings.Contains(config.BootSource.BootArgs, want) {
			t.Fatalf("boot args %q do not contain %q", config.BootSource.BootArgs, want)
		}
	}
	if config.NetworkInterfaces[0].HostDevName != "exe-test" {
		t.Fatalf("network interfaces = %#v", config.NetworkInterfaces)
	}
}

func TestStateLockIsExclusive(t *testing.T) {
	state := t.TempDir()
	first, err := acquireStateLock(state, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if first != nil {
			releaseStateLock(first)
		}
	}()

	if second, err := acquireStateLock(state, 10*time.Millisecond); err == nil {
		releaseStateLock(second)
		t.Fatal("second state lock unexpectedly succeeded")
	}
	releaseStateLock(first)
	first = nil
	third, err := acquireStateLock(state, time.Second)
	if err != nil {
		t.Fatalf("state lock was not released: %v", err)
	}
	releaseStateLock(third)
}

func TestReconcileNetworksCleansPersistedVMs(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	vms := filepath.Join(state, "vms")
	if err := os.MkdirAll(filepath.Join(vms, "stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vms, "never-started"), 0o755); err != nil {
		t.Fatal(err)
	}

	stale := vmMeta{
		Spec: Spec{Name: "stale"},
		Network: &vmNetwork{
			HostIP:            "172.30.0.1",
			GuestIP:           "172.30.0.2",
			PrefixLen:         30,
			OutboundInterface: "eth0",
		},
	}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(vms, "stale", "vm.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(vmMeta{Spec: Spec{Name: "never-started"}})
	if err := os.WriteFile(filepath.Join(vms, "never-started", "vm.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(root, "helper.log")
	helper := filepath.Join(root, "helper")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$EXE_RECONCILE_LOG\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXE_RECONCILE_LOG", logPath)
	manager := &fcManager{
		opts:     Options{StateDir: state},
		helper:   helper,
		ownerUID: uint32(os.Getuid()),
	}
	if err := manager.reconcileNetworks(); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(logged)
	if !strings.Contains(text, "cleanup --name stale --host-cidr 172.30.0.1/30 --outbound eth0") {
		t.Fatalf("unexpected helper invocation: %s", text)
	}
	if strings.Contains(text, "never-started") {
		t.Fatalf("reconciled VM without persisted network setup: %s", text)
	}
}

func TestNewWithoutFirecrackerIsNoBackend(t *testing.T) {
	_, err := New(Options{
		StateDir:    t.TempDir(),
		Firecracker: FirecrackerOptions{Binary: "exe-test-no-such-firecracker", KernelURL: "http://example.invalid/vmlinux"},
	})
	if err == nil {
		t.Fatal("New() succeeded without a Firecracker binary")
	}
	if !errors.Is(err, ErrNoBackend) {
		t.Fatalf("New() error = %v, want ErrNoBackend", err)
	}
	if !strings.Contains(err.Error(), "exe-test-no-such-firecracker") {
		t.Fatalf("New() error %q does not name the missing binary", err)
	}
}
