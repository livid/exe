// The connections open on the API, each with what let it in: the token in
// a header or on the address ("k"), a session cookie ("t" for one the
// token began, "p<ref>" for a passkey's: session.go), or nothing, where no
// token is asked (""). What ends a session ends its connections with it.
// Removing a passkey closes the terminal sockets and event streams it
// signed in; a new token closes every one, those with the old token in a
// header too. tmux keeps the terminal sessions: the browser is detached,
// and comes back to them when it has signed in again.
//
// auth registers every request to the API under a context of its own and
// drops it when its handler returns; a stream or a socket is a request
// that stays. Ending one cancels that context, which the stream handlers
// wait on, and the terminal handlers watch (signOutWatch).
package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

type liveConn struct {
	via    string
	cancel context.CancelFunc
}

type conns struct {
	mu   sync.Mutex
	next uint64
	open map[uint64]*liveConn
}

type connKey struct{}

func (c *conns) add(via string, cancel context.CancelFunc) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		c.open = map[uint64]*liveConn{}
	}
	c.next++
	c.open[c.next] = &liveConn{via, cancel}
	return c.next
}

func (c *conns) drop(id uint64) {
	c.mu.Lock()
	delete(c.open, id)
	c.mu.Unlock()
}

// end cancels every connection whose via match says so, but keep (the
// request that asked, which is still answering), and says how many.
func (c *conns) end(keep uint64, match func(via string) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for id, lc := range c.open {
		if id != keep && match(lc.via) {
			lc.cancel()
			n++
		}
	}
	return n
}

// connID is the registration of the request r, to keep out of an end it
// asks for.
func connID(r *http.Request) uint64 {
	id, _ := r.Context().Value(connKey{}).(uint64)
	return id
}

// signOutWatch closes the socket c, saying "signed out", when the daemon
// ends the request's session (conns.end), and then stops the handler
// (cancel): the window shows it and does not try to come back. The close
// frame goes first: a read the handler's ctx cancelled would drop the
// socket without one, and the window would read that as a line lost and
// come back for a refusal. It returns when the handler's ctx ends.
func signOutWatch(r *http.Request, ctx context.Context, cancel context.CancelFunc, c *websocket.Conn) {
	select {
	case <-r.Context().Done():
		c.Close(websocket.StatusPolicyViolation, "signed out")
		cancel()
	case <-ctx.Done():
	}
}
