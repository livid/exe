package server

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// The homepage's last window: the newest posts on the project's blog,
// read from its RSS feed. The daemon reads the feed and draws the posts
// into the page, so they are there in the first paint and nothing on the
// page moves after it; a reader's browser never asks the blog for
// anything. The feed is read only when the homepage is served — a daemon
// that publishes no site never reaches the blog — and a reading is kept
// for a quarter of an hour, so a busy page asks the blog four times an
// hour.

// siteBlogFeed is the feed the window reads; "" draws no window (the
// unit tests, which never reach the real blog).
var siteBlogFeed = "https://blog.v2core.com/rss.xml"

const (
	siteBlogShown = 5                // the newest five
	siteBlogTTL   = 15 * time.Minute // how long a reading is kept
	siteBlogRetry = time.Minute      // how soon a failed one is tried again
	siteBlogWait  = 2 * time.Second  // how long the first reader waits for the first reading
)

var siteBlogClient = &http.Client{Timeout: 10 * time.Second}

type siteBlogPost struct {
	Title, Link string
	Date        time.Time // zero when the feed gave none it could be read by
}

// siteBlogData is what the page's window is drawn from.
type siteBlogData struct {
	Home, Feed string
	Posts      []siteBlogPost
}

// siteBlog is the feed as last read. Posts is replaced whole, never
// changed in place, so a page may draw from the slice it was handed.
var siteBlog struct {
	sync.Mutex
	data    *siteBlogData
	next    time.Time     // when to read the feed again
	reading chan struct{} // closed when the reading under way ends; nil when none is
}

// siteBlogNow is the window's posts, or nil for no window: nothing read
// yet, or a feed with no posts in it. A reading past its time is renewed
// behind the page, which draws the one it has. Only the first reader
// after a start waits for the blog, and not for long.
func siteBlogNow() *siteBlogData {
	if siteBlogFeed == "" {
		return nil
	}
	siteBlog.Lock()
	data, done := siteBlog.data, siteBlog.reading
	if done == nil && time.Now().After(siteBlog.next) {
		done = make(chan struct{})
		siteBlog.reading = done
		go siteBlogRead(siteBlogFeed, done)
	}
	siteBlog.Unlock()
	if data == nil && done != nil {
		select {
		case <-done:
		case <-time.After(siteBlogWait):
		}
		siteBlog.Lock()
		data = siteBlog.data
		siteBlog.Unlock()
	}
	if data == nil || len(data.Posts) == 0 {
		return nil
	}
	return data
}

// siteBlogRead reads the feed once and keeps what it found. A failure
// keeps the reading before it, so the window shows the last posts it
// knew rather than going away while the blog is down.
func siteBlogRead(feed string, done chan struct{}) {
	data, err := siteBlogFetch(feed)
	siteBlog.Lock()
	if err != nil {
		log.Printf("site: blog: %v", err)
		siteBlog.next = time.Now().Add(siteBlogRetry)
	} else {
		siteBlog.data, siteBlog.next = data, time.Now().Add(siteBlogTTL)
	}
	siteBlog.reading = nil
	siteBlog.Unlock()
	close(done)
}

// siteBlogFetch reads an RSS 2.0 feed: its newest posts, each a title,
// a link and the day it went up.
func siteBlogFetch(feed string) (*siteBlogData, error) {
	req, err := http.NewRequest("GET", feed, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "exe")
	resp, err := siteBlogClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", feed, resp.Status)
	}
	var rss struct {
		// the channel's own <link> is the blog; an <atom:link> beside it,
		// which has the same local name, is empty and is passed over
		Links []string `xml:"channel>link"`
		Items []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			PubDate string `xml:"pubDate"`
		} `xml:"channel>item"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rss); err != nil {
		return nil, fmt.Errorf("%s: %v", feed, err)
	}
	data := &siteBlogData{Feed: feed}
	for _, l := range rss.Links {
		if l = strings.TrimSpace(l); siteBlogWebLink(l) {
			data.Home = l
			break
		}
	}
	if data.Home == "" {
		u, _ := url.Parse(feed)
		data.Home = u.ResolveReference(&url.URL{Path: "/"}).String()
	}
	for _, it := range rss.Items {
		p := siteBlogPost{Title: strings.TrimSpace(it.Title), Link: strings.TrimSpace(it.Link)}
		if p.Title == "" || !siteBlogWebLink(p.Link) {
			continue
		}
		d := strings.TrimSpace(it.PubDate)
		for _, layout := range []string{time.RFC1123Z, time.RFC1123} {
			if t, err := time.Parse(layout, d); err == nil {
				p.Date = t
				break
			}
		}
		data.Posts = append(data.Posts, p)
	}
	// newest first, whatever order the feed keeps
	slices.SortStableFunc(data.Posts, func(a, b siteBlogPost) int { return b.Date.Compare(a.Date) })
	if len(data.Posts) > siteBlogShown {
		data.Posts = data.Posts[:siteBlogShown]
	}
	return data, nil
}

// siteBlogWebLink is an address a reader can follow: a web page, never a
// script or a file on their own machine.
func siteBlogWebLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
