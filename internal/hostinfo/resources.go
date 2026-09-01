package hostinfo

import (
	"runtime/debug"
	"runtime/metrics"
	"strings"
)

// Memory is the host's physical memory: what is installed, and what the
// kernel could hand a new process right now — the About This Computer
// window's Built-in Memory and Largest Unused Block.
type Memory struct {
	Total     uint64
	Available uint64
}

// Disk is the capacity and free space of the filesystem holding a path —
// for the state dir, where VM disks grow.
type Disk struct {
	Total uint64
	Free  uint64
}

// Mem reports the host's memory; zeros when the platform lookup fails.
func Mem() Memory { return readMem() }

// DiskUsage reports the filesystem behind path; zeros when it fails.
func DiskUsage(path string) Disk { return readDisk(path) }

// OS names the operating system and its version the way its About box
// would: "Linux 6.17.0", "macOS 15.6", "Windows 10.0.26100".
func OS() string { return osName() }

// ProcessMemory is the memory the Go runtime holds from the OS for this
// process: heap, stacks and runtime structures, minus pages already
// released. On macOS the VMs' guest memory lives in this same process but
// outside the Go heap, so this stays the daemon's own footprint everywhere.
func ProcessMemory() uint64 {
	s := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 || s[1].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	total, released := s[0].Value.Uint64(), s[1].Value.Uint64()
	if released > total {
		return 0
	}
	return total - released
}

// Build describes the binary from the VCS stamp the Go toolchain embeds:
// the commit date (YYYY-MM-DD), the short revision, and whether the tree
// had uncommitted changes. Empty when the build carried no stamp.
type Build struct {
	Date     string
	Commit   string
	Modified bool
}

// BuildInfo reads the embedded VCS stamp.
func BuildInfo() Build {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Build{}
	}
	var b Build
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				b.Commit = s.Value[:7]
			} else {
				b.Commit = s.Value
			}
		case "vcs.time":
			if i := strings.IndexByte(s.Value, 'T'); i > 0 {
				b.Date = s.Value[:i]
			} else {
				b.Date = s.Value
			}
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	return b
}
