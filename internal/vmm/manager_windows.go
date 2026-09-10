//go:build windows

package vmm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"exe/internal/cloudinit"
	"exe/internal/sshexec"
)

const (
	qemuBootTimeout = 3 * time.Minute
	qemuStopTimeout = 30 * time.Second
	qemuLockTimeout = 15 * time.Second
)

// qemuManager runs VMs with QEMU on the Windows Hypervisor Platform (the
// hypervisor beneath WSL2, available even on Windows Home). Each VM is an
// unprivileged qemu-system-x86_64 child process booting the same EFI cloud
// image as the macOS backend; the guest network is a userspace TCP/IP stack
// inside this process (see network_windows.go), so no drivers, TAPs or admin
// rights are needed.
type qemuManager struct {
	opts         Options
	binary       string
	firmwareCode string // edk2-x86_64-code.fd (read-only pflash)
	firmwareVars string // edk2-i386-vars.fd (template, copied per VM)
	network      *guestNetwork
	job          windows.Handle // kill-on-close: daemon death takes QEMU with it

	mu      sync.Mutex
	running map[string]*qemuRuntime
	locks   map[string]*sync.Mutex

	createMu  sync.Mutex
	dlMu      sync.Mutex
	stateLock *os.File
}

type qemuRuntime struct {
	cmd     *exec.Cmd
	done    chan struct{}
	state   string
	exitErr error
	logFile *os.File
}

// New creates the Windows QEMU backend.
func New(opts Options) (Manager, error) {
	if runtime.GOARCH != "amd64" {
		return nil, noBackend(fmt.Errorf("the Windows backend needs QEMU's WHPX accelerator, which is x86-64 only (this is windows/%s)", runtime.GOARCH))
	}
	if err := checkWHPX(); err != nil {
		return nil, noBackend(err)
	}
	binary, err := findQEMU(opts.QEMU.Binary)
	if err != nil {
		return nil, noBackend(err)
	}
	code, vars, err := findFirmware(binary, opts.QEMU.FirmwareDir)
	if err != nil {
		return nil, noBackend(err)
	}
	if opts.QEMU.NetworkCIDR == "" {
		opts.QEMU.NetworkCIDR = "192.168.127.0/24"
	}
	network, err := newGuestNetwork(opts.QEMU.NetworkCIDR)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{opts.StateDir, filepath.Join(opts.StateDir, "vms"), filepath.Join(opts.StateDir, "images")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			network.Close()
			return nil, err
		}
	}
	stateLock, err := acquireStateLock(opts.StateDir, qemuLockTimeout)
	if err != nil {
		network.Close()
		return nil, err
	}
	job, err := newKillOnCloseJob()
	if err != nil {
		releaseStateLock(stateLock)
		network.Close()
		return nil, fmt.Errorf("create job object: %w", err)
	}
	return &qemuManager{
		opts:         opts,
		binary:       binary,
		firmwareCode: code,
		firmwareVars: vars,
		network:      network,
		job:          job,
		running:      make(map[string]*qemuRuntime),
		locks:        make(map[string]*sync.Mutex),
		stateLock:    stateLock,
	}, nil
}

// checkWHPX verifies the Windows Hypervisor Platform is present and the
// hypervisor is actually running, so QEMU's -accel whpx can work at all.
func checkWHPX() error {
	errNotAvailable := errors.New(`the Windows Hypervisor Platform is not available — enable "Windows Hypervisor Platform" and "Virtual Machine Platform" under Windows optional features, reboot, and retry`)
	proc := windows.NewLazySystemDLL("WinHvPlatform.dll").NewProc("WHvGetCapability")
	if err := proc.Find(); err != nil {
		return errNotAvailable
	}
	var present, written uint32
	// Capability code 0 = WHvCapabilityCodeHypervisorPresent (a 4-byte BOOL).
	hr, _, _ := proc.Call(0, uintptr(unsafe.Pointer(&present)), 4, uintptr(unsafe.Pointer(&written)))
	if int32(hr) < 0 || present == 0 {
		return errNotAvailable
	}
	return nil
}

