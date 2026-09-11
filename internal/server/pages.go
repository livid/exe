package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
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
const pageTicketTTL = 10 * time.Minute

// pageSandbox mirrors the sandbox attribute of the desktop's page frame.
const pageSandbox = "sandbox allow-scripts allow-popups allow-forms allow-modals"

type pageTicket struct {
	path string // workspace-relative, slash-separated
	exp  time.Time
}

func isPagePath(rel string) bool {
	n := strings.ToLower(rel)
	return strings.HasSuffix(n, ".html") || strings.HasSuffix(n, ".htm")
}

// handlePageTicket (POST /v1/pages, {"path"}) mints a ticket for one
// Workspace page and answers with the URL to load it from, plus its size
// for the window's status line.
func (s *Server) handlePageTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
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
	tok := rand.Text()
	now := time.Now()
	s.pageMu.Lock()
	if s.pageTickets == nil {
		s.pageTickets = map[string]pageTicket{}
	}
	for k, t := range s.pageTickets {
		if now.After(t.exp) {
			delete(s.pageTickets, k)
		}
	}
	s.pageTickets[tok] = pageTicket{path: req.Path, exp: now.Add(pageTicketTTL)}
	s.pageMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"url":  "/pages/" + tok + "/" + url.PathEscape(path.Base(req.Path)),
		"size": info.Size(),
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
	if !ok || time.Now().After(t.exp) || path.Base(t.path) != r.PathValue("name") {
		http.Error(w, "This page's link has expired. Open it from the Workspace again.", http.StatusNotFound)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageSandbox)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer") // the ticket URL stays out of Referer headers
	http.ServeContent(w, r, "", info.ModTime(), f)
}
