//go:build darwin

package hostinfo

import (
	"os/exec"
	"strconv"
	"strings"
)

// readMem takes the installed size from sysctl and the available figure
// from vm_stat's page counts: free, inactive and speculative pages are
// what the kernel would give a new allocation without paging anything out.
func readMem() Memory {
	var m Memory
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		m.Total, _ = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	}
	if out, err := exec.Command("vm_stat").Output(); err == nil {
		m.Available = parseVMStat(string(out))
	}
	return m
}

// parseVMStat reads vm_stat's header for the page size and sums the free,
// inactive and speculative page counts ("Pages free:   12345.").
func parseVMStat(out string) uint64 {
	var pageSize uint64 = 4096
	var pages uint64
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if _, rest, ok := strings.Cut(line, "page size of "); ok {
				if n, err := strconv.ParseUint(strings.Fields(rest)[0], 10, 64); err == nil && n > 0 {
					pageSize = n
				}
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Pages free", "Pages inactive", "Pages speculative":
			n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
			if err == nil {
				pages += n
			}
		}
	}
	return pages * pageSize
}

// osName is the marketing version: "macOS 15.6".
func osName() string {
	if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return "macOS " + v
		}
	}
	return "macOS"
}
