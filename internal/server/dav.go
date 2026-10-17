package server

// The Workspace as a volume on a Mac. The daemon serves its workspace
// folder over WebDAV at /Workspace/, the one network file system a Mac OS X
// Finder mounts by itself (Go → Connect to Server…), and a Mac that exe
// runs in QEMU reaches this machine at 10.0.2.2, the host's address under
// QEMU's user networking. Any user name is accepted; the password is the
// share's own, kept in StateDir/workspace.password, made on first use and
// renewed on request — never the API token: a guest that mounts the volume
// learns nothing it could call the API with. A dot name goes through, since
// the Mac writes .DS_Store and ._name beside what it copies, and stays
// node-local and unlisted like every hidden path; a symlink that leaves the
// folder is refused. A write lands as one the desk made: the sync engine
// versions it and open Workspace windows refresh.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/net/webdav"

	"exe/internal/peer"
)

const davPrefix = "/Workspace"

var shareMu sync.Mutex

func (s *Server) sharePasswordPath() string { return filepath.Join(s.StateDir, "workspace.password") }

// sharePassword returns the volume's password, making one when none is
// kept; renew replaces it, which drops every Mac that had the old one at
// its next request.
func (s *Server) sharePassword(renew bool) (string, error) {
	shareMu.Lock()
	defer shareMu.Unlock()
	p := s.sharePasswordPath()
	if !renew {
		if b, err := os.ReadFile(p); err == nil {
			if pw := strings.TrimSpace(string(b)); pw != "" {
				return pw, nil
			}
		}
	}
	pw, err := newSharePassword()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.StateDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(pw+"\n"), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

// newSharePassword is twelve letters and digits a person types into a Mac
// of 1999 without a second look: no l, 1, o or 0.
func newSharePassword() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

type shareInfo struct {
	User      string   `json:"user"`
	Password  string   `json:"password"`
	Mac       string   `json:"mac"`       // the address from a Mac exe runs
	Addresses []string `json:"addresses"` // the addresses from other computers
}

// handleShare says how to mount the volume (GET) or renews its password (POST).
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	pw, err := s.sharePassword(r.Method == http.MethodPost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	listen := ""
	if cfg := s.Config(); cfg != nil {
		listen = cfg.Listen
	}
	host, port := listenHostPort(listen)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, shareInfo{
		User:      "workspace",
		Password:  pw,
		Mac:       fmt.Sprintf("http://10.0.2.2:%s%s/", port, davPrefix),
		Addresses: shareAddresses(host, port),
	})
}

func listenHostPort(listen string) (host, port string) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", "7777"
	}
	return host, port
}

// shareAddresses is where another computer reaches the volume: the one
// address the daemon listens on, or, listening on every interface, each
// address of this machine that is not loopback or a VM's bridge.
func shareAddresses(host, port string) []string {
	out := []string{}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		if !ip.IsLoopback() {
			out = append(out, davURL(ip, port))
		}
		return out
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(ifc.Name, "exe-") || strings.HasPrefix(ifc.Name, "docker") || strings.HasPrefix(ifc.Name, "br-") || strings.HasPrefix(ifc.Name, "veth") {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, davURL(ipn.IP, port))
		}
	}
	return out
}

func davURL(ip net.IP, port string) string {
	return fmt.Sprintf("http://%s%s/", net.JoinHostPort(ip.String(), port), davPrefix)
}

// davHandler serves the Workspace over WebDAV behind the share's password.
func (s *Server) davHandler() http.Handler {
	h := &webdav.Handler{
		Prefix:     davPrefix,
		FileSystem: davFS{s: s, root: s.workspaceDir()},
		LockSystem: webdav.NewMemLS(),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw, err := s.sharePassword(false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, got, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(pw)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="exe Workspace"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := os.MkdirAll(s.workspaceDir(), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// davFS is the workspace folder as a webdav.FileSystem that keeps to the
// folder: webdav.Dir follows a symlink wherever it points, this does not.
type davFS struct {
	s    *Server
	root string
}

func davRel(name string) string { return strings.TrimPrefix(path.Clean("/"+name), "/") }

// resolve turns a volume path into the file's path on disk, refusing one
// whose nearest existing ancestor, symlinks followed, lies outside the
// folder.
func (f davFS) resolve(name string) (string, error) {
	p := filepath.Join(f.root, filepath.FromSlash(path.Clean("/"+name)))
	probe := p
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	real, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		return "", err
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return "", os.ErrPermission
	}
	return p, nil
}

func (f davFS) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	p, err := f.resolve(name)
	if err != nil {
		return err
	}
	if err := os.Mkdir(p, perm); err != nil {
		return err
	}
	f.s.davChanged(davRel(name), false, false)
	return nil
}

func (f davFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	p, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err
	}
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) == 0 {
		return file, nil
	}
	return &davWritten{File: file, fs: f, rel: davRel(name)}, nil
}

func (f davFS) RemoveAll(_ context.Context, name string) error {
	p, err := f.resolve(name)
	if err != nil {
		return err
	}
	rel := davRel(name)
	if rel == "" {
		return errors.New("the Workspace itself stays")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	f.s.davChanged(rel, true, !fi.IsDir())
	return nil
}

func (f davFS) Rename(_ context.Context, oldName, newName string) error {
	from, err := f.resolve(oldName)
	if err != nil {
		return err
	}
	to, err := f.resolve(newName)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	f.s.davChanged(davRel(oldName), true, !fi.IsDir())
	f.s.davChanged(davRel(newName), false, !fi.IsDir())
	return nil
}

func (f davFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	p, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

// davWritten tells the daemon about a file when the Mac is done writing it.
type davWritten struct {
	*os.File
	fs  davFS
	rel string
}

func (w *davWritten) Close() error {
	err := w.File.Close()
	w.fs.s.davChanged(w.rel, false, true)
	return err
}

// davChanged versions and announces a change the Mac made, as the desk's
// own PUT and DELETE do; a folder is announced only, and a hidden path
// (the Mac's ._name and .DS_Store) not at all.
func (s *Server) davChanged(rel string, deleted, file bool) {
	if rel == "" {
		return
	}
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return
		}
	}
	if file && s.Peers != nil {
		s.withFileLock(func() {
			if deleted {
				s.Peers.LocalDelete(peer.WorkspaceNS, rel)
			} else {
				s.Peers.LocalWrite(peer.WorkspaceNS, rel)
			}
		})
	}
	s.BroadcastAppData(peer.WorkspaceNS, rel, deleted, "")
}
