package macos9

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// The guest driver reads our restricted EDID instead of a fixed mode list.
// See display/README.md for upstream source and license.
//
//go:embed display/qemu_vga.ndrv
var vgaDriver []byte

func (m *Manager) ensureDisplayDriver() error {
	path := m.path("firmware/qemu_vga.ndrv")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, vgaDriver) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", vgaDriver, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
