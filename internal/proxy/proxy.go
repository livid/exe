// Package proxy is a hostname-routed HTTP reverse proxy: the Cloudflare
// tunnel forwards every hostname here, and each Host header maps to a VM
// backend URL. Routes persist across daemon restarts.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
)

type Proxy struct {
	mu     sync.RWMutex
	file   string
	routes map[string]string // lowercase host (no port) -> backend URL

	// dial, when set, opens backend connections — used when VM IPs only
	// exist inside the daemon process (Windows). Set before Handler.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)

	// builtin backends: a route whose backend is one of these names (they
	// all read "exe:<something>", which no VM URL can) is answered by the
	// daemon itself instead of dialled — the homepage is the one such
	// backend. Registered before Handler.
	builtin map[string]http.Handler
}

// Builtin is the prefix of every backend the daemon answers itself.
const Builtin = "exe:"

// SetBuiltin registers a backend the daemon serves in place of a VM, e.g.
// "exe:site" for the homepage. Call before Handler.
func (p *Proxy) SetBuiltin(backend string, h http.Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.builtin == nil {
		p.builtin = map[string]http.Handler{}
	}
	p.builtin[backend] = h
}

// BuiltinHandler returns the handler a builtin backend ("exe:site") is
// served by, for callers that read a published app without the network.
func (p *Proxy) BuiltinHandler(backend string) (http.Handler, bool) { return p.builtinFor(backend) }

// builtinFor returns the handler for a builtin backend: one registered
// by name, or a directory on this machine ("exe:dir:<path>", dir.go).
func (p *Proxy) builtinFor(backend string) (http.Handler, bool) {
	if !strings.HasPrefix(backend, Builtin) {
		return nil, false
	}
	if h, ok := dirHandlerFor(backend); ok {
		return h, true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	h, ok := p.builtin[backend]
	return h, ok
}

// SetDial installs a custom backend dialer; call before Handler.
func (p *Proxy) SetDial(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	p.dial = dial
}

func New(file string) (*Proxy, error) {
	p := &Proxy{file: file, routes: map[string]string{}}
	b, err := os.ReadFile(file)
	if err == nil {
		if err := json.Unmarshal(b, &p.routes); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return p, nil
}

func (p *Proxy) save() error {
	b, _ := json.MarshalIndent(p.routes, "", "  ")
	return os.WriteFile(p.file, append(b, '\n'), 0o644)
}

func (p *Proxy) Set(host, backend string) error {
	if strings.HasPrefix(backend, Redirect) {
		var err error
		backend, err = RedirectBackend(host, strings.TrimPrefix(backend, Redirect))
		if err != nil {
			return err
		}
	}
	if _, err := url.Parse(backend); err != nil {
		return err
	}
	if strings.HasPrefix(backend, Dir) {
		var err error
		if backend, err = DirBackend(strings.TrimPrefix(backend, Dir)); err != nil {
			return err
		}
	} else if strings.HasPrefix(backend, Builtin) {
		if _, ok := p.builtinFor(backend); !ok {
			return errors.New("no builtin backend " + backend)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[strings.ToLower(host)] = backend
	return p.save()
}

func (p *Proxy) Remove(host string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, strings.ToLower(host))
	return p.save()
}

func (p *Proxy) Snapshot() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]string, len(p.routes))
	for k, v := range p.routes {
		out[k] = v
	}
	return out
}

// Transport returns a RoundTripper that reaches backends the way the proxy
// does — through the custom dialer when one is installed (Windows, where VM
// IPs only exist inside the daemon process).
func (p *Proxy) Transport() http.RoundTripper {
	if p.dial == nil {
		return http.DefaultTransport
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = p.dial
	return tr
}

func (p *Proxy) lookup(hostHeader string) (string, bool) {
	h := hostHeader
	if host, _, err := net.SplitHostPort(hostHeader); err == nil {
		h = host
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	b, ok := p.routes[strings.ToLower(h)]
	return b, ok
}

func (p *Proxy) Handler() http.Handler {
	var transport http.RoundTripper
	if p.dial != nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = p.dial
		transport = tr
	}
	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			backend, ok := p.lookup(pr.In.Host)
			if !ok {
				return
			}
			u, err := url.Parse(backend)
			if err != nil {
				return
			}
			pr.SetURL(u)
			pr.SetXForwarded()
			// Keep the public hostname so apps see the real Host.
			pr.Out.Host = pr.In.Host
			// cloudflared hands the tunnel's requests over plain HTTP, so
			// SetXForwarded would call every published site "http"; keep
			// the scheme the edge reported (Cloudflare sends
			// X-Forwarded-Proto: https), so an app behind exe expose builds
			// https links to itself.
			if proto := pr.In.Header.Get("X-Forwarded-Proto"); proto != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", proto)
			}
		},
	}
	// Gzip wraps every route — VM, builtin and directory alike — so text
	// goes up the tunnel compressed whichever backend wrote it.
	return Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend, ok := p.lookup(r.Host)
		if !ok {
			http.Error(w, "exe proxy: no route for host "+r.Host, http.StatusBadGateway)
			return
		}
		if strings.HasPrefix(backend, Redirect) {
			// Validate persisted routes too, before putting any of their
			// content in a Location header. Only the path/query come from
			// the request; its Host can never change the destination.
			host := r.Host
			if name, _, err := net.SplitHostPort(host); err == nil {
				host = name
			}
			validated, err := RedirectBackend(host, strings.TrimPrefix(backend, Redirect))
			if err != nil {
				http.Error(w, "exe proxy: invalid redirect", http.StatusBadGateway)
				return
			}
			u, _ := url.Parse(strings.TrimPrefix(validated, Redirect))
			u.Path, u.RawPath = r.URL.Path, r.URL.RawPath
			u.RawQuery, u.ForceQuery = r.URL.RawQuery, r.URL.ForceQuery
			http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
			return
		}
		// A backend the daemon answers itself (the homepage) never leaves
		// this process; an "exe:" route whose builtin this binary does not
		// have reads as a route to nowhere, which is what it is.
		if h, ok := p.builtinFor(backend); ok {
			h.ServeHTTP(w, r)
			return
		} else if strings.HasPrefix(backend, Builtin) {
			http.Error(w, "exe proxy: no builtin backend "+backend, http.StatusBadGateway)
			return
		}
		rp.ServeHTTP(w, r)
	}))
}
