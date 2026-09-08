package macos9

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const MaxCDSize int64 = 2 << 30

var ErrCDLocked = errors.New("The Mac has locked this CD. Eject it in Finder first. Close programs using it before Force eject; a Mac restart may be needed.")

type CDImage struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}
type CDStatus struct {
	Running   bool      `json:"running"`
	Available bool      `json:"available"`
	Filename  string    `json:"filename"`
	Locked    bool      `json:"locked"`
	Images    []CDImage `json:"images"`
}
type cdBlock struct {
	Device    string `json:"device"`
	ID        string `json:"qdev"`
	Removable bool   `json:"removable"`
	Locked    bool   `json:"locked"`
	TrayOpen  bool   `json:"tray_open"`
	Inserted  *struct {
		File string `json:"file"`
	} `json:"inserted"`
}

func validCDName(name string) bool {
	if name == "" || len(name) > 240 || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".iso", ".cdr", ".img", ".toast":
		return true
	}
	return false
}
func (m *Manager) cdImage(name string) (string, error) {
	if !validCDName(name) {
		return "", errors.New("Choose an ISO, CDR, IMG, or Toast disc image.")
	}
	path := m.path(filepath.Join("media", name))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("That CD image is unavailable. Choose another image.")
	}
	return path, nil
}
func (m *Manager) cdImages() ([]CDImage, error) {
	images := []CDImage{}
	entries, err := os.ReadDir(m.path("media"))
	if os.IsNotExist(err) {
		return images, nil
	}
	if err != nil {
		return nil, errors.New("Cannot read the CD image library.")
	}
	for _, e := range entries {
		if !validCDName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			images = append(images, CDImage{e.Name(), info.Size()})
		}
	}
	return images, nil
}

// Uploaded images are streamed to a private temporary file. A hard link publishes
// the complete image without ever replacing an existing or mounted file.
func (m *Manager) ImportCD(name string, reader io.Reader) (CDImage, error) {
	if !validCDName(name) {
		return CDImage{}, errors.New("Choose an ISO, CDR, IMG, or Toast disc image.")
	}
	dir := m.path("media")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return CDImage{}, err
	}
	f, err := os.CreateTemp(dir, ".cd-upload-*")
	if err != nil {
		return CDImage{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	size, err := io.Copy(f, io.LimitReader(reader, MaxCDSize+1))
	if err != nil {
		return CDImage{}, err
	}
	if size == 0 || size > MaxCDSize {
		return CDImage{}, errors.New("Choose a nonempty disc image no larger than 2 GiB.")
	}
	if err = f.Close(); err != nil {
		return CDImage{}, err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i+1, ext)
		}
		err = os.Link(f.Name(), filepath.Join(dir, candidate))
		if err == nil {
			return CDImage{candidate, size}, nil
		}
		if !os.IsExist(err) {
			return CDImage{}, err
		}
	}
	return CDImage{}, errors.New("Too many images share that filename. Rename the file and try again.")
}

type cdMonitor struct {
	net.Conn
	dec *json.Decoder
	enc *json.Encoder
}

