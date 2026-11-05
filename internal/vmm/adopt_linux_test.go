//go:build linux

package vmm

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// adoptManager is a manager over a scratch state dir whose network helper
// only logs what it was asked.
func adoptManager(t *testing.T) (*fcManager, string) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(state, "vms"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "helper.log")
	helper := filepath.Join(root, "helper")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$EXE_RECONCILE_LOG\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXE_RECONCILE_LOG", logPath)
	return &fcManager{
		opts:     Options{StateDir: state},
		helper:   helper,
		ownerUID: uint32(os.Getuid()),
		ownerGID: uint32(os.Getgid()),
		running:  make(map[string]*fcRuntime),
		locks:    make(map[string]*sync.Mutex),
	}, logPath
}

// adoptVM makes a VM folder with networking on record, as a VM that has
// been started has.
func adoptVM(t *testing.T, m *fcManager, name, hostIP, guestIP string) {
	t.Helper()
	if err := os.MkdirAll(m.vmDir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := vmMeta{Spec: Spec{Name: name}, Network: &vmNetwork{HostIP: hostIP, GuestIP: guestIP, PrefixLen: 30, OutboundInterface: "eth0"}}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(m.vmDir(name), "vm.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.vmDir(name), "firecracker.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// standIn runs a process that looks to the manager like name's Firecracker:
// it names the VM's config file among its arguments and runs until signalled.
func standIn(t *testing.T, m *fcManager, name string, extra ...*os.File) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("tail", "-f", "/dev/null", filepath.Join(m.vmDir(name), "firecracker.json"))
	cmd.ExtraFiles = extra
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func writeRecord(t *testing.T, m *fcManager, name string, rec runRecord) {
	t.Helper()
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(m.runRecordPath(name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A VM whose Firecracker a previous daemon left running is adopted with its
// networking as it is; a record that names a dead process, a process of a
// previous boot or some unrelated live process adopts nothing, signals
// nothing, and its VM's stale networking is swept as before.
func TestAdoptRunningTakesSurvivorsOnly(t *testing.T) {
	if !pidfdWorks() {
		t.Skip("no pidfd on this kernel")
	}
	m, logPath := adoptManager(t)
	self := os.Getpid()
	selfStart, err := procStartTime(self)
	if err != nil {
		t.Fatal(err)
	}

	adoptVM(t, m, "alive", "172.30.0.1", "172.30.0.2")
	alive := standIn(t, m, "alive")
	if err := m.writeRunRecord("alive", alive.Process.Pid); err != nil {
		t.Fatal(err)
	}

	// this test's own process, alive and unrelated: the right pid with the
	// wrong start time, then with the right start time but not a Firecracker
	// of that VM, then as a record of another boot
	adoptVM(t, m, "reused", "172.30.0.5", "172.30.0.6")
	writeRecord(t, m, "reused", runRecord{PID: self, StartTime: selfStart + 1, BootID: bootID()})
	adoptVM(t, m, "other", "172.30.0.9", "172.30.0.10")
	writeRecord(t, m, "other", runRecord{PID: self, StartTime: selfStart, BootID: bootID()})
	adoptVM(t, m, "rebooted", "172.30.0.13", "172.30.0.14")
	rebooted := standIn(t, m, "rebooted")
	rebootedStart, _ := procStartTime(rebooted.Process.Pid)
	writeRecord(t, m, "rebooted", runRecord{PID: rebooted.Process.Pid, StartTime: rebootedStart, BootID: "00000000-0000-0000-0000-000000000000"})

	// a Firecracker that ended while no daemon ran
	adoptVM(t, m, "gone", "172.30.0.17", "172.30.0.18")
	gone := standIn(t, m, "gone")
	if err := m.writeRunRecord("gone", gone.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_ = gone.Process.Kill()
	_ = gone.Wait()

	m.adoptRunning()
	if err := m.reconcileNetworks(); err != nil {
		t.Fatal(err)
	}

	if len(m.running) != 1 || m.running["alive"] == nil {
		t.Fatalf("adopted %v, want alive only", keys(m.running))
	}
	rt := m.running["alive"]
	if rt.state != "running" || rt.pid != alive.Process.Pid || rt.pidfd < 0 || rt.cmd != nil {
		t.Fatalf("adopted runtime = %+v", rt)
	}
	swept, _ := os.ReadFile(logPath)
	for _, name := range []string{"reused", "other", "rebooted", "gone"} {
		if !strings.Contains(string(swept), "cleanup --name "+name+" ") {
			t.Errorf("%s: stale networking not swept: %s", name, swept)
		}
		if _, err := os.Stat(m.runRecordPath(name)); !os.IsNotExist(err) {
			t.Errorf("%s: its record stayed", name)
		}
	}
	if strings.Contains(string(swept), "--name alive ") {
		t.Fatalf("the survivor's networking was swept: %s", swept)
	}
	if err := rebooted.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("a process of an unadopted record was signalled: %v", err)
	}
	if info, err := m.Get(t.Context(), "alive"); err != nil || info.State != "running" || info.IP != "172.30.0.2" {
		t.Fatalf("Get(alive) = %+v, %v", info, err)
	}

	// stopping it goes through the pidfd, and what a child's exit clears up
	// is cleared up for it too
	m.signalRuntime(rt, syscall.SIGTERM)
	if !waitRuntime(t.Context(), rt.done, 10*time.Second) {
		t.Fatal("the adopted process did not end on SIGTERM")
	}
	if len(m.running) != 0 || rt.pidfd != -1 {
		t.Fatalf("after its end: running %v, pidfd %d", keys(m.running), rt.pidfd)
	}
	if _, err := os.Stat(m.runRecordPath("alive")); !os.IsNotExist(err) {
		t.Fatal("the record of an ended VM stayed")
	}
	swept, _ = os.ReadFile(logPath)
	if !strings.Contains(string(swept), "cleanup --name alive --host-cidr 172.30.0.1/30 --outbound eth0") {
		t.Fatalf("the ended VM's networking was not cleaned up: %s", swept)
	}
	m.signalRuntime(rt, syscall.SIGKILL) // no handle left: reaches nothing
}

// The run lock is the Firecracker's for as long as it lives: no second
// guest can be started on the VM's disk meanwhile.
func TestRunLockIsHeldByTheProcess(t *testing.T) {
	m, _ := adoptManager(t)
	adoptVM(t, m, "vm", "172.30.0.1", "172.30.0.2")
	lock, err := m.lockRun("vm")
	if err != nil {
		t.Fatal(err)
	}
	proc := standIn(t, m, "vm", lock)
	lock.Close()
	if _, err := m.lockRun("vm"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("lockRun beside a live process = %v, want already running", err)
	}
	_ = proc.Process.Kill()
	_ = proc.Wait()
	again, err := m.lockRun("vm")
	if err != nil {
		t.Fatalf("lockRun after the process ended: %v", err)
	}
	again.Close()
}

func TestKeepVMsDefault(t *testing.T) {
	t.Setenv("EXE_KEEP_VMS", "")
	t.Setenv("SYSTEMD_EXEC_PID", "1")
	if keepVMsDefault() {
		t.Fatal("kept under a systemd that started another process")
	}
	t.Setenv("SYSTEMD_EXEC_PID", strconv.Itoa(os.Getpid()))
	if !keepVMsDefault() {
		t.Fatal("not kept as the process systemd started")
	}
	t.Setenv("EXE_KEEP_VMS", "0")
	if keepVMsDefault() {
		t.Fatal("EXE_KEEP_VMS=0 did not turn it off")
	}
}

func keys(m map[string]*fcRuntime) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
