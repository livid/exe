// Published-app icons for the My Apps window.
//
// GET /v1/appicons/{host} serves the icon a published app declares for
// itself: its touch icon (<link rel="apple-touch-icon">, or the bare
// /apple-touch-icon.png convention), falling back to <link rel="icon">.
// The daemon fetches straight from the route's backend — no Cloudflare
// round-trip, so icons work even while the tunnel is down — and caches
// bytes + metadata under ~/.exe/appicons/. A cache entry refreshes on
// demand once it ages past its TTL, so an app changing its icon shows up
// on the next My Apps open without any watcher; while the app itself is
// down, the cached icon keeps serving.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	appIconTTL     = 6 * time.Hour    // re-check a cached icon this often
	appIconMissTTL = 15 * time.Minute // re-try a host with no icon this often
	appIconHTMLMax = 512 << 10        // of the app's page, when hunting link tags
	appIconMax     = 2 << 20          // a single icon file
)

func (s *Server) appIconsDir() string         { return filepath.Join(s.StateDir, "appicons") }
func (s *Server) appIconPath(h string) string { return filepath.Join(s.appIconsDir(), h+".img") }
func (s *Server) appIconMetaPath(h string) string {
	return filepath.Join(s.appIconsDir(), h+".json")
}

type appIconMeta struct {
	ContentType string    `json:"content_type,omitempty"`
	Checked     time.Time `json:"checked"`
	Missing     bool      `json:"missing,omitempty"` // the app declares no icon
}