func findQEMU(configured string) (string, error) {
	name := strings.TrimSpace(configured)
	if name == "" {
		name = "qemu-system-x86_64"
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if configured == "" {
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			p := filepath.Join(pf, "qemu", "qemu-system-x86_64.exe")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("QEMU %q not found — install it (winget install SoftwareFreedomConservancy.QEMU) or set qemu.binary", name)
}

// findFirmware locates QEMU's EFI code flash and the variable-store template
// (the vars file is arch-independent between i386 and x86_64, so QEMU ships
// only the i386-named one).
func findFirmware(binary, configured string) (code, vars string, err error) {
	dirs := []string{filepath.Join(filepath.Dir(binary), "share"), filepath.Dir(binary)}
	if configured != "" {
		dirs = []string{configured}
	}
	for _, d := range dirs {
		c := filepath.Join(d, "edk2-x86_64-code.fd")
		v := filepath.Join(d, "edk2-i386-vars.fd")
		if _, cerr := os.Stat(c); cerr == nil {
			if _, verr := os.Stat(v); verr == nil {
				return c, v, nil
			}
		}
	}
	return "", "", fmt.Errorf("EFI firmware (edk2-x86_64-code.fd + edk2-i386-vars.fd) not found near %s; set qemu.firmware_dir", binary)
}

func acquireStateLock(stateDir string, timeout time.Duration) (*os.File, error) {
	path := filepath.Join(stateDir, "qemu.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open QEMU state lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		ol := new(windows.Overlapped)
		err = windows.LockFileEx(windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("another exe daemon is using %s", stateDir)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := file.Truncate(0); err == nil {
		if _, err := file.Seek(0, io.SeekStart); err == nil {
			fmt.Fprintf(file, "%d\n", os.Getpid())
		}
	}
	return file, nil
}

func releaseStateLock(file *os.File) {
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, ol)
	_ = file.Close()
}

// newKillOnCloseJob creates a job object that terminates every assigned
// process when its last handle closes — i.e. when this daemon exits for any
// reason, including a crash. The Windows stand-in for Pdeathsig.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignToJob(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

func (m *qemuManager) vmDir(name string) string {
	return filepath.Join(m.opts.StateDir, "vms", name)
}

func (m *qemuManager) vmLock(name string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock := m.locks[name]
	if lock == nil {
		lock = &sync.Mutex{}
		m.locks[name] = lock
	}
	return lock
}

func (m *qemuManager) loadMeta(name string) (*vmMeta, error) {
	data, err := os.ReadFile(filepath.Join(m.vmDir(name), "vm.json"))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var mt vmMeta
	if err := json.Unmarshal(data, &mt); err != nil {
		return nil, fmt.Errorf("read metadata for %s: %w", name, err)
	}
	if mt.Network == nil {
		return nil, fmt.Errorf("VM %s was not created with the Windows QEMU backend", name)
	}
	return &mt, nil
}

func (m *qemuManager) saveMeta(name string, mt *vmMeta) error {
	data, err := json.MarshalIndent(mt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.vmDir(name), "vm.json"), append(data, '\n'), 0o644)
}

func (m *qemuManager) Create(ctx context.Context, spec Spec) (*Info, error) {
	if err := ValidateName(spec.Name); err != nil {
		return nil, err
	}
	if spec.CPUs < 1 || spec.CPUs > 32 {
		return nil, fmt.Errorf("cpus must be between 1 and 32")
	}
	if spec.MemoryMB < 128 {
		return nil, fmt.Errorf("memory must be at least 128 MB")
	}
	if spec.DiskGB < 1 {
		return nil, fmt.Errorf("disk must be at least 1 GB")
	}

	lock := m.vmLock(spec.Name)
	lock.Lock()
	m.createMu.Lock()
	keep := false
	dir := m.vmDir(spec.Name)
	unlock := func() {
		if !keep {
			os.RemoveAll(dir)
		}
		m.createMu.Unlock()
		lock.Unlock()
	}
	if _, err := os.Stat(dir); err == nil {
		keep = true
		unlock()
		return nil, fmt.Errorf("vm %q already exists", spec.Name)
	} else if !os.IsNotExist(err) {
		keep = true
		unlock()
		return nil, err
	}

	base, err := m.EnsureImage(ctx)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := checkBootableImage(base); err != nil {
		unlock()
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		unlock()
		return nil, err
	}
	guestIP, err := m.allocateIP()
	if err != nil {
		unlock()
		return nil, err
	}
	disk := filepath.Join(dir, "disk.raw")
	if err := cloneDisk(ctx, base, disk); err != nil {
		unlock()
		return nil, err
	}
	if st, err := os.Stat(disk); err == nil {
		if target := int64(spec.DiskGB) << 30; target > st.Size() {
			if err := os.Truncate(disk, target); err != nil {
				unlock()
				return nil, err
			}
		}
	}
	if err := cloudinit.BuildSeed(filepath.Join(dir, "seed.iso"), spec.Name, m.opts.SSHUser, m.opts.AuthorizedKey); err != nil {
		unlock()
		return nil, err
	}
	mt := &vmMeta{
		Spec:      spec,
		MAC:       m.network.MACForIP(guestIP),
		CreatedAt: time.Now().UTC(),
		Network: &vmNetwork{
			HostIP:    m.network.GatewayIP(),
			GuestIP:   guestIP,
			PrefixLen: m.network.PrefixLen(),
		},
	}
	if err := m.saveMeta(spec.Name, mt); err != nil {
		unlock()
		return nil, err
	}
	keep = true
	unlock()

	return m.Start(ctx, spec.Name)
}

// allocateIP picks the first guest address not claimed by an existing VM.
func (m *qemuManager) allocateIP() (string, error) {
	used := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(m.opts.StateDir, "vms"))
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if mt, err := m.loadMeta(entry.Name()); err == nil && mt.Network != nil {
			used[mt.Network.GuestIP] = true
		}
	}
	return m.network.Allocate(used)
}

