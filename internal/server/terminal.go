package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

// wsWriter serializes terminal output into binary WebSocket frames.
type wsWriter struct {
	ctx context.Context
	c   *websocket.Conn
	mu  sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.c.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteText sends a control message ({"status":…}) as a text frame, in
// order with the terminal bytes.
func (w *wsWriter) WriteText(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.c.Write(w.ctx, websocket.MessageText, p)
}

// handleTerminal bridges a browser WebSocket to an interactive SSH shell in
// the VM, or a single command when ?cmd= is set. Binary frames carry terminal
// bytes both ways; text frames carry resize and ping control messages.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	info, err := s.runningVM(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	target := s.vmTarget(info)
	dctx, dcancel := context.WithTimeout(r.Context(), 15*time.Second)
	client, err := target.Dial(dctx)
	dcancel()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		client.Close()
		return
	}
	c.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer client.Close()
	defer c.CloseNow()
	go signOutWatch(r, ctx, cancel, c) // the session ending ends the socket (conns.go)

	sess, err := client.NewSession()
	if err != nil {
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer sess.Close()

	out := &wsWriter{ctx: ctx, c: c}
	sess.Stdout = out
	sess.Stderr = out
	stdin, err := sess.StdinPipe()
	if err != nil {
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	command := r.URL.Query().Get("cmd")
	if command != "" {
		err = sess.Start(command)
	} else {
		err = sess.Shell()
	}
	if err != nil {
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}

	go func() {
		err := sess.Wait()
		// A command window may close itself on success. Keep a failed launch
		// (for example, btop not installed) distinct so its output stays visible.
		if command != "" && err != nil {
			c.Close(websocket.StatusInternalError, "command failed")
		} else {
			c.Close(websocket.StatusNormalClosure, "session ended")
		}
		cancel()
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			if _, err := stdin.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			var msg struct {
				Resize []int           `json:"resize"`
				Ping   json.RawMessage `json:"ping"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if len(msg.Ping) > 0 {
				pong, _ := json.Marshal(map[string]json.RawMessage{"pong": msg.Ping})
				out.WriteText(pong)
			}
			if len(msg.Resize) == 2 {
				sess.WindowChange(msg.Resize[1], msg.Resize[0])
			}
		}
	}
}
