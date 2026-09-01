//go:build linux

package hostinfo

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// readMem reads /proc/meminfo. MemAvailable is the kernel's own estimate of
// what can be allocated without swapping — closer to a "largest unused
// block" than MemFree, which ignores reclaimable cache.
func readMem() Memory {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}
	}
	defer f.Close()
	return parseMeminfo(f)
}

// parseMeminfo reads the "Key:  value kB" lines of /proc/meminfo.
func parseMeminfo(r io.Reader) Memory {
	var m Memory
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		switch k {
		case "MemTotal":
			m.Total = n
		case "MemAvailable":
			m.Available = n
		}
	}
	return m
}

// osName is the kernel version without the distro suffix: "Linux 6.17.0"
// from a 6.17.0-1014-nvidia release.
func osName() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "Linux"
	}
	v, _, _ := strings.Cut(strings.TrimSpace(string(b)), "-")
	return "Linux " + v
}