// checkBootableImage rejects base images the EFI boot path cannot start —
// the Windows backend boots full disk images (GPT with an EFI partition),
// like macOS, not the bare ext4 filesystems the Linux backend accepts.
func checkBootableImage(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 8)
	if _, err := file.ReadAt(header, 512); err != nil || string(header) != "EFI PART" {
		return errors.New("the Windows backend needs a full GPT disk image with an EFI partition (like the default Debian genericcloud raw image)")
	}
	return nil
}

// sparseChunk is the granularity at which zero runs in the base image become
// holes in the cloned disk.
const sparseChunk = 64 << 10

// fsctlSetSparse marks a file sparse so seeked-over ranges become holes
// (x/sys/windows has no constant for it).
const fsctlSetSparse = 0x000900C4

// cloneDisk copies the base image into dst as an NTFS sparse file, seeking
// over zero runs so a mostly-empty cloud image stays small on disk.
func cloneDisk(ctx context.Context, base, dst string) error {
	in, err := os.Open(base)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	var ret uint32
	if err := windows.DeviceIoControl(windows.Handle(out.Fd()), fsctlSetSparse, nil, 0, nil, 0, &ret, nil); err != nil {
		log.Printf("clone %s: FSCTL_SET_SPARSE failed (%v); the disk will not be sparse", dst, err)
	}
	buf := make([]byte, 8<<20)
	zero := make([]byte, sparseChunk)
	var copyErr error
	for {
		select {
		case <-ctx.Done():
			copyErr = ctx.Err()
		default:
		}
		if copyErr != nil {
			break
		}
		n, err := in.Read(buf)
		if n > 0 {
			if werr := writeSparse(out, buf[:n], zero); werr != nil {
				copyErr = werr
				break
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			copyErr = err
			break
		}
	}
	// Zero runs were seeked over, not written; extend to the full size so a
	// trailing run stays a hole.
	if copyErr == nil {
		copyErr = out.Truncate(st.Size())
	}
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(dst)
		return copyErr
	}
	return closeErr
}

