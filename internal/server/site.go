package server

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"image/gif"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	stats "github.com/livid/exe-stats"
	_ "modernc.org/sqlite"
)

// The project homepage — the public front door at https://<sub>.<domain>,
// as opposed to the desktop, which is this daemon's own UI. It is one
// static page in the desktop's Platinum blocks, and the daemon serves it
// out of the binary: a route whose backend is the builtin name below goes
// here instead of to a VM, so publishing it is `exe site` and nothing on
// any disk has to be kept in step.
//
// The page's icons are the desktop's own (uiFS), served here so the site
// host is self-contained and the repository keeps one copy of each.

//go:embed site/index.html site/site.css site/screenshot.png site/badge.gif site/badge.html site/install.sh site/install.ps1
var siteFS embed.FS

// SiteBackend is the proxy backend that names this handler. `exe site`
// writes it into the route table; the proxy serves it from the daemon.
const SiteBackend = "exe:site"

// siteBuild names this binary's copy of the site's assets: a short hash
// over the bytes of every file a page links to. The pages link them
// under /v<build>/, so a rebuild that changes one changes its address —
// which is the only way to be sure a reader sees it. The edge in front
// of this site raises the lifetime of a stylesheet or a picture to four
// hours whatever the daemon answers (seen on exe.v2core.com: a
// no-cache stylesheet came back max-age=14400), so a stamped address is
// what makes a change arrive at once. The pages themselves are no-cache
// and are not cached, so they always name the current stamp.
var siteBuild = func() string {
	sum := sha256.New()
	for _, n := range []string{"/site.css", "/icon.svg", "/icon-192.png", "/screenshot.png", "/badge.gif"} {
		f := siteFiles[n]
		var b []byte
		switch {
		case f.bytes != nil:
			b = f.bytes
		case f.fromUI:
			b, _ = uiFS.ReadFile(f.name)
		default:
			b, _ = f.fs.ReadFile(f.name)
		}
		sum.Write(b)
	}
	return hex.EncodeToString(sum.Sum(nil))[:10]
}()

// siteStamped is the address a page links an asset by: /v<build>/<name>.
func siteStamped(name string) string { return "/v" + siteBuild + name }

// siteStamp matches an address a page linked, whatever build made it.
var siteStamp = regexp.MustCompile(`^/v[0-9a-f]{6,32}(/.+)$`)

// siteFile is one file of the homepage: where its bytes come from, what
// it is, and how long a browser and the edge may keep it.
type siteFile struct {
	fs     embed.FS
	name   string
	kind   string
	maxAge string
	fromUI bool
	bytes  []byte // a file the daemon puts together rather than embeds
}

// siteCSS is the stylesheet every page of the site links: the Platinum
// chrome from github.com/livid/exe-stats, which the stats desk is drawn
// in too, and then this site's own styles. One chrome, written once —
// and TestSiteChromeFollowsTheDesktop holds it to what the desktop says.
var siteCSS = []byte(stats.ChromeCSS() + "\n" + string(mustSiteFile("site/site.css")))

// siteRobots is the site's robots.txt: the page and the documentation
// are open to crawlers, the stats desk and its JSON are not — every
// filter, range and view there is a link, an endless space, and on
// 2026-09-24 one GPTBot address fetched those two paths some 3,000 times
// an hour for a day (the hub, which draws the same desk, saw twice that).
var siteRobots = []byte("User-agent: *\nDisallow: /stats\nDisallow: /v1/stats\n")

var siteFiles = map[string]siteFile{
	"/":               {fs: siteFS, name: "site/index.html", kind: "text/html; charset=utf-8", maxAge: "no-cache"},
	"/site.css":       {bytes: siteCSS, kind: "text/css; charset=utf-8", maxAge: "no-cache"},
	"/screenshot.png": {fs: siteFS, name: "site/screenshot.png", kind: "image/png", maxAge: "max-age=14400"},
	"/icon.svg":       {name: "ui/icon.svg", kind: "image/svg+xml", maxAge: "max-age=14400", fromUI: true},
	"/icon-192.png":   {name: "ui/icon-192.png", kind: "image/png", maxAge: "max-age=14400", fromUI: true},
	"/robots.txt":     {bytes: siteRobots, kind: "text/plain; charset=utf-8", maxAge: "max-age=14400"},
	// the badge other pages link: its plain address is the one they are
	// given, so it is kept no longer than the screenshot and a new
	// drawing reaches them within hours
	"/badge.gif": {fs: siteFS, name: "site/badge.gif", kind: "image/gif", maxAge: "max-age=14400"},
	// the installer: `curl -fsSL https://<site>/install.sh | sh`. It is
	// plain text, so a browser shows what it would run; it names no
	// version (it asks GitHub for the latest release when it runs), so
	// the copy in this binary serves every release.
	"/install.sh": {fs: siteFS, name: "site/install.sh", kind: "text/plain; charset=utf-8", maxAge: "no-cache"},
	// and the one for Windows: `irm https://<site>/install.ps1 | iex`
	"/install.ps1": {fs: siteFS, name: "site/install.ps1", kind: "text/plain; charset=utf-8", maxAge: "no-cache"},
}

