//go:build linux

package vmm

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"exe/internal/fcnet"
)

// reconcileNetworks removes host networking left by a previous daemon. The
// state lock is held before this runs, so another daemon cannot own these VMs.
// A VM that daemon left running, adopted by now, keeps its networking.
func (m *fcManager) reconcileNetworks() error {
	entries, err := os.ReadDir(filepath.Join(m.opts.vmRoot(), "vms"))
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		m.mu.Lock()
		adopted := m.running[entry.Name()] != nil
		m.mu.Unlock()
		if adopted {
			continue
		}
		path := filepath.Join(m.vmDir(entry.Name()), "vm.json")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		var meta vmMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		if meta.Network == nil || meta.Network.OutboundInterface == "" {
			continue
		}
		if err := m.runNetworkHelper("cleanup", entry.Name(), meta.Network); err != nil {
			errs = append(errs, fmt.Errorf("VM %s: %w", entry.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func (m *fcManager) allocateNetwork() (*vmNetwork, error) {
	used := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(m.opts.vmRoot(), "vms"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mt, err := m.loadMeta(entry.Name())
		if err == nil && mt.Network != nil {
			used[mt.Network.HostIP] = true
		}
	}

	base := uint64(ipv4Number(m.network.IP))
	size := uint64(1) << uint(32-m.prefixLen)
	for offset := uint64(0); offset+3 < size; offset += 4 {
		host := numberIPv4(uint32(base + offset + 1))
		guest := numberIPv4(uint32(base + offset + 2))
		if used[host.String()] {
			continue
		}
		return &vmNetwork{
			HostIP:    host.String(),
			GuestIP:   guest.String(),
			PrefixLen: 30,
		}, nil
	}
	return nil, fmt.Errorf("Firecracker network %s has no free /30 subnets", m.network.String())
}

func tapName(name string) string {
	return fcnet.TapName(name)
}

func ipv4Number(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip.To4())
}

func numberIPv4(n uint32) net.IP {
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}

func networkCIDR(ip string, prefix int) (string, error) {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return "", fmt.Errorf("invalid IPv4 address %q", ip)
	}
	if prefix < 0 || prefix > 32 {
		return "", fmt.Errorf("invalid prefix length %d", prefix)
	}
	return parsed.Mask(net.CIDRMask(prefix, 32)).String() + "/" + strconv.Itoa(prefix), nil
}

func (m *fcManager) setupNetwork(name string, mt *vmMeta) error {
	if mt.Network == nil {
		return fmt.Errorf("VM %s has no Firecracker network metadata", name)
	}
	mt.Network.Tap = tapName(name)

	if mt.Network.OutboundInterface != "" {
		if err := m.runNetworkHelper("cleanup", name, mt.Network); err != nil {
			return fmt.Errorf("clean stale network for VM %s: %w", name, err)
		}
	}
	outbound, err := m.outboundInterface()
	if err != nil {
		return err
	}
	if outbound == mt.Network.Tap {
		return fmt.Errorf("outbound interface cannot be VM TAP %s", outbound)
	}
	mt.Network.OutboundInterface = outbound
	if err := m.saveMeta(name, mt); err != nil {
		return err
	}
	if err := m.runNetworkHelper("setup", name, mt.Network); err != nil {
		return fmt.Errorf("set up network for VM %s: %w", name, err)
	}
	return nil
}

func (m *fcManager) cleanupNetwork(name string, mt *vmMeta) {
	if mt == nil || mt.Network == nil || mt.Network.OutboundInterface == "" {
		return
	}
	if err := m.runNetworkHelper("cleanup", name, mt.Network); err != nil {
		log.Printf("vm %s: network cleanup: %v", name, err)
	}
}

func (m *fcManager) runNetworkHelper(action, name string, network *vmNetwork) error {
	hostCIDR := network.HostIP + "/" + strconv.Itoa(network.PrefixLen)
	args := []string{
		action,
		"--name", name,
		"--host-cidr", hostCIDR,
		"--outbound", network.OutboundInterface,
		"--owner-uid", strconv.FormatUint(uint64(m.ownerUID), 10),
	}
	output, err := exec.Command(m.helper, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, text)
}

func (m *fcManager) outboundInterface() (string, error) {
	if configured := strings.TrimSpace(m.opts.Firecracker.OutboundInterface); configured != "" {
		return configured, nil
	}
	output, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return "", fmt.Errorf("detect outbound interface: %w", err)
	}
	fields := strings.Fields(string(output))
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "dev" {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("no default IPv4 route; set firecracker.outbound_interface")
}
