//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"exe/internal/server"
)

// keepVMs decides, as the daemon stops, whether its running VMs stay up for
// the next daemon to adopt. They do when the manager started them to outlive
// it (under systemd), the host itself is not going down, and the binary that
// runs next knows how to adopt them. Otherwise why says what stood in the
// way, and the VMs are stopped as they always were.
func keepVMs(srv *server.Server) (keep bool, why string) {
	if !srv.KeepsVMs() {
		return false, ""
	}
	if mode := unitKillMode(); mode != "process" {
		return false, fmt.Sprintf("this unit's KillMode is %q, not \"process\": systemd would end the VMs' processes with the daemon", mode)
	}
	if state := hostStopping(); state != "" {
		return false, "the host is " + state + ": the VMs stop with it"
	}
	if err := nextDaemonAdopts(); err != nil {
		return false, fmt.Sprintf("the exe on disk would not adopt running VMs (%v)", err)
	}
	return true, ""
}

// unitKillMode is the KillMode of the systemd unit this daemon runs in. Only
// "process" leaves the VMs' Firecrackers alone when the unit stops or
// restarts; under any other, systemd ends them with the daemon, and the
// guests would lose power. A daemon told to keep its VMs by EXE_KEEP_VMS=1
// is not asked (a test daemon runs in no unit of its own).
func unitKillMode() string {
	if os.Getenv("EXE_KEEP_VMS") == "1" {
		return "process"
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if i := strings.LastIndexByte(path, ':'); i >= 0 {
		path = path[i+1:]
	}
	unit := path[strings.LastIndexByte(path, '/')+1:]
	if !strings.HasSuffix(unit, ".service") {
		return ""
	}
	args := []string{"show", "-p", "KillMode", "--value", unit}
	if strings.Contains(path, "/user@") {
		args = append([]string{"--user"}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "systemctl", args...).Output()
	return strings.TrimSpace(string(out))
}

// hostStopping names the state that means this machine, or this user's
// session, is going down: whatever is left running is then killed without
// a word to the guest. "" while it is up. systemd's two managers are asked;
// one that cannot be reached counts as going down, since at a shutdown the
// bus goes early.
func hostStopping() string {
	for _, scope := range [][]string{nil, {"--user"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, _ := exec.CommandContext(ctx, "systemctl", append(scope, "is-system-running")...).Output()
		cancel()
		switch state := strings.TrimSpace(string(out)); state {
		case "running", "degraded", "starting", "initializing":
		case "":
			return "not answering"
		default:
			return state
		}
	}
	return ""
}

// startedAs is the path this daemon was started from, read before anything
// can rename the file under it: the path the service manager starts next.
var startedAs, startedAsErr = os.Executable()

// nextDaemonAdopts asks the binary at the path this daemon was started
// from, which is the one the service manager starts next, whether it adopts
// running VMs: a build from before it could would boot each of them a
// second time, on a disk in use.
func nextDaemonAdopts() error {
	if startedAsErr != nil {
		return startedAsErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, startedAs, adoptsVMsCommand).Output()
	if err != nil {
		return fmt.Errorf("%s %s: %w", startedAs, adoptsVMsCommand, err)
	}
	if strings.TrimSpace(string(out)) != adoptsVMsCommand {
		return fmt.Errorf("%s %s: unexpected answer", startedAs, adoptsVMsCommand)
	}
	return nil
}
