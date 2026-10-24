// Package vncpace relays a VNC (RFB) session between a browser viewer and an emulator's VNC
// server, and paces the screen updates for the viewer.
//
// A VNC server sends one screen update for each update request. Through a network, a viewer
// that asks after each update it gets waits a round trip for every frame, and QEMU's server
// also slows its refresh while no request is waiting. The relay sits next to the server: it
// keeps one request of its own waiting at the server at all times, and lets updates through
// as long as the viewer has asked for them (its requests are credits). The server then sends
// an update on each of its refreshes, and the round trip to the viewer no longer sets the
// pace; a viewer that falls behind stops asking, and so stops the updates.
//
// To find where each update ends, the relay reads the messages without decoding pixels. It
// offers the server only the encodings it can measure. If it meets something it cannot read,
// it stops reading and copies the rest of the session as it is.
package vncpace

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MaxCredits bounds how many updates may be on their way to the viewer: enough for 60 a
// second over a round trip of 100 ms, few enough that a slow link holds only a short backlog.
const MaxCredits = 6

// Encodings and pseudo-encodings the relay knows the size of.
const (
	encRaw            = 0
	encCopyRect       = 1
	encZlib           = 6
	encTight          = 7
	encZRLE           = 16
	encZYWRLE         = 17
	encTightPNG       = -260
	encCursor         = -239
	encXCursor        = -240
	encDesktopSize    = -223
	encLastRect       = -224
	encPointerChange  = -257
	encQEMUExtKey     = -258
	encQEMUAudio      = -259
	encQEMULed        = -261
	encDesktopName    = -307
	encExtDesktopSize = -308
	encXvp            = -309
	encExtClipboard   = -1063131698 // 0xc0a1e5ce
)

func known(enc int32) bool {
	switch enc {
	case encRaw, encCopyRect, encZlib, encTight, encZRLE, encZYWRLE, encTightPNG, encCursor,
		encXCursor, encDesktopSize, encLastRect, encPointerChange, encQEMUExtKey, encQEMUAudio,
		encQEMULed, encDesktopName, encExtDesktopSize, encXvp, encExtClipboard:
		return true
	}
	// JPEG quality and compression levels: settings, no data
	return enc >= -32 && enc <= -23 || enc >= -256 && enc <= -247
}

type relay struct {
	viewer io.ReadWriter
	server io.ReadWriter
	sr     *bufio.Reader // from the server
	vr     *bufio.Reader // from the viewer
	vw     *bufio.Writer // to the viewer

	swMu sync.Mutex // writes to the server come from both directions

	mu          sync.Mutex
	passthrough bool // stopped reading: everything is copied as it is
	credits     int  // updates the viewer asked for and has not had
	asked       bool // the relay's own request is waiting at the server
	full        bool // the viewer asked for a whole update (non-incremental)
	fx, fy      uint16
	fw, fh      uint16
	width       uint16
	height      uint16
	bpp         int // bytes per pixel of the viewer's pixel format
	tpixel      int // a Tight pixel: 3 for 32-bit pixels of depth 24
}

// Relay runs the session until either side closes; it returns the first error, io.EOF for a
// side that closed.
func Relay(viewer, server io.ReadWriter) error {
	r := &relay{viewer: viewer, server: server, sr: bufio.NewReaderSize(server, 64<<10),
		vr: bufio.NewReaderSize(viewer, 16<<10), vw: bufio.NewWriterSize(viewer, 64<<10)}
	if err := r.handshake(); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() { done <- r.fromViewer() }()
	go func() { done <- r.fromServer() }()
	return <-done
}

// copy n bytes from the server to the viewer
func (r *relay) pass(n int64) error {
	_, err := io.CopyN(r.vw, r.sr, n)
	return err
}

// read exactly len(b) bytes from the server and pass them on
func (r *relay) take(b []byte) error {
	if _, err := io.ReadFull(r.sr, b); err != nil {
		return err
	}
	_, err := r.vw.Write(b)
	return err
}

func (r *relay) toServer(b []byte) error {
	r.swMu.Lock()
	defer r.swMu.Unlock()
	_, err := r.server.Write(b)
	return err
}