// SiteStats opens the homepage's own analytics — github.com/livid/exe-stats,
// the same package that draws the hub's /stats, over a database of this
// node's own (stats.db in the state directory, nothing to do with the
// hub's). A failure here only means the page is not counted.
func SiteStats(stateDir string) *stats.Stats {
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "stats.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Printf("site: stats: %v (the homepage will not be counted)", err)
		return nil
	}
	db.SetMaxOpenConns(1)
	an, err := stats.New(db, stats.Options{
		Location:  time.Local,
		Title:     "Stats · exe",
		HomeURL:   "/",
		HomeLabel: "Back to the homepage",
		PathLabel: siteLabel,
	})
	if err != nil {
		log.Printf("site: stats: %v (the homepage will not be counted)", err)
		db.Close()
		return nil
	}
	return an // the caller starts Run
}

// siteOnline is how many are reading the site: the visitors with a page
// in the last five minutes, and the person being served this request,
// whose visit is counted a moment after it. A crawler belongs to no
// human number and is told what the number really is.
func siteOnline(an *stats.Stats, r *http.Request, kind string) int {
	if an == nil {
		return 0
	}
	n := an.Online()
	if _, _, _, bot := stats.Classify(r.UserAgent()); !bot && n < 1 && an.Wanted(r, kind) {
		n = 1
	}
	return n
}

// siteLabel names a path in the stats: the homepage as itself and a
// documentation page by its title, so the report reads as the site does.
func siteLabel(path string) string {
	if path == "/" {
		return "/ (the homepage)"
	}
	if path == "/badge/" {
		return "The badge"
	}
	if slug, ok := strings.CutPrefix(path, "/docs/"); ok {
		if slug == "" {
			return "Docs: all of them"
		}
		if ch, ok := strings.CutPrefix(slug, "using/"); ok {
			if i, ok := manualChapterAt(ch); ok {
				return "Manual: " + manualChapters[i].Title
			}
		}
		for _, d := range siteDocs {
			if d.Slug == slug {
				return "Docs: " + d.Title
			}
		}
	}
	return path
}

// sitePageTmpl is the page with the one thing about it that is not the
// same for everyone: how many are reading it. It is rendered per visit
// (the page is revalidated every time anyway), so the number is there in
// the first paint and nothing moves afterwards.
var sitePageTmpl = template.Must(template.New("site").Parse(string(mustSiteFile("site/index.html"))))

