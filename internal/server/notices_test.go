package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"exe/internal/config"
)

func noticePost(s *Server, body, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/notices", strings.NewReader(body))
	req.RemoteAddr, req.Host = remote, "127.0.0.1:7777"
	rec := httptest.NewRecorder()
	s.handleNoticePost(rec, req)
	return rec
}

func noticeList(t *testing.T, s *Server) []notice {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleNoticesGet(rec, httptest.NewRequest("GET", "/v1/notices", nil))
	var resp struct {
		Notices []notice `json:"notices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Notices
}

func TestNoticesStayUntilClosed(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	if got := noticeList(t, s); got == nil || len(got) != 0 {
		t.Fatalf("a fresh daemon lists %#v, want an empty list", got)
	}

	// an open desk hears every change, the whole list
	ch := make(chan []byte, 8)
	s.appEv.mu.Lock()
	s.appEv.subs = map[chan []byte]struct{}{ch: {}}
	s.appEv.mu.Unlock()

	if rec := noticePost(s, `{"id":"hub-watch-limit","title":"Claude hit its weekly limit","body":"Holding 1 post."}`, "127.0.0.1:5000"); rec.Code != http.StatusOK {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}
	var ev struct {
		App     string   `json:"app"`
		Notices []notice `json:"notices"`
	}
	json.Unmarshal(<-ch, &ev)
	if ev.App != "@notices" || len(ev.Notices) != 1 || ev.Notices[0].Title != "Claude hit its weekly limit" || ev.Notices[0].Time == 0 {
		t.Fatalf("desk heard %+v", ev.Notices)
	}

	// said again under its id: changed in place, not stacked
	noticePost(s, `{"id":"hub-watch-limit","title":"Claude hit its weekly limit","body":"Holding 2 posts."}`, "127.0.0.1:5000")
	rec := noticePost(s, `{"title":"Another"}`, "[::1]:5000")
	var made struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &made)
	list := noticeList(t, s)
	if len(list) != 2 || list[0].Body != "Holding 2 posts." || list[1].ID != made.ID || made.ID == "" {
		t.Fatalf("list after a repeat and a new one: %+v (new id %q)", list, made.ID)
	}

	// it survives a restart: a new server on the same state folder
	s2 := New(&config.Config{}, nil, nil, "", s.StateDir)
	if got := noticeList(t, s2); len(got) != 2 {
		t.Fatalf("after a restart: %+v", got)
	}

	// the close box takes it off every desk
	del := httptest.NewRequest("DELETE", "/v1/notices/hub-watch-limit", nil)
	del.SetPathValue("id", "hub-watch-limit")
	rec = httptest.NewRecorder()
	s.handleNoticeDelete(rec, del)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if list := noticeList(t, s); len(list) != 1 || list[0].ID != made.ID {
		t.Fatalf("after closing: %+v", list)
	}
	rec = httptest.NewRecorder()
	s.handleNoticeDelete(rec, del)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("closing it twice: %d, want 404", rec.Code)
	}
}

func TestNoticesRefuseOtherMachinesAndBadInput(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	if rec := noticePost(s, `{"title":"from afar"}`, "192.168.1.20:5000"); rec.Code != http.StatusForbidden {
		t.Fatalf("a LAN caller: %d, want 403", rec.Code)
	}
	for _, body := range []string{`{"body":"no title"}`, `{"id":"Bad Id","title":"t"}`, `{"title":"` + strings.Repeat("x", noticeTitleMax+1) + `"}`} {
		if rec := noticePost(s, body, "127.0.0.1:5000"); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", body, rec.Code)
		}
	}
	for i := 0; i < noticeKeep+3; i++ {
		noticePost(s, `{"title":"n"}`, "127.0.0.1:5000")
	}
	if got := noticeList(t, s); len(got) != noticeKeep {
		t.Fatalf("%d notices up, want the %d cap", len(got), noticeKeep)
	}
}
