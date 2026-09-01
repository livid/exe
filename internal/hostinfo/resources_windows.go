//go:build windows

package hostinfo

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// memoryStatusEx mirrors MEMORYSTATUSEX; dwLength must be set before the call.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// readMem calls GlobalMemoryStatusEx; AvailPhys is what a new process could
// take without paging.
func readMem() Memory {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 {
		return Memory{}
	}
	return Memory{Total: st.TotalPhys, Available: st.AvailPhys}
}

// readDisk asks GetDiskFreeSpaceEx for the caller's free bytes and the
// volume size.
func readDisk(path string) Disk {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Disk{}
	}
	var free, total uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, nil); err != nil {
		return Disk{}
	}
	return Disk{Total: total, Free: free}
}

// osName is the kernel version: "Windows 10.0.26100".
func osName() string {
	v := windows.RtlGetVersion()
	if v == nil {
		return "Windows"
	}
	return fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
