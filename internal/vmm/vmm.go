// Package vmm manages virtual machines behind a platform-neutral interface.
// macOS uses Virtualization.framework, Linux uses KVM through Firecracker,
// and Windows uses QEMU on the Windows Hypervisor Platform.
package vmm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"
)

type Spec struct {
	Name     string `json:"name"`
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memory_mb"`
	DiskGB   int    `json:"disk_gb"`
}

type Info struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	CPUs      int       `json:"cpus"`
	MemoryMB  int       `json:"memory_mb"`
	DiskGB    int       `json:"disk_gb"`
	MAC       string    `json:"mac"`
	IP        string    `json:"ip,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Manager interface {
	// Create provisions a new VM from the base image and boots it.
	Create(ctx context.Context, spec Spec) (*Info, error)
	Start(ctx context.Context, name string) (*Info, error)
	Stop(ctx context.Context, name string) error
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]*Info, error)
	Get(ctx context.Context, name string) (*Info, error)
}

// ImageEnsurer is implemented by managers that can pre-fetch their base image.
type ImageEnsurer interface {
	EnsureImage(ctx context.Context) (string, error)
}

// GuestDialer is implemented by managers whose guest network lives inside the
// daemon process (Windows): VM IPs are unreachable through the host's TCP
// stack, so anything dialing a guest must go through the manager. DialGuest
// falls back to the host network for addresses outside the guest subnet.
type GuestDialer interface {
	DialGuest(ctx context.Context, network, addr string) (net.Conn, error)
}

// PortForwarder is implemented by managers whose guests the host cannot reach
// directly. ForwardGuestPort publishes a guest port on a host-local address
// (e.g. "127.0.0.1:53422") so a browser on this machine can open it; forwards
// live until the VM stops.
type PortForwarder interface {
	ForwardGuestPort(name string, port int) (string, error)
}

type Options struct {
	StateDir       string
	ImageURL       string
	SSHUser        string
	AuthorizedKey  string
	PrivateKeyPath string
	Firecracker    FirecrackerOptions
	QEMU           QEMUOptions
}

type FirecrackerOptions struct {
	Binary            string
	KernelURL         string
	NetworkHelper     string
	NetworkCIDR       string
	OutboundInterface string
}

// QEMUOptions configures the Windows backend: QEMU accelerated by the
// Windows Hypervisor Platform, with the guest network running inside the
// daemon process.
type QEMUOptions struct {
	Binary      string
	FirmwareDir string
	NetworkCIDR string
}

type vmNetwork struct {
	Tap               string `json:"tap"`
	HostIP            string `json:"host_ip"`
	GuestIP           string `json:"guest_ip"`
	PrefixLen         int    `json:"prefix_len"`
	OutboundInterface string `json:"outbound_interface,omitempty"`
}

type vmMeta struct {
	Spec      Spec       `json:"spec"`
	MAC       string     `json:"mac"`
	CreatedAt time.Time  `json:"created_at"`
	Network   *vmNetwork `json:"network,omitempty"`
}

var (
	ErrNotFound   = errors.New("vm not found")
	ErrNotRunning = errors.New("vm is not running (VMs live inside the `exe serve` process)")
	// ErrNoBackend marks a New failure that means this machine cannot run
	// VMs at all — no hypervisor, Firecracker or QEMU not installed, an
	// unsupported CPU — as opposed to a bad configuration or a busy state
	// directory. The daemon then serves everything else behind Unavailable
	// (a NAS, a container without /dev/kvm) instead of refusing to start.
	ErrNoBackend = errors.New("no VM backend on this node")
)

func noBackend(err error) error { return fmt.Errorf("%w: %w", ErrNoBackend, err) }

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid vm name %q (use lowercase letters, digits, hyphens)", name)
	}
	return nil
}

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
