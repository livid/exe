//go:build !darwin

package main

import (
	"context"
	"log"
	"strings"
	"time"

	"exe/internal/server"
)

// serveWait blocks until the daemon stops. Linux VMs are child processes, so
// shut them down before returning and triggering Firecracker's parent-death
// fallback — unless they are to outlive this daemon (keepVMs): then they
// run on, and the next daemon adopts them.
func serveWait(srv *server.Server, wait func() error, shutdown func()) error {
	err := wait()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if running := srv.RunningVMNames(ctx); len(running) > 0 {
		if keep, why := keepVMs(srv); keep {
			log.Printf("daemon shutdown: leaving %s running for the next daemon", strings.Join(running, ", "))
		} else {
			if why != "" {
				log.Printf("daemon shutdown: %s", why)
			}
			log.Printf("daemon shutdown: stopping %s", strings.Join(running, ", "))
			srv.StopVMs(ctx, running)
		}
	}
	shutdown()
	return err
}
