// Package macos9 manages the persistent, software-emulated Power Macs of the
// web apps: one that runs Mac OS 9 and one that runs Mac OS X (system.go
// says what tells them apart). Their disks and the emulator runtime they
// share are node-local and deliberately outside synced app data.
package macos9

import (
	"context"
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MIT-licensed OS 9 tablet driver loaded into the guest ROM at boot.
// See tablet/README.md for the pinned source and compatibility requirements.
//
//go:embed tablet/nvramrc.fth
var tabletNVRAMRC string

type Step struct {
	Title  string `json:"title"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}
type Status struct {
	Phase     string `json:"phase"`
	Message   string `json:"message"`
	Steps     []Step `json:"steps"`
	Active    bool   `json:"active"`
	Running   bool   `json:"running"`
	Installed bool   `json:"installed"`
	Bytes     int64  `json:"bytes"`
	Total     int64  `json:"total"`
	// Loopback ports of this machine that lead to services inside a running
	// Mac ("ssh", "vnc"), for a Mac that has any (system.forwards).
	Ports map[string]int `json:"ports,omitempty"`
	// The share of an absolute pointer's range this Mac's system leaves
	// unused (system.trims), as a viewer last found it.
	Trim float64 `json:"trim,omitempty"`
	// Agent is an agent's way in (agent.go), for a Mac with a shell; the
	// server fills it.
	Agent *Agent `json:"agent,omitempty"`
}
type Manager struct {
	sys         *system
	root        string
	runtimeRoot string         // where the emulator runtime is unpacked
	ports       map[string]int // host ports of the running Mac's forwards
	mu          sync.Mutex
	agentMu     sync.Mutex // the agent key's minting
	cdMu        sync.Mutex
	state       Status
	cancel      context.CancelFunc
}

func (m *Manager) newSteps() []Step {
	titles := []string{"Prepare the emulator", "Download " + m.sys.name, "Verify the download", "Prepare your hard disk", "Start the Mac", "Install " + m.sys.name}
	steps := make([]Step, len(titles))
	for i, t := range titles {
		steps[i] = Step{Title: t, State: "pending"}
	}
	return steps
}

// New is the Mac that runs Mac OS 9. The emulator runtime is unpacked in
// its root.
func New(root string) *Manager { return newManager(macOS9, root, root) }

// NewMacOSX is the Mac that runs Mac OS X. It has a root of its own and
// runs the emulator in the Mac OS 9 root, so a node unpacks one runtime.
func NewMacOSX(root, runtimeRoot string) *Manager { return newManager(macOSX, root, runtimeRoot) }

func newManager(sys *system, root, runtimeRoot string) *Manager {
	m := &Manager{sys: sys, root: root, runtimeRoot: runtimeRoot}
	m.state = Status{Phase: "new", Message: "Your Mac will be set up on this computer.", Steps: m.newSteps()}
	if b, err := os.ReadFile(filepath.Join(root, "setup.json")); err == nil {
		var saved Status
		if json.Unmarshal(b, &saved) == nil && len(saved.Steps) == len(m.state.Steps) {
			m.state = saved
			if saved.Active {
				m.state.Phase = "interrupted"
				m.state.Message = "Setup was interrupted. Continue to resume safely."
			}
		}
	}
	m.state.Active = false
	if !m.installed() && m.running() {
		m.state.Phase = "installing"
	}
	if m.installed() && m.state.Phase == "new" {
		for i := range m.state.Steps {
			m.state.Steps[i].State = "done"
			m.state.Steps[i].Detail = "Using the saved installation."
		}
	}
	return m
}
func (m *Manager) path(name string) string { return filepath.Join(m.root, name) }

// runtimePath names a file of the emulator runtime, which both Macs use.
func (m *Manager) runtimePath(name string) string { return filepath.Join(m.runtimeRoot, name) }
func (m *Manager) installed() bool                { _, err := os.Stat(m.path("installed.json")); return err == nil }
func (m *Manager) running() bool {
	// QEMU holds a POSIX lock on its PID file for its entire lifetime. Query
	// that lock instead of treating an occupied QMP listener as a dead guest.
	// A stale, unlocked PID file cannot mistake a reused PID for our process.
	locked, err := qemuPIDLocked(m.path("qemu.pid"))
	if locked || err != nil {
		return true // Fail closed: uncertainty must never authorize replacing sockets.
	}
	// Support guests launched without a PID file, including older installations.
	if _, err := os.Lstat(m.path("qmp.sock")); err != nil {
		return !os.IsNotExist(err)
	}
	c, err := net.DialTimeout("unix", m.path("qmp.sock"), 300*time.Millisecond)
	if err == nil {
		c.Close()
		return true
	}
	return !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED)
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.Steps = append([]Step(nil), s.Steps...)
	s.Installed = m.installed()
	s.Running = m.running()
	if s.Running && s.Installed {
		s.Ports = m.forwardedLocked()
	}
	s.Trim = m.pointerTrim()
	if !s.Active && s.Running && !s.Installed {
		s.Phase = "installing"
	}
	if !s.Active && s.Installed && s.Phase != "error" {
		if s.Running {
			s.Phase = "ready"
			s.Message = "Your Mac is running."
		} else {
			s.Phase = "stopped"
			s.Message = "Your Mac is shut down. Start it whenever you like."
		}
	}
	return s
}
func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(m.path("setup.json.tmp"), b, 0600); err != nil {
		return err
	}
	return os.Rename(m.path("setup.json.tmp"), m.path("setup.json"))
}
func (m *Manager) step(i int, state, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Steps[i].State = state
	m.state.Steps[i].Detail = detail
	m.state.Message = detail
	// Progress survives closing the app or restarting the daemon.
	_ = m.saveLocked()
}
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Active {
		return nil
	}
	if m.running() {
		return nil
	}
	m.state = Status{Phase: "setup", Message: "Preparing your Mac…", Steps: m.newSteps(), Active: true}
	if err := m.saveLocked(); err != nil {
		// Saving can fail before the setup worker starts. Keep the failure in
		// memory so later status polls and a reopened app still explain it.
		err = fmt.Errorf("Cannot save setup progress: %w", err)
		m.state.Active = false
		m.state.Phase = "error"
		m.state.Message = err.Error()
		m.state.Steps[0].State = "error"
		m.state.Steps[0].Detail = err.Error()
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.sys.setup)
	m.cancel = cancel
	go func() {
		defer cancel()
		err := m.prepare(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.state.Active = false
		m.cancel = nil
		if err != nil {
			m.state.Phase = "error"
			m.state.Message = err.Error()
			if ctx.Err() != nil {
				m.state.Phase = "interrupted"
				m.state.Message = "Setup stopped. Continue to retry; your hard disk is kept."
			}
			for i := range m.state.Steps {
				if m.state.Steps[i].State == "working" {
					m.state.Steps[i].State = "error"
					m.state.Steps[i].Detail = m.state.Message
				}
			}
		} else if m.installed() {
			m.state.Phase = "ready"
			m.state.Message = "Your Mac is running."
		} else {
			m.state.Phase = "installing"
			m.state.Message = "Follow the installation guide beside the Mac screen."
		}
		_ = m.saveLocked()
	}()
	return nil
}
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// Finish is a user acknowledgement of the installer's success. It is
// allowed only after a clean guest shutdown, never while the disk is in use.
func (m *Manager) Finish() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Active || m.running() {
		return errors.New(m.sys.shutDown)
	}
	if m.state.Phase != "installing" {
		return errors.New("Complete the installation guide first.")
	}
	if _, err := os.Stat(m.path(m.sys.disk)); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := m.command(ctx, m.binary("qemu-img"), "check", "-f", "qcow2", m.path(m.sys.disk)); err != nil {
		return err
	}
	if err := os.WriteFile(m.path("installed.json"), []byte("{\"version\":\""+m.sys.version+"\"}\n"), 0600); err != nil {
		return err
	}
	m.state.Steps[5].State = "done"
	m.state.Steps[5].Detail = "Installation confirmed. Your disk is saved."
	m.state.Phase = "stopped"
	m.state.Message = "Installation complete. Start your Mac."
	return m.saveLocked()
}
func (m *Manager) prepare(ctx context.Context) error {
	m.step(0, "working", "Checking for a PowerPC emulator…")
	if err := m.ensureRuntime(ctx); err != nil {
		return err
	}
	if err := m.ensureDisplayDriver(); err != nil {
		return err
	}
	m.step(0, "done", "PowerPC emulator ready.")
	installed := m.installed()
	if !installed {
		m.step(1, "working", m.sys.downloading)
		if err := m.download(ctx); err != nil {
			return err
		}
		m.step(1, "done", "Installer downloaded.")
		m.step(2, "done", "Installer matches the published checksum.")
	} else {
		m.step(1, "done", "Using your existing installation.")
		m.step(2, "done", "No download needed.")
	}
	m.step(3, "working", "Checking your persistent hard disk…")
	disk := m.path(m.sys.disk)
	if _, err := os.Stat(disk); os.IsNotExist(err) {
		if installed {
			return errors.New("The installed Mac's disk is missing. Restore " + m.sys.disk + " from a backup.")
		}
		tmp := disk + ".tmp"
		if err = m.command(ctx, m.binary("qemu-img"), "create", "-f", "qcow2", tmp, m.sys.diskSize); err != nil {
			return err
		}
		if err = os.Rename(tmp, disk); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	m.step(3, "done", m.sys.diskReady)
	m.step(4, "working", "Starting your Power Mac G4…")
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.launch(ctx, !installed); err != nil {
		return err
	}
	m.step(4, "done", "Mac display connected. Startup may take a minute.")
	if installed {
		m.step(5, "done", m.sys.name+" is already installed.")
	} else {
		m.step(5, "waiting", m.sys.install)
	}
	return nil
}
func (m *Manager) binary(name string) string {
	if name == "qemu-system-ppc" && m.hasAudioRuntime() {
		return m.runtimePath("audio/qemu-system-ppc")
	}
	local := m.runtimePath(filepath.Join("runtime", "usr", "bin", name))
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return name
}

// Screamer is not in upstream QEMU. An optional locally installed build and
// its matching OpenBIOS must be present together before selecting it. It is
// part of the runtime both Macs run: Mac OS X plays through it as Mac OS 9
// does.
func (m *Manager) hasAudioRuntime() bool {
	for _, name := range []string{"audio/qemu-system-ppc", "audio/openbios-ppc"} {
		info, err := os.Stat(m.runtimePath(name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
	}
	return true
}
// The qegl device (OpenGL on this machine's GPU for Mac OS X) is not in
// upstream QEMU either: an emulator has it when it lists its options. Asked
// once per emulator file.
var qeglChecked struct {
	sync.Mutex
	key      string
	has, log bool // the device, and its logfile property
}

// qeglDevice is the -device argument for the Mac's GPU device, "" without
// one. Its log goes to qegl.log in the Mac's folder: a daemonized emulator
// writes its own messages nowhere.
func (m *Manager) qeglDevice() string {
	if !m.sys.gpu {
		return ""
	}
	has, log := m.qeglSupport()
	if !has {
		return ""
	}
	if !log {
		return "qegl"
	}
	return "qegl,logfile=" + strings.ReplaceAll(m.path("qegl.log"), ",", ",,")
}

func (m *Manager) qeglSupport() (has, log bool) {
	bin := m.binary("qemu-system-ppc")
	info, err := os.Stat(bin)
	if err != nil || !info.Mode().IsRegular() {
		return false, false
	}
	key := fmt.Sprintf("%s %d %d", bin, info.Size(), info.ModTime().UnixNano())
	qeglChecked.Lock()
	defer qeglChecked.Unlock()
	if qeglChecked.key != key {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "-device", "qegl,help")
		cmd.Env = m.env()
		out, err := cmd.CombinedOutput()
		qeglChecked.key = key
		qeglChecked.has = err == nil && strings.Contains(string(out), "qegl options")
		qeglChecked.log = qeglChecked.has && strings.Contains(string(out), "logfile=")
	}
	return qeglChecked.has, qeglChecked.log
}

func (m *Manager) env() []string { return runtimeEnv(m.runtimePath("runtime")) }
func runtimeEnv(dir string) []string {
	env := os.Environ()
	lib := filepath.Join(dir, "usr", "lib")
	dirs, _ := filepath.Glob(filepath.Join(lib, "*-linux-gnu"))
	dirs = append(dirs, lib)
	env = append(env, "LD_LIBRARY_PATH="+strings.Join(dirs, string(os.PathListSeparator)))
	if len(dirs) > 1 {
		env = append(env, "QEMU_MODULE_DIR="+filepath.Join(dirs[0], "qemu"))
	}
	return env
}
func (m *Manager) command(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = m.env()
	cmd.Dir = m.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 3000 {
			out = out[len(out)-3000:]
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// repairRuntimeFirmware also repairs installations interrupted after unpacking
// QEMU but before copying Debian's separately packaged VGA firmware.
func repairRuntimeFirmware(dir string) error {
	dst := filepath.Join(dir, "usr/share/qemu/vgabios-stdvga.bin")
	if info, err := os.Stat(dst); err == nil && info.Size() > 0 {
		return nil
	}
	bios, err := os.ReadFile(filepath.Join(dir, "usr/share/seabios/vgabios-stdvga.bin"))
	if err != nil {
		return err
	}
	if len(bios) == 0 {
		return errors.New("The VGA firmware is empty.")
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(dst+".tmp", bios, 0600); err != nil {
		return err
	}
	return os.Rename(dst+".tmp", dst)
}

func validateRuntime(ctx context.Context, dir string) error {
	for _, name := range []string{"usr/bin/qemu-system-ppc", "usr/bin/qemu-img", "usr/share/qemu/openbios-ppc", "usr/share/qemu/vgabios-stdvga.bin"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("Incomplete emulator component: %s", name)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	img := exec.CommandContext(ctx, filepath.Join(dir, "usr/bin/qemu-img"), "--version")
	img.Env = runtimeEnv(dir)
	if out, err := img.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img: %w: %.2000s", err, out)
	}
	// Actually initialize the machine: --version cannot detect missing firmware
	// or device modules. This paused probe has no guest disk and exits via QMP.
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "usr/bin/qemu-system-ppc"),
		"-L", filepath.Join(dir, "usr/share/qemu"), "-machine", "mac99", "-cpu", "G4",
		"-m", "32", "-usb", "-device", "usb-tablet", "-vga", "std", "-display", "none",
		"-nic", "none", "-S", "-qmp", "stdio")
	cmd.Env = runtimeEnv(dir)
	cmd.Stdin = strings.NewReader("{\"execute\":\"qmp_capabilities\"}\n{\"execute\":\"quit\"}\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Emulator startup check: %w: %.2000s", err, out)
	}
	return nil
}

func (m *Manager) recoverRuntime() error {
	if _, err := os.Stat(m.runtimePath("runtime")); !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(m.runtimePath("runtime.previous")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Rename(m.runtimePath("runtime.previous"), m.runtimePath("runtime"))
}

func (m *Manager) activateRuntime(staging string) error {
	previous := m.runtimePath("runtime.previous")
	if err := os.RemoveAll(previous); err != nil {
		return err
	}
	if err := os.Rename(m.runtimePath("runtime"), previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staging, m.runtimePath("runtime")); err != nil {
		_ = m.recoverRuntime()
		return err
	}
	return os.RemoveAll(previous)
}

// Both Macs run one runtime: only one of them checks or unpacks it at a time.
var runtimeMu sync.Mutex

func (m *Manager) ensureRuntime(ctx context.Context) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if err := m.recoverRuntime(); err != nil {
		return err
	}
	dir := m.runtimePath("runtime")
	if _, err := os.Stat(dir); err == nil {
		if repairRuntimeFirmware(dir) == nil && validateRuntime(ctx, dir) == nil {
			return nil
		}
	} else if os.IsNotExist(err) && m.command(ctx, "qemu-system-ppc", "--version") == nil && m.command(ctx, "qemu-img", "--version") == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return errors.New("Install QEMU (qemu-system-ppc and qemu-img) on this computer, then Continue. On macOS: brew install qemu.")
	}
	release, _ := os.ReadFile("/etc/os-release")
	if !strings.Contains(string(release), "ID=ubuntu") || !strings.Contains(string(release), "VERSION_ID=\"24.04\"") {
		return errors.New("Install QEMU with PowerPC, VNC and user networking support on this computer, then Continue. Automatic emulator setup supports Ubuntu 24.04.")
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return err
	}
	// A fresh package set prevents mixing cached versions from older attempts.
	if err := os.MkdirAll(m.runtimeRoot, 0700); err != nil {
		return err
	}
	pkgDir, err := os.MkdirTemp(m.runtimeRoot, "packages-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(pkgDir)
	pkgs := []string{"qemu-system-ppc", "qemu-system-common", "qemu-system-data", "qemu-utils", "seabios", "libfdt1", "libslirp0", "liburing2", "libpmem1", "libndctl6", "libdaxctl1"}
	for i, pkg := range pkgs {
		m.step(0, "working", fmt.Sprintf("Downloading emulator component %d of %d: %s", i+1, len(pkgs), pkg))
		cmd := exec.CommandContext(ctx, "apt-get", "download", pkg)
		cmd.Dir = pkgDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Cannot download %s: %w: %.2000s", pkg, err, out)
		}
	}
	staging := m.runtimePath("runtime.staging")
	if err = os.RemoveAll(staging); err != nil {
		return err
	}
	if err = os.MkdirAll(staging, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	files, err := filepath.Glob(filepath.Join(pkgDir, "*.deb"))
	if err != nil {
		return err
	}
	m.step(0, "working", "Unpacking and checking the emulator…")
	for _, p := range files {
		if err = m.command(ctx, "dpkg-deb", "-x", p, staging); err != nil {
			return err
		}
	}
	if err = repairRuntimeFirmware(staging); err != nil {
		return err
	}
	if err = validateRuntime(ctx, staging); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return m.activateRuntime(staging)
}
func (m *Manager) download(ctx context.Context) error {
	media := m.path("media/" + m.sys.media)
	if err := os.MkdirAll(filepath.Dir(media), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(media); err == nil {
		m.step(2, "working", "Verifying the saved installer…")
		if err = verifyFile(ctx, media, m.sys.mediaSHA1, m.sys.mediaSize); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	// A download that stopped part way goes on from where it stopped: an
	// install DVD is gigabytes, and the checksum judges the whole file.
	tmp := media + ".part"
	var have int64
	if info, err := os.Stat(tmp); err == nil && info.Size() <= m.sys.mediaSize {
		have = info.Size()
	}
	if have < m.sys.mediaSize {
		if err := m.fetch(ctx, tmp, have); err != nil {
			return err
		}
	}
	m.step(1, "done", "Installer downloaded.")
	m.step(2, "working", "Checking the installer against its published checksum…")
	if err := verifyFile(ctx, tmp, m.sys.mediaSHA1, m.sys.mediaSize); err != nil {
		if ctx.Err() == nil {
			os.Remove(tmp) // a wrong file is not what the next try goes on from
		}
		return err
	}
	return os.Rename(tmp, media)
}

// fetch writes the installer to tmp, after the have bytes already there.
func (m *Manager) fetch(ctx context.Context, tmp string, have int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.sys.mediaURL, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Installer download failed: %w", err)
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	switch {
	case have > 0 && resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK:
		// The whole file, also from a server that does not answer ranges.
		have, flags = 0, os.O_CREATE|os.O_WRONLY|os.O_TRUNC
	default:
		return fmt.Errorf("Installer download returned HTTP %d. Continue to retry.", resp.StatusCode)
	}
	f, err := os.OpenFile(tmp, flags, 0600)
	if err != nil {
		return err
	}
	count := have
	last := time.Now()
	buf := make([]byte, 256*1024)
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			count += int64(n)
			if count > m.sys.mediaSize {
				f.Close()
				os.Remove(tmp)
				return errors.New("Installer exceeds its expected size.")
			}
			if _, err = f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			if time.Since(last) > 500*time.Millisecond {
				m.mu.Lock()
				m.state.Bytes = count
				m.state.Total = m.sys.mediaSize
				m.mu.Unlock()
				last = time.Now()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			f.Close()
			return e
		}
	}
	return f.Close()
}
func verifyFile(ctx context.Context, path, digest string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha1.New()
	buf := make([]byte, 256*1024)
	var count int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, e := f.Read(buf)
		if n > 0 {
			count += int64(n)
			h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if count != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("Installer checksum failed. Continue to download it again.")
	}
	return nil
}
func (m *Manager) launchArgs(installer bool) []string {
	args := append([]string{"-L", m.path("firmware"), "-name", m.sys.process}, m.sys.machine(installer)...)
	args = append(args, "-drive", "file="+strings.ReplaceAll(m.path(m.sys.disk), ",", ",,")+",format=qcow2,media=disk", "-display", "none", "-vnc", "unix:"+m.path("vnc.sock"), "-qmp", "unix:"+m.path("qmp.sock")+",server=on,wait=off", "-monitor", "none", "-serial", "file:"+m.path("serial.log"), "-pidfile", m.path("qemu.pid"), "-daemonize")
	if _, err := os.Stat(m.runtimePath("runtime/usr/share/qemu/openbios-ppc")); err == nil {
		args = append(args, "-L", m.runtimePath("runtime/usr/share/qemu"))
	}
	if m.hasAudioRuntime() {
		// The dummy host backend still supplies PCM to VNC's capture channel.
		// Playback stays in the authenticated browser, never on host speakers.
		args = append(args, "-bios", m.runtimePath("audio/openbios-ppc"), "-audiodev", "none,id=mac-audio", "-global", "screamer.audiodev=mac-audio")
		for i := range args {
			if args[i] == "-vnc" {
				args[i+1] += ",audiodev=mac-audio"
				break
			}
		}
	}
	if installer {
		args = append(args, "-nic", "none")
		args = append(args, "-cdrom", m.path("media/"+m.sys.media), "-boot", "d")
	} else {
		args = append(args, "-boot", "c", "-netdev", m.sys.network+m.hostForwards(), "-device", "sungem,netdev=net0,mac="+m.sys.ethernet)
		if dev := m.qeglDevice(); dev != "" {
			args = append(args, "-device", dev)
		}
	}
	return args
}

func (m *Manager) launch(ctx context.Context, installer bool) error {
	// Preparation may take a while. Recheck immediately before touching sockets.
	if m.running() {
		return nil
	}
	for _, name := range []string{"vnc.sock", "qmp.sock"} {
		if err := os.Remove(m.path(name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	ports, err := m.pickPorts(installer)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.ports = ports
	m.mu.Unlock()
	args := m.launchArgs(installer)
	if err := m.command(ctx, m.binary("qemu-system-ppc"), args...); err != nil {
		return err
	}
	for i := 0; i < 20; i++ {
		if m.running() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("The emulator started but its control socket did not respond. Continue to retry.")
}

// SetPointerTrim keeps what a viewer found out about this Mac's system: how
// much of an absolute pointer's range it leaves unused. It is one of the
// system's known values, and it stays with the Mac, so that the next viewer
// starts with the arrow in the right place.
func (m *Manager) SetPointerTrim(trim float64) error {
	for _, t := range m.sys.trims {
		if t == trim {
			if err := os.MkdirAll(m.root, 0700); err != nil {
				return err
			}
			b, _ := json.Marshal(map[string]float64{"trim": trim})
			return os.WriteFile(m.path("pointer.json"), append(b, '\n'), 0600)
		}
	}
	return errors.New("This Mac's pointer takes no such correction.")
}

func (m *Manager) pointerTrim() float64 {
	var saved struct {
		Trim float64 `json:"trim"`
	}
	if b, err := os.ReadFile(m.path("pointer.json")); err == nil && json.Unmarshal(b, &saved) == nil {
		for _, t := range m.sys.trims {
			if t == saved.Trim {
				return t
			}
		}
	}
	return 0
}

// pickPorts chooses a loopback port of this machine for each of the Mac's
// forwards and writes them down, so that a daemon started later can still
// say where the running Mac's services are. The ports of the last run are
// taken again while they are free: an address somebody saved keeps working.
// The installer boot has no network and so no forwards.
func (m *Manager) pickPorts(installer bool) (map[string]int, error) {
	if installer || len(m.sys.forwards) == 0 {
		_ = os.Remove(m.path("ports.json"))
		return nil, nil
	}
	last := m.savedPorts()
	ports := map[string]int{}
	for _, f := range m.sys.forwards {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", last[f.name]))
		if err != nil {
			l, err = net.Listen("tcp", "127.0.0.1:0")
		}
		if err != nil {
			return nil, fmt.Errorf("No free port on this computer for the Mac's %s: %w", f.name, err)
		}
		ports[f.name] = l.Addr().(*net.TCPAddr).Port
		l.Close()
	}
	b, err := json.Marshal(ports)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(m.root, 0700); err != nil {
		return nil, err
	}
	return ports, os.WriteFile(m.path("ports.json"), append(b, '\n'), 0600)
}

func (m *Manager) savedPorts() map[string]int {
	ports := map[string]int{}
	if b, err := os.ReadFile(m.path("ports.json")); err == nil {
		_ = json.Unmarshal(b, &ports)
	}
	return ports
}

// forwardedLocked is where a running Mac's forwards are: what this daemon
// launched it with, or what the daemon that did wrote down.
func (m *Manager) forwardedLocked() map[string]int {
	if len(m.sys.forwards) == 0 {
		return nil
	}
	if m.ports == nil {
		m.ports = m.savedPorts()
	}
	out := map[string]int{}
	for _, f := range m.sys.forwards {
		if p := m.ports[f.name]; p > 0 {
			out[f.name] = p
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hostForwards is the forwards as QEMU's user network takes them: bound to
// this machine's loopback address, never to one the network can reach.
func (m *Manager) hostForwards() string {
	var b strings.Builder
	for _, f := range m.sys.forwards {
		if p := m.ports[f.name]; p > 0 {
			fmt.Fprintf(&b, ",hostfwd=tcp:127.0.0.1:%d-:%d", p, f.guest)
		}
	}
	return b.String()
}
func (m *Manager) Console(ctx context.Context) (net.Conn, error) {
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", m.path("vnc.sock"))
}
