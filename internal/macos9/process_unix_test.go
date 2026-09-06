//go:build !windows

package macos9

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPIDLockHelper(t *testing.T) {
	path := os.Getenv("EXE_MAC9_TEST_PID_LOCK")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err = syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, os.Getpid())
	fmt.Println("locked")
	io.Copy(io.Discard, os.Stdin)
}

func TestLivePIDLockProtectsGuestWithMissingControlSocket(t *testing.T) {
	root := t.TempDir()
	pidfile := filepath.Join(root, "qemu.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPIDLockHelper$")
	cmd.Env = append(os.Environ(), "EXE_MAC9_TEST_PID_LOCK="+pidfile)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("helper: %q %v", line, err)
	}
	m := New(root)
	vnc := m.path("vnc.sock")
	if err = os.WriteFile(vnc, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if !m.Status().Running {
		t.Fatal("lost live process when QMP was unavailable")
	}
	if err = m.Start(); err != nil || m.state.Active {
		t.Fatalf("started a duplicate guest: %v", err)
	}
	if err = m.launch(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(vnc); err != nil || string(b) != "preserve" {
		t.Fatal("removed live display socket")
	}
	if err = m.Finish(); err == nil {
		t.Fatal("allowed disk access while QEMU owns the PID lock")
	}
	input.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if m.running() {
		t.Fatal("stale unlocked PID file counted as a running process")
	}
}

func TestBusyAndStaleControlSocket(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "qmp.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	defer ln.Close()
	raw, err := ln.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var listenErr error
	if err = raw.Control(func(fd uintptr) { listenErr = syscall.Listen(int(fd), 1) }); err != nil || listenErr != nil {
		t.Fatalf("small backlog: %v %v", err, listenErr)
	}
	m := New(root)
	// No accept loop: an occupied QMP monitor fills this listener's backlog.
	for i := 0; i < 6; i++ {
		if !m.running() {
			t.Fatalf("busy socket reported stopped on probe %d", i)
		}
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Start(); err != nil || m.state.Active {
		t.Fatalf("started despite busy control socket: %v", err)
	}
	if err = m.launch(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("replaced busy socket")
	}
	ln.Close()
	if m.running() {
		t.Fatal("stale socket reported running after listener exited")
	}
}
