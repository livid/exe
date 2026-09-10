package vmm

import (
	"context"
	"errors"
	"strings"
)

// Unavailable is the Manager for a node that cannot run VMs (see
// ErrNoBackend): the VM list is empty, Get finds nothing, and every
// operation that would need a hypervisor explains why it cannot happen.
// Everything else the daemon does — the desktop, apps, agents, the hub,
// the Mac — works as usual behind it.
type Unavailable struct{ err error }

// NewUnavailable wraps the New failure that disabled VMs on this node.
func NewUnavailable(reason error) *Unavailable {
	if !errors.Is(reason, ErrNoBackend) {
		reason = noBackend(reason)
	}
	return &Unavailable{err: reason}
}

// Reason is the failure in words, without the ErrNoBackend preamble:
// `find Firecracker binary "firecracker": ... not found in $PATH`.
func (u *Unavailable) Reason() string {
	return strings.TrimPrefix(u.err.Error(), ErrNoBackend.Error()+": ")
}

func (u *Unavailable) Create(context.Context, Spec) (*Info, error)  { return nil, u.err }
func (u *Unavailable) Start(context.Context, string) (*Info, error) { return nil, u.err }
func (u *Unavailable) Stop(context.Context, string) error           { return u.err }
func (u *Unavailable) Delete(context.Context, string) error         { return u.err }
func (u *Unavailable) List(context.Context) ([]*Info, error)        { return []*Info{}, nil }
func (u *Unavailable) Get(context.Context, string) (*Info, error)   { return nil, ErrNotFound }
