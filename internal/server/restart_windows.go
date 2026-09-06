//go:build windows

package server

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// restartSysProcAttr detaches the handed-over daemon from this console so a
// closing terminal window (or Ctrl+C in it) cannot take the new daemon down.
func restartSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// termSelf: no service manager restarts the daemon on Windows; the
// spawn-and-exit handover is the only restart there.
func termSelf() bool { return false }
