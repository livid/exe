// vncbench serves exe's Mac viewer (noVNC from the macos9 sysapp) against a QEMU VNC socket,
// as the daemon's /v1/macos9/console does, with an optional added network delay, and a page
// (bench.html) that counts the frame updates the viewer completes. bench.js drives it in
// headless Chromium and prints frames a second and bytes a second.
//
//	go run ./internal/vncpace/vncbench -sock VNC.sock [-listen 127.0.0.1:7801] [-delay 20ms] [-pace]
//
// -delay holds every chunk that long in each direction: a round trip of twice it, like a
// desk reached over a network. -pace puts the daemon's relay (internal/vncpace) between the
// delay and the emulator, where the daemon has it. Nothing else may be connected to the
// emulator's VNC server while it measures: a second client keeps QEMU's refresh at its
// fastest and hides how a lone viewer is paced.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"exe/internal/vncpace"

	"github.com/coder/websocket"
)

var (
	sock           = flag.String("sock", "", "QEMU VNC unix socket")
	listen         = flag.String("listen", "127.0.0.1:7801", "address to serve on")
	static         = flag.String("static", "/www/exe/internal/server/sysapps/macos9", "the viewer's files")
	page           = flag.String("page", "", "bench.html (default: beside this program's source)")
	delay          = flag.Duration("delay", 0, "added delay in each direction (POST /delay?ms=N changes it)")
	pace           = flag.Bool("pace", false, "relay through internal/vncpace, as the daemon does")
	toVNC, fromVNC atomic.Int64
	delayNow       atomic.Int64 // nanoseconds
)

type chunk struct {
	at   time.Time
	data []byte
}

// relay copies src to dst, each chunk no sooner than -delay after it was read
func relay(dst io.Writer, src io.Reader, count *atomic.Int64) {
	q := make(chan chunk, 4096)
	go func() {
		defer close(q)
		for {
			b := make([]byte, 64<<10)
			n, err := src.Read(b)
			if n > 0 {
				q <- chunk{time.Now().Add(time.Duration(delayNow.Load())), b[:n]}
			}
			if err != nil {
				return
			}
		}
	}()
	for c := range q {
		if d := time.Until(c.at); d > 0 {
			time.Sleep(d)
		}
		if _, err := dst.Write(c.data); err != nil {
			return
		}
		count.Add(int64(len(c.data)))
	}
}

func console(w http.ResponseWriter, r *http.Request) {
	vnc, err := net.Dial("unix", *sock)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer vnc.Close()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"},
		CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	// the network: delayed chunks between the viewer and the daemon's side
	near, far := net.Pipe()
	done := make(chan struct{}, 4)
	go func() { relay(far, conn, &toVNC); done <- struct{}{} }()
	go func() { relay(conn, far, &fromVNC); done <- struct{}{} }()
	// the daemon's side: the pacing relay, or a plain copy as before it
	if *pace {
		go func() { _ = vncpace.Relay(near, vnc); done <- struct{}{} }()
	} else {
		go func() { io.Copy(vnc, near); done <- struct{}{} }()
		go func() { io.Copy(near, vnc); done <- struct{}{} }()
	}
	<-done
	near.Close()
	far.Close()
}

func main() {
	flag.Parse()
	if *sock == "" {
		log.Fatal("-sock is needed")
	}
	if *page == "" {
		*page = "/www/exe/internal/vncpace/vncbench/bench.html"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/console", console)
	mux.HandleFunc("/bench.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, *page)
	})
	delayNow.Store(int64(*delay))
	mux.HandleFunc("/delay", func(w http.ResponseWriter, r *http.Request) {
		ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
		if err != nil || ms < 0 {
			http.Error(w, "ms=N", http.StatusBadRequest)
			return
		}
		delayNow.Store(int64(time.Duration(ms) * time.Millisecond))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"toVNC":%d,"fromVNC":%d}`, toVNC.Load(), fromVNC.Load())
	})
	mux.Handle("/", http.FileServer(http.Dir(*static)))
	log.Printf("serving %s on http://%s/bench.html (delay %v each way, pace %v)", *sock, *listen,
		*delay, *pace)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
