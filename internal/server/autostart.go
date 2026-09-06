package server

import (
	"os"
	"path/filepath"
	"strings"
)

// The daemon brings back the VMs that were running when it last stopped.
// A spawn-and-exit restart hands their names to the replacement in
// EXE_AUTOSTART; under a service manager the daemon never spawns anything —
// systemd starts the next one — so the names go through a file in the
// state dir instead. A `systemctl restart`, the restart endpoint and a
// reboot then all return the same VMs to running.

const autostartFile = "autostart"

// SaveAutostart records the VMs the next daemon should start, one name per
// line; no names removes the record so a stale one cannot revive VMs the
// user has since stopped.
func SaveAutostart(stateDir string, names []string) error {
	path := filepath.Join(stateDir, autostartFile)
	if len(names) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(names, "\n")+"\n"), 0o600)
}

// TakeAutostart returns the recorded names and forgets them, so a record is
// acted on once even if this start fails partway.
func TakeAutostart(stateDir string) []string {
	path := filepath.Join(stateDir, autostartFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	os.Remove(path)
	var names []string
	for _, ln := range strings.Split(string(data), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			names = append(names, ln)
		}
	}
	return names
}
