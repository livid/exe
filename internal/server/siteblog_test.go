package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A unit test never reaches the real blog: the homepage draws no Blog
// window unless a test points it at a feed of its own.
func init() { siteBlogFeed = "" }

// testBlogFeed is an RSS feed in the shape the blog writes: an
// <atom:link> before the channel's own <link>, posts newest first but
// one out of order, a title that needs escaping, and an item whose link
// is a script.
const testBlogFeed = `<?xml version="1.0" encoding="UTF-8"?>
<?xml-stylesheet type="text/xsl" href="assets/rss.xsl"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
  <title>exe</title>
  <atom:link href="https://blog.example.com/rss.xml" rel="self" type="application/rss+xml" />
  <link>https://blog.example.com/</link>
  <item><title>Six</title><link>https://blog.example.com/6/</link><pubDate>Fri, 16 Oct 2026 16:36:29 -0700</pubDate>
    <description><![CDATA[<p>The whole post.</p>]]></description></item>
  <item><title>Five &amp; &lt;b&gt;bold&lt;/b&gt;</title><link>https://blog.example.com/5/</link><pubDate>Tue, 13 Oct 2026 16:20:04 -0700</pubDate></item>
  <item><title>Three</title><link>https://blog.example.com/3/</link><pubDate>Sun, 27 Sep 2026 08:35:55 -0700</pubDate></item>
  <item><title>Four</title><link>https://blog.example.com/4/</link><pubDate>Sun, 11 Oct 2026 02:52:32 -0700</pubDate></item>
  <item><title>Script</title><link>javascript:alert(1)</link><pubDate>Mon, 12 Oct 2026 00:00:00 -0700</pubDate></item>
  <item><title>Two</title><link>https://blog.example.com/2/</link><pubDate>Sat, 26 Sep 2026 12:16:20 -0700</pubDate></item>
  <item><title>One</title><link>https://blog.example.com/1/</link><pubDate>Fri, 25 Sep 2026 21:25:04 -0700</pubDate></item>
</channel>
</rss>`

// useTestBlog points the homepage at a feed served by the test, forgets
// any reading before it, and puts both back when the test ends. The
// returned flag fails the feed while it is set.
func useTestBlog(t *testing.T) (reads *atomic.Int32, failing *atomic.Bool) {
	t.Helper()
	reads, failing = new(atomic.Int32), new(atomic.Bool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if failing.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, testBlogFeed)
	}))
	reset := func() {
		siteBlog.Lock()
		siteBlog.data, siteBlog.next, siteBlog.reading = nil, time.Time{}, nil
		siteBlog.Unlock()
	}
	was := siteBlogFeed
	siteBlogFeed = srv.URL + "/rss.xml"
	reset()
	t.Cleanup(func() {
		// a renewal still under way ends before the feed goes back
		siteBlog.Lock()
		done := siteBlog.reading
		siteBlog.Unlock()
		if done != nil {
			<-done
		}
		srv.Close()
		siteBlogFeed = was
		reset()
	})
	return reads, failing
}

// The feed comes back as the five newest posts that can be followed,
// newest first, with the blog's own address from the channel.
func TestSiteBlogFetch(t *testing.T) {
	useTestBlog(t)
	data, err := siteBlogFetch(siteBlogFeed)
	if err != nil {
		t.Fatal(err)
	}
	if data.Home != "https://blog.example.com/" {
		t.Errorf("home %q, want the channel's <link>", data.Home)
	}
	var titles []string
	for _, p := range data.Posts {
		titles = append(titles, p.Title)
	}
	if got, want := strings.Join(titles, ","), "Six,Five & <b>bold</b>,Four,Three,Two"; got != want {
		t.Errorf("posts %s, want %s", got, want)
	}
	if p := data.Posts[0]; p.Link != "https://blog.example.com/6/" || p.Date.Format("Jan 2, 2006") != "Oct 16, 2026" {
		t.Errorf("first post %+v", p)
	}
}

// The page draws the window from the feed, escaped, and the window's
// links are the blog's.
func TestSiteBlogWindow(t *testing.T) {
	h := SiteHandler(nil)
	page := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://exe.example.com/", nil))
		return rec.Body.String()
	}
	if strings.Contains(page(), `class="posts"`) {
		t.Fatal("a Blog window with no feed to read")
	}
	reads, _ := useTestBlog(t)
	body := page()
	for _, want := range []string{
		`<div class="title">Blog</div>`,
		`<a href="https://blog.example.com/6/" target="_blank" rel="noopener">Six</a><span>Oct 16, 2026</span>`,
		`Five &amp; &lt;b&gt;bold&lt;/b&gt;`,
		`<a href="https://blog.example.com/" target="_blank" rel="noopener">All posts</a>`,
		`<a href="` + siteBlogFeed + `" target="_blank" rel="noopener">RSS feed</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not carry %s", want)
		}
	}
	if strings.Contains(body, ">One<") || strings.Contains(body, "javascript:") {
		t.Error("the window shows more than the five newest posts it can link")
	}
	if i, j := strings.Index(body, `class="posts"`), strings.Index(body, `class="foot"`); i < 0 || i > j {
		t.Error("the Blog window is not the page's last")
	}
	page()
	if n := reads.Load(); n != 1 {
		t.Errorf("two pages read the feed %d times, want once", n)
	}
}

// A feed that fails keeps the window it last drew, and is asked again
// a minute later rather than on every page.
func TestSiteBlogKeepsLastReading(t *testing.T) {
	reads, failing := useTestBlog(t)
	if d := siteBlogNow(); d == nil || len(d.Posts) != 5 {
		t.Fatalf("first reading: %+v", d)
	}
	failing.Store(true)
	siteBlog.Lock()
	siteBlog.next = time.Now().Add(-time.Second) // the reading is past its time
	siteBlog.Unlock()
	if d := siteBlogNow(); d == nil || len(d.Posts) != 5 {
		t.Fatalf("a page drawn while the feed is renewed: %+v", d)
	}
	waitBlogRead(t)
	if d := siteBlogNow(); d == nil || len(d.Posts) != 5 {
		t.Fatalf("after a failed reading: %+v", d)
	}
	siteBlog.Lock()
	next := time.Until(siteBlog.next)
	siteBlog.Unlock()
	if next <= 0 || next > siteBlogRetry {
		t.Errorf("a failed reading is tried again in %v, want within %v", next, siteBlogRetry)
	}
	if n := reads.Load(); n != 2 {
		t.Errorf("the feed was read %d times, want twice", n)
	}
}

// waitBlogRead waits for the reading under way, if any, to end.
func waitBlogRead(t *testing.T) {
	t.Helper()
	siteBlog.Lock()
	done := siteBlog.reading
	siteBlog.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the feed's reading never ended")
	}
}
