//go:build !linux && !darwin

package main

import "exe/internal/server"

// keepVMs: only Linux has VMs that outlive the daemon (keepvms_linux.go).
func keepVMs(*server.Server) (bool, string) { return false, "" }
