package macos9

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyInstaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installer.iso")
	data := []byte("an installer image")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha1.Sum(data)
	digest := hex.EncodeToString(h[:])
	if err := verifyFile(context.Background(), path, digest, int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if err := verifyFile(context.Background(), path, digest, 999); err == nil {
		t.Fatal("accepted truncated media")
	}
	if err := verifyFile(context.Background(), path, strings.Repeat("0", 40), int64(len(data))); err == nil {
		t.Fatal("accepted corrupt media")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyFile(ctx, path, digest, int64(len(data))); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}
func TestSetupSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	m.state.Active = true
	m.state.Phase = "setup"
	m.state.Steps[2].State = "working"
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	resumed := New(root).Status()
	if resumed.Active || resumed.Phase != "interrupted" {
		t.Fatalf("cannot resume: %+v", resumed)
	}
	if resumed.Steps[2].State != "working" {
		t.Fatal("lost step progress")
	}
	// Returned slices cannot mutate the manager's state.
	s := m.Status()
	s.Steps[0].Title = "changed"
	if m.Status().Steps[0].Title == "changed" {
		t.Fatal("status aliases mutable state")
	}
}
func TestExistingInstallationAndStoppedGuest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "installed.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(root)
	s := m.Status()
	if !s.Installed || s.Running || s.Phase != "stopped" {
		t.Fatalf("wrong existing installation state: %+v", s)
	}
	if err := m.Finish(); err == nil {
		t.Fatal("allowed finish without an installer session")
	}
}
func TestRunningGuestCannotBeReinstalledOrStartedTwice(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	ln, err := net.Listen("unix", filepath.Join(root, "qmp.sock"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	m.state.Phase = "installing"
	if err = m.Finish(); err == nil {
		t.Fatal("allowed installation completion while guest disk is open")
	}
	if err = m.Start(); err != nil {
		t.Fatal(err)
	}
	if m.state.Active {
		t.Fatal("launched setup for an existing process")
	}
	if _, err = os.Stat(filepath.Join(root, "installed.json")); !os.IsNotExist(err) {
		t.Fatal("marked running installer as installed")
	}
}
func TestMalformedProgressDoesNotBreakFirstRun(t *testing.T) {
	root := t.TempDir()
	saved, _ := json.Marshal(Status{Phase: "setup", Active: true, Steps: []Step{{Title: "old schema"}}})
	os.WriteFile(filepath.Join(root, "setup.json"), saved, 0600)
	s := New(root).Status()
	if s.Phase != "new" || len(s.Steps) != 6 {
		t.Fatalf("bad recovery: %+v", s)
	}
}