func (m *Manager) cdMonitor(ctx context.Context) (*cdMonitor, error) {
	c, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", m.path("qmp.sock"))
	if err != nil {
		return nil, errors.New("Cannot reach the Mac’s CD drive. Try again after it has started.")
	}
	deadline := time.Now().Add(4 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.SetDeadline(deadline)
	q := &cdMonitor{c, json.NewDecoder(io.LimitReader(c, 4<<20)), json.NewEncoder(c)}
	var hello struct {
		QMP json.RawMessage `json:"QMP"`
	}
	if err = q.dec.Decode(&hello); err == nil && hello.QMP == nil {
		err = errors.New("Invalid Mac control greeting.")
	}
	if err == nil {
		err = q.call("qmp_capabilities", nil, nil)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return q, nil
}
func (q *cdMonitor) call(command string, args any, out any) error {
	request := map[string]any{"execute": command}
	if args != nil {
		request["arguments"] = args
	}
	if err := q.enc.Encode(request); err != nil {
		return err
	}
	for {
		var r struct {
			Event  string          `json:"event"`
			Result json.RawMessage `json:"return"`
			Error  *struct {
				Description string `json:"desc"`
			} `json:"error"`
		}
		if err := q.dec.Decode(&r); err != nil {
			return err
		}
		if r.Event != "" {
			continue
		}
		if r.Error != nil {
			return errors.New(r.Error.Description)
		}
		if r.Result == nil {
			return errors.New("Invalid Mac control response.")
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	}
}
func (q *cdMonitor) drive() (*cdBlock, error) {
	var blocks []cdBlock
	if err := q.call("query-block", nil, &blocks); err != nil {
		return nil, err
	}
	for _, b := range blocks {
		// This is the IDE CD drive created by both supported launch configurations.
		// Do not accidentally target QEMU's other removable devices (floppy/SD).
		if b.Device == "ide1-cd0" && b.Removable && b.ID != "" {
			return &b, nil
		}
	}
	return nil, errors.New("This Mac has no CD drive.")
}
func (m *Manager) CD(ctx context.Context) (CDStatus, error) {
	m.cdMu.Lock()
	defer m.cdMu.Unlock()
	return m.cdStatus(ctx)
}
func (m *Manager) cdStatus(ctx context.Context) (CDStatus, error) {
	s := CDStatus{Running: m.running()}
	var err error
	s.Images, err = m.cdImages()
	if err != nil || !s.Running {
		return s, err
	}
	q, err := m.cdMonitor(ctx)
	if err != nil {
		return s, err
	}
	defer q.Close()
	b, err := q.drive()
	if err != nil {
		return s, err
	}
	s.Available = true
	s.Locked = b.Locked
	if b.Inserted != nil && !b.TrayOpen {
		s.Filename = filepath.Base(b.Inserted.File)
	}
	return s, nil
}

// An empty filename ejects. Normal changes respect the guest's tray lock;
// forcing an eject is a separate, explicit action in the viewer.
func (m *Manager) ChangeCD(ctx context.Context, name string, force bool) (CDStatus, error) {
	m.cdMu.Lock()
	defer m.cdMu.Unlock()
	var path string
	var err error
	if name != "" {
		if force {
			return CDStatus{}, errors.New("Eject the current CD before mounting another image.")
		}
		path, err = m.cdImage(name)
		if err != nil {
			return CDStatus{}, err
		}
	}
	q, err := m.cdMonitor(ctx)
	if err != nil {
		return CDStatus{}, err
	}
	defer q.Close()
	b, err := q.drive()
	if err != nil {
		return CDStatus{}, err
	}
	if path != "" && b.Inserted != nil && !b.TrayOpen {
		current, e1 := os.Stat(b.Inserted.File)
		selected, e2 := os.Stat(path)
		if e1 == nil && e2 == nil && os.SameFile(current, selected) {
			q.Close()
			return m.cdStatus(ctx)
		}
	}
	if b.Locked && !force {
		// Ask the guest to unlock/eject. Some Mac drivers require Finder's Eject.
		if err = q.call("blockdev-open-tray", map[string]any{"id": b.ID}, nil); err != nil {
			return CDStatus{}, err
		}
		b, err = q.drive()
		if err != nil {
			return CDStatus{}, err
		}
		if b.Locked && !b.TrayOpen {
			return CDStatus{}, ErrCDLocked
		}
	}
	if name == "" {
		err = q.call("eject", map[string]any{"id": b.ID, "force": force}, nil)
	} else {
		err = q.call("blockdev-change-medium", map[string]any{"id": b.ID, "filename": path, "format": "raw", "read-only-mode": "read-only"}, nil)
	}
	if err != nil {
		return CDStatus{}, err
	}
	q.Close() // Release the single monitor before querying the resulting state.
	return m.cdStatus(ctx)
}
