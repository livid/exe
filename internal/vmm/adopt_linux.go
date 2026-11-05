//go:build linux

package vmm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// A VM can outlive the daemon that started it. Under systemd the unit's
// KillMode=process signals only the daemon, so a Firecracker started without
// a parent-death signal runs on through a restart, as the tmux servers of the
// agent windows do, and the next daemon adopts it instead of booting it: the
// guest never notices. run.json in the VM's folder is what the next daemon
// goes by, and vm.lock, held by the Firecracker process itself, is what keeps
// any daemon from booting a second guest on a disk that is in use.

const (
	runRecordFile = "run.json"
	runLockFile   = "vm.lock"
)

// runRecord names one Firecracker process past any reuse of its pid: a pid
// is only unique together with the process's start time, and a start time
// only within one boot of the host.
type runRecord struct {
	PID       int    `json:"pid"`
	StartTime uint64 `json:"start_time"`
	BootID    string `json:"boot_id"`
}

// keepVMsDefault reports whether this daemon's VMs should outlive it: only
// when systemd started this very process, since then a next daemon is
// certain to come and adopt them. SYSTEMD_EXEC_PID rather than
// INVOCATION_ID: a daemon started by hand from a shell that descends from
// the unit inherits both, but only the unit's own process has that pid.
// EXE_KEEP_VMS=1 or 0 overrides it, for tests and as the way to turn it off.
func keepVMsDefault() bool {
	switch os.Getenv("EXE_KEEP_VMS") {
	case "1":
		return true
	case "0":
		return false
	}
	return os.Getenv("SYSTEMD_EXEC_PID") == strconv.Itoa(os.Getpid())
}

// pidfdWorks reports whether this kernel hands out pidfds (5.3 and later).
// Without one an adopted process can be neither waited on nor signalled
// safely, so nothing is left running for a next daemon.
func pidfdWorks() bool {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return false
	}
	unix.Close(fd)
	return true
}

func bootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// procStartTime is field 22 of /proc/<pid>/stat: when the process started,
// in clock ticks since boot. The command name (field 2) may hold spaces and
// parentheses, so the fields are counted from its last closing parenthesis.
func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return 0, fmt.Errorf("unexpected /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 20 {
		return 0, fmt.Errorf("unexpected /proc/%d/stat", pid)
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

// procHasArg reports whether the process was started with arg among its
// arguments.
func procHasArg(pid int, arg string) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	for _, a := range bytes.Split(data, []byte{0}) {
		if string(a) == arg {
			return true
		}
	}
	return false
}

func (m *fcManager) runRecordPath(name string) string {
	return filepath.Join(m.vmDir(name), runRecordFile)
}

// writeRunRecord notes the Firecracker process just started for name.
func (m *fcManager) writeRunRecord(name string, pid int) error {
	start, err := procStartTime(pid)
	if err != nil {
		return err
	}
	data, err := json.Marshal(runRecord{PID: pid, StartTime: start, BootID: bootID()})
	if err != nil {
		return err
	}
	path := m.runRecordPath(name)
	if err := os.WriteFile(path, append(data, '\n'), 0o640); err != nil {
		return err
	}
	return os.Chown(path, int(m.ownerUID), int(m.ownerGID))
}

func (m *fcManager) removeRunRecord(name string) {
	if err := os.Remove(m.runRecordPath(name)); err != nil && !os.IsNotExist(err) {
		log.Printf("vm %s: remove %s: %v", name, runRecordFile, err)
	}
}

// lockRun takes the VM's run lock for a Firecracker about to start. The
// process inherits the file and so holds the lock for as long as it lives,
// whatever becomes of the daemon and of run.json: a locked file means a
// guest is running on this disk.
func (m *fcManager) lockRun(name string) (*os.File, error) {
	path := filepath.Join(m.vmDir(name), runLockFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("VM %s is already running in a Firecracker this daemon does not manage (see %s); stop that process first", name, m.runRecordPath(name))
		}
		return nil, err
	}
	_ = os.Chown(path, int(m.ownerUID), int(m.ownerGID))
	return file, nil
}

// openRecorded returns a pidfd for the process a run record names, or -1
// when that process is gone or is not the one recorded. The handle is
// opened first and the identity read after it, then the handle is checked
// to be alive still: what was read is then this process's, and every later
// wait and signal goes through the handle, so a pid reused afterwards can
// never be mistaken for the VM.
func openRecorded(rec runRecord, arg string) int {
	if rec.PID <= 1 || rec.BootID == "" || rec.BootID != bootID() {
		return -1
	}
	fd, err := unix.PidfdOpen(rec.PID, 0)
	if err != nil {
		return -1
	}
	start, err := procStartTime(rec.PID)
	if err != nil || start != rec.StartTime || !procHasArg(rec.PID, arg) || pidfdExited(fd) {
		unix.Close(fd)
		return -1
	}
	return fd
}

// pidfdExited reports whether the process behind a pidfd has ended: the
// handle polls readable from then on.
func pidfdExited(fd int) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, 0)
		if err == unix.EINTR {
			continue
		}
		return err != nil || n > 0
	}
}

// adoptRunning takes over the VMs a previous daemon left running, before
// reconcileNetworks sweeps the networking of the ones that are not: each
// becomes a running VM of this daemon with its TAP and firewall rules as
// they are. A record whose process is gone, or is some other process by
// now, is dropped and nothing is signalled.
func (m *fcManager) adoptRunning() {
	entries, err := os.ReadDir(filepath.Join(m.opts.vmRoot(), "vms"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, err := os.ReadFile(m.runRecordPath(name))
		if err != nil {
			continue
		}
		var rec runRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			m.removeRunRecord(name)
			continue
		}
		mt, err := m.loadMeta(name)
		if err != nil {
			continue
		}
		fd := openRecorded(rec, filepath.Join(m.vmDir(name), "firecracker.json"))
		if fd < 0 {
			m.removeRunRecord(name)
			continue
		}
		rt := &fcRuntime{pid: rec.PID, pidfd: fd, done: make(chan struct{}), state: "running"}
		m.mu.Lock()
		m.running[name] = rt
		m.mu.Unlock()
		go m.waitAdopted(name, mt, rt)
		log.Printf("vm %s: still running as Firecracker pid %d, adopted at %s", name, rec.PID, mt.Network.GuestIP)
	}
}

// waitAdopted is waitProcess for a Firecracker this daemon did not start:
// not its child, so its end is read from the pidfd.
func (m *fcManager) waitAdopted(name string, mt *vmMeta, rt *fcRuntime) {
	fds := []unix.PollFd{{Fd: int32(rt.pidfd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n > 0 {
			break
		}
	}
	m.processEnded(name, mt, rt, nil)
}

// signal reaches the VM's Firecracker: through the pidfd for an adopted one,
// so the signal can only ever land on that process.
func (rt *fcRuntime) signal(sig syscall.Signal) {
	if rt.cmd != nil {
		if rt.cmd.Process != nil {
			_ = rt.cmd.Process.Signal(sig)
		}
		return
	}
	if rt.pidfd >= 0 {
		_ = unix.PidfdSendSignal(rt.pidfd, sig, nil, 0)
	}
}

// KeepsVMs reports whether the VMs running now may be left running when
// this daemon stops, for the next one to adopt (vmm.Keeper).
func (m *fcManager) KeepsVMs() bool {
	return m.keep
}

// signalRuntime signals a VM's Firecracker under the manager's lock, which
// is what closes an adopted VM's pidfd once it has ended.
func (m *fcManager) signalRuntime(rt *fcRuntime, sig syscall.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt.signal(sig)
}
