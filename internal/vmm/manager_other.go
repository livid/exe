//go:build !darwin && !linux && !windows

package vmm

import (
	"fmt"
	"runtime"
)

// New returns ErrNoBackend on platforms without a VM backend.
func New(opts Options) (Manager, error) {
	return nil, noBackend(fmt.Errorf("no vm backend for %s/%s", runtime.GOOS, runtime.GOARCH))
}