// Hostnames come from the proxy's own route table, but they are also about
// to become cache filenames — allow DNS-name characters only.
var iconHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.PathValue("host"))
	backend, ok := s.Proxy.Snapshot()[host]
	if !ok || !iconHostRE.MatchString(host) || strings.Contains(host, "..") {
		writeErr(w, http.StatusNotFound, errors.New("no such route"))
		return
	}

	// one fetch at a time per host — a window opening with N tiles must not
	// stampede a backend that is slow to answer
	mu, _ := s.appIconMu.LoadOrStore(host, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	m := s.loadIconMeta(host)
	ttl := appIconTTL
	if m.Missing {
		ttl = appIconMissTTL
	}
	if time.Since(m.Checked) > ttl {
		m = s.refreshAppIcon(r.Context(), host, backend)
	}
	if m.Missing {
		writeErr(w, http.StatusNotFound, errors.New("app has no icon"))
		return
	}
	b, err := os.ReadFile(s.appIconPath(host))
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("app has no icon"))
		return
	}
	ct := m.ContentType
	if ct == "" {
		ct = "image/png"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (s *Server) loadIconMeta(host string) appIconMeta {
	var m appIconMeta
	if b, err := os.ReadFile(s.appIconMetaPath(host)); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (s *Server) saveIconMeta(host string, m appIconMeta) {
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(s.appIconsDir(), 0o755); err == nil {
		os.WriteFile(s.appIconMetaPath(host), append(b, '\n'), 0o644)
	}
}

// refreshAppIcon re-fetches host's icon and records the outcome. A fetch
// failure with an icon already on disk keeps serving the stale bytes (the
// app may just be down); only a host with nothing cached records a miss.
func (s *Server) refreshAppIcon(ctx context.Context, host, backend string) appIconMeta {
	m, err := s.fetchAppIcon(ctx, host, backend)
	if err != nil {
		log.Printf("appicon %s: %v", host, err)
		if _, serr := os.Stat(s.appIconPath(host)); serr == nil {
			m = s.loadIconMeta(host)
			m.Missing = false
		} else {
			m = appIconMeta{Missing: true}
		}
	}
	m.Checked = time.Now()
	s.saveIconMeta(host, m)
	return m
}

// fetchAppIcon asks the app itself: its page's apple-touch-icon link, the
// well-known /apple-touch-icon.png, then its plain icon link. Every request
// goes to the route's backend with the public Host header — the same view
// of the app the proxy serves — and hrefs that resolve off-host are skipped,
// so this never talks to anything but the app.
func (s *Server) fetchAppIcon(ctx context.Context, host, backend string) (appIconMeta, error) {
	bu, err := url.Parse(backend)
	if err != nil {
		return appIconMeta{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{
		Transport: s.Proxy.Transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != bu.Host {
				return errors.New("redirect left the app")
			}
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			req.Host = host
			return nil
		},
	}

	touchHref, plainHref := s.findIconLinks(ctx, client, bu, host)
	var lastErr error = errors.New("no icon declared")
	for _, href := range []string{touchHref, "/apple-touch-icon.png", plainHref} {
		if href == "" {
			continue
		}
		m, err := s.fetchIconFile(ctx, client, bu, host, href)
		if err == nil {
			return m, nil
		}
		lastErr = fmt.Errorf("%s: %w", href, err)
	}
	return appIconMeta{}, lastErr
}

// findIconLinks scans the app's page for its best touch-icon and plain-icon
// hrefs ("" when absent). Best-effort: a page that won't fetch or parse just
// means the well-known path is the only candidate.
func (s *Server) findIconLinks(ctx context.Context, client *http.Client, bu *url.URL, host string) (touch, plain string) {
	resp, err := iconGet(ctx, client, bu, host, &url.URL{Path: "/"})
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	page, _ := io.ReadAll(io.LimitReader(resp.Body, appIconHTMLMax))
	var touchSize, plainSize = -1, -1
	for _, tag := range linkTagRE.FindAllString(string(page), -1) {
		attrs := map[string]string{}
		for _, m := range attrValRE.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = m[2] + m[3] + m[4]
		}
		href := strings.TrimSpace(attrs["href"])
		if href == "" || strings.HasPrefix(strings.ToLower(href), "data:") {
			continue
		}
		size := iconSizes(attrs["sizes"])
		for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
			switch rel {
			case "apple-touch-icon", "apple-touch-icon-precomposed":
				if size > touchSize {
					touch, touchSize = href, size
				}
			case "icon":
				if size > plainSize {
					plain, plainSize = href, size
				}
			}
		}
	}
	return touch, plain
}

var (
	linkTagRE = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	attrValRE = regexp.MustCompile(`(?is)([a-z-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// iconSizes returns the largest edge in a sizes attribute ("180x180",
// "32x32 16x16"), 0 when unparseable — so any declared size beats none.
func iconSizes(sizes string) int {
	best := 0
	for _, f := range strings.Fields(strings.ToLower(sizes)) {
		if n, _, ok := strings.Cut(f, "x"); ok {
			if v, err := strconv.Atoi(n); err == nil && v > best {
				best = v
			}
		}
	}
	return best
}

// fetchIconFile downloads one candidate href, insists the bytes are an
// image, and lands them in the cache.
func (s *Server) fetchIconFile(ctx context.Context, client *http.Client, bu *url.URL, host, href string) (appIconMeta, error) {
	base := &url.URL{Scheme: "https", Host: host, Path: "/"}
	ref, err := url.Parse(href)
	if err != nil {
		return appIconMeta{}, err
	}
	res := base.ResolveReference(ref)
	if !strings.EqualFold(res.Hostname(), host) {
		return appIconMeta{}, errors.New("icon lives off-host")
	}
	resp, err := iconGet(ctx, client, bu, host, res)
	if err != nil {
		return appIconMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return appIconMeta{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, appIconMax+1))
	if err != nil {
		return appIconMeta{}, err
	}
	if len(b) == 0 || len(b) > appIconMax {
		return appIconMeta{}, errors.New("empty or oversized icon")
	}
	// a catch-all app route answers 200 text/html for any path — only image
	// bytes (declared, or sniffed when the app's content-type is sloppy) count
	ct, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	ct = strings.TrimSpace(strings.ToLower(ct))
	if !strings.HasPrefix(ct, "image/") {
		if ct = http.DetectContentType(b); !strings.HasPrefix(ct, "image/") {
			return appIconMeta{}, errors.New("not an image: " + ct)
		}
	}
	if err := os.MkdirAll(s.appIconsDir(), 0o755); err != nil {
		return appIconMeta{}, err
	}
	// atomic-ish, same as app-data writes: temp in the same dir, rename over
	tmp, err := os.CreateTemp(s.appIconsDir(), ".icon-*")
	if err != nil {
		return appIconMeta{}, err
	}
	if _, err := tmp.Write(b); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.appIconPath(host))
	}
	if err != nil {
		os.Remove(tmp.Name())
		return appIconMeta{}, err
	}
	return appIconMeta{ContentType: ct}, nil
}

// iconGet requests ref's path from the app's backend, presenting the public
// hostname the way the reverse proxy does.
func iconGet(ctx context.Context, client *http.Client, bu *url.URL, host string, ref *url.URL) (*http.Response, error) {
	u := *bu
	u.Path, u.RawPath, u.RawQuery = ref.Path, ref.RawPath, ref.RawQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	return client.Do(req)
}
