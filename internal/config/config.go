// Package config loads the exe daemon/CLI configuration from ~/.exe/config.json.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const defaultImageURLTmpl = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-%s.raw"

const defaultAlpineImageURLTmpl = "https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/cloud/alpine-3.24.2-%s-cloudinit-r0.raw.tar.gz"

const defaultFirecrackerKernelURLTmpl = "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/20260805-f2f43b669a02-0/%s/vmlinux-6.18.39"

type OllamaConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	// Effort is the thinking effort for models that support it (Ollama's
	// think option): "low"/"medium"/"high", "off" to disable thinking;
	// empty uses the model's default.
	Effort string `json:"effort,omitempty"`
}

// OpenAIConfig configures the ChatGPT-subscription chat backend. The tokens
// from Sign in with ChatGPT live in ~/.exe/openai.json, not here.
type OpenAIConfig struct {
	// Model is a ChatGPT model id, e.g. "gpt-5.4" or "gpt-5.4-codex".
	Model string `json:"model"`
	// Effort is the reasoning effort (minimal/low/medium/high/xhigh);
	// empty uses the model's default.
	Effort string `json:"effort,omitempty"`
}

// GitHubConfig configures Sign in with GitHub — the OAuth device flow
// behind Publish to GitHub. The token lives in ~/.exe/github.json, not here.
type GitHubConfig struct {
	// ClientID is an OAuth app's client ID (github.com → Settings →
	// Developer settings → OAuth Apps) with device flow enabled. Not a
	// secret: device flow needs no client secret.
	ClientID string `json:"client_id"`
}

type CloudflareConfig struct {
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id"`
	ZoneID    string `json:"zone_id"`
	TunnelID  string `json:"tunnel_id"`
	Domain    string `json:"domain"`
	// MetricsURL is the local cloudflared metrics server, without /metrics.
	// Empty uses http://127.0.0.1:20241. Only loopback addresses are accepted.
	MetricsURL string `json:"metrics_url,omitempty"`
}

// JupiterConfig is the key for Jupiter's price API, the fallback behind the
// Control Strip's Solana ticker when Coinbase cannot quote a token (portal.jup.ag).
// Empty uses Jupiter's keyless, rate-limited host.
type JupiterConfig struct {
	APIKey string `json:"api_key,omitempty"`
}

type FirecrackerConfig struct {
	// Binary is either a command on PATH or an absolute path.
	Binary string `json:"binary"`
	// KernelURL is the direct-boot Linux kernel downloaded for Firecracker VMs.
	KernelURL string `json:"kernel_url"`
	// NetworkHelper is the root-owned helper granted only CAP_NET_ADMIN.
	NetworkHelper string `json:"network_helper"`
	// NetworkCIDR is divided into one /30 subnet per VM.
	NetworkCIDR string `json:"network_cidr"`
	// OutboundInterface is auto-detected from the default route when empty.
	OutboundInterface string `json:"outbound_interface"`
}

// QEMUConfig configures the Windows backend: QEMU accelerated by the Windows
// Hypervisor Platform, with the guest network living inside the daemon.
type QEMUConfig struct {
	// Binary is a command on PATH or an absolute path; empty auto-detects
	// qemu-system-x86_64 (PATH, then C:\Program Files\qemu).
	Binary string `json:"binary"`
	// FirmwareDir holds QEMU's EFI firmware (edk2-x86_64-code.fd and
	// edk2-i386-vars.fd); empty auto-detects the share dir next to the binary.
	FirmwareDir string `json:"firmware_dir"`
	// NetworkCIDR is the flat in-process guest subnet; each VM gets one
	// address, the daemon is the gateway at the first host address.
	NetworkCIDR string `json:"network_cidr"`
}

