package macos9

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCDLibraryAndImports(t *testing.T) {
	m := New(t.TempDir())
	first, err := m.ImportCD("Game.cdr", strings.NewReader("original image"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.ImportCD("Game.cdr", strings.NewReader("different image"))
	if err != nil || first.Filename == second.Filename {
		t.Fatalf("duplicate import: %+v %v", second, err)
	}
	if b, _ := os.ReadFile(m.path("media/Game.cdr")); string(b) != "original image" {
		t.Fatal("overwrote original")
	}
	outside := filepath.Join(t.TempDir(), "outside.iso")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, m.path("media/Link.iso"))
	for _, name := range []string{"../outside.iso", outside, "https://example.com/a.iso", `C:\disc.iso`, "a\n.iso", ".hidden.iso", "disk.qcow2", "Link.iso"} {
		if _, err := m.cdImage(name); err == nil {
			t.Errorf("accepted unsafe or unsupported image %q", name)
		}
	}
	for _, name := range []string{"../a.iso", ".hidden.iso", "archive.zip"} {
		if _, err := m.ImportCD(name, strings.NewReader("bad")); err == nil {
			t.Errorf("accepted upload %q", name)
		}
	}
	if _, err := m.ImportCD("Empty.iso", strings.NewReader("")); err == nil {
		t.Fatal("accepted empty image")
	}
	if _, err := m.ImportCD("Interrupted.iso", &brokenCDReader{}); err == nil {
		t.Fatal("accepted interrupted upload")
	}
	files, _ := os.ReadDir(m.path("media"))
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".cd-upload-") || f.Name() == "Interrupted.iso" {
			t.Fatal("left partial upload")
		}
	}
	images, err := m.cdImages()
	if err != nil || len(images) != 2 {
		t.Fatalf("library includes symlinks/partial files: %+v %v", images, err)
	}
	s, err := m.CD(context.Background())
	if err != nil || s.Running || s.Filename != "" || len(s.Images) != 2 {
		t.Fatalf("stopped CD: %+v %v", s, err)
	}
}

type brokenCDReader struct{}

func (*brokenCDReader) Read(p []byte) (int, error) { return copy(p, "incomplete"), io.ErrUnexpectedEOF }

type fakeCD struct {
	mu                  sync.Mutex
	path                string
	locked, tray        bool
	forceCalls, changes int
}

func fakeCDMonitor(t *testing.T, root string, cd *fakeCD) {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(root, "qmp.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				dec := json.NewDecoder(c)
				enc := json.NewEncoder(c)
				enc.Encode(map[string]any{"QMP": map[string]any{"version": map[string]any{}}})
				for {
					var req struct {
						Execute   string
						Arguments map[string]any
					}
					if dec.Decode(&req) != nil {
						return
					}
					cd.mu.Lock()
					var result any = map[string]any{}
					var failure string
					switch req.Execute {
					case "qmp_capabilities":
					case "query-block":
						block := map[string]any{"device": "ide1-cd0", "qdev": "/machine/cd", "removable": true, "locked": cd.locked, "tray_open": cd.tray}
						if cd.path != "" {
							block["inserted"] = map[string]any{"file": cd.path}
						}
						result = []any{map[string]any{"device": "ide0-hd0", "removable": false}, block, map[string]any{"device": "floppy0", "removable": true}}
					case "blockdev-open-tray":
						if !cd.locked {
							cd.tray = true
						}
					case "eject":
						if req.Arguments["force"] == true {
							cd.forceCalls++
						}
						if cd.locked && req.Arguments["force"] != true {
							failure = "locked"
						} else {
							cd.path = ""
							cd.locked = false
							cd.tray = true
						}
					case "blockdev-change-medium":
						if req.Arguments["format"] != "raw" || req.Arguments["read-only-mode"] != "read-only" {
							failure = "unsafe image open"
						} else {
							cd.path, _ = req.Arguments["filename"].(string)
							cd.tray = false
							cd.changes++
						}
					default:
						failure = "unexpected command"
					}
					if req.Execute != "query-block" && req.Execute != "qmp_capabilities" && req.Arguments["id"] != "/machine/cd" {
						failure = "wrong drive"
					}
					cd.mu.Unlock()
					// Events may arrive before any command response.
					enc.Encode(map[string]any{"event": "DEVICE_TRAY_MOVED"})
					if failure != "" {
						enc.Encode(map[string]any{"error": map[string]any{"desc": failure}})
					} else {
						enc.Encode(map[string]any{"return": result})
					}
				}
			}()
		}
	}()
}
func TestCDLiveStateAndSafeChanges(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	image, err := m.ImportCD("Game.cdr", bytes.NewReader([]byte("image")))
	if err != nil {
		t.Fatal(err)
	}
	cd := &fakeCD{path: m.path("media/" + image.Filename), locked: true}
	fakeCDMonitor(t, root, cd)
	ctx := context.Background()
	s, err := m.CD(ctx)
	if err != nil || s.Filename != "Game.cdr" || !s.Locked || !s.Available {
		t.Fatalf("CD: %+v %v", s, err)
	}
	if _, err = m.ChangeCD(ctx, "Game.cdr", false); err != nil {
		t.Fatal("same image should be a no-op", err)
	}
	if _, err = m.ChangeCD(ctx, "", false); !errors.Is(err, ErrCDLocked) {
		t.Fatalf("ignored lock: %v", err)
	}
	cd.mu.Lock()
	if cd.path == "" || cd.forceCalls != 0 || cd.changes != 0 {
		t.Fatal("changed locked media")
	}
	cd.mu.Unlock()
	if _, err = m.ChangeCD(ctx, "missing.iso", false); err == nil {
		t.Fatal("accepted missing image")
	}
	s, err = m.ChangeCD(ctx, "", true)
	if err != nil || s.Filename != "" {
		t.Fatalf("explicit force eject: %+v %v", s, err)
	}
	s, err = m.ChangeCD(ctx, "Game.cdr", false)
	if err != nil || s.Filename != "Game.cdr" {
		t.Fatalf("mount: %+v %v", s, err)
	}
	// Finder can eject while QEMU retains an image behind the open tray.
	cd.mu.Lock()
	cd.tray = true
	cd.mu.Unlock()
	s, err = m.CD(ctx)
	if err != nil || s.Filename != "" {
		t.Fatalf("stale mounted name after guest eject: %+v %v", s, err)
	}
}
