package macos9

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// exe's own PowerPC emulator: QEMU 9.1 with the Screamer sound device, a VNC server paced for
// the browser viewer, JPEG for the screen and, on Linux, the qegl GPU device. It is built for
// every system exe runs on (docs/emulator.md), published on exe-assets.v2core.com with its
// source and build scripts, and pinned here by checksum: the Macs fetch it when they prepare
// the emulator, in place of the system's QEMU.
const emulatorVersion = "9.1.0-exe.1"

// emulatorBase is where the builds are; "" turns them off (tests, and EXE_EMULATOR_URL="").
var emulatorBase = "https://exe-assets.v2core.com/qemu/"

type emulatorBuild struct {
	sha256 string
	size   int64
}

// GOOS-GOARCH -> qemu-VERSION-GOOS-GOARCH.tar.gz
var emulatorBuilds = map[string]emulatorBuild{
	"linux-arm64":   {"3c6951b08ca0f3026865187753cd92186031f6c7bd73d5c57b545f05e5011621", 6221918},
	"linux-amd64":   {"518c126163b5feb813d275c415fb6d13a6c2aa4921eb07ce75f9dbe5afcc1dc7", 6439287},
	"darwin-arm64":  {"65fb41424425a618752d525146fc34c13bd213e9a0bb909486e1592ff471e24a", 5330717},
	"darwin-amd64":  {"ba41022d863124f103d33a8b10a7e473f5f5b9e92f21b321151c82e3acfdb139", 5842675},
	"windows-amd64": {"a60744dac254cba5f26306830ae781b2dda42af59f9b4e90eb5995d31b93ef51", 6741796},
}

// exe's x86 emulator, for the Intel Mac (intel.go): the same QEMU tree as the PowerPC one, its
// qemu-system-x86_64 built for KVM with the block drivers that Mac reads (vvfat, dmg) and OVMF's
// 32-bit firmware beside it. Built for Linux on x86-64 only, where the Intel Mac runs, and
// published beside the PowerPC builds (docs/emulator.md).
const intelEmulatorVersion = "9.1.0-exe.2"

// intelEmulatorBase is where its builds are; "" turns them off (tests, EXE_EMULATOR_URL="")
var intelEmulatorBase = "https://exe-assets.v2core.com/qemu-x86/"

// qemu-x86-VERSION-linux-amd64.tar.gz
var intelEmulatorBuild = emulatorBuild{"8b5c8b0df0ce548ac9254e3a328e4e6f015a94ea0e5292cb687ae397dc7455a7", 7972968}

func init() {
	if u, ok := os.LookupEnv("EXE_EMULATOR_URL"); ok {
		emulatorBase = u
		if u == "" {
			intelEmulatorBase = ""
		}
	}
	if u, ok := os.LookupEnv("EXE_EMULATOR_X86_URL"); ok {
		intelEmulatorBase = u
	}
}

// exeEmulator is what EXE-QEMU.json says of a runtime made of one of these builds
type exeEmulator struct {
	Version  string   `json:"version"`
	Features []string `json:"features"`
}

// emulatorInfo reads a runtime's EXE-QEMU.json; nil for a runtime of the system's QEMU
func emulatorInfo(dir string) *exeEmulator {
	b, err := os.ReadFile(filepath.Join(dir, "EXE-QEMU.json"))
	if err != nil {
		return nil
	}
	var e exeEmulator
	if json.Unmarshal(b, &e) != nil || e.Version == "" {
		return nil
	}
	return &e
}

func (e *exeEmulator) has(feature string) bool {
	if e == nil {
		return false
	}
	for _, f := range e.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// emulatorBuildHere is this system's build of the Mac's emulator, if there is one
func (m *Manager) emulatorBuildHere() (url string, b emulatorBuild, ok bool) {
	if m.sys.x86 {
		if intelEmulatorBase == "" || !intelHost || intelEmulatorBuild.sha256 == "" {
			return "", b, false
		}
		return intelEmulatorBase + intelEmulatorVersion + "/qemu-x86-" + intelEmulatorVersion + "-linux-amd64.tar.gz", intelEmulatorBuild, true
	}
	if emulatorBase == "" {
		return "", b, false
	}
	plat := runtime.GOOS + "-" + runtime.GOARCH
	b, ok = emulatorBuilds[plat]
	if !ok || b.sha256 == "" {
		return "", b, false
	}
	return emulatorBase + emulatorVersion + "/qemu-" + emulatorVersion + "-" + plat + ".tar.gz", b, true
}

// fetchEmulator downloads this system's build, checks it against its pinned checksum and
// unpacks it into staging.
func (m *Manager) fetchEmulator(ctx context.Context, staging string) error {
	url, want, ok := m.emulatorBuildHere()
	if !ok {
		return errors.New("no emulator build for this system")
	}
	if m.sys.x86 {
		m.step(0, "working", "Downloading exe's x86 emulator…")
	} else {
		m.step(0, "working", "Downloading exe's PowerPC emulator…")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err = os.MkdirAll(m.runtimeRoot, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.runtimeRoot, "emulator-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, want.size+1))
	if err != nil {
		return err
	}
	if n != want.size || hex.EncodeToString(h.Sum(nil)) != want.sha256 {
		return fmt.Errorf("The emulator download does not match its published checksum (%d bytes).", n)
	}
	m.step(0, "working", "Unpacking and checking the emulator…")
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err = os.RemoveAll(staging); err != nil {
		return err
	}
	return untarGz(tmp, staging)
}

// untarGz unpacks regular files and directories, and nothing outside dir
func untarGz(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." ||
			strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unexpected path in the emulator archive: %q", hdr.Name)
		}
		path := filepath.Join(dir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if hdr.Mode&0111 != 0 {
				mode = 0700
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected entry in the emulator archive: %q", hdr.Name)
		}
	}
}

// exeName is a program's file name on this system
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