type Config struct {
	// Listen is the API address. Defaults to the Tailscale IP when one is
	// detected ("100.x.y.z:7777") so other devices reach the daemon,
	// otherwise "127.0.0.1:7777".
	Listen string `json:"listen"`
	// ProxyListen is the HTTP reverse-proxy address that the Cloudflare
	// tunnel (or anything else) forwards traffic to.
	ProxyListen string `json:"proxy_listen"`
	// AdvertiseHost is the address of THIS machine as reachable from the
	// cloudflared tunnel server (LAN IP or Tailscale IP).
	AdvertiseHost string `json:"advertise_host"`
	// SSHListen is the SSH gate address (default ":2222"):
	// `ssh -p 2222 exe@host` is the lobby, `ssh -p 2222 <vm>@host` is full
	// SSH into the VM. Set to "off" to disable.
	SSHListen string `json:"ssh_listen"`
	// APIToken, when set, is required as a Bearer token on every API call.
	APIToken string `json:"api_token"`

	// Language is the desk's language, one of Languages: its menus,
	// windows and messages. Empty is English.
	Language string `json:"language,omitempty"`

	// VMDir is where the VMs' disks and the base images are kept: its
	// vms/ and images/ folders. Empty means the state folder itself
	// (~/.exe), which is where they have always been. It is the one part
	// of the state that is large — a base image is 3 GB, a VM up to its
	// disk size — so it is the part that may want another drive: a PC
	// whose system drive is nearly full, a Linux box with a data volume.
	// Read at start; moving VMs that exist means moving the two folders.
	VMDir string `json:"vm_dir,omitempty"`

	// AppsDirs lists extra folders scanned for desktop app bundles in
	// addition to ~/.exe/apps — e.g. a separate git repo of experimental
	// apps. A leading ~ expands to the daemon user's home.
	AppsDirs []string `json:"apps_dirs,omitempty"`
	// Services names local HTTP services the desktop reaches through the
	// daemon, e.g. {"planet": "http://127.0.0.1:7799"}: a request to
	// /v1/svc/<name>/<path> is relayed to <url>/<path> with the API token
	// checked here, so a browser that cannot reach the machine's loopback
	// (the desk opened over Tailscale Serve, a phone) still talks to it.
	Services map[string]string `json:"services,omitempty"`

	SSHUser  string `json:"ssh_user"`
	ImageURL string `json:"image_url"`
	// AlpineImageURL is the Alpine base image the New VM dialog's System
	// pop-up offers beside the default; a .raw.tar.gz is unpacked to the
	// raw disk it holds.
	AlpineImageURL string `json:"alpine_image_url"`

	DefaultCPUs     int `json:"default_cpus"`
	DefaultMemoryMB int `json:"default_memory_mb"`
	DefaultDiskGB   int `json:"default_disk_gb"`

	// ChatProvider picks the Chat window's LLM backend: "ollama" (default)
	// or "openai" (a ChatGPT subscription via Sign in with ChatGPT).
	ChatProvider string `json:"chat_provider,omitempty"`

	Ollama      OllamaConfig      `json:"ollama"`
	OpenAI      OpenAIConfig      `json:"openai"`
	GitHub      GitHubConfig      `json:"github"`
	Cloudflare  CloudflareConfig  `json:"cloudflare"`
	Jupiter     JupiterConfig     `json:"jupiter"`
	Firecracker FirecrackerConfig `json:"firecracker"`
	QEMU        QEMUConfig        `json:"qemu"`
}

// Dir returns the state directory (~/.exe, or $EXE_HOME).
func Dir() string {
	if d := os.Getenv("EXE_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".exe"
	}
	return filepath.Join(home, ".exe")
}

func Path() string { return filepath.Join(Dir(), "config.json") }

// VMRoot is the folder that holds vms/ and images/: vm_dir, or the state
// folder when none is set.
func (c *Config) VMRoot() string {
	if c.VMDir != "" {
		return c.VMDir
	}
	return Dir()
}