// writeSparse writes data to out, seeking over sparseChunk-sized runs of
// zeros so the clone stays sparse.
func writeSparse(out *os.File, data, zero []byte) error {
	for len(data) > 0 {
		n := len(zero)
		if n > len(data) {
			n = len(data)
		}
		chunk := data[:n]
		data = data[n:]
		if bytes.Equal(chunk, zero[:n]) {
			if _, err := out.Seek(int64(n), io.SeekCurrent); err != nil {
				return err
			}
			continue
		}
		if _, err := out.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (m *qemuManager) Start(ctx context.Context, name string) (*Info, error) {
	lock := m.vmLock(name)
	lock.Lock()
	defer lock.Unlock()

	mt, err := m.loadMeta(name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if rt := m.running[name]; rt != nil {
		m.mu.Unlock()
		return m.info(name, mt)
	}
	m.mu.Unlock()

	dir := m.vmDir(name)
	// Per-VM EFI variable store, seeded from QEMU's template on first boot.
	varsPath := filepath.Join(dir, "efivars.fd")
	if _, err := os.Stat(varsPath); os.IsNotExist(err) {
		if err := copyFile(m.firmwareVars, varsPath); err != nil {
			return nil, fmt.Errorf("EFI variable store: %w", err)
		}
	}
	logPath := filepath.Join(dir, "qemu.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}

	// Relative paths + cmd.Dir keep commas and drive-letter colons out of
	// QEMU option values; only the firmware path stays absolute (it has no
	// commas). kernel-irqchip=off trades a little interrupt performance for
	// reliable delivery under WHPX.
	args := []string{
		"-name", name,
		"-machine", "q35",
		"-accel", "whpx,kernel-irqchip=off",
		// Not -cpu max: on some QEMU dev snapshots it advertises conflicting
		// feature bits (APX vs MPX) that crash OVMF in early PEI under WHPX.
		// qemu64 boots on every build; WHPX executes guest code on the host
		// CPU either way.
		"-cpu", "qemu64",
		"-smp", strconv.Itoa(mt.Spec.CPUs),
		"-m", strconv.Itoa(mt.Spec.MemoryMB),
		"-display", "none",
		"-monitor", "none",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + m.firmwareCode,
		"-drive", "if=pflash,format=raw,file=efivars.fd",
		"-drive", "file=disk.raw,format=raw,if=virtio,discard=unmap",
		"-drive", "file=seed.iso,format=raw,media=cdrom",
		"-netdev", fmt.Sprintf("stream,id=net0,addr.type=inet,addr.host=127.0.0.1,addr.port=%d", m.network.Port()),
		"-device", "virtio-net-pci,netdev=net0,mac=" + mt.MAC,
		"-device", "virtio-rng-pci",
		"-chardev", "file,id=ser0,path=console.log,append=on",
		"-serial", "chardev:ser0",
	}
	cmd := exec.Command(m.binary, args...)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// A new process group keeps Ctrl+C in the daemon's console away from
	// QEMU; no window when the daemon itself runs detached.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start QEMU: %w", err)
	}
	if err := assignToJob(m.job, cmd.Process.Pid); err != nil {
		log.Printf("vm %s: job assignment failed (%v); QEMU may outlive a crashed daemon", name, err)
	}
	rt := &qemuRuntime{cmd: cmd, done: make(chan struct{}), state: "starting", logFile: logFile}
	m.mu.Lock()
	m.running[name] = rt
	m.mu.Unlock()
	go m.waitProcess(name, mt, rt)

	select {
	case <-rt.done:
		return nil, fmt.Errorf("QEMU exited during startup: %w (see %s)", runtimeError(rt), logPath)
	case <-time.After(500 * time.Millisecond):
	}

	m.mu.Lock()
	if m.running[name] == rt {
		rt.state = "running"
	}
	m.mu.Unlock()
	log.Printf("vm %s: QEMU pid %d, waiting for SSH at %s", name, cmd.Process.Pid, mt.Network.GuestIP)
	if err := m.waitSSH(ctx, mt.Network.GuestIP, qemuBootTimeout, rt); err != nil {
		return nil, fmt.Errorf("VM %s did not become ready: %w (see %s)", name, err, filepath.Join(dir, "console.log"))
	}
	log.Printf("vm %s: ready at %s", name, mt.Network.GuestIP)
	return m.info(name, mt)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(dst)
		return copyErr
	}
	return closeErr
}

func (m *qemuManager) waitProcess(name string, mt *vmMeta, rt *qemuRuntime) {
	err := rt.cmd.Wait()
	m.mu.Lock()
	rt.exitErr = err
	stopping := rt.state == "stopping"
	m.mu.Unlock()
	rt.logFile.Close()
	m.network.CloseForwards(mt.Network.GuestIP)
	m.mu.Lock()
	if m.running[name] == rt {
		delete(m.running, name)
	}
	m.mu.Unlock()
	close(rt.done)
	if err != nil && !stopping {
		log.Printf("vm %s: QEMU exited: %v", name, err)
	}
}

func runtimeError(rt *qemuRuntime) error {
	if rt.exitErr != nil {
		return rt.exitErr
	}
	return errors.New("process exited")
}

func runtimeState(rt *qemuRuntime) string {
	if rt.state == "" {
		return "starting"
	}
	return rt.state
}

func (m *qemuManager) waitSSH(ctx context.Context, host string, timeout time.Duration, rt *qemuRuntime) error {
	deadline := time.Now().Add(timeout)
	target := sshexec.Target{Host: host, User: m.opts.SSHUser, KeyPath: m.opts.PrivateKeyPath, Dialer: m.DialGuest}
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, code, err := target.Run(attemptCtx, "true", 1024)
		cancel()
		if err == nil && code == 0 {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out after %s: %w", timeout, lastErr)
			}
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-rt.done:
			return fmt.Errorf("QEMU exited: %w", runtimeError(rt))
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *qemuManager) Stop(ctx context.Context, name string) error {
	lock := m.vmLock(name)
	lock.Lock()
	defer lock.Unlock()
	if _, err := m.loadMeta(name); err != nil {
		return err
	}
	m.mu.Lock()
	rt := m.running[name]
	if rt != nil {
		rt.state = "stopping"
	}
	m.mu.Unlock()
	if rt == nil {
		return ErrNotRunning
	}
	return m.stopRuntime(ctx, name, rt)
}

// stopRuntime powers the VM off over SSH; if the guest does not exit in
// time, TerminateProcess is the fallback — same guarantee as the Linux
// backend's SIGKILL, the guest's journaled ext4 absorbs it.
func (m *qemuManager) stopRuntime(ctx context.Context, name string, rt *qemuRuntime) error {
	if mt, err := m.loadMeta(name); err == nil {
		target := sshexec.Target{Host: mt.Network.GuestIP, User: m.opts.SSHUser, KeyPath: m.opts.PrivateKeyPath, Dialer: m.DialGuest}
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, _, _ = target.Run(shutdownCtx, "sudo -n poweroff", 1024)
		cancel()
	}
	if waitRuntime(ctx, rt.done, qemuStopTimeout) {
		return nil
	}
	if rt.cmd.Process != nil {
		_ = rt.cmd.Process.Kill()
	}
	if !waitRuntime(context.Background(), rt.done, 10*time.Second) {
		return fmt.Errorf("VM %s did not stop", name)
	}
	return nil
}

func waitRuntime(ctx context.Context, done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (m *qemuManager) Delete(ctx context.Context, name string) error {
	lock := m.vmLock(name)
	lock.Lock()
	defer lock.Unlock()
	mt, err := m.loadMeta(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	rt := m.running[name]
	if rt != nil {
		rt.state = "stopping"
	}
	m.mu.Unlock()
	if rt != nil {
		if err := m.stopRuntime(ctx, name, rt); err != nil {
			return err
		}
	}
	m.network.CloseForwards(mt.Network.GuestIP)
	if err := os.RemoveAll(m.vmDir(name)); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.locks, name)
	m.mu.Unlock()
	return nil
}

func (m *qemuManager) List(ctx context.Context) ([]*Info, error) {
	entries, err := os.ReadDir(filepath.Join(m.opts.StateDir, "vms"))
	if err != nil {
		return nil, err
	}
	out := make([]*Info, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mt, err := m.loadMeta(entry.Name())
		if err != nil {
			continue
		}
		info, err := m.info(entry.Name(), mt)
		if err == nil {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *qemuManager) Get(ctx context.Context, name string) (*Info, error) {
	mt, err := m.loadMeta(name)
	if err != nil {
		return nil, err
	}
	return m.info(name, mt)
}

func (m *qemuManager) info(name string, mt *vmMeta) (*Info, error) {
	state := "stopped"
	m.mu.Lock()
	if rt := m.running[name]; rt != nil {
		state = runtimeState(rt)
	}
	m.mu.Unlock()
	info := &Info{
		Name:      name,
		State:     state,
		CPUs:      mt.Spec.CPUs,
		MemoryMB:  mt.Spec.MemoryMB,
		DiskGB:    mt.Spec.DiskGB,
		MAC:       mt.MAC,
		CreatedAt: mt.CreatedAt,
	}
	if state == "running" || state == "starting" {
		info.IP = mt.Network.GuestIP
	}
	return info, nil
}

// DialGuest implements GuestDialer: guest-subnet addresses go through the
// in-process network stack, anything else through the host network — so it
// is safe as a blanket dialer for the proxy.
func (m *qemuManager) DialGuest(ctx context.Context, network, addr string) (net.Conn, error) {
	return m.network.DialContext(ctx, network, addr)
}

// ForwardGuestPort implements PortForwarder for the web UI's Local links.
func (m *qemuManager) ForwardGuestPort(name string, port int) (string, error) {
	mt, err := m.loadMeta(name)
	if err != nil {
		return "", err
	}
	return m.network.Forward(mt.Network.GuestIP, port)
}

func (m *qemuManager) EnsureImage(ctx context.Context) (string, error) {
	m.dlMu.Lock()
	defer m.dlMu.Unlock()
	return m.ensureDownload(ctx, m.opts.ImageURL)
}

func (m *qemuManager) ensureDownload(ctx context.Context, sourceURL string) (string, error) {
	if sourceURL == "" {
		return "", errors.New("download URL is empty")
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return "", err
	}
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return "", fmt.Errorf("URL has no filename: %s", sourceURL)
	}
	dest := filepath.Join(m.opts.StateDir, "images", name)
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, nil
	}
	log.Printf("downloading %s", sourceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", sourceURL, resp.StatusCode)
	}
	tmp := dest + ".exe-download"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", err
	}
	log.Printf("download ready: %s", dest)
	return dest, nil
}
