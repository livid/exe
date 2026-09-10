//go:build linux

package vmm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
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
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"exe/internal/cloudinit"
	"exe/internal/sshexec"
)

const (
	firecrackerBootTimeout = 3 * time.Minute
	firecrackerStopTimeout = 30 * time.Second
	firecrackerLockTimeout = 15 * time.Second
)

type fcManager struct {
	opts      Options
	binary    string
	helper    string
	network   *net.IPNet
	prefixLen int

	ownerUID    uint32
	ownerGID    uint32
	ownerGroups []uint32

	mu      sync.Mutex
	running map[string]*fcRuntime
	locks   map[string]*sync.Mutex

	createMu  sync.Mutex
	dlMu      sync.Mutex
	stateLock *os.File
}

type fcRuntime struct {
	cmd     *exec.Cmd
	done    chan struct{}
	state   string
	exitErr error
	logFile *os.File
}

type fcConfig struct {
	BootSource        fcBootSource         `json:"boot-source"`
	Drives            []fcDrive            `json:"drives"`
	MachineConfig     fcMachineConfig      `json:"machine-config"`
	NetworkInterfaces []fcNetworkInterface `json:"network-interfaces"`
	Logger            fcLogger             `json:"logger"`
}

type fcBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}

type fcDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type fcMachineConfig struct {
	VCPUCount       int  `json:"vcpu_count"`
	MemSizeMiB      int  `json:"mem_size_mib"`
	SMT             bool `json:"smt"`
	TrackDirtyPages bool `json:"track_dirty_pages"`
}

type fcNetworkInterface struct {
	IfaceID     string `json:"iface_id"`
	GuestMAC    string `json:"guest_mac"`
	HostDevName string `json:"host_dev_name"`
}

type fcLogger struct {
	LogPath       string `json:"log_path"`
	Level         string `json:"level"`
	ShowLevel     bool   `json:"show_level"`
	ShowLogOrigin bool   `json:"show_log_origin"`
}

// New creates the Firecracker backend on Linux. TAP and firewall changes are
// delegated to a small root-owned helper; the daemon and VMs stay unprivileged.
func New(opts Options) (Manager, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, noBackend(fmt.Errorf("firecracker is unsupported on linux/%s", runtime.GOARCH))
	}
	if strings.TrimSpace(opts.Firecracker.Binary) == "" {
		opts.Firecracker.Binary = "firecracker"
	}
	binary, err := exec.LookPath(opts.Firecracker.Binary)
	if err != nil {
		return nil, noBackend(fmt.Errorf("find Firecracker binary %q: %w", opts.Firecracker.Binary, err))
	}
	if strings.TrimSpace(opts.Firecracker.NetworkHelper) == "" {
		opts.Firecracker.NetworkHelper = "/usr/local/libexec/exe-net-helper"
	}
	helper, err := exec.LookPath(opts.Firecracker.NetworkHelper)
	if err != nil {
		return nil, noBackend(fmt.Errorf("find Firecracker network helper %q: %w", opts.Firecracker.NetworkHelper, err))
	}
	if err := validateNetworkHelper(helper); err != nil {
		return nil, err
	}
	for _, command := range []string{"debugfs", "resize2fs"} {
		if _, err := exec.LookPath(command); err != nil {
			return nil, noBackend(fmt.Errorf("the Linux Firecracker backend requires %s: %w", command, err))
		}
	}
	if opts.Firecracker.KernelURL == "" {
		return nil, errors.New("firecracker.kernel_url is required")
	}
	if opts.Firecracker.NetworkCIDR == "" {
		opts.Firecracker.NetworkCIDR = "172.30.0.0/16"
	}
	ip, network, err := net.ParseCIDR(opts.Firecracker.NetworkCIDR)
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("invalid Firecracker IPv4 network %q", opts.Firecracker.NetworkCIDR)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones > 30 {
		return nil, fmt.Errorf("Firecracker network %q must contain at least one /30 subnet", opts.Firecracker.NetworkCIDR)
	}
	network.IP = ip.To4().Mask(network.Mask)

	for _, d := range []string{opts.StateDir, filepath.Join(opts.StateDir, "vms"), filepath.Join(opts.StateDir, "images")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	uid, gid, groups, err := stateOwner(opts.StateDir)
	if err != nil {
		return nil, err
	}
	if err := checkKVMAccess(uid, gid, groups); err != nil {
		return nil, err
	}
	for _, d := range []string{filepath.Join(opts.StateDir, "vms"), filepath.Join(opts.StateDir, "images")} {
		if err := os.Chown(d, int(uid), int(gid)); err != nil {
			return nil, err
		}
	}
	stateLock, err := acquireStateLock(opts.StateDir, firecrackerLockTimeout)
	if err != nil {
		return nil, err
	}

	manager := &fcManager{
		opts:        opts,
		binary:      binary,
		helper:      helper,
		network:     network,
		prefixLen:   ones,
		ownerUID:    uid,
		ownerGID:    gid,
		ownerGroups: groups,
		running:     make(map[string]*fcRuntime),
		locks:       make(map[string]*sync.Mutex),
		stateLock:   stateLock,
	}
	if err := manager.reconcileNetworks(); err != nil {
		releaseStateLock(stateLock)
		return nil, fmt.Errorf("reconcile stale Firecracker networking: %w", err)
	}
	return manager, nil
}