func Default() *Config {
	arch := "arm64"
	firecrackerArch := "aarch64"
	if runtime.GOARCH == "amd64" {
		arch = "amd64"
		firecrackerArch = "x86_64"
	}
	// On a tailnet the API defaults to the Tailscale IP (the daemon adds a
	// 127.0.0.1 companion listener) and advertises this machine at it, so
	// other devices reach the daemon with zero config.
	listen, advertise := "127.0.0.1:7777", ""
	if ip := TailscaleIP(); ip != "" {
		listen = net.JoinHostPort(ip, "7777")
		advertise = ip
	}
	return &Config{
		Listen:          listen,
		AdvertiseHost:   advertise,
		ProxyListen:     ":8090",
		SSHListen:       ":2222",
		SSHUser:         "dev",
		ImageURL:        fmt.Sprintf(defaultImageURLTmpl, arch),
		AlpineImageURL:  fmt.Sprintf(defaultAlpineImageURLTmpl, firecrackerArch),
		DefaultCPUs:     2,
		DefaultMemoryMB: 2048,
		DefaultDiskGB:   20,
		Ollama: OllamaConfig{
			BaseURL: "https://ollama.com",
			Model:   "glm-5.2",
		},
		OpenAI: OpenAIConfig{
			Model: "gpt-5.4",
		},
		Firecracker: FirecrackerConfig{
			Binary:        "firecracker",
			KernelURL:     fmt.Sprintf(defaultFirecrackerKernelURLTmpl, firecrackerArch),
			NetworkHelper: "/usr/local/libexec/exe-net-helper",
			NetworkCIDR:   "172.30.0.0/16",
		},
		QEMU: QEMUConfig{
			NetworkCIDR: "192.168.127.0/24",
		},
	}
}

// TailscaleIP is this host's Tailscale IPv4, detected as a CGNAT
// (100.64.0.0/10) address on a local interface — no tailscale CLI needed.
// Empty when not on a tailnet.
func TailscaleIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip4 := ipn.IP.To4(); ip4 != nil && cgnat.Contains(ip4) {
				return ip4.String()
			}
		}
	}
	return ""
}

// LANIP returns this host's primary private LAN IPv4 — the address the OS
// would use for its default route — excluding loopback and the Tailscale
// CGNAT range. Empty when none is found. The UDP "connect" sends no packets;
// it only makes the kernel pick the outbound source address.
func LANIP() string {
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer conn.Close()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			if ip4 := a.IP.To4(); ip4 != nil && !ip4.IsLoopback() && !cgnat.Contains(ip4) {
				return ip4.String()
			}
		}
	}
	// No usable default route: fall back to the first private IPv4 on any
	// interface (skips CGNAT via IsPrivate).
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip4 := ipn.IP.To4(); ip4 != nil && ip4.IsPrivate() {
				return ip4.String()
			}
		}
	}
	return ""
}

// SSHEnabled reports whether addr enables the SSH gate; empty, "off",
// "none" and "disabled" turn it off.
func SSHEnabled(addr string) bool {
	switch strings.ToLower(strings.TrimSpace(addr)) {
	case "", "off", "none", "disabled":
		return false
	}
	return true
}

// NormalizeListen makes listen addresses forgiving: a bare port ("8090")
// becomes ":8090", surrounding whitespace is dropped, and anything else
// (host:port, "off", empty) passes through unchanged.
func NormalizeListen(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr != "" && !strings.ContainsAny(addr, ":.") {
		if _, err := strconv.Atoi(addr); err == nil {
			return ":" + addr
		}
	}
	return addr
}

// Normalize cleans up user-entered values in place.
// Languages are the desk's languages, as BCP 47 tags, English first.
var Languages = []string{"en", "zh-Hans", "ja"}

// LanguageTag is the one of Languages that s names, whatever its case,
// or "" when it names none.
func LanguageTag(s string) string {
	for _, l := range Languages {
		if strings.EqualFold(l, strings.TrimSpace(s)) {
			return l
		}
	}
	return ""
}

// UILanguage is the language the desk is drawn in: the configured one,
// or English when none (or none it has) is set.
func (c *Config) UILanguage() string {
	if l := LanguageTag(c.Language); l != "" {
		return l
	}
	return "en"
}

