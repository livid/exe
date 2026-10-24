package vncpace

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// a pixel format of 32 bits, depth 24, true colour, 8 bits a colour
var format32 = []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}

type fakeServer struct {
	t        *testing.T
	conn     net.Conn
	requests chan []byte
	msgs     chan []byte // every other message from the viewer side
}

// startPair: a viewer conn and a fake server behind a relay
func startPair(t *testing.T) (viewer net.Conn, s *fakeServer, done chan error) {
	vSide, rView := net.Pipe()
	rServer, sSide := net.Pipe()
	s = &fakeServer{t: t, conn: sSide, requests: make(chan []byte, 64), msgs: make(chan []byte, 64)}
	done = make(chan error, 1)
	go func() { done <- Relay(rView, rServer) }()
	t.Cleanup(func() { vSide.Close(); sSide.Close(); rView.Close(); rServer.Close() })
	return vSide, s, done
}

func mustWrite(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatal(err)
	}
	return b
}

// handshake: the server side of RFB 3.8 with no authentication
func (s *fakeServer) handshake() {
	go func() {
		w := func(b []byte) { s.conn.Write(b) }
		w([]byte("RFB 003.008\n"))
		io.ReadFull(s.conn, make([]byte, 12))
		w([]byte{1, 1})
		io.ReadFull(s.conn, make([]byte, 1))
		w([]byte{0, 0, 0, 0})
		io.ReadFull(s.conn, make([]byte, 1))
		init := []byte{0x03, 0x20, 0x02, 0x58} // 800x600
		init = append(init, format32...)
		init = append(init, 0, 0, 0, 4, 't', 'e', 's', 't')
		w(init)
		s.readLoop()
	}()
}

// readLoop sorts what the relay sends the server: update requests, everything else
func (s *fakeServer) readLoop() {
	for {
		t := make([]byte, 1)
		if _, err := io.ReadFull(s.conn, t); err != nil {
			return
		}
		var n int
		switch t[0] {
		case 0:
			n = 19
		case 2:
			h := make([]byte, 3)
			io.ReadFull(s.conn, h)
			rest := make([]byte, 4*int(binary.BigEndian.Uint16(h[1:])))
			io.ReadFull(s.conn, rest)
			s.msgs <- append(append(t, h...), rest...)
			continue
		case 3:
			b := make([]byte, 9)
			io.ReadFull(s.conn, b)
			s.requests <- append(t, b...)
			continue
		case 4:
			n = 7
		case 5:
			n = 5
		default:
			s.msgs <- t
			return
		}
		b := make([]byte, n)
		io.ReadFull(s.conn, b)
		s.msgs <- append(t, b...)
	}
}

func viewerHandshake(t *testing.T, v net.Conn) {
	t.Helper()
	if got := string(mustRead(t, v, 12)); got != "RFB 003.008\n" {
		t.Fatalf("version %q", got)
	}
	mustWrite(t, v, []byte("RFB 003.008\n"))
	mustRead(t, v, 2)
	mustWrite(t, v, []byte{1})
	mustRead(t, v, 4)
	mustWrite(t, v, []byte{1})
	init := mustRead(t, v, 24)
	mustRead(t, v, int(binary.BigEndian.Uint32(init[20:])))
}

func setEncodings(encs ...int32) []byte {
	b := []byte{2, 0, 0, byte(len(encs))}
	for _, e := range encs {
		b = binary.BigEndian.AppendUint32(b, uint32(e))
	}
	return b
}

func rect(x, y, w, h uint16, enc int32, payload ...byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, x)
	b = binary.BigEndian.AppendUint16(b, y)
	b = binary.BigEndian.AppendUint16(b, w)
	b = binary.BigEndian.AppendUint16(b, h)
	b = binary.BigEndian.AppendUint32(b, uint32(enc))
	return append(b, payload...)
}

func update(rects ...[]byte) []byte {
	b := []byte{0, 0, 0, byte(len(rects))}
	for _, r := range rects {
		b = append(b, r...)
	}
	return b
}

func expectRequest(t *testing.T, s *fakeServer) []byte {
	t.Helper()
	select {
	case r := <-s.requests:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no update request reached the server")
		return nil
	}
}

