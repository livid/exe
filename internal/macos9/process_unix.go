//go:build !windows

package macos9

import (
	"os"
	"syscall"
)

// QEMU's qemu_write_pidfile holds an F_WRLCK until the process exits.
// F_GETLK queries it without acquiring or disturbing the owner's lock.
func qemuPIDLocked(path string) (bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err = syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lock); err != nil {
		return false, err
	}
	return lock.Type != syscall.F_UNLCK, nil
}