func (r *relay) handshake() error {
	// versions
	sv := make([]byte, 12)
	if err := r.take(sv); err != nil {
		return err
	}
	if err := r.vw.Flush(); err != nil {
		return err
	}
	cv := make([]byte, 12)
	if _, err := io.ReadFull(r.vr, cv); err != nil {
		return err
	}
	if err := r.toServer(cv); err != nil {
		return err
	}
	if string(sv) != "RFB 003.008\n" || string(cv) != "RFB 003.008\n" {
		return r.giveUp()
	}
	// security: only None is read through; anything else is copied as it is
	n := []byte{0}
	if err := r.take(n); err != nil {
		return err
	}
	types := make([]byte, n[0])
	if err := r.take(types); err != nil {
		return err
	}
	if err := r.vw.Flush(); err != nil {
		return err
	}
	if n[0] == 0 {
		return r.giveUp() // a reason follows, and the server closes
	}
	choice := []byte{0}
	if _, err := io.ReadFull(r.vr, choice); err != nil {
		return err
	}
	if err := r.toServer(choice); err != nil {
		return err
	}
	if choice[0] != 1 {
		return r.giveUp()
	}
	result := make([]byte, 4)
	if err := r.take(result); err != nil {
		return err
	}
	if err := r.vw.Flush(); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(result) != 0 {
		return r.giveUp()
	}
	// ClientInit, ServerInit
	shared := []byte{0}
	if _, err := io.ReadFull(r.vr, shared); err != nil {
		return err
	}
	if err := r.toServer(shared); err != nil {
		return err
	}
	init := make([]byte, 24)
	if err := r.take(init); err != nil {
		return err
	}
	r.width = binary.BigEndian.Uint16(init[0:])
	r.height = binary.BigEndian.Uint16(init[2:])
	r.setFormat(init[4:20])
	if err := r.pass(int64(binary.BigEndian.Uint32(init[20:]))); err != nil {
		return err
	}
	return r.vw.Flush()
}

func (r *relay) setFormat(pf []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bpp = int(pf[0]) / 8
	r.tpixel = r.bpp
	// Tight sends 24-bit true colour in 3 bytes
	if pf[0] == 32 && pf[1] == 24 && pf[3] != 0 && binary.BigEndian.Uint16(pf[4:]) == 255 &&
		binary.BigEndian.Uint16(pf[6:]) == 255 && binary.BigEndian.Uint16(pf[8:]) == 255 {
		r.tpixel = 3
	}
}

// stop ends the pacing: from here on the viewer's requests go to the server as they are. One
// request goes at once if the viewer is waiting for an update the relay had not asked for.
func (r *relay) stop() error {
	r.mu.Lock()
	first := !r.passthrough
	r.passthrough = true
	ask := first && r.credits > 0 && !r.asked
	full, x, y, w, h := r.full, r.fx, r.fy, r.fw, r.fh
	if !full {
		x, y, w, h = 0, 0, r.width, r.height
	}
	r.mu.Unlock()
	if ask {
		return r.toServer(request(!full, x, y, w, h))
	}
	return nil
}

// copyFromServer and copyFromViewer copy the rest of one direction as it is (each called by
// the goroutine that reads that direction)
func (r *relay) copyFromServer() error {
	if err := r.vw.Flush(); err != nil {
		return err
	}
	_, err := io.Copy(r.viewer, r.sr)
	return err
}

func (r *relay) copyFromViewer() error {
	_, err := io.Copy(writerFunc(r.toServer), r.vr)
	return err
}

// giveUp, during the handshake: both directions are copied as they are
func (r *relay) giveUp() error {
	if err := r.stop(); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() { done <- r.copyFromServer() }()
	go func() { done <- r.copyFromViewer() }()
	return <-done
}

type writerFunc func([]byte) error