func expectNoRequest(t *testing.T, s *fakeServer) {
	t.Helper()
	select {
	case r := <-s.requests:
		t.Fatalf("an update request the viewer did not ask for: %v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

// Every encoding the relay reads arrives byte for byte, and the relay knows where each update
// ends: the next request goes when the last one is through.
func TestRelayPassesUpdatesUnchanged(t *testing.T) {
	v, s, _ := startPair(t)
	s.handshake()
	viewerHandshake(t, v)
	mustWrite(t, v, append([]byte{0, 0, 0, 0}, format32...))
	mustWrite(t, v, setEncodings(encTight, 5 /* Hextile */, 50 /* H.264 */, encZRLE, encRaw,
		encCopyRect, encCursor, encDesktopName, encQEMULed, -26 /* quality 6 */))
	<-s.msgs // SetPixelFormat
	if got, want := <-s.msgs, setEncodings(encTight, encZRLE, encRaw, encCopyRect, encCursor,
		encDesktopName, encQEMULed, -26); !bytes.Equal(got, want) {
		t.Fatalf("SetEncodings to the server %v, want %v (Hextile and H.264 dropped)", got, want)
	}
	mustWrite(t, v, []byte{3, 0, 0, 0, 0, 0, 0x03, 0x20, 0x02, 0x58}) // whole screen
	if r := expectRequest(t, s); r[1] != 0 {
		t.Fatalf("the first request %v, want non-incremental", r)
	}

	big := bytes.Repeat([]byte{7}, 200)
	u := update(
		rect(0, 0, 10, 10, encTight, 0x80, 1, 2, 3),                                     // fill
		rect(0, 0, 64, 64, encTight, append([]byte{0x90, 0x85, 0x01}, big[:133]...)...), // JPEG
		rect(0, 0, 2, 1, encTight, 0x40, 1, 1, 1, 2, 3, 4, 5, 6, 0xc0),                  // palette, 2 colours, 1 byte
		rect(0, 0, 4, 4, encTight, append([]byte{0x00, 48}, big[:48]...)...),            // copy, zlib'd
		rect(0, 0, 3, 1, encRaw, big[:12]...),
		rect(5, 5, 3, 1, encCopyRect, 0, 1, 0, 2),
		rect(0, 0, 8, 8, encZRLE, 0, 0, 0, 5, 1, 2, 3, 4, 5),
		rect(0, 0, 2, 2, encCursor, append(big[:16], 0xc0, 0xc0)...),
		rect(0, 0, 0, 0, encDesktopName, 0, 0, 0, 3, 'M', 'a', 'c'),
		rect(0, 0, 0, 0, encQEMULed, 2),
	)
	go s.conn.Write(u)
	if got := mustRead(t, v, len(u)); !bytes.Equal(got, u) {
		t.Fatalf("the update changed on its way")
	}
	expectNoRequest(t, s) // the viewer has not asked again
	mustWrite(t, v, []byte{3, 1, 0, 0, 0, 0, 0x03, 0x20, 0x02, 0x58})
	if r := expectRequest(t, s); r[1] != 1 {
		t.Fatalf("request %v, want incremental", r)
	}
}

// Requests are credits: with three asked for, the server sees one request at a time, the next
// as soon as an update is through, and none once the three have been sent.
func TestRelayKeepsOneRequestAtTheServer(t *testing.T) {
	v, s, _ := startPair(t)
	s.handshake()
	viewerHandshake(t, v)
	for i := 0; i < 3; i++ {
		mustWrite(t, v, []byte{3, 1, 0, 0, 0, 0, 0x03, 0x20, 0x02, 0x58})
	}
	expectRequest(t, s)
	expectNoRequest(t, s)
	u := update(rect(0, 0, 1, 1, encTight, 0x80, 9, 9, 9))
	go func() {
		for i := 0; i < 3; i++ {
			mustRead(t, v, len(u))
		}
	}()
	for i := 0; i < 3; i++ {
		s.conn.Write(u)
		if i < 2 {
			expectRequest(t, s)
		}
	}
	expectNoRequest(t, s)
}

// Something the relay cannot read: it stops reading, copies the rest as it is, and the
// viewer's requests reach the server again.
func TestRelayCopiesWhatItCannotRead(t *testing.T) {
	v, s, _ := startPair(t)
	s.handshake()
	viewerHandshake(t, v)
	mustWrite(t, v, []byte{3, 1, 0, 0, 0, 0, 0x03, 0x20, 0x02, 0x58})
	expectRequest(t, s)
	odd := update(rect(0, 0, 16, 16, 5 /* Hextile */, 1, 2, 3, 4, 5, 6, 7, 8))
	go s.conn.Write(odd)
	if got := mustRead(t, v, len(odd)); !bytes.Equal(got, odd) {
		t.Fatal("the update changed on its way")
	}
	mustWrite(t, v, []byte{3, 1, 0, 0, 0, 0, 0x03, 0x20, 0x02, 0x58})
	expectRequest(t, s)
	mustWrite(t, v, []byte{3, 1, 0, 0, 0, 0, 0x03, 0x20, 0x02, 0x58})
	expectRequest(t, s) // as the viewer sent it, not paced
}
