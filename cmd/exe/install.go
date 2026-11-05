package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"exe/internal/config"
	"exe/internal/release"
)

// The installer. https://exe.v2core.com/install.sh downloads a release,
// checks it and runs `exe setup -from <folder>`: everything a person is
// asked, and everything done with the answers, is here rather than in
// shell — so it can be tested, and `exe setup` can be run again later.
//
// All the questions come first and nothing is written until the last one
// is answered. Return on every question is the careful install: this
// machine only, no sudo.

// layout is where an installed exe keeps its files.
type layout struct {
	Bin   string // ~/.local/bin/exe
	State string // ~/.exe (or $EXE_HOME)
	// Unit is the service that runs the daemon: a systemd user unit,
	// ~/.config/systemd/user/exe.service, or on a Mac a launchd agent,
	// ~/Library/LaunchAgents/com.v2core.exe.plist.
	Unit string
}

func (l layout) Config() string       { return filepath.Join(l.State, "config.json") }
func (l layout) Apps() string         { return filepath.Join(l.State, "apps") }
func (l layout) Release() string      { return filepath.Join(l.State, "release") }
func (l layout) Helper() string       { return filepath.Join(l.Release(), "exe-net-helper") }
func (l layout) AppsManifest() string { return filepath.Join(l.Release(), "apps.json") }

func installLayout() (layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return layout{}, err
	}
	l := layout{
		Bin:   filepath.Join(home, ".local", "bin", "exe"),
		State: config.Dir(),
		Unit:  filepath.Join(home, ".config", "systemd", "user", "exe.service"),
	}
	switch runtime.GOOS {
	case "darwin":
		l.Unit = filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	case "windows":
		// where a program installed for one user goes; what starts it at
		// sign-in is an entry in the registry, not a file
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		l.Bin = filepath.Join(local, "Programs", "exe", "exe.exe")
		l.Unit = ""
	}
	return l, nil
}

// binName is what the binary is called inside a release's tarball.
func binName(goos string) string {
	if goos == "windows" {
		return "exe.exe"
	}
	return "exe"
}

// What the VM step puts in place, as root. The helper's path is the
// daemon's default (firecracker.network_helper); Firecracker's version is
// the one exe is run against, pinned by the checksum of its release
// archive.
const (
	helperPath         = "/usr/local/libexec/exe-net-helper"
	firecrackerPath    = "/usr/local/bin/firecracker"
	firecrackerVersion = "v1.16.1"
)

var firecrackerArchives = map[string]struct{ arch, sum string }{
	"amd64": {"x86_64", "382a02a869e4d6d5cb14c40577f9545e8458021ea8b0b2d3fc10ec14d9c242e6"},
	"arm64": {"aarch64", "8d0e69f6d6f9a1724551f607f18504052c16c1828ee3d4d7b6e6c73380871e0e"},
}

// host is the machine as the installer sees and touches it. The real one
// is realHost; a test hands in its own.
type host struct {
	OS          string // "linux", "darwin" or "windows": which service manager, and what VMs need
	User, Group string // who exe will run as, and their primary group
	UID         string
	Home        string
	Root        bool
	TailscaleIP string
	LANIP       string
	KVM         bool // /dev/kvm is there
	Tty         bool // someone is at a keyboard to answer
	Env         func(string) string
	In          *bufio.Reader
	Out         io.Writer

	PortFree func(addr string) bool
	LookTool func(name string) string   // its path, or ""
	Run      func(argv ...string) error // a command with this terminal attached
	Quiet    func(argv ...string) error // a command whose output is not shown
	Reach    func(url string) bool
	// RestartDaemon asks the running daemon to restart itself.
	RestartDaemon func(listen, token string) error
	GroupInfo     func(name string) (exists, member bool)
	OpenKVM       func() bool
	// Helper describes a network helper on disk: its checksum, and
	// whether it is root's and carries a capability.
	Helper func(path string) (sum string, rooted bool)
	// Firecracker downloads the pinned Firecracker into dir.
	Firecracker func(dir string) (string, error)
	// Win is Windows itself, as far as the installer deals with it; nil
	// everywhere else.
	Win *winHost
}

// winHost is what the installer asks of and does through Windows.
type winHost struct {
	Elevated bool // this process has an administrator's rights
	// Session says this process is in the user's own desktop session. Over
	// ssh it is not, and what it starts there ends with the connection.
	Session  bool
	CanVM    bool                   // an x86-64 PC: the VM backend exists for it
	Drives   func() []drive         // the fixed NTFS drives
	WHPX     func() bool            // the Windows Hypervisor Platform is on
	QEMU     func() string          // qemu-system-x86_64, or ""
	Login    func() string          // the command Windows runs for exe at sign-in, "" for none
	SetLogin func(cmd string) error // "" removes it
	AddPath  func(dir string) (added bool, err error)
	DelPath  func(dir string) error
	// Start starts a program that outlives this one, with a console of
	// its own that nobody sees.
	Start func(argv []string) error
	Kill  func(bin string) int // end every other process running this binary
	// DeleteLater removes a file this process cannot remove because it is
	// the program that is running, once it has exited.
	DeleteLater func(path string) error
}

// drive is one place the VMs could be kept.
type drive struct {
	Letter  string // "C:"
	Free    int64
	Size    int64
	Kind    string // "SSD", "hard disk", "network disk (iSCSI)", …; "" when Windows does not say
	Network bool   // reached over a network or a cable that can be pulled: never the default
	Home    bool   // the user's home folder is on it
}

func realHost() *host {
	h := &host{OS: runtime.GOOS, Env: os.Getenv, In: bufio.NewReader(os.Stdin), Out: os.Stdout}
	h.Tty = term.IsTerminal(int(os.Stdin.Fd()))
	h.Home, _ = os.UserHomeDir()
	if u, err := user.Current(); err == nil {
		h.User, h.Group, h.UID, h.Root = u.Username, u.Gid, u.Uid, u.Uid == "0"
		if g, err := user.LookupGroupId(u.Gid); err == nil {
			h.Group = g.Name
		}
	}
	h.TailscaleIP, h.LANIP = config.TailscaleIP(), config.LANIP()
	_, err := os.Stat("/dev/kvm")
	h.KVM = err == nil
	h.PortFree = func(addr string) bool {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		ln.Close()
		return true
	}
	h.LookTool = func(name string) string {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		// Debian keeps the sbin folders off an ordinary user's PATH
		for _, dir := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/sbin"} {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return p
			}
		}
		return ""
	}
	h.Run = func(argv ...string) error {
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	h.Quiet = func(argv ...string) error {
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			if msg := strings.TrimSpace(string(out)); msg != "" {
				return fmt.Errorf("%w: %s", err, msg)
			}
		}
		return err
	}
	h.Reach = func(url string) bool {
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(url)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}
	h.RestartDaemon = func(listen, token string) error {
		req, err := http.NewRequest("POST", "http://"+displayAddr(listen)+"/v1/daemon/restart", nil)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("the daemon answered %s", resp.Status)
		}
		return nil
	}
	h.GroupInfo = func(name string) (bool, bool) {
		g, err := user.LookupGroup(name)
		if err != nil {
			return false, false
		}
		u, err := user.Current()
		if err != nil {
			return true, false
		}
		ids, _ := u.GroupIds()
		for _, id := range ids {
			if id == g.Gid {
				return true, true
			}
		}
		return true, false
	}
	h.OpenKVM = func() bool {
		f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			return false
		}
		f.Close()
		return true
	}
	h.Helper = helperState
	h.Win = newWinHost()
	h.Firecracker = fetchFirecracker
	return h
}

func (h *host) say(format string, a ...any) { fmt.Fprintf(h.Out, format, a...) }

// tilde shows a path the way its owner says it.
func (h *host) tilde(p string) string {
	if h.OS == "windows" {
		return p // a path is said whole there
	}
	if h.Home != "" && (p == h.Home || strings.HasPrefix(p, h.Home+"/")) {
		return "~" + strings.TrimPrefix(p, h.Home)
	}
	return p
}