func (f writerFunc) Write(b []byte) (int, error) {
	if err := f(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func request(incremental bool, x, y, w, h uint16) []byte {
	b := make([]byte, 10)
	b[0] = 3
	if incremental {
		b[1] = 1
	}
	binary.BigEndian.PutUint16(b[2:], x)
	binary.BigEndian.PutUint16(b[4:], y)
	binary.BigEndian.PutUint16(b[6:], w)
	binary.BigEndian.PutUint16(b[8:], h)
	return b
}

// ask sends the relay's own request to the server when the viewer wants an update and none is
// waiting there
func (r *relay) ask() error {
	r.mu.Lock()
	if r.passthrough || r.asked || r.credits == 0 {
		r.mu.Unlock()
		return nil
	}
	r.asked = true
	b := request(true, 0, 0, r.width, r.height)
	if r.full {
		b = request(false, r.fx, r.fy, r.fw, r.fh)
		r.full = false
	}
	r.mu.Unlock()
	return r.toServer(b)
}

// ---------------------------------------------------------------- viewer to server

var errUnknown = errors.New("vncpace: a message the relay cannot read")

func (r *relay) fromViewer() error {
	for {
		msg, err := r.viewerMessage()
		if errors.Is(err, errUnknown) {
			if err := r.toServer(msg); err != nil { // what was read of it
				return err
			}
			if err := r.stop(); err != nil {
				return err
			}
			return r.copyFromViewer()
		}
		if err != nil {
			return err
		}
		if msg == nil {
			continue
		}
		if err := r.toServer(msg); err != nil {
			return err
		}
	}
}

func (r *relay) need(b []byte, n int) ([]byte, error) {
	if len(b) >= n {
		return b, nil
	}
	more := make([]byte, n-len(b))
	if _, err := io.ReadFull(r.vr, more); err != nil {
		return b, err
	}
	return append(b, more...), nil
}

// viewerMessage reads one message from the viewer: what to send to the server, nil for an
// update request (taken as a credit), or errUnknown with the bytes read.
func (r *relay) viewerMessage() ([]byte, error) {
	b, err := r.need(nil, 1)
	if err != nil {
		return nil, err
	}
	switch b[0] {
	case 0: // SetPixelFormat
		if b, err = r.need(b, 20); err == nil {
			r.setFormat(b[4:20])
		}
		return b, err
	case 2: // SetEncodings
		if b, err = r.need(b, 4); err != nil {
			return nil, err
		}
		if b, err = r.need(b, 4+4*int(binary.BigEndian.Uint16(b[2:]))); err != nil {
			return nil, err
		}
		return keepKnown(b), nil
	case 3: // FramebufferUpdateRequest
		if b, err = r.need(b, 10); err != nil {
			return nil, err
		}
		r.mu.Lock()
		if r.passthrough {
			r.mu.Unlock()
			return b, nil
		}
		if r.credits < MaxCredits {
			r.credits++
		}
		if b[1] == 0 {
			r.full = true
			r.fx = binary.BigEndian.Uint16(b[2:])
			r.fy = binary.BigEndian.Uint16(b[4:])
			r.fw = binary.BigEndian.Uint16(b[6:])
			r.fh = binary.BigEndian.Uint16(b[8:])
		}
		r.mu.Unlock()
		return nil, r.ask()
	case 4: // KeyEvent
		return r.need(b, 8)
	case 5: // PointerEvent
		return r.need(b, 6)
	case 6: // ClientCutText (extended clipboard: a negative length)
		if b, err = r.need(b, 8); err != nil {
			return nil, err
		}
		return r.need(b, 8+cutLength(b[4:]))
	case 150: // EnableContinuousUpdates
		return r.need(b, 10)
	case 248: // Fence
		if b, err = r.need(b, 9); err != nil {
			return nil, err
		}
		return r.need(b, 9+int(b[8]))
	case 250: // xvp
		return r.need(b, 4)
	case 251: // SetDesktopSize
		if b, err = r.need(b, 8); err != nil {
			return nil, err
		}
		return r.need(b, 8+16*int(b[6]))
	case 255: // QEMU
		if b, err = r.need(b, 2); err != nil {
			return nil, err
		}
		switch b[1] {
		case 0: // extended key event
			return r.need(b, 12)
		case 1: // audio
			if b, err = r.need(b, 4); err != nil {
				return nil, err
			}
			if binary.BigEndian.Uint16(b[2:]) == 2 { // set format
				return r.need(b, 10)
			}
			return b, nil
		}
	}
	return b, errUnknown
}

func cutLength(b []byte) int {
	n := int32(binary.BigEndian.Uint32(b))
	if n < 0 {
		n = -n
	}
	return int(n)
}

// keepKnown drops from SetEncodings what the relay cannot measure (Hextile, H.264, ...). The
// viewer's order is kept.
func keepKnown(b []byte) []byte {
	n := int(binary.BigEndian.Uint16(b[2:]))
	out := append([]byte{}, b[:4]...)
	kept := 0
	for i := 0; i < n; i++ {
		e := b[4+4*i : 8+4*i]
		if known(int32(binary.BigEndian.Uint32(e))) {
			out = append(out, e...)
			kept++
		}
	}
	binary.BigEndian.PutUint16(out[2:], uint16(kept))
	return out
}

// ---------------------------------------------------------------- server to viewer

func (r *relay) fromServer() error {
	for {
		err := r.serverMessage()
		if errors.Is(err, errUnknown) {
			if err := r.stop(); err != nil {
				return err
			}
			return r.copyFromServer()
		}
		if err != nil {
			return err
		}
		// the message is whole: on its way, unless more is already waiting behind it
		if r.sr.Buffered() == 0 {
			if err := r.vw.Flush(); err != nil {
				return err
			}
		}
	}
}

func (r *relay) serverMessage() error {
	t, err := r.sr.Peek(1)
	if err != nil {
		return err
	}
	switch t[0] {
	case 0: // FramebufferUpdate
		h := make([]byte, 4)
		if err := r.take(h); err != nil {
			return err
		}
		if err := r.update(int(binary.BigEndian.Uint16(h[2:]))); err != nil {
			return err
		}
		r.mu.Lock()
		r.asked = false
		if r.credits > 0 {
			r.credits--
		}
		r.mu.Unlock()
		return r.ask()
	case 1: // SetColourMapEntries
		h := make([]byte, 6)
		if err := r.take(h); err != nil {
			return err
		}
		return r.pass(6 * int64(binary.BigEndian.Uint16(h[4:])))
	case 2, 150: // Bell, EndOfContinuousUpdates
		return r.take(make([]byte, 1))
	case 3: // ServerCutText
		h := make([]byte, 8)
		if err := r.take(h); err != nil {
			return err
		}
		return r.pass(int64(cutLength(h[4:])))
	case 248: // Fence
		h := make([]byte, 9)
		if err := r.take(h); err != nil {
			return err
		}
		return r.pass(int64(h[8]))
	case 250: // xvp
		return r.take(make([]byte, 4))
	case 255: // QEMU
		h, err := r.sr.Peek(4)
		if err != nil {
			return err
		}
		if h[1] != 1 { // only audio is known
			return errUnknown
		}
		switch binary.BigEndian.Uint16(h[2:]) {
		case 0, 1: // stream end, begin
			return r.take(make([]byte, 4))
		case 2: // data
			d := make([]byte, 8)
			if err := r.take(d); err != nil {
				return err
			}
			return r.pass(int64(binary.BigEndian.Uint32(d[4:])))
		}
	}
	return errUnknown
}

// update reads the rectangles of one FramebufferUpdate (n of them, or up to LastRect)
func (r *relay) update(n int) error {
	r.mu.Lock()
	bpp, tpixel := r.bpp, r.tpixel
	r.mu.Unlock()
	for i := 0; i < n; i++ {
		h := make([]byte, 12)
		if err := r.take(h); err != nil {
			return err
		}
		w := int(binary.BigEndian.Uint16(h[4:]))
		ht := int(binary.BigEndian.Uint16(h[6:]))
		enc := int32(binary.BigEndian.Uint32(h[8:]))
		switch enc {
		case encLastRect:
			return nil
		case encRaw:
			if err := r.pass(int64(w * ht * bpp)); err != nil {
				return err
			}
		case encCopyRect:
			if err := r.pass(4); err != nil {
				return err
			}
		case encZlib, encZRLE, encZYWRLE:
			if err := r.lengthPrefixed(); err != nil {
				return err
			}
		case encTight, encTightPNG:
			if err := r.tight(w, ht, tpixel, enc == encTightPNG); err != nil {
				return err
			}
		case encCursor:
			if err := r.pass(int64(w*ht*bpp + (w+7)/8*ht)); err != nil {
				return err
			}
		case encXCursor:
			if w*ht > 0 {
				if err := r.pass(int64(6 + 2*((w+7)/8)*ht)); err != nil {
					return err
				}
			}
		case encDesktopSize:
			r.resize(w, ht)
		case encExtDesktopSize:
			s := make([]byte, 4)
			if err := r.take(s); err != nil {
				return err
			}
			if err := r.pass(16 * int64(s[0])); err != nil {
				return err
			}
			r.resize(w, ht)
		case encQEMULed:
			if err := r.pass(1); err != nil {
				return err
			}
		case encDesktopName:
			if err := r.lengthPrefixed(); err != nil {
				return err
			}
		case encPointerChange, encQEMUExtKey, encQEMUAudio, encXvp, encExtClipboard:
		default:
			return fmt.Errorf("%w: encoding %d", errUnknown, enc)
		}
	}
	return nil
}

func (r *relay) resize(w, h int) {
	r.mu.Lock()
	r.width, r.height = uint16(w), uint16(h)
	r.mu.Unlock()
}

func (r *relay) lengthPrefixed() error {
	l := make([]byte, 4)
	if err := r.take(l); err != nil {
		return err
	}
	return r.pass(int64(binary.BigEndian.Uint32(l)))
}

// compactLength reads Tight's 1-3 byte length
func (r *relay) compactLength() (int, error) {
	n := 0
	for i := 0; i < 3; i++ {
		b := []byte{0}
		if err := r.take(b); err != nil {
			return 0, err
		}
		if i < 2 {
			n |= int(b[0]&0x7f) << (7 * i)
			if b[0]&0x80 == 0 {
				return n, nil
			}
		} else {
			n |= int(b[0]) << 14
		}
	}
	return n, nil
}

func (r *relay) tight(w, h, tpixel int, png bool) error {
	c := []byte{0}
	if err := r.take(c); err != nil {
		return err
	}
	switch comp := c[0] >> 4; {
	case comp == 8: // fill
		return r.pass(int64(tpixel))
	case comp == 9 || png && comp == 10: // JPEG, PNG
		n, err := r.compactLength()
		if err != nil {
			return err
		}
		return r.pass(int64(n))
	case comp > 7 || png:
		return fmt.Errorf("%w: tight control 0x%02x", errUnknown, c[0])
	}
	// basic compression: an optional filter, then the data, zlib'd from 12 bytes on
	size := w * h * tpixel
	if c[0]&0x40 != 0 {
		f := []byte{0}
		if err := r.take(f); err != nil {
			return err
		}
		switch f[0] {
		case 0, 2: // copy, gradient
		case 1: // palette
			p := []byte{0}
			if err := r.take(p); err != nil {
				return err
			}
			colors := int(p[0]) + 1
			if err := r.pass(int64(colors * tpixel)); err != nil {
				return err
			}
			if colors == 2 {
				size = (w + 7) / 8 * h
			} else {
				size = w * h
			}
		default:
			return fmt.Errorf("%w: tight filter %d", errUnknown, f[0])
		}
	}
	if size < 12 {
		return r.pass(int64(size))
	}
	n, err := r.compactLength()
	if err != nil {
		return err
	}
	return r.pass(int64(n))
}

// for tests and logs
func (r *relay) String() string {
	var b bytes.Buffer
	r.mu.Lock()
	fmt.Fprintf(&b, "credits %d asked %v passthrough %v %dx%d", r.credits, r.asked,
		r.passthrough, r.width, r.height)
	r.mu.Unlock()
	return b.String()
}