func acquireStateLock(stateDir string, timeout time.Duration) (*os.File, error) {
	path := filepath.Join(stateDir, "firecracker.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Firecracker state lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			file.Close()
			return nil, fmt.Errorf("lock Firecracker state: %w", err)
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("another exe daemon is using %s", stateDir)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := file.Truncate(0); err != nil {
		releaseStateLock(file)
		return nil, fmt.Errorf("reset Firecracker state lock: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		releaseStateLock(file)
		return nil, fmt.Errorf("seek Firecracker state lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		releaseStateLock(file)
		return nil, fmt.Errorf("write Firecracker state lock: %w", err)
	}
	return file, nil
}

func releaseStateLock(file *os.File) {
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	_ = file.Close()
}

func validateNetworkHelper(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect Firecracker network helper %s", path)
	}
	if sys.Uid != 0 {
		return fmt.Errorf("Firecracker network helper %s must be owned by root", path)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("Firecracker network helper %s must not be group- or world-writable", path)
	}
	return nil
}

func stateOwner(stateDir string) (uint32, uint32, []uint32, error) {
	ownerPath := stateDir
	if home := os.Getenv("HOME"); home != "" {
		if rel, err := filepath.Rel(home, stateDir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			ownerPath = home
		}
	}
	st, err := os.Stat(ownerPath)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("stat state owner %s: %w", ownerPath, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, nil, fmt.Errorf("determine state owner for %s", ownerPath)
	}
	uid, gid := sys.Uid, sys.Gid
	groups := []uint32{gid}
	if account, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
		if ids, err := account.GroupIds(); err == nil {
			groups = groups[:0]
			seen := make(map[uint32]bool)
			for _, id := range ids {
				n, err := strconv.ParseUint(id, 10, 32)
				if err == nil && !seen[uint32(n)] {
					groups = append(groups, uint32(n))
					seen[uint32(n)] = true
				}
			}
			if !seen[gid] {
				groups = append(groups, gid)
			}
		}
	}
	return uid, gid, groups, nil
}

func checkKVMAccess(uid, gid uint32, groups []uint32) error {
	st, err := os.Stat("/dev/kvm")
	if err != nil {
		return fmt.Errorf("Firecracker requires /dev/kvm: %w", err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("could not inspect /dev/kvm permissions")
	}
	if uid == 0 {
		return nil
	}
	mode := st.Mode().Perm()
	allowed := sys.Uid == uid && mode&0o600 == 0o600
	if !allowed {
		inGroup := sys.Gid == gid
		for _, group := range groups {
			inGroup = inGroup || sys.Gid == group
		}
		allowed = inGroup && mode&0o060 == 0o060
	}
	if !allowed && mode&0o006 != 0o006 {
		return fmt.Errorf("user %d cannot access /dev/kvm; add the EXE_HOME owner to the kvm group", uid)
	}
	return nil
}

func (m *fcManager) vmDir(name string) string {
	return filepath.Join(m.opts.StateDir, "vms", name)
}

func (m *fcManager) vmLock(name string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock := m.locks[name]
	if lock == nil {
		lock = &sync.Mutex{}
		m.locks[name] = lock
	}
	return lock
}

func (m *fcManager) loadMeta(name string) (*vmMeta, error) {
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
		return nil, fmt.Errorf("VM %s was not created with the Linux Firecracker backend", name)
	}
	return &mt, nil
}

func (m *fcManager) saveMeta(name string, mt *vmMeta) error {
	data, err := json.MarshalIndent(mt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(m.vmDir(name), "vm.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Chown(path, int(m.ownerUID), int(m.ownerGID))
}

func (m *fcManager) Create(ctx context.Context, spec Spec) (*Info, error) {
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
	dir := m.vmDir(spec.Name)
	if _, err := os.Stat(dir); err == nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, fmt.Errorf("vm %q already exists", spec.Name)
	} else if !os.IsNotExist(err) {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}

	base, err := m.EnsureImage(ctx)
	if err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	if err := os.Chown(dir, int(m.ownerUID), int(m.ownerGID)); err != nil {
		os.RemoveAll(dir)
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()

	network, err := m.allocateNetwork()
	if err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	disk := filepath.Join(dir, "disk.raw")
	if err := cloneLinuxDisk(ctx, base, disk); err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	if st, err := os.Stat(disk); err == nil {
		if target := int64(spec.DiskGB) << 30; target > st.Size() {
			if err := os.Truncate(disk, target); err != nil {
				m.createMu.Unlock()
				lock.Unlock()
				return nil, err
			}
		}
	}
	if output, err := exec.CommandContext(ctx, "resize2fs", "-f", disk).CombinedOutput(); err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, fmt.Errorf("resize root filesystem: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Chown(disk, int(m.ownerUID), int(m.ownerGID)); err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	mac, err := randomMAC()
	if err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	if err := configureLinuxGuest(disk, spec.Name, m.opts.SSHUser, m.opts.AuthorizedKey, mac, network); err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	mt := &vmMeta{Spec: spec, MAC: mac, CreatedAt: time.Now().UTC(), Network: network}
	if err := m.saveMeta(spec.Name, mt); err != nil {
		m.createMu.Unlock()
		lock.Unlock()
		return nil, err
	}
	keep = true
	m.createMu.Unlock()
	lock.Unlock()

	return m.Start(ctx, spec.Name)
}

type imageRegion struct {
	offset int64
	size   int64
}

func cloneLinuxDisk(ctx context.Context, base, dst string) error {
	region, err := linuxRootRegion(base)
	if err != nil {
		return err
	}
	in, err := os.Open(base)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := in.Seek(region.offset, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	buf := make([]byte, 8<<20)
	zero := make([]byte, sparseChunk)
	remaining := region.size
	var copyErr error
	for remaining > 0 {
		select {
		case <-ctx.Done():
			copyErr = ctx.Err()
			remaining = 0
			continue
		default:
		}
		n := int64(len(buf))
		if n > remaining {
			n = remaining
		}
		read, err := io.ReadFull(in, buf[:n])
		if read > 0 {
			if writeErr := writeSparse(out, buf[:read], zero); writeErr != nil {
				copyErr = writeErr
				break
			}
			remaining -= int64(read)
		}
		if err != nil {
			copyErr = err
			break
		}
	}
	// Zero runs were seeked over, not written; extend to the full size so a
	// trailing run stays a hole.
	if copyErr == nil {
		copyErr = out.Truncate(region.size)
	}
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(dst)
		return copyErr
	}
	return closeErr
}

func linuxRootRegion(path string) (imageRegion, error) {
	file, err := os.Open(path)
	if err != nil {
		return imageRegion{}, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return imageRegion{}, err
	}
	if hasExt4Magic(file, 0) {
		return imageRegion{size: st.Size()}, nil
	}

	header := make([]byte, 92)
	if _, err := file.ReadAt(header, 512); err != nil || string(header[:8]) != "EFI PART" {
		return imageRegion{}, fmt.Errorf("base image must be an ext4 filesystem or a GPT disk containing one")
	}
	tableLBA := binary.LittleEndian.Uint64(header[72:80])
	entryCount := binary.LittleEndian.Uint32(header[80:84])
	entrySize := binary.LittleEndian.Uint32(header[84:88])
	if entryCount == 0 || entryCount > 4096 || entrySize < 128 || entrySize > 4096 {
		return imageRegion{}, errors.New("invalid GPT partition table")
	}
	entry := make([]byte, entrySize)
	var best imageRegion
	for i := uint32(0); i < entryCount; i++ {
		offset := int64(tableLBA*512 + uint64(i)*uint64(entrySize))
		if _, err := file.ReadAt(entry, offset); err != nil {
			return imageRegion{}, fmt.Errorf("read GPT entry %d: %w", i, err)
		}
		if allZero(entry[:16]) {
			continue
		}
		first := binary.LittleEndian.Uint64(entry[32:40])
		last := binary.LittleEndian.Uint64(entry[40:48])
		if last < first {
			continue
		}
		candidate := imageRegion{offset: int64(first * 512), size: int64((last - first + 1) * 512)}
		if candidate.offset < 0 || candidate.size <= 0 || candidate.offset+candidate.size > st.Size() {
			continue
		}
		if candidate.size > best.size && hasExt4Magic(file, candidate.offset) {
			best = candidate
		}
	}
	if best.size == 0 {
		return imageRegion{}, errors.New("base GPT image has no ext4 root partition")
	}
	return best, nil
}

// sparseChunk is the granularity at which zero runs in the base image become
// holes in the cloned disk.
const sparseChunk = 64 << 10

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

func hasExt4Magic(file *os.File, offset int64) bool {
	magic := make([]byte, 2)
	_, err := file.ReadAt(magic, offset+1024+56)
	return err == nil && magic[0] == 0x53 && magic[1] == 0xef
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func randomMAC() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[0] = (buf[0] | 2) & 0xfe
	return net.HardwareAddr(buf).String(), nil
}

func systemdNetworkConfig(mac string, network *vmNetwork) string {
	return fmt.Sprintf(`[Match]
MACAddress=%s

[Network]
Address=%s/%d
Gateway=%s
DNS=1.1.1.1
DNS=8.8.8.8
`, mac, network.GuestIP, network.PrefixLen, network.HostIP)
}

func configureLinuxGuest(disk, hostname, user, authorizedKey, mac string, network *vmNetwork) error {
	if err := writeExt4File(disk, "/etc/systemd/network/10-exe.network", systemdNetworkConfig(mac, network)); err != nil {
		return fmt.Errorf("configure guest network: %w", err)
	}
	if err := makeExt4Dir(disk, "/var/lib/exe-seed"); err != nil {
		return fmt.Errorf("create guest NoCloud directory: %w", err)
	}
	userData, metaData := cloudinit.Documents(hostname, user, authorizedKey, false)
	for path, data := range map[string]string{
		"/var/lib/exe-seed/user-data": userData,
		"/var/lib/exe-seed/meta-data": metaData,
	} {
		if err := writeExt4File(disk, path, data); err != nil {
			return fmt.Errorf("write guest NoCloud data: %w", err)
		}
	}
	return nil
}

func writeExt4File(disk, target, data string) error {
	file, err := os.CreateTemp("", "exe-guest-file-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err := file.WriteString(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	request := "write " + temp + " " + target
	output, err := exec.Command("debugfs", "-w", "-R", request, disk).CombinedOutput()
	// debugfs exits 0 even when a command fails, so require the marker it
	// prints after creating the inode.
	if err == nil && !strings.Contains(string(output), "Allocated inode:") {
		err = errors.New("debugfs allocated no inode")
	}
	if err != nil {
		return fmt.Errorf("write %s: %w: %s", target, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// makeExt4Dir creates target inside the ext4 image; an already existing
// directory counts as success. debugfs exits 0 even when a command fails, so
// the directory is verified with a read-back.
func makeExt4Dir(disk, target string) error {
	output, err := exec.Command("debugfs", "-w", "-R", "mkdir "+target, disk).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkdir %s: %w: %s", target, err, strings.TrimSpace(string(output)))
	}
	stat, err := exec.Command("debugfs", "-R", "stat "+target, disk).CombinedOutput()
	if err != nil || !strings.Contains(string(stat), "Type: directory") {
		return fmt.Errorf("mkdir %s: %s", target, strings.TrimSpace(string(output)))
	}
	return nil
}
func (m *fcManager) Start(ctx context.Context, name string) (*Info, error) {
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

	kernel, err := m.ensureKernel(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.setupNetwork(name, mt); err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			m.cleanupNetwork(name, mt)
		}
	}()

	configPath, err := m.writeFirecrackerConfig(name, mt, kernel)
	if err != nil {
		return nil, err
	}
	consolePath := filepath.Join(m.vmDir(name), "console.log")
	console, err := os.OpenFile(consolePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(consolePath, int(m.ownerUID), int(m.ownerGID)); err != nil {
		console.Close()
		return nil, err
	}

	cmd := exec.Command(m.binary, "--no-api", "--config-file", configPath)
	cmd.Stdout = console
	cmd.Stderr = console
	cmd.Dir = m.vmDir(name)
	sys := &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
		Setpgid:   true,
	}
	if os.Geteuid() == 0 {
		sys.Credential = &syscall.Credential{
			Uid:    m.ownerUID,
			Gid:    m.ownerGID,
			Groups: m.ownerGroups,
		}
	}
	cmd.SysProcAttr = sys
	if err := cmd.Start(); err != nil {
		console.Close()
		return nil, fmt.Errorf("start Firecracker: %w", err)
	}
	rt := &fcRuntime{cmd: cmd, done: make(chan struct{}), state: "starting", logFile: console}
	m.mu.Lock()
	m.running[name] = rt
	m.mu.Unlock()
	started = true
	go m.waitProcess(name, mt, rt)

	select {
	case <-rt.done:
		return nil, fmt.Errorf("Firecracker exited during startup: %w (see %s)", runtimeError(rt), consolePath)
	case <-time.After(500 * time.Millisecond):
	}

	m.mu.Lock()
	if m.running[name] == rt {
		rt.state = "running"
	}
	m.mu.Unlock()
	log.Printf("vm %s: Firecracker pid %d, waiting for SSH at %s", name, cmd.Process.Pid, mt.Network.GuestIP)
	if err := m.waitSSH(ctx, mt.Network.GuestIP, firecrackerBootTimeout, rt); err != nil {
		return nil, fmt.Errorf("VM %s did not become ready: %w (see %s)", name, err, consolePath)
	}
	log.Printf("vm %s: ready at %s", name, mt.Network.GuestIP)
	return m.info(name, mt)
}

func (m *fcManager) waitProcess(name string, mt *vmMeta, rt *fcRuntime) {
	err := rt.cmd.Wait()
	m.mu.Lock()
	rt.exitErr = err
	stopping := rt.state == "stopping"
	m.mu.Unlock()
	rt.logFile.Close()
	m.cleanupNetwork(name, mt)
	m.mu.Lock()
	if m.running[name] == rt {
		delete(m.running, name)
	}
	m.mu.Unlock()
	close(rt.done)
	if err != nil && !stopping {
		log.Printf("vm %s: Firecracker exited: %v", name, err)
	}
}

func runtimeError(rt *fcRuntime) error {
	if rt.exitErr != nil {
		return rt.exitErr
	}
	return errors.New("process exited")
}

func runtimeState(rt *fcRuntime) string {
	if rt.state == "" {
		return "starting"
	}
	return rt.state
}

func (m *fcManager) waitSSH(ctx context.Context, host string, timeout time.Duration, rt *fcRuntime) error {
	deadline := time.Now().Add(timeout)
	target := sshexec.Target{Host: host, User: m.opts.SSHUser, KeyPath: m.opts.PrivateKeyPath}
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
			return fmt.Errorf("Firecracker exited: %w", runtimeError(rt))
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *fcManager) writeFirecrackerConfig(name string, mt *vmMeta, kernel string) (string, error) {
	dir := m.vmDir(name)
	bootArgs := fmt.Sprintf(
		"console=ttyS0 reboot=k panic=1 pci=off fstab=no network-config=disabled ds=nocloud;s=file:///var/lib/exe-seed/ "+
			"ifname=eth0:%s ip=%s::%s:255.255.255.252:%s:eth0:off:1.1.1.1:8.8.8.8 root=/dev/vda rw",
		mt.MAC, mt.Network.GuestIP, mt.Network.HostIP, name,
	)
	cfg := fcConfig{
		BootSource: fcBootSource{
			KernelImagePath: kernel,
			BootArgs:        bootArgs,
		},
		Drives: []fcDrive{
			{DriveID: "rootfs", PathOnHost: filepath.Join(dir, "disk.raw"), IsRootDevice: true, IsReadOnly: false},
		},
		MachineConfig: fcMachineConfig{
			VCPUCount:       mt.Spec.CPUs,
			MemSizeMiB:      mt.Spec.MemoryMB,
			SMT:             false,
			TrackDirtyPages: false,
		},
		NetworkInterfaces: []fcNetworkInterface{{
			IfaceID:     "eth0",
			GuestMAC:    mt.MAC,
			HostDevName: mt.Network.Tap,
		}},
		Logger: fcLogger{
			LogPath:       filepath.Join(dir, "firecracker.log"),
			Level:         "Info",
			ShowLevel:     true,
			ShowLogOrigin: false,
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "firecracker.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o640); err != nil {
		return "", err
	}
	if err := os.Chown(path, int(m.ownerUID), int(m.ownerGID)); err != nil {
		return "", err
	}
	logPath := filepath.Join(dir, "firecracker.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640); err == nil {
		f.Close()
		if err := os.Chown(logPath, int(m.ownerUID), int(m.ownerGID)); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (m *fcManager) Stop(ctx context.Context, name string) error {
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

func (m *fcManager) stopRuntime(ctx context.Context, name string, rt *fcRuntime) error {
	target := sshexec.Target{Host: "", User: m.opts.SSHUser, KeyPath: m.opts.PrivateKeyPath}
	if mt, err := m.loadMeta(name); err == nil {
		target.Host = mt.Network.GuestIP
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, _, _ = target.Run(shutdownCtx, "sudo -n poweroff", 1024)
		cancel()
	}
	if waitRuntime(ctx, rt.done, firecrackerStopTimeout) {
		return nil
	}
	if rt.cmd.Process != nil {
		_ = rt.cmd.Process.Signal(syscall.SIGTERM)
	}
	if waitRuntime(context.Background(), rt.done, 10*time.Second) {
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

func (m *fcManager) Delete(ctx context.Context, name string) error {
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
	m.cleanupNetwork(name, mt)
	if err := os.RemoveAll(m.vmDir(name)); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.locks, name)
	m.mu.Unlock()
	return nil
}

func (m *fcManager) List(ctx context.Context) ([]*Info, error) {
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

func (m *fcManager) Get(ctx context.Context, name string) (*Info, error) {
	mt, err := m.loadMeta(name)
	if err != nil {
		return nil, err
	}
	return m.info(name, mt)
}

func (m *fcManager) info(name string, mt *vmMeta) (*Info, error) {
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

func (m *fcManager) EnsureImage(ctx context.Context) (string, error) {
	m.dlMu.Lock()
	defer m.dlMu.Unlock()
	base, err := m.ensureDownload(ctx, m.opts.ImageURL, "")
	if err != nil {
		return "", fmt.Errorf("base image: %w", err)
	}
	if _, err := m.ensureDownload(ctx, m.opts.Firecracker.KernelURL, "firecracker-"); err != nil {
		return "", fmt.Errorf("kernel: %w", err)
	}
	return base, nil
}

func (m *fcManager) ensureKernel(ctx context.Context) (string, error) {
	m.dlMu.Lock()
	defer m.dlMu.Unlock()
	return m.ensureDownload(ctx, m.opts.Firecracker.KernelURL, "firecracker-")
}

func (m *fcManager) ensureDownload(ctx context.Context, sourceURL, prefix string) (string, error) {
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
	dest := filepath.Join(m.opts.StateDir, "images", prefix+name)
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
	if err := os.Chown(tmp, int(m.ownerUID), int(m.ownerGID)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", err
	}
	log.Printf("download ready: %s", dest)
	return dest, nil
}
