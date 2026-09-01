//go:build !windows

package hostinfo

import "syscall"

// readDisk asks statfs; Bavail is what an unprivileged writer may still
// use, which is the honest "free" for VM disks owned by the daemon user.
func readDisk(path string) Disk {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{}
	}
	bs := uint64(st.Bsize)
	return Disk{Total: uint64(st.Blocks) * bs, Free: uint64(st.Bavail) * bs}
}