func mustSiteFile(name string) []byte {
	b, err := siteFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

// siteStatsData is the stats desk with what this site's own document
// needs around it.
type siteStatsData struct {
	P     *stats.Page
	Build string
}

// siteStatsTmpl draws the stats desk in the site's own chrome rather
// than the one the package carries for a host that has none: the window,
// its striped bar and its close box are the desktop's, here as on every
// other page of the site, and there is one stylesheet for all of them.
var siteStatsTmpl = template.Must(template.Must(template.New("statsdoc").
	Funcs(stats.Funcs()).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>{{.P.Title}}{{with .P.Host}} · {{.}}{{end}}</title>
<meta name="robots" content="noindex, nofollow">
<meta name="theme-color" content="#dddddd">
<link rel="icon" href="/v{{.Build}}/icon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/v{{.Build}}/icon-192.png">
<link rel="stylesheet" href="/v{{.Build}}/site.css">
{{template "statshead" .P}}{{template "statscss"}}</head>
<body>
{{template "stats" .P}}
</body>
</html>
`)).Parse(stats.TemplateHTML()))

// siteStatsHandler answers with the desk in this site's chrome.
func siteStatsHandler(an *stats.Stats) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := an.PageData(r)
		if err != nil {
			log.Printf("site: stats: %v", err)
			http.Error(w, "the stats could not be read", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if err := siteStatsTmpl.Execute(w, siteStatsData{P: p, Build: siteBuild}); err != nil {
			log.Printf("site: stats: %v", err)
		}
	}
}

// SiteHandler serves the homepage on the proxy listener, its readers
// counted and read back at /stats. The page itself is revalidated on
// every visit, so a new binary shows at once; its pictures may sit in a
// cache for four hours.
func SiteHandler(an *stats.Stats) http.Handler {
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sitePage(w, r, an) })
	if an == nil {
		// without stats the site is the page and its documentation, and
		// nothing is counted
		mux := http.NewServeMux()
		docs := siteDocsHandler(nil)
		mux.Handle("GET /docs/{$}", docs)
		mux.Handle("GET /docs/{page}", docs)
		mux.Handle("GET /docs/using/{chapter}", docs)
		mux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
		mux.Handle("GET /badge/{$}", siteBadgeHandler(nil))
		mux.Handle("GET /badge", http.RedirectHandler("/badge/", http.StatusMovedPermanently))
		mux.Handle("/", page)
		return mux
	}
	mux := http.NewServeMux()
	// the pages are counted; the stylesheet, the pictures and the icons
	// are not a visit
	mux.Handle("GET /{$}", an.Counted("home", page))
	docs := siteDocsHandler(an)
	mux.Handle("GET /docs/{$}", an.Counted("docs", docs))
	mux.Handle("GET /docs/{page}", an.Counted("docs", docs))
	mux.Handle("GET /docs/using/{chapter}", an.Counted("docs", docs))
	mux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
	mux.Handle("GET /badge/{$}", an.Counted("badge", siteBadgeHandler(an)))
	mux.Handle("GET /badge", http.RedirectHandler("/badge/", http.StatusMovedPermanently))
	mux.Handle("GET /stats", siteStatsHandler(an))
	mux.Handle("GET /v1/stats", an.JSONHandler())
	mux.Handle("/", page)
	return mux
}

func sitePage(w http.ResponseWriter, r *http.Request, an *stats.Stats) {
	{
		// A bookmark of /index.html is the homepage under a second name.
		// It used to be served as an alias, which meant it arrived through
		// the fallback and was never counted; counting it there instead
		// would split the homepage into two rows in the report. So it is
		// a redirect, and the query goes with it — the navigation lands on
		// "/" as a document fetch and counts once, campaign and all.
		if r.URL.Path == "/index.html" {
			to := *r.URL
			to.Path = "/"
			http.Redirect(w, r, to.RequestURI(), http.StatusMovedPermanently)
			return
		}
		// an asset asked for under a build's stamp is that build's copy
		// for good: the address changes when the bytes do, so it may be
		// kept for as long as anyone likes
		path, stamped := r.URL.Path, false
		if m := siteStamp.FindStringSubmatch(path); m != nil {
			path, stamped = m[1], true
		}
		f, ok := siteFiles[path]
		if !ok {
			http.Error(w, "exe site: no such page", http.StatusNotFound)
			return
		}
		var b []byte
		var err error
		switch {
		case f.bytes != nil:
			b = f.bytes
		case f.fromUI:
			b, err = uiFS.ReadFile(f.name)
		default:
			b, err = f.fs.ReadFile(f.name)
		}
		if err != nil {
			http.Error(w, "exe site: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", f.kind)
		if stamped {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", f.maxAge)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if f.name != "site/index.html" {
			w.Write(b)
			return
		}
		// how many are reading: the visitors with a page in the last five
		// minutes. A person reading this very request is one of them — it
		// is being served and will be counted a moment later — so a reader
		// is never told that nobody is here. A crawler is counted as a
		// crawl and belongs to no human number, so it is told what the
		// number really is, as is a curl that counts for nothing.
		if err := sitePageTmpl.Execute(w, struct {
			Online int
			Build  string
			Blog   *siteBlogData
		}{siteOnline(an, r, "home"), siteBuild, siteBlogNow()}); err != nil {
			log.Printf("site: %v", err)
		}
	}
}

// The badge: an 88×31 animated GIF (site/badge.gif, drawn by
// site/badge.py) at the foot of the homepage, for other pages to link
// here with, and a page of its own at /badge/ that hands out the HTML and
// the Markdown to do it.

var siteBadgeTmpl = template.Must(template.New("badge").Parse(string(mustSiteFile("site/badge.html"))))

// siteBadgeFacts are what the page's status line says of the GIF, read
// off the file itself so a new drawing is never described as the old.
var siteBadgeFrames, siteBadgeSize = func() (int, string) {
	b := mustSiteFile("site/badge.gif")
	g, err := gif.DecodeAll(bytes.NewReader(b))
	if err != nil {
		panic("site/badge.gif: " + err.Error())
	}
	return len(g.Image), fmt.Sprintf("%.1f KB", float64(len(b))/1000)
}()

// siteBadgeHandler serves /badge/.
func siteBadgeHandler(an *stats.Stats) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := siteBadgeTmpl.Execute(w, struct {
			Build, Size    string
			Frames, Online int
		}{siteBuild, siteBadgeSize, siteBadgeFrames, siteOnline(an, r, "badge")}); err != nil {
			log.Printf("site: badge: %v", err)
		}
	}
}