func (c *Config) Normalize() {
	if l := LanguageTag(c.Language); l != "" {
		c.Language = l
	} else {
		c.Language = strings.TrimSpace(c.Language)
	}
	c.Listen = NormalizeListen(c.Listen)
	c.ProxyListen = NormalizeListen(c.ProxyListen)
	c.SSHListen = NormalizeListen(c.SSHListen)
	c.AdvertiseHost = strings.TrimSpace(c.AdvertiseHost)
	c.VMDir = strings.TrimSpace(c.VMDir)
	c.ChatProvider = strings.ToLower(strings.TrimSpace(c.ChatProvider))
	c.Ollama.Effort = strings.ToLower(strings.TrimSpace(c.Ollama.Effort))
	c.OpenAI.Model = strings.TrimSpace(c.OpenAI.Model)
	c.OpenAI.Effort = strings.ToLower(strings.TrimSpace(c.OpenAI.Effort))
	c.GitHub.ClientID = strings.TrimSpace(c.GitHub.ClientID)
	var dirs []string
	for _, d := range c.AppsDirs {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}
	c.AppsDirs = dirs
	var services map[string]string
	for name, u := range c.Services {
		name, u = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(u)
		if name == "" || u == "" {
			continue
		}
		if services == nil {
			services = map[string]string{}
		}
		services[name] = u
	}
	c.Services = services
	c.Firecracker.Binary = strings.TrimSpace(c.Firecracker.Binary)
	c.Firecracker.KernelURL = strings.TrimSpace(c.Firecracker.KernelURL)
	c.Firecracker.NetworkHelper = strings.TrimSpace(c.Firecracker.NetworkHelper)
	c.Firecracker.NetworkCIDR = strings.TrimSpace(c.Firecracker.NetworkCIDR)
	c.Firecracker.OutboundInterface = strings.TrimSpace(c.Firecracker.OutboundInterface)
	c.Jupiter.APIKey = strings.TrimSpace(c.Jupiter.APIKey)
	c.QEMU.Binary = strings.TrimSpace(c.QEMU.Binary)
	c.QEMU.FirmwareDir = strings.TrimSpace(c.QEMU.FirmwareDir)
	c.QEMU.NetworkCIDR = strings.TrimSpace(c.QEMU.NetworkCIDR)
}

// Load reads config.json over the defaults; secrets can also come from
// OLLAMA_API_KEY, CLOUDFLARE_API_TOKEN and EXE_API_TOKEN.
func Load() (*Config, error) {
	cfg := Default()
	b, err := os.ReadFile(Path())
	if err == nil {
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", Path(), err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cfg.Normalize()
	if v := os.Getenv("OLLAMA_API_KEY"); v != "" {
		cfg.Ollama.APIKey = v
	}
	if v := os.Getenv("CLOUDFLARE_API_TOKEN"); v != "" {
		cfg.Cloudflare.APIToken = v
	}
	if v := os.Getenv("EXE_API_TOKEN"); v != "" {
		cfg.APIToken = v
	}
	return cfg, nil
}

// WriteTemplate writes a starter config; it refuses to overwrite.
func WriteTemplate() (string, error) {
	p := Path()
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("%s already exists", p)
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return p, err
	}
	b, _ := json.MarshalIndent(Default(), "", "  ")
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		return p, err
	}
	// a new home greets its owner; a failure here is no reason to fail init
	SetWelcome(Dir(), true)
	return p, nil
}

// A new install greets its owner: `exe setup` and `exe init` leave an empty
// file beside the configuration they write, and while it is there the
// desktop opens its Welcome window each time it loads. The window's check
// box takes the file away and puts it back. It is a file of its own rather
// than a setting, so that it belongs to this machine alone: nothing that
// is synced between joined nodes carries it.
const welcomeFile = "welcome"

// Welcome reports whether the desktop kept in dir opens its Welcome window
// by itself.
func Welcome(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, welcomeFile))
	return err == nil
}

// SetWelcome turns that on or off.
func SetWelcome(dir string, on bool) error {
	p := filepath.Join(dir, welcomeFile)
	if on {
		return os.WriteFile(p, nil, 0o644)
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
