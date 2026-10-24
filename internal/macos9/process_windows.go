package macos9

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func qemuPIDLocked(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 32)
	if err != nil {
		return false, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	err = windows.GetExitCodeProcess(h, &code)
	return code == 259, err // STILL_ACTIVE
}

// startEmulator starts the Mac in the background, as -daemonize does elsewhere (QEMU has no
// -daemonize on Windows): no window, a process group of its own, and outside the daemon's job
// when the job allows leaving it, so that the Mac goes on through an exe restart. What QEMU
// says goes to qemu.log in the Mac's folder; one that ends at once is an error.
func (m *Manager) startEmulator(ctx context.Context, bin string, args []string) error {
	var kept []string
	for _, a := range args {
		if a != "-daemonize" {
			kept = append(kept, a)
		}
	}
	logPath := m.path("qemu.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(bin, kept...)
		cmd.Env = m.env()
		cmd.Dir = m.root
		cmd.Stdout, cmd.Stderr = logFile, logFile
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true,
			CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW | flags}
		return cmd, cmd.Start()
	}
	cmd, err := start(windows.CREATE_BREAKAWAY_FROM_JOB)
	if err != nil {
		if cmd, err = start(0); err != nil {
			return err
		}
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		b, _ := os.ReadFile(logPath)
		return fmt.Errorf("qemu-system-ppc ended at once: %v: %.2000s", err, strings.TrimSpace(string(b)))
	case <-time.After(time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// hideWindow: a program the daemon runs briefly opens no console window
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
