package server

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// Rendered Workspace pages. The desktop shows an .html from the Workspace
// inside a sandboxed frame (no allow-same-origin, so the page cannot reach
// the desktop's token). A blob: URL served that until the page's own links
// broke: Chromium refuses a fragment navigation — a #section link,
// location.hash — inside a sandboxed blob document, since the frame's
// opaque origin may not "load" a blob of the desktop's origin again. A real
// URL on this origin has no such trouble, but /v1/workspace wants the token
// (which must not land in a URL the page can read) and serves attachments.
// So the desktop mints a ticket: random, bound to one file, good for a few
// minutes — long enough for a reload or a Back inside the frame — and GET
// /pages/<ticket>/<name> serves that file as a page, under a CSP sandbox
// that keeps the document off this origin even when the URL is opened at
// top level. A leaked ticket exposes nothing but that one file, which the
// page holding the URL already has.
//
// A page from the web — a hub embed the Hub app hands over through the app
// bridge — takes the same road: the desktop fetches it bare (no token, no
// cookies), posts its text here, and the ticket holds that text instead of
// a path. The hub never serves HTML as a document, and srcdoc would break
// the page's #anchor links the way it did for the Workspace.
const pageTicketTTL = 10 * time.Minute

// webPageMax is a handed-over page's cap — the hub's embed cap — and
// webPagesHeld bounds the text all live tickets keep in memory; past it the
// oldest handed-over pages go first (reopening one mints a fresh ticket).
const (
	webPageMax   = 8 << 20
	webPagesHeld = 64 << 20
)

// pageSandbox mirrors the sandbox attribute of the desktop's page frame.
const pageSandbox = "sandbox allow-scripts allow-popups allow-forms allow-modals"

type pageTicket struct {
	path string // workspace-relative, slash-separated; "" for a handed-over page
	name string // the file name the URL ends in
	html []byte // a handed-over page's text
	exp  time.Time
}

func isPagePath(rel string) bool {
	n := strings.ToLower(rel)
	return strings.HasSuffix(n, ".html") || strings.HasSuffix(n, ".htm")
}

// handlePageTicket mints a ticket for one page and answers with the URL to
// load it from, plus its size for the window's status line. POST /v1/pages
// takes {"path"} for a Workspace page, or {"name", "html"} for a page the
// desktop fetched from the web itself.
func (s *Server) handlePageTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string  `json:"path"`
		Name string  `json:"name"`
		HTML *string `json:"html"`
	}
	// JSON escaping at most doubles the text
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*webPageMax+4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var t pageTicket
	var size int64
	if req.HTML != nil {
		if len(*req.HTML) > webPageMax {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("pages are capped at %dMB", webPageMax>>20))
			return
		}
		name := path.Base(strings.ReplaceAll(req.Name, "\\", "/"))
		if name == "." || name == "/" {
			name = "page.html"
		}
		t = pageTicket{name: name, html: []byte(*req.HTML)}
		size = int64(len(t.html))
	} else {
		if !isPagePath(req.Path) {
			writeErr(w, http.StatusBadRequest, errors.New("not a web page"))
			return
		}
		p, err := scopedPath(s.workspaceDir(), req.Path)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			writeErr(w, http.StatusNotFound, errors.New("not found"))
			return
		}
		t = pageTicket{path: req.Path, name: path.Base(req.Path)}
		size = info.Size()
	}
	tok := rand.Text()
	now := time.Now()
	t.exp = now.Add(pageTicketTTL)
	s.pageMu.Lock()
	if s.pageTickets == nil {
		s.pageTickets = map[string]pageTicket{}
	}
	held := len(t.html)
	for k, o := range s.pageTickets {
		if now.After(o.exp) {
			delete(s.pageTickets, k)
		} else {
			held += len(o.html)
		}
	}
	for held > webPagesHeld {
		oldest := ""
		for k, o := range s.pageTickets {
			if o.html != nil && (oldest == "" || o.exp.Before(s.pageTickets[oldest].exp)) {
				oldest = k
			}
		}
		if oldest == "" {
			break
		}
		held -= len(s.pageTickets[oldest].html)
		delete(s.pageTickets, oldest)
	}
	s.pageTickets[tok] = t
	s.pageMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"url":  "/pages/" + tok + "/" + url.PathEscape(t.name),
		"size": size,
	})
}

// handlePage (GET /pages/{ticket}/{name}) serves the page a ticket names.
// The name is the file's own, so the page sees a sensible location; it has
// to match. No bearer token: the ticket is the authorization, and auth()
// leaves /pages/ alone.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	s.pageMu.Lock()
	t, ok := s.pageTickets[r.PathValue("ticket")]
	s.pageMu.Unlock()
	if !ok || time.Now().After(t.exp) || t.name != r.PathValue("name") {
		http.Error(w, "This page's link has expired. Open the page again.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageSandbox)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer") // the ticket URL stays out of Referer headers
	if t.html != nil {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(t.html))
		return
	}
	p, err := scopedPath(s.workspaceDir(), t.path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}
