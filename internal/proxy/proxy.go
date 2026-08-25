// Package proxy is a hostname-routed HTTP reverse proxy: the Cloudflare
// tunnel forwards every hostname here, and each Host header maps to a VM
// backend URL. Routes persist across daemon restarts.
package proxy

import (
	"context"
	"encoding/json"
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
	if _, err := url.Parse(backend); err != nil {
		return err
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
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := p.lookup(r.Host); !ok {
			http.Error(w, "exe proxy: no route for host "+r.Host, http.StatusBadGateway)
			return
		}
		rp.ServeHTTP(w, r)
	})
}