// ---- asking ----------------------------------------------------------------

// readLine reads one answer; ok is false when there is no more input.
func (h *host) readLine() (string, bool) {
	s, err := h.In.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return strings.TrimSpace(s), true
}

// yesNo asks until it has a yes or a no; Return, or no more input, is def.
func (h *host) yesNo(def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		h.say("   %s: ", hint)
		s, ok := h.readLine()
		if !ok {
			h.say("\n")
			return def
		}
		switch strings.ToLower(s) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

// envBool reads a yes-or-no answer given ahead of time in the environment.
func (h *host) envBool(name string) (val, set bool, err error) {
	switch strings.ToLower(strings.TrimSpace(h.Env(name))) {
	case "":
		return false, false, nil
	case "y", "yes", "true", "1", "on":
		return true, true, nil
	case "n", "no", "false", "0", "off":
		return false, true, nil
	}
	return false, false, fmt.Errorf("%s=%q: say yes or no", name, h.Env(name))
}

const (
	listenLocal     = "local"
	listenAll       = "all"
	listenTailscale = "tailscale"
)

// The answers given ahead of time, for an install nobody is sitting at.
const (
	envListen = "EXE_INSTALL_LISTEN" // local, all or tailscale
	envToken  = "EXE_INSTALL_TOKEN"  // yes or no
	envApps   = "EXE_INSTALL_APPS"   // yes or no
	envVMs    = "EXE_INSTALL_VMS"    // yes or no
	// envTokenValue is the daemon's own variable: a token given here is the
	// one written to the configuration, instead of a generated one.
	envTokenValue = "EXE_API_TOKEN"
)

type answers struct {
	Listen string // "" when a configuration was already there and is kept
	Token  string // "" for none
	Apps   bool
	VMs    bool
	VMDir  string // where the VMs go when not beside everything else; "" for the state folder
}

// offer is what the installer has to ask about: what it found on the
// machine and in the release.
type offer struct {
	Fresh    bool     // no configuration yet
	Apps     []string // the bundles in the release, when they are a question
	AppsSize int64
	Plan     []step // what the VM step would run; empty when there is nothing to do
	// Drives are the places the VMs could go, when that is a question: a
	// new install on a Windows PC with more than one drive.
	Drives []drive
}

func (h *host) ask(l layout, o offer) (answers, error) {
	var a answers
	n := 0
	num := func() int { n++; return n }

	if o.Fresh {
		listen, err := h.askListen(num())
		if err != nil {
			return a, err
		}
		a.Listen = listen
		if a.Token, err = h.askToken(num(), listen); err != nil {
			return a, err
		}
	}

	if len(o.Apps) > 0 {
		want, set, err := h.envBool(envApps)
		if err != nil {
			return a, err
		}
		a.Apps = true
		switch {
		case set:
			a.Apps = want
			h.say("\n%d. Extra desktop apps: %s (%s)\n", num(), yesWord(want), envApps)
		case !h.Tty:
			h.say("\n%d. Extra desktop apps: yes\n", num())
		default:
			h.say("\n%d. Install the extra desktop apps?\n   %s (%s, into %s)\n",
				num(), listWords(o.Apps), megabytes(o.AppsSize), h.tilde(l.Apps()))
			a.Apps = h.yesNo(true)
		}
	}

	if len(o.Plan) > 0 {
		want, set, err := h.envBool(envVMs)
		if err != nil {
			return a, err
		}
		switch {
		case set:
			a.VMs = want
			h.say("\n%d. Set up for VMs: %s (%s)\n", num(), yesWord(want), envVMs)
		case !h.Tty:
			h.say("\n%d. Set up for VMs: no — do it later with: exe setup vms\n", num())
		default:
			needs := "This needs sudo, and would run:"
			switch {
			case h.Root, h.Win != nil && h.Win.Elevated:
				needs = "This would run:"
			case h.Win != nil:
				needs = "Windows asks for an administrator's approval. This would run:"
			}
			h.say("\n%d. Set this machine up to run VMs? %s\n", num(), needs)
			for _, s := range o.Plan {
				h.say("     %s\n", h.shown(s))
			}
			h.say("   You can do this later with: exe setup vms\n")
			a.VMs = h.yesNo(false)
		}
	}

	// Where the VMs are kept is asked once, on a new install — the answer
	// goes into the configuration — and only where VMs will run: nothing
	// was missing for them, or the step that sets them up was accepted.
	if o.Fresh && len(o.Drives) > 1 && (len(o.Plan) == 0 || a.VMs) {
		dir, err := h.askVMDrive(num(), o.Drives)
		if err != nil {
			return a, err
		}
		a.VMDir = dir
	}
	return a, nil
}

const envVMDrive = "EXE_INSTALL_VM_DRIVE" // a drive letter, with or without its colon

// vmRoom is what a drive should have free before the VMs are put on it
// unasked: the base image, one VM of the default size, and room to work.
const vmRoom = 40 << 30

// pickDrive is the drive the VMs go to when nobody chooses: the one the
// home folder is on if it has the room, otherwise the drive of the
// machine's own with the most free space. Never a network disk — a VM
// whose disk can go away is worse than a full drive.
func pickDrive(drives []drive) int {
	best := -1
	for i, d := range drives {
		if d.Home && d.Free >= vmRoom {
			return i
		}
		if !d.Network && (best < 0 || d.Free > drives[best].Free) {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	for i, d := range drives {
		if d.Home {
			return i
		}
	}
	return 0
}

func gigabytes(n int64) string { return strconv.FormatInt(n>>30, 10) + " GB" }

// vmDirOn is the folder the VMs get on a drive: none of their own on the
// home drive, where they stay beside everything else.
func vmDirOn(d drive) string {
	if d.Home {
		return ""
	}
	return d.Letter + `\exe`
}

func (h *host) askVMDrive(n int, drives []drive) (string, error) {
	def := pickDrive(drives)
	if v := strings.ToUpper(strings.TrimRight(strings.TrimSpace(h.Env(envVMDrive)), `:\/`)); v != "" {
		for _, d := range drives {
			if strings.TrimRight(d.Letter, ":") == v {
				h.say("\n%d. VMs on %s, %s free (%s)\n", n, d.Letter, gigabytes(d.Free), envVMDrive)
				return vmDirOn(d), nil
			}
		}
		return "", fmt.Errorf("%s=%q: that is not one of this PC's fixed NTFS drives", envVMDrive, h.Env(envVMDrive))
	}
	if !h.Tty {
		h.say("\n%d. VMs on %s, %s free\n", n, drives[def].Letter, gigabytes(drives[def].Free))
		return vmDirOn(drives[def]), nil
	}
	h.say("\n%d. Which drive should hold the VMs?\n   A VM takes up to its disk size (20 GB by default); the base image takes 3 GB.\n", n)
	for i, d := range drives {
		var about []string
		if d.Kind != "" {
			about = append(about, d.Kind)
		}
		if d.Home {
			about = append(about, "your home folder")
		}
		h.say("     %d) %s  %7s free of %7s   %s\n", i+1, d.Letter, gigabytes(d.Free), gigabytes(d.Size), strings.Join(about, ", "))
	}
	for {
		h.say("   Choice [%d]: ", def+1)
		s, ok := h.readLine()
		if !ok {
			h.say("\n")
			return vmDirOn(drives[def]), nil
		}
		if s == "" {
			return vmDirOn(drives[def]), nil
		}
		s = strings.ToUpper(strings.TrimRight(s, `:\/`))
		for i, d := range drives {
			if s == strconv.Itoa(i+1) || s == strings.TrimRight(d.Letter, ":") {
				return vmDirOn(d), nil
			}
		}
	}
}

func (h *host) askListen(n int) (string, error) {
	kinds := []string{listenLocal, listenAll}
	if h.TailscaleIP != "" {
		kinds = append(kinds, listenTailscale)
	}
	if v := strings.ToLower(strings.TrimSpace(h.Env(envListen))); v != "" {
		for _, k := range kinds {
			if v == k {
				h.say("\n%d. Listen: %s (%s)\n", n, h.listenWords(k), envListen)
				return k, nil
			}
		}
		if v == listenTailscale {
			return "", fmt.Errorf("%s=tailscale, but this machine has no Tailscale address", envListen)
		}
		return "", fmt.Errorf("%s=%q: say local, all or tailscale", envListen, v)
	}
	if !h.Tty {
		h.say("\n%d. Listen: %s\n", n, h.listenWords(listenLocal))
		return listenLocal, nil
	}
	h.say("\n%d. Where should exe listen?\n", n)
	h.say("     1) This machine only      127.0.0.1\n")
	h.say("     2) All interfaces         0.0.0.0   (plain HTTP: for a network you trust)\n")
	if h.TailscaleIP != "" {
		h.say("     3) Tailscale              %s\n", h.TailscaleIP)
	}
	for {
		h.say("   Choice [1]: ")
		s, ok := h.readLine()
		if !ok {
			h.say("\n")
			return listenLocal, nil
		}
		s = strings.ToLower(s)
		if s == "" {
			return listenLocal, nil
		}
		for i, k := range kinds {
			if s == strconv.Itoa(i+1) || s == k {
				return k, nil
			}
		}
	}
}

func (h *host) listenWords(kind string) string {
	switch kind {
	case listenAll:
		return "all interfaces (0.0.0.0)"
	case listenTailscale:
		return "Tailscale (" + h.TailscaleIP + ")"
	}
	return "this machine only (127.0.0.1)"
}

func (h *host) askToken(n int, listen string) (string, error) {
	if given := strings.TrimSpace(h.Env(envTokenValue)); given != "" {
		h.say("\n%d. API token: the one in %s\n", n, envTokenValue)
		return given, nil
	}
	want, set, err := h.envBool(envToken)
	if err != nil {
		return "", err
	}
	switch {
	case listen == listenAll:
		// anyone who can reach the API has the host's Terminal
		h.say("\n%d. API token: required when exe listens on every interface. One will be generated.\n", n)
		want = true
	case set:
		h.say("\n%d. API token: %s (%s)\n", n, yesWord(want), envToken)
	case !h.Tty:
		want = listen == listenTailscale
		h.say("\n%d. API token: %s\n", n, yesWord(want))
	case listen == listenTailscale:
		h.say("\n%d. Require an API token?\n   Other devices on your tailnet will be able to reach exe.\n", n)
		want = h.yesNo(true)
	default:
		h.say("\n%d. Require an API token?\n   Every API call then needs it; you paste it once into each browser.\n   Nothing outside this machine can reach exe either way.\n", n)
		want = h.yesNo(false)
	}
	if !want {
		return "", nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func yesWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// listWords joins names the way a sentence does: "A, B and C".
func listWords(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func megabytes(n int64) string {
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
}

// ---- the first configuration -------------------------------------------------

// firstConfig is the configuration a new install starts with: only what
// was answered. Everything else stays at the daemon's defaults, so a
// default that a later release changes reaches this install too.
type firstConfig struct {
	Listen        string `json:"listen"`
	ProxyListen   string `json:"proxy_listen"`
	SSHListen     string `json:"ssh_listen"`
	AdvertiseHost string `json:"advertise_host"`
	APIToken      string `json:"api_token,omitempty"`
	VMDir         string `json:"vm_dir,omitempty"`
}

// firstConfig turns the answers into addresses. The answer to "where
// should exe listen" is the answer for all three listeners — the desk and
// API, the proxy and the SSH gate. A port something else holds is passed
// over for the next free one, and moved says which.
func (h *host) firstConfig(a answers) (cfg firstConfig, moved []string) {
	bind, advertise := "127.0.0.1", ""
	switch a.Listen {
	case listenAll:
		bind, advertise = "", h.TailscaleIP
	case listenTailscale:
		bind, advertise = h.TailscaleIP, h.TailscaleIP
	}
	pick := func(what string, port int, loopbackToo bool) string {
		got := port
		for p := port; p < port+100; p++ {
			if !h.PortFree(net.JoinHostPort(bind, strconv.Itoa(p))) {
				continue
			}
			// the API on a named address keeps a 127.0.0.1 companion
			if loopbackToo && !h.PortFree(net.JoinHostPort("127.0.0.1", strconv.Itoa(p))) {
				continue
			}
			got = p
			break
		}
		if got != port {
			moved = append(moved, fmt.Sprintf("Port %d is taken on this machine, so %s is on %d.", port, what, got))
		}
		return net.JoinHostPort(bind, strconv.Itoa(got))
	}
	cfg.Listen = pick("the desk", 7777, a.Listen == listenTailscale)
	cfg.ProxyListen = pick("the proxy", 8090, false)
	cfg.SSHListen = pick("the SSH gate", 2222, false)
	cfg.AdvertiseHost = advertise
	cfg.APIToken = a.Token
	cfg.VMDir = a.VMDir
	return cfg, moved
}

func writeFirstConfig(l layout, cfg firstConfig) error {
	if err := os.MkdirAll(l.State, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	// O_EXCL: a configuration that appeared since the questions is not ours
	f, err := os.OpenFile(l.Config(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// keptConfig reads what the installer needs from a configuration it did
// not write: where the daemon answers.
func keptConfig(l layout) (listen, token string) {
	var cfg struct {
		Listen string `json:"listen"`
		Token  string `json:"api_token"`
	}
	if b, err := os.ReadFile(l.Config()); err == nil {
		json.Unmarshal(b, &cfg)
	}
	if cfg.Listen == "" {
		cfg.Listen = config.Default().Listen
	}
	return config.NormalizeListen(cfg.Listen), cfg.Token
}

// deskURL is where a browser finds the desk for a listen address.
func (h *host) deskURL(listen string) string {
	hostname, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if ip := net.ParseIP(hostname); hostname == "" || (ip != nil && ip.IsUnspecified()) {
		hostname = "127.0.0.1"
		if h.LANIP != "" {
			hostname = h.LANIP
		}
	}
	return "http://" + net.JoinHostPort(hostname, port)
}

// ---- the VM step -------------------------------------------------------------

// step is one command of the VM step, run as root.
type step struct {
	Argv  []string
	Note  string       // said after the command where the question lists it
	Prep  func() error // makes what the command needs, just before it runs
	Retry []string     // run once, and the command again, should it fail
	// AsUser runs the command as it is, where the others run as root: it
	// asks for an administrator itself (winget).
	AsUser bool
	// Fine lists exit codes that are not failures (dism: 3010, "done, and
	// Windows has to restart").
	Fine []int
}

func (h *host) asRoot(s step) []string {
	argv := s.Argv
	switch {
	case s.AsUser:
		return argv
	case h.Win != nil:
		if h.Win.Elevated {
			return argv
		}
		// Windows has no sudo to put in front: the command is started
		// again with an administrator's rights, which is what brings up
		// the approval dialog, and its exit code is carried back
		quote := func(w string) string { return "'" + strings.ReplaceAll(w, "'", "''") + "'" }
		args := make([]string, len(argv)-1)
		for i, w := range argv[1:] {
			args[i] = quote(w)
		}
		return []string{"powershell", "-NoProfile", "-Command",
			"$p = Start-Process -FilePath " + quote(argv[0]) + " -ArgumentList " + strings.Join(args, ",") + " -Verb RunAs -Wait -PassThru; exit $p.ExitCode"}
	case h.Root:
		return argv
	case h.Tty:
		return append([]string{"sudo"}, argv...)
	}
	return append([]string{"sudo", "-n"}, argv...) // nobody is there to type a password
}

// fine reports whether a command's failure is one of the exit codes its
// step counts as success.
func (s step) fine(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		for _, code := range s.Fine {
			if ee.ExitCode() == code {
				return true
			}
		}
	}
	return false
}

// shown is a step as a person reads it.
func (h *host) shown(s step) string {
	argv := s.Argv
	if !h.Root && h.Win == nil && !s.AsUser {
		argv = append([]string{"sudo"}, argv...)
	}
	words := make([]string, len(argv))
	for i, w := range argv {
		w = h.tilde(w)
		if strings.ContainsAny(w, " \t'\"") {
			w = strconv.Quote(w)
		}
		words[i] = w
	}
	line := strings.Join(words, " ")
	if s.Note != "" {
		line += "   # " + s.Note
	}
	return line
}

// planVMs is what stands between this machine and running VMs, as the
// commands that would close the gap, and what no command here can do.
// helper is the network helper this release brought; scratch is a folder
// for what a step has to download.
func (h *host) planVMs(l layout, helper, scratch string) (plan []step, notes []string) {
	if exists, member := h.GroupInfo("kvm"); exists && !member && !h.Root {
		plan = append(plan, step{Argv: []string{"usermod", "-aG", "kvm", h.User}})
		// a new group reaches a session only when it starts; an ACL on
		// the device reaches this one now, and the group takes over at
		// the next boot
		if !h.OpenKVM() {
			if h.LookTool("setfacl") != "" {
				plan = append(plan, step{Argv: []string{"setfacl", "-m", "u:" + h.User + ":rw", "/dev/kvm"},
					Note: "until the kvm group applies, at the next boot"})
			} else {
				notes = append(notes, "The kvm group applies to a new login session: restart this machine before creating a VM.")
			}
		}
	}

	if h.LookTool("firecracker") == "" {
		if _, ok := firecrackerArchives[runtime.GOARCH]; ok {
			bin := filepath.Join(scratch, "firecracker")
			plan = append(plan, step{
				Argv: []string{"install", "-m", "0755", bin, firecrackerPath},
				Note: "Firecracker " + firecrackerVersion + ", downloaded first",
				Prep: func() error {
					h.say("  downloading Firecracker %s…\n", firecrackerVersion)
					got, err := h.Firecracker(scratch)
					if err != nil {
						return err
					}
					if got != bin {
						return os.Rename(got, bin)
					}
					return nil
				},
			})
		}
	}

	want, _ := release.FileSum(helper)
	have, rooted := h.Helper(helperPath)
	needHelper := want != "" && (have != want || !rooted)

	// what the daemon and its helper run: the helper finds ip and
	// iptables only in the system folders
	tools := []struct{ tool, pkg string }{
		{"ip", "iproute2"}, {"iptables", "iptables"}, {"debugfs", "e2fsprogs"}, {"resize2fs", "e2fsprogs"},
	}
	if needHelper {
		tools = append(tools, struct{ tool, pkg string }{"setcap", "libcap2-bin"})
	}
	var pkgs []string
	seen := map[string]bool{}
	for _, t := range tools {
		if h.LookTool(t.tool) == "" && !seen[t.pkg] {
			seen[t.pkg] = true
			pkgs = append(pkgs, t.pkg)
		}
	}
	sort.Strings(pkgs)
	if len(pkgs) > 0 {
		if h.LookTool("apt-get") != "" {
			plan = append(plan, step{
				Argv:  append([]string{"apt-get", "install", "-y"}, pkgs...),
				Retry: []string{"apt-get", "update"}, // a new machine has no package lists yet
			})
		} else {
			notes = append(notes, "VMs also need these, which this installer only installs with apt: "+listWords(pkgs)+".")
		}
	}

	if needHelper {
		plan = append(plan,
			step{Argv: []string{"install", "-D", "-o", "root", "-g", h.Group, "-m", "0750", l.Helper(), helperPath}},
			step{Argv: []string{"setcap", "cap_net_admin=ep", helperPath}},
		)
	}
	return plan, notes
}

// runPlan runs the VM step, each command said as it runs.
func (h *host) runPlan(plan []step) error {
	for _, s := range plan {
		if s.Prep != nil {
			if err := s.Prep(); err != nil {
				return err
			}
		}
		plain := step{Argv: s.Argv, AsUser: s.AsUser}
		h.say("  $ %s\n", h.shown(plain))
		err := h.Run(h.asRoot(s)...)
		if err != nil && s.Retry != nil {
			again := step{Argv: s.Retry, AsUser: s.AsUser}
			h.say("  $ %s\n", h.shown(again))
			if rerr := h.Run(h.asRoot(again)...); rerr == nil {
				h.say("  $ %s\n", h.shown(plain))
				err = h.Run(h.asRoot(s)...)
			}
		}
		if err != nil && !s.fine(err) {
			return fmt.Errorf("%s: %w", h.shown(plain), err)
		}
	}
	return nil
}

// fetchFirecracker downloads the pinned Firecracker release, checks the
// archive against the checksum this binary carries and unpacks the one
// program exe runs.
func fetchFirecracker(dir string) (string, error) {
	fc, ok := firecrackerArchives[runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("no Firecracker for linux/%s", runtime.GOARCH)
	}
	name := "firecracker-" + firecrackerVersion + "-" + fc.arch
	from := "https://github.com/firecracker-microvm/firecracker/releases/download/" + firecrackerVersion + "/" + name + ".tgz"
	tgz := filepath.Join(dir, name+".tgz")
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(from)
	if err != nil {
		return "", fmt.Errorf("download Firecracker: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", from, resp.Status)
	}
	f, err := os.Create(tgz)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 256<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download Firecracker: %w", err)
	}
	if sum, err := release.FileSum(tgz); err != nil || sum != fc.sum {
		return "", fmt.Errorf("the Firecracker %s archive does not match its checksum (got %s, want %s)", firecrackerVersion, sum, fc.sum)
	}
	bin := filepath.Join(dir, "firecracker")
	if err := untarMember(tgz, "release-"+firecrackerVersion+"-"+fc.arch+"/"+name, bin); err != nil {
		return "", err
	}
	return bin, nil
}

// untarMember writes one file of a .tar.gz to a runnable file at to.
func untarMember(tgz, member, to string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s holds no %s", filepath.Base(tgz), member)
		}
		if err != nil {
			return err
		}
		if hdr.Name != member || hdr.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, 256<<20)); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}

// ---- the service -------------------------------------------------------------

// unitMark heads a unit this installer wrote. One without it is somebody
// else's — a checkout's own deployment, say — and is never touched.
const (
	serviceMark = "Written by exe setup."
	unitMark    = "# " + serviceMark
)

func unitText(l layout, home string) string {
	quote := func(s string) string {
		if strings.ContainsAny(s, " \t") {
			return strconv.Quote(s)
		}
		return s
	}
	var b strings.Builder
	b.WriteString(unitMark + " `exe uninstall` removes it; an update leaves it as it is.\n")
	b.WriteString(`#
# KillMode=process is deliberate: the daemon starts tmux servers for the
# Terminal, Claude Code and Codex windows and they live in this unit's
# cgroup. Signalling only the main process keeps them up across a restart.
# The VMs run on across a restart as well, and the next daemon adopts them;
# the daemon stops them itself only when the host goes down.
[Unit]
Description=exe daemon

[Service]
`)
	fmt.Fprintf(&b, "ExecStart=%s serve\n", quote(l.Bin))
	if l.State != filepath.Join(home, ".exe") {
		fmt.Fprintf(&b, "Environment=%s\n", quote("EXE_HOME="+l.State))
	}
	// the sbin folders hold what the VM backend runs (debugfs, resize2fs),
	// and Debian leaves them off a user's PATH
	fmt.Fprintf(&b, "Environment=%s\n", quote("PATH="+filepath.Dir(l.Bin)+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"))
	b.WriteString(`Restart=always
RestartSec=1
TimeoutStopSec=75
KillMode=process
UMask=0027

[Install]
WantedBy=default.target
`)
	return b.String()
}

// unitOurs reports whether a unit file stands at l.Unit, and whether this
// installer wrote it.
func unitOurs(l layout) (present, ours bool) {
	b, err := os.ReadFile(l.Unit)
	if err != nil {
		return false, false
	}
	return true, strings.Contains(string(b), serviceMark)
}

// startService puts the service in place and (re)starts the daemon under
// the user's own service manager: systemd on Linux, launchd on a Mac.
// started is false, with the reason in notes, where that cannot be done —
// the binary is installed all the same.
func (h *host) startService(l layout) (started bool, notes []string) {
	if present, ours := unitOurs(l); present && !ours {
		return false, []string{fmt.Sprintf("%s was not written by this installer, so it is left as it is — and so is the daemon it runs.", h.tilde(l.Unit))}
	}
	switch h.OS {
	case "darwin":
		return h.startLaunchd(l)
	case "windows":
		return h.startWindows(l)
	}
	return h.startSystemd(l)
}

// serviceState reports whether something already starts an exe for this
// user, and whether this installer put it there.
func (h *host) serviceState(l layout) (present, ours bool) {
	if h.OS == "windows" {
		have := h.Win.Login()
		return have != "", strings.Contains(strings.ToLower(have), strings.ToLower(l.Bin))
	}
	return unitOurs(l)
}

// serviceWords are what a person types to restart the service and to read
// its log, for the messages that send them there.
func (h *host) serviceWords() (restart, log string) {
	switch h.OS {
	case "darwin":
		return "launchctl kickstart -k " + h.launchdTarget(), "~/.exe/daemon.log"
	case "windows":
		return "exe daemon restart", `%USERPROFILE%\.exe\daemon.log`
	}
	return "systemctl --user restart exe", "journalctl --user -u exe"
}

// stopService stops the daemon and takes the installer's service away.
func (h *host) stopService(l layout) error {
	switch h.OS {
	case "darwin":
		h.Quiet("launchctl", "bootout", h.launchdTarget())
		return os.Remove(l.Unit)
	case "windows":
		if err := h.Win.SetLogin(""); err != nil {
			return err
		}
		h.Win.Kill(l.Bin)
		// a daemon that has not restarted since an update still runs
		// from the binary that update moved aside
		for _, old := range movedAside(l.Bin) {
			h.Win.Kill(old)
		}
		return nil
	}
	h.Quiet("systemctl", "--user", "disable", "--now", "exe")
	if err := os.Remove(l.Unit); err != nil {
		return err
	}
	h.Quiet("systemctl", "--user", "daemon-reload")
	return nil
}

// ---- launchd (macOS) ---------------------------------------------------------

// launchdLabel names the agent. It is also the identifier the release's
// code signature carries, so macOS shows one name for both.
const launchdLabel = "com.v2core.exe"

// launchdTarget is the agent as launchctl names it: in the domain of the
// user's login session, which is where the menu bar is.
func (h *host) launchdTarget() string { return "gui/" + h.UID + "/" + launchdLabel }

// launchdPlist is the agent that runs the daemon for a logged-in user.
//
// KeepAlive only after a failure: a daemon that crashed comes back, and
// one that was told to quit from its menu-bar item stays quit until the
// next login. A restart is therefore not "exit and be started again" as
// under systemd but `launchctl kickstart -k`, which the daemon runs on
// itself when it finds EXE_LAUNCHD (server.RestartDaemon).
//
// AssociatedBundleIdentifiers tells macOS which program an agent that no
// app installed belongs to — the identifier in the binary's own embedded
// Info.plist — so Login Items and the Local Network alert name it.
//
// AbandonProcessGroup is this file's KillMode=process: the tmux servers
// behind the Terminal, Claude Code and Codex windows outlive a restart.
// ExitTimeOut gives the daemon the time it gives itself to shut down.
// ProcessType Interactive keeps macOS from throttling a process that runs
// VMs as if it were background housekeeping.
func launchdPlist(l layout, h *host) string {
	x := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	env := [][2]string{
		// Homebrew's folders hold what the desk's windows run (tmux, the
		// agents' CLIs); launchd starts an agent with a bare PATH
		{"PATH", filepath.Dir(l.Bin) + ":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"},
		{"EXE_LAUNCHD", h.launchdTarget()},
	}
	if l.State != filepath.Join(h.Home, ".exe") {
		env = append(env, [2]string{"EXE_HOME", l.State})
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- ` + serviceMark + " `exe uninstall` removes it; an update leaves it as it is. -->" + `
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + x(l.Bin) + `</string>
		<string>serve</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
`)
	for _, kv := range env {
		fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", kv[0], x(kv[1]))
	}
	b.WriteString(`	</dict>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>` + launchdLabel + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ExitTimeOut</key>
	<integer>75</integer>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + x(filepath.Join(l.State, "launchd.log")) + `</string>
	<key>StandardErrorPath</key>
	<string>` + x(filepath.Join(l.State, "launchd.log")) + `</string>
</dict>
</plist>
`)
	return b.String()
}

// startLaunchd loads the agent into the user's login session, or restarts
// it there. With nobody logged in at the screen there is no such session:
// the agent is left where launchd finds it at the next login.
func (h *host) startLaunchd(l layout) (started bool, notes []string) {
	want := []byte(launchdPlist(l, h))
	have, _ := os.ReadFile(l.Unit)
	if err := os.MkdirAll(filepath.Dir(l.Unit), 0o755); err != nil {
		return false, []string{err.Error()}
	}
	if err := os.MkdirAll(l.State, 0o755); err != nil { // the agent's log goes there
		return false, []string{err.Error()}
	}
	domain := "gui/" + h.UID
	if h.Quiet("launchctl", "print", domain) != nil {
		if err := os.WriteFile(l.Unit, want, 0o644); err != nil {
			return false, []string{err.Error()}
		}
		return false, []string{"Nobody is logged in at this Mac's screen, so exe starts at the next login. To run it now: exe serve"}
	}
	target := h.launchdTarget()
	loaded := h.Quiet("launchctl", "print", target) == nil
	if loaded && string(have) == string(want) {
		// the agent as it is, with the binary that is in place now
		if err := h.Quiet("launchctl", "kickstart", "-k", target); err != nil {
			return false, []string{fmt.Sprintf("launchctl kickstart -k %s: %v", target, err)}
		}
		return true, nil
	}
	if loaded {
		// launchd keeps the agent it read; a changed one is loaded anew,
		// once the old one has let go
		h.Quiet("launchctl", "bootout", target)
		for i := 0; i < 100 && h.Quiet("launchctl", "print", target) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := os.WriteFile(l.Unit, want, 0o644); err != nil {
		return false, []string{err.Error()}
	}
	if err := h.Quiet("launchctl", "bootstrap", domain, l.Unit); err != nil {
		return false, []string{fmt.Sprintf("launchctl bootstrap %s %s: %v", domain, h.tilde(l.Unit), err)}
	}
	return true, nil
}

// ---- Windows -----------------------------------------------------------------

// winLogin is the command Windows runs when the user signs in, and the
// one a task runs to start exe from an ssh session: `exe daemon start`,
// under conhost --headless so that this short-lived console program opens
// no window on its way.
//
// The daemon itself is started by that command, in Go, with a console of
// its own that is hidden (winHost.Start). It has to have a console:
// Windows tells a program that its user is signing out, or the PC shutting
// down, through its console, and that is the daemon's only chance to write
// down which VMs were running so that they come back. A program with no
// console at all is simply ended.
//
// conhost --headless cannot hold the daemon itself, though it looks made
// for it: started from another program it is handed that program's idea
// of standard input, reads the end of it as its terminal going away, and
// closes — with the daemon inside (seen on a real PC: started, and gone
// within half a second, without a line in the log).
func (h *host) winLogin(l layout) []string {
	root := h.Env("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return []string{root + `\System32\conhost.exe`, "--headless", l.Bin, "daemon", "start"}
}

// winCommandLine joins a command the way Windows reads one back.
func winCommandLine(argv []string) string {
	words := make([]string, len(argv))
	for i, w := range argv {
		if w == "" || strings.ContainsAny(w, " \t") {
			w = `"` + w + `"`
		}
		words[i] = w
	}
	return strings.Join(words, " ")
}

// startWindows makes exe start when its user signs in (the Run key of
// their own registry: nothing an administrator has to grant) and starts
// or restarts it now.
func (h *host) startWindows(l layout) (started bool, notes []string) {
	w := h.Win
	if present, ours := h.serviceState(l); present && !ours {
		return false, []string{fmt.Sprintf("Windows already starts an exe when you sign in (%s). That entry is left as it is, and so is the daemon it runs.", w.Login())}
	}
	argv := h.winLogin(l)
	if err := w.SetLogin(winCommandLine(argv)); err != nil {
		return false, []string{fmt.Sprintf("exe could not be set to start when you sign in: %v", err)}
	}
	// a daemon that is running restarts itself into the binary that is in
	// place now, taking its VMs down and bringing them back as it does
	listen, token := keptConfig(l)
	if h.Reach("http://" + displayAddr(listen) + "/") {
		if err := h.RestartDaemon(listen, token); err == nil {
			time.Sleep(time.Second) // let the old one stop answering
			return true, nil
		}
		w.Kill(l.Bin)
	}
	if err := h.launchWindows(l); err != nil {
		return false, []string{fmt.Sprintf("exe starts the next time you sign in to Windows (%v). To start it before that, run at the PC: exe daemon start", err)}
	}
	return true, nil
}

// launchWindows starts the daemon where it will stay: in the desktop
// session of its user. From that session it is simply started. From
// anywhere else (ssh) what is started ends with the connection, so a task
// that runs once, in the session of the signed-in user, starts it there —
// which fails, and says so, when nobody is signed in.
//
// The task does not run the daemon itself. A task's process is the
// task: Windows would end the daemon when the task's time is up (three
// days, unless told otherwise). The task runs `exe daemon start`
// (winLogin), which starts the daemon free of the task and is done.
func (h *host) launchWindows(l layout) error {
	if h.Win.Session {
		return h.Win.Start([]string{l.Bin, "serve"})
	}
	const task = "exe daemon start"
	if err := h.Quiet("schtasks", "/Create", "/TN", task, "/TR", winCommandLine(h.winLogin(l)), "/SC", "ONCE", "/ST", "23:59", "/IT", "/F"); err != nil {
		return errors.New("nobody is signed in at this PC")
	}
	err := h.Quiet("schtasks", "/Run", "/TN", task)
	h.Quiet("schtasks", "/Delete", "/TN", task, "/F")
	if err != nil {
		return errors.New("nobody is signed in at this PC")
	}
	return nil
}

// planVMsWindows is what a Windows PC lacks to run VMs: the hypervisor
// platform, which is two optional features of Windows and a restart, and
// QEMU. Unlike on Linux nothing of exe's own needs installing — the guest
// network is inside the daemon.
func (h *host) planVMsWindows() (plan []step, notes []string) {
	if !h.Win.WHPX() {
		plan = append(plan, step{
			Argv: []string{"dism", "/online", "/enable-feature", "/featurename:HypervisorPlatform", "/featurename:VirtualMachinePlatform", "/all", "/norestart"},
			Fine: []int{3010},
		})
		notes = append(notes, "Restart Windows before creating a VM: the hypervisor platform starts with it.")
	}
	if h.Win.QEMU() == "" {
		if h.LookTool("winget") != "" {
			plan = append(plan, step{
				Argv:   []string{"winget", "install", "--id", "SoftwareFreedomConservancy.QEMU", "-e", "--accept-package-agreements", "--accept-source-agreements"},
				Note:   "QEMU",
				AsUser: true,
			})
		} else {
			notes = append(notes, "VMs also need QEMU, which this installer installs with winget: get it from https://www.qemu.org/download/#windows")
		}
	}
	return plan, notes
}

// ---- systemd (Linux) ---------------------------------------------------------

func (h *host) startSystemd(l layout) (started bool, notes []string) {
	if h.Quiet("systemctl", "--user", "show-environment") != nil {
		return false, []string{"There is no systemd user session here, so nothing starts exe for you. Start it with: exe serve"}
	}
	if err := os.MkdirAll(filepath.Dir(l.Unit), 0o755); err != nil {
		return false, []string{err.Error()}
	}
	if err := os.WriteFile(l.Unit, []byte(unitText(l, h.Home)), 0o644); err != nil {
		return false, []string{err.Error()}
	}
	for _, argv := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "exe"},
		{"systemctl", "--user", "restart", "exe"},
	} {
		if err := h.Quiet(argv...); err != nil {
			return false, []string{fmt.Sprintf("%s: %v", strings.Join(argv, " "), err)}
		}
	}
	// lingering keeps a user's services running with nobody logged in,
	// and starts them at boot
	if h.Quiet("loginctl", "enable-linger") != nil && !h.Root {
		notes = append(notes, fmt.Sprintf("exe stops when you log out, and does not start at boot, until you run: sudo loginctl enable-linger %s", h.User))
	}
	return true, notes
}

// ---- putting files in place --------------------------------------------------

// prepareBinary copies src beside dst, ready to take its place. It is the
// one large write of an install, so it comes first: a full disk, or a
// folder that cannot be written, stops things before anything has changed.
//
// commit is the rename that puts the copy in place, and it comes last —
// it is what makes an install or an update done. Until then the old
// binary is the one that runs, so running it again does every step again;
// were the new binary in place with a later step unfinished, the next
// `exe update` would be the new binary finding itself up to date. A
// running exe keeps the file it started from. discard removes the copy
// when commit was never reached.
func prepareBinary(src, dst string) (commit func() error, discard func(), err error) {
	if a, err := os.Stat(src); err == nil {
		if b, err := os.Stat(dst); err == nil && os.SameFile(a, b) {
			return func() error { return nil }, func() {}, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, nil, err
	}
	tmp := dst + ".new"
	if err := release.CopyFile(src, tmp, 0o755); err != nil {
		os.Remove(tmp)
		return nil, nil, err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return nil, nil, err
	}
	done := false
	commit = func() error {
		if err := replaceFile(tmp, dst); err != nil {
			return err
		}
		done = true
		return nil
	}
	discard = func() {
		if !done {
			os.Remove(tmp)
		}
	}
	return commit, discard, nil
}

// movedAside lists the binaries that updates moved out of dst's way
// (replaceFile) and that are still there.
func movedAside(dst string) []string {
	old, _ := filepath.Glob(dst + ".old*")
	return old
}

// renameFile is os.Rename; a test stands in for Windows with it.
var renameFile = os.Rename

// replaceFile puts tmp in dst's place. One rename does it wherever a file
// that is in use may be replaced. Windows refuses that for a program that
// is running — and the program that is running is the one being updated —
// but lets it be renamed, so there the old binary is moved aside first
// and stays, in use, until the daemon restarts; what earlier updates left
// that way is cleared out when it can be.
func replaceFile(tmp, dst string) error {
	for _, f := range movedAside(dst) {
		os.Remove(f) // one still in use stays, and is tried again next time
	}
	err := renameFile(tmp, dst)
	if err == nil {
		return nil
	}
	if fi, serr := os.Stat(dst); serr != nil || !fi.Mode().IsRegular() {
		return err // not a program in the way: whatever it was stands
	}
	aside := dst + ".old"
	for i := 2; ; i++ {
		if _, serr := os.Stat(aside); os.IsNotExist(serr) {
			break
		}
		aside = dst + ".old" + strconv.Itoa(i)
	}
	if rerr := renameFile(dst, aside); rerr != nil {
		return err
	}
	if rerr := renameFile(tmp, dst); rerr != nil {
		renameFile(aside, dst) // put back what was there
		return rerr
	}
	return nil
}

// installBinary puts a copy of src at dst, replacing what is there in one
// rename.
func installBinary(src, dst string) error {
	commit, discard, err := prepareBinary(src, dst)
	if err != nil {
		return err
	}
	defer discard()
	return commit()
}

// stageRelease keeps what a release brought beside the binary: the network
// helper, which only root can put where the daemon looks for it.
func stageRelease(l layout, from string) error {
	src := filepath.Join(from, "exe-net-helper")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("the release holds no exe-net-helper: %w", err)
	}
	return installBinary(src, l.Helper())
}

// unpackApps unpacks the release's apps into scratch and names the
// bundles. A release without them is an empty answer.
func unpackApps(from, scratch string) (dir string, names []string, size int64, err error) {
	tgz := filepath.Join(from, release.AppsAsset)
	if _, serr := os.Stat(tgz); serr != nil {
		return "", nil, 0, nil
	}
	dir = filepath.Join(scratch, "apps")
	if err := release.Extract(tgz, dir); err != nil {
		return "", nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, 0, err
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "app.json")); err == nil && e.IsDir() {
			names = append(names, e.Name())
		}
	}
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return dir, names, size, nil
}

// placeApps brings the unpacked bundles into the apps folder and says
// which it left alone; kept is how many. It may be run again after a
// failure (release.InstallApps).
func (h *host) placeApps(l layout, dir string) (kept int, err error) {
	changes, err := release.InstallApps(dir, l.Apps(), filepath.Join(l.Release(), "staging"), l.AppsManifest())
	for _, c := range changes {
		if c.Action == "kept" {
			kept++
			h.say("  %s is left as it is: %s.\n", c.Name, c.Why)
		}
	}
	return kept, err
}

// ---- exe setup ---------------------------------------------------------------

func cmdSetup(args []string) error {
	if release.Version == "" {
		return errors.New("exe setup belongs to a released exe; " + sourceBuildHint)
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	from := fs.String("from", "", "the folder an unpacked release is in (the installer passes it)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	l, err := installLayout()
	if err != nil {
		return err
	}
	h := realHost()
	switch fs.Arg(0) {
	case "":
		return runSetup(l, h, *from)
	case "vms":
		return runSetupVMs(l, h)
	}
	return fmt.Errorf("exe setup %s: there is `exe setup` and `exe setup vms`", fs.Arg(0))
}

// platformWords names what this binary was built for, as a person says it.
func platformWords(goos, goarch string) string {
	if goos == "windows" {
		if goarch == "amd64" {
			return "Windows x86-64"
		}
		return "Windows ARM64"
	}
	if goos == "darwin" {
		if goarch == "amd64" {
			return "macOS (Intel)"
		}
		return "macOS (Apple silicon)"
	}
	if goarch == "amd64" {
		return "Linux x86-64"
	}
	return "Linux ARM64"
}

// runSetup is the installer. from is the folder a release was unpacked
// into; empty when an installed exe runs its setup again.
func runSetup(l layout, h *host, from string) error {
	scratch, err := os.MkdirTemp("", "exe-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	h.say("\nexe %s for %s\n", release.Version, platformWords(runtime.GOOS, runtime.GOARCH))
	h.say("  binary   %s\n  data     %s\n", h.tilde(l.Bin), h.tilde(l.State))

	_, cfgErr := os.Stat(l.Config())
	o := offer{Fresh: os.IsNotExist(cfgErr)}
	if !o.Fresh {
		h.say("\nThe configuration in %s is kept as it is.\n", h.tilde(l.Config()))
	}

	var appsDir string
	// apps that came with an install — or with one that did not finish —
	// follow it without being asked about again
	tracked := release.AppsTracked(l.AppsManifest())
	if from != "" {
		var names []string
		var size int64
		if appsDir, names, size, err = unpackApps(from, scratch); err != nil {
			return err
		}
		if !tracked { // never installed here: a question
			o.Apps, o.AppsSize = names, size
		}
	}

	helper := l.Helper()
	if from != "" {
		helper = filepath.Join(from, "exe-net-helper")
	}
	var vmNotes []string
	switch {
	case h.OS == "windows":
		if h.Win.CanVM {
			o.Plan, vmNotes = h.planVMsWindows()
			o.Drives = h.Win.Drives()
		} else {
			h.say("\nexe runs VMs on x86-64 Windows only: on this PC it will run the desktop without them.\n")
		}
	case h.OS != "linux":
		// a Mac runs VMs through Virtualization.framework, which asks for
		// nothing but the entitlement the release is signed with
	case !h.KVM:
		h.say("\nThere is no /dev/kvm on this machine: exe will run the desktop without VMs.\n")
	default:
		o.Plan, vmNotes = h.planVMs(l, helper, scratch)
	}

	a, err := h.ask(l, o)
	if err != nil {
		return err
	}
	h.say("\n")

	// ---- from here on, things are written ----
	// The binary goes in place last (prepareBinary): an install that
	// stops before that is finished by running the installer again.
	commit := func() error { return nil }
	if from != "" {
		var discard func()
		if commit, discard, err = prepareBinary(filepath.Join(from, binName(h.OS)), l.Bin); err != nil {
			return err
		}
		defer discard()
		if h.OS == "linux" { // the network helper is a Linux thing
			if err := stageRelease(l, from); err != nil {
				return err
			}
		}
	}
	var notes []string
	listen, _ := keptConfig(l)
	if o.Fresh {
		cfg, moved := h.firstConfig(a)
		if err := writeFirstConfig(l, cfg); err != nil {
			return err
		}
		listen = cfg.Listen
		notes = append(notes, moved...)
		// a new install greets its owner: the desktop opens its Welcome
		// window until its check box says otherwise
		config.SetWelcome(l.State, true)
	}
	spoke := false // something was said since the questions
	if appsDir != "" && (a.Apps || tracked) {
		kept, err := h.placeApps(l, appsDir)
		if err != nil {
			return err
		}
		spoke = kept > 0
	}
	if a.VMs {
		spoke = true
		if err := h.runPlan(o.Plan); err != nil {
			// the rest of the install does not need VMs
			notes = append(notes, fmt.Sprintf("The VM step stopped (%v). Run it again with: exe setup vms", err))
		} else {
			notes = append(notes, vmNotes...)
		}
	}

	if err := commit(); err != nil {
		return err
	}
	started, svcNotes := h.startService(l)
	notes = append(notes, svcNotes...)
	url := h.deskURL(listen)
	if spoke {
		h.say("\n")
	}
	if started {
		up := false
		for i := 0; i < 60 && !up; i++ {
			if up = h.Reach("http://" + displayAddr(listen) + "/"); !up {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if !up {
			_, log := h.serviceWords()
			return fmt.Errorf("exe %s is installed and was started, but it is not answering at %s — its log: %s", release.Version, url, log)
		}
		h.say("exe %s is running.\n\n", release.Version)
	} else {
		h.say("exe %s is installed.\n\n", release.Version)
	}
	h.say("  Desk     %s\n", url)
	if a.Token != "" {
		h.say("  Token    %s\n           the desk asks for it once in each browser; `exe token` shows it again\n", a.Token)
	}
	h.say("  Update   exe update\n  Remove   exe uninstall\n")

	if h.OS == "darwin" {
		notes = append(notes, "The first time exe starts a VM, macOS asks to let it reach devices on your local network: its VMs are there. Choose Allow.")
	}
	switch {
	case h.OS == "windows":
		// Windows keeps a user's PATH in the registry, where an installer
		// is expected to put itself; a terminal reads it when it opens
		if added, err := h.Win.AddPath(filepath.Dir(l.Bin)); err != nil {
			notes = append(notes, fmt.Sprintf("%s could not be added to your PATH (%v): run exe by its whole name, %s", filepath.Dir(l.Bin), err, l.Bin))
		} else if added {
			notes = append(notes, "exe is on your PATH in every terminal you open from now on.")
		}
	case !onPath(h.Env("PATH"), filepath.Dir(l.Bin)):
		notes = append(notes, fmt.Sprintf("%s is not on your PATH. For this shell:\n  export PATH=\"%s:$PATH\"",
			h.tilde(filepath.Dir(l.Bin)), strings.Replace(filepath.Dir(l.Bin), h.Home, "$HOME", 1)))
	}
	for _, n := range notes {
		h.say("\n%s\n", n)
	}
	return nil
}

func onPath(path, dir string) bool {
	for _, p := range filepath.SplitList(path) {
		if filepath.Clean(p) == dir {
			return true
		}
	}
	return false
}

// runSetupVMs is the VM step by itself, for a machine that was installed
// without it.
func runSetupVMs(l layout, h *host) error {
	if h.OS == "darwin" {
		h.say("A Mac needs no setup to run VMs.\n")
		return nil
	}
	if h.OS == "windows" {
		return h.runSetupVMsWindows(l)
	}
	if !h.KVM {
		return errors.New("there is no /dev/kvm on this machine, so it cannot run VMs; exe runs the desktop without them")
	}
	if _, err := os.Stat(l.Helper()); err != nil {
		return fmt.Errorf("%s is missing — install exe with https://exe.v2core.com/install.sh first", h.tilde(l.Helper()))
	}
	scratch, err := os.MkdirTemp("", "exe-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	plan, notes := h.planVMs(l, l.Helper(), scratch)
	if len(plan) == 0 {
		h.say("This machine is already set up to run VMs.\n")
		for _, n := range notes {
			h.say("\n%s\n", n)
		}
		return nil
	}
	a, err := h.ask(l, offer{Plan: plan})
	if err != nil {
		return err
	}
	if !a.VMs {
		return nil
	}
	h.say("\n")
	if err := h.runPlan(plan); err != nil {
		return err
	}
	// the daemon looks for its hypervisor when it starts
	if _, ours := unitOurs(l); ours && h.Quiet("systemctl", "--user", "restart", "exe") == nil {
		h.say("\nexe was restarted and can run VMs now.\n")
	} else {
		h.say("\nRestart exe for it to run VMs.\n")
	}
	for _, n := range notes {
		h.say("\n%s\n", n)
	}
	return nil
}

// runSetupVMsWindows is `exe setup vms` on a PC.
func (h *host) runSetupVMsWindows(l layout) error {
	if !h.Win.CanVM {
		return errors.New("exe runs VMs on x86-64 Windows only; on this PC it runs the desktop without them")
	}
	plan, notes := h.planVMsWindows()
	if len(plan) == 0 {
		h.say("This PC is already set up to run VMs.\n")
		for _, n := range notes {
			h.say("\n%s\n", n)
		}
		return nil
	}
	a, err := h.ask(l, offer{Plan: plan})
	if err != nil || !a.VMs {
		return err
	}
	h.say("\n")
	if err := h.runPlan(plan); err != nil {
		return err
	}
	// the daemon looks for its hypervisor when it starts
	listen, token := keptConfig(l)
	if h.Reach("http://"+displayAddr(listen)+"/") && h.RestartDaemon(listen, token) == nil {
		h.say("\nexe was restarted.\n")
	}
	for _, n := range notes {
		h.say("\n%s\n", n)
	}
	return nil
}

// ---- exe uninstall -----------------------------------------------------------

func cmdUninstall(args []string) error {
	if release.Version == "" {
		return errors.New("exe uninstall removes an installed release; this exe was built from source and is removed by deleting its checkout")
	}
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	yes := fs.Bool("y", false, "do not ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	l, err := installLayout()
	if err != nil {
		return err
	}
	if self, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(self); err == nil {
			l.Bin = real
		}
	}
	return runUninstall(l, realHost(), *yes)
}

// runUninstall takes away what the installer put in the user's own
// folders. The data in the state folder stays: VMs, Workspace, app data
// and the configuration are not the installer's to delete.
func runUninstall(l layout, h *host, yes bool) error {
	if !yes {
		if !h.Tty {
			return errors.New("exe uninstall asks before it removes anything; say `exe uninstall -y` where nobody can answer")
		}
		h.say("Remove exe from this machine? Its VMs stop. Your data in %s is kept.\n", h.tilde(l.State))
		if !h.yesNo(false) {
			return nil
		}
	}
	switch present, ours := h.serviceState(l); {
	case ours:
		if err := h.stopService(l); err != nil {
			return err
		}
		if h.OS == "windows" {
			h.say("Stopped exe; it no longer starts when you sign in.\n")
		} else {
			h.say("Stopped exe and removed %s\n", h.tilde(l.Unit))
		}
	case present && h.OS == "windows":
		h.say("What starts exe when you sign in was not set by the installer and is left as it is.\n")
	case present:
		h.say("%s was not written by the installer and is left as it is.\n", h.tilde(l.Unit))
	}
	if h.OS == "windows" {
		// the program that is running — this one — cannot delete its own
		// file there: Windows removes it a moment after it has exited
		h.Win.DelPath(filepath.Dir(l.Bin))
		// what updates moved aside goes too. A daemon that was running
		// from one has just been ended, and Windows takes a moment to
		// let go of its file.
		for _, old := range movedAside(l.Bin) {
			for try := 0; os.Remove(old) != nil && try < 20; try++ {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if err := os.Remove(l.Bin); err != nil && !os.IsNotExist(err) {
			if derr := h.Win.DeleteLater(l.Bin); derr != nil {
				return err
			}
		}
	} else if err := os.Remove(l.Bin); err != nil && !os.IsNotExist(err) {
		return err
	}
	h.say("Removed %s\n", h.tilde(l.Bin))
	// the apps the installer brought go with it; one that was edited since
	// is its owner's
	if release.AppsTracked(l.AppsManifest()) {
		removed, kept, err := release.RemoveApps(l.Apps(), l.AppsManifest())
		if err != nil {
			return err
		}
		if len(removed) > 0 {
			h.say("Removed the apps it installed: %s\n", listWords(removed))
		}
		if len(kept) > 0 {
			h.say("Kept, because you edited %s: %s\n", map[bool]string{true: "it", false: "them"}[len(kept) == 1], listWords(kept))
		}
	}
	if err := os.RemoveAll(l.Release()); err != nil {
		return err
	}
	if sum, _ := h.Helper(helperPath); h.OS == "linux" && sum != "" {
		h.say("\nThe network helper is root's. Remove it with:\n  sudo rm %s\n", helperPath)
	}
	h.say("\nYour data is still in %s.\n", h.tilde(l.State))
	if dir := keptVMDir(l); dir != "" {
		h.say("Your VMs are in %s.\n", dir)
	}
	return nil
}

// keptVMDir is the configuration's vm_dir: where the VMs are when they
// are not in the state folder.
func keptVMDir(l layout) string {
	var cfg struct {
		VMDir string `json:"vm_dir"`
	}
	if b, err := os.ReadFile(l.Config()); err == nil {
		json.Unmarshal(b, &cfg)
	}
	return cfg.VMDir
}

// ---- exe daemon start, exe daemon restart -------------------------------------

// cmdDaemon is the daemon's own on and again (`exe start` and `exe stop`
// are a VM's).
func cmdDaemon(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "start":
		return cmdStart()
	case len(args) == 1 && args[0] == "restart":
		return cmdRestart()
	}
	return errors.New("exe daemon start, or exe daemon restart")
}

// cmdStart starts the installed daemon the way its service does: for when
// it was quit, or installed with nobody signed in.
func cmdStart() error {
	if release.Version == "" {
		return errors.New("exe daemon start is for an installed release; from a checkout, run `exe serve`")
	}
	l, err := installLayout()
	if err != nil {
		return err
	}
	h := realHost()
	if listen, _ := keptConfig(l); h.Reach("http://" + displayAddr(listen) + "/") {
		h.say("exe is already running.\n")
		return nil
	}
	switch h.OS {
	case "windows":
		if self, err := os.Executable(); err == nil {
			l.Bin = self
		}
		if err := h.launchWindows(l); err != nil {
			return fmt.Errorf("%w: exe starts when you sign in", err)
		}
	case "darwin":
		if err := h.Quiet("launchctl", "kickstart", h.launchdTarget()); err != nil {
			return err
		}
	default:
		if err := h.Quiet("systemctl", "--user", "start", "exe"); err != nil {
			return err
		}
	}
	h.say("exe is starting.\n")
	return nil
}

// cmdRestart asks the running daemon to restart itself.
func cmdRestart() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/daemon/restart", nil, 30*time.Second)
	if err != nil {
		return err
	}
	if err := decodeInto(resp, nil); err != nil {
		return err
	}
	fmt.Println("exe is restarting.")
	return nil
}
