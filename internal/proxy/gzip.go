package proxy

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// compressMin is the smallest body worth compressing: under it gzip's
// header and trailer eat most of what it would save.
const compressMin = 1024

var gzipWriters = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// Gzip compresses the responses a client takes gzip for when the backend
// sent them as they are. Every request up the tunnel comes from Cloudflare
// with "Accept-Encoding: gzip", and Cloudflare compresses again for the
// visitor whatever it gets; what this saves is the upload from this
// machine, where a stats page of HTML went up the tunnel at four times its
// gzip size.
//
// It leaves alone what compressing would break or not shrink: a byte range
// (the bytes asked for are the file's, not a gzip's), a WebSocket upgrade,
// a HEAD, a body the backend already encoded or marked no-transform, a
// stream of server-sent events, a type that is not text, and a body under
// compressMin.
func Gzip(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || r.Header.Get("Upgrade") != "" ||
			!acceptsGzip(r.Header.Values("Accept-Encoding")) {
			h.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		// Not deferred: a handler that panics (ReverseProxy does, to abort
		// a broken copy) must not have a 200 and a gzip trailer sent for it.
		h.ServeHTTP(gw, r)
		gw.close()
	})
}

// acceptsGzip reads Accept-Encoding: gzip, unless its q is 0.
func acceptsGzip(values []string) bool {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			coding, params, _ := strings.Cut(part, ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
				continue
			}
			if q, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
				f, err := strconv.ParseFloat(q, 64)
				return err == nil && f > 0
			}
			return true
		}
	}
	return false
}

// compressible is a type gzip shrinks: text, and the formats that are text
// under another name. Server-sent events are text but a stream, each event
// due the moment it is written.
func compressible(contentType string) bool {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil || base == "text/event-stream" {
		return false
	}
	if strings.HasPrefix(base, "text/") || strings.HasSuffix(base, "+json") || strings.HasSuffix(base, "+xml") {
		return true
	}
	switch base {
	case "application/json", "application/javascript", "application/x-javascript", "application/xml",
		"application/wasm", "font/ttf", "font/otf":
		return true
	}
	return false
}

// gzipWriter holds the status and the first compressMin bytes until it
// knows whether the response is worth compressing, then sends the headers
// for what it chose.
type gzipWriter struct {
	http.ResponseWriter
	code    int
	buf     []byte
	started bool
	gz      *gzip.Writer // set when the body is being compressed
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) WriteHeader(code int) {
	if code < 200 { // 103 Early Hints and the like go out as they come
		g.ResponseWriter.WriteHeader(code)
		return
	}
	if g.code == 0 {
		g.code = code
	}
}

// eligible says whether the response's status and headers allow gzip.
// With no Content-Type set it names the type from sample, as net/http
// would have, since net/http cannot tell it from gzipped bytes.
func (g *gzipWriter) eligible(sample []byte) bool {
	h := g.Header()
	switch {
	case g.code == http.StatusNoContent, g.code == http.StatusPartialContent, g.code == http.StatusNotModified:
		return false
	case h.Get("Content-Encoding") != "", strings.Contains(h.Get("Cache-Control"), "no-transform"):
		return false
	}
	if n, err := strconv.ParseInt(h.Get("Content-Length"), 10, 64); err == nil && n < compressMin {
		return false
	}
	if ct, ok := h["Content-Type"]; !ok {
		if len(sample) == 0 {
			return false
		}
		h.Set("Content-Type", http.DetectContentType(sample))
	} else if len(ct) == 0 { // the handler asked for no Content-Type at all
		return false
	}
	return compressible(h.Get("Content-Type"))
}

func (g *gzipWriter) start(compress bool) error {
	g.started = true
	if compress {
		h := g.Header()
		h.Del("Content-Length")
		h.Del("Accept-Ranges")
		h.Set("Content-Encoding", "gzip")
		if !strings.Contains(strings.ToLower(strings.Join(h.Values("Vary"), ",")), "accept-encoding") {
			h.Add("Vary", "Accept-Encoding")
		}
		// The gzipped body is not the bytes the tag was made for.
		if et := h.Get("Etag"); et != "" && !strings.HasPrefix(et, "W/") {
			h.Set("Etag", "W/"+et)
		}
		g.gz = gzipWriters.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(g.code)
	buf := g.buf
	g.buf = nil
	if len(buf) == 0 {
		return nil
	}
	var err error
	if g.gz != nil {
		_, err = g.gz.Write(buf)
	} else {
		_, err = g.ResponseWriter.Write(buf)
	}
	return err
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	if g.started {
		if g.gz != nil {
			return g.gz.Write(p)
		}
		return g.ResponseWriter.Write(p)
	}
	if g.code == 0 {
		g.code = http.StatusOK
	}
	if !g.eligible(p) {
		if err := g.start(false); err != nil {
			return 0, err
		}
		return g.ResponseWriter.Write(p)
	}
	g.buf = append(g.buf, p...)
	if len(g.buf) >= compressMin {
		if err := g.start(true); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush sends what is held and what gzip has, so a response that streams
// (ReverseProxy flushes after every read of a body of unknown length)
// still arrives as it is written. A flush before compressMin bytes cannot
// wait to learn the size, and compresses whatever is eligible.
func (g *gzipWriter) Flush() {
	if !g.started {
		if g.code == 0 {
			g.code = http.StatusOK
		}
		if g.start(g.eligible(g.buf)) != nil {
			return
		}
	}
	if g.gz != nil {
		g.gz.Flush()
	}
	http.NewResponseController(g.ResponseWriter).Flush()
}

// close sends a body that stayed under compressMin as it is, and ends the
// gzip stream.
func (g *gzipWriter) close() {
	if !g.started && g.code != 0 {
		g.start(false)
	}
	if g.gz != nil {
		g.gz.Close()
		gzipWriters.Put(g.gz)
		g.gz = nil
	}
}
