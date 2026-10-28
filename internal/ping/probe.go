package ping

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sample is what one destination answered in a round.
type sample struct {
	rtts []float64 // seconds, shortest first; one per echo answered
	note string    // why nothing came back, when that is known
}

func (s sample) result(t int64) Result {
	r := Result{T: t, Loss: Pings - len(s.rtts), Note: s.note}
	if len(s.rtts) > 0 {
		med := s.rtts[len(s.rtts)/2]
		r.Median = &med
	}
	return r
}

// prober is the program a round pings with: fping, as SmokePing's FPing
// probe runs it, or the system's ping.
func (m *Monitor) prober() (name, path string) {
	if p := m.tool("fping"); p != "" {
		return "fping", p
	}
	if p := m.tool("ping"); p != "" {
		return "ping", p
	}
	return "", ""
}

// ping sends every host Pings echo requests and gathers the answers, by
// host in lower case.
func (m *Monitor) ping(ctx context.Context, probe, bin string, hosts []string) map[string]sample {
	got := map[string]sample{}
	if probe == "fping" {
		// SmokePing's FPing probe: -C counts and prints each host's times on
		// one line (to stderr), -B1 -r1 one retry with no back-off, -i10 at
		// least 10 ms between any two packets. A host's own pings are fping's
		// default 1 s apart, so a round takes some 20 s however many hosts.
		args := []string{"-C", strconv.Itoa(Pings), "-q", "-B1", "-r1", "-i10", "--"}
		seen := map[string]bool{}
		for _, h := range hosts {
			if k := strings.ToLower(h); !seen[k] {
				seen[k] = true
				args = append(args, h)
			}
		}
		stdout, stderr, _ := m.exec(ctx, bin, args...) // 1 and 2 only say some host did not answer
		for k, v := range parseFping(append(stderr, stdout...)) {
			got[k] = sample{rtts: v}
		}
	} else {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, h := range hosts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				name, args := pingCommand(bin, h)
				stdout, _, _ := m.exec(ctx, name, args...)
				mu.Lock()
				got[strings.ToLower(h)] = sample{rtts: parsePing(stdout)}
				mu.Unlock()
			}()
		}
		wg.Wait()
	}
	// a name that got no answer at all is looked up, to tell an unknown
	// host from one that is down
	for _, h := range hosts {
		k := strings.ToLower(h)
		if len(got[k].rtts) > 0 {
			continue
		}
		s := got[k]
		if _, err := netip.ParseAddr(h); err != nil {
			lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := net.DefaultResolver.LookupHost(lctx, h)
			cancel()
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
				s.note = "unknown host"
			}
		}
		got[k] = s
	}
	return got
}

// fpingLine is one host's line of fping -C: its name, a colon, and a time
// in milliseconds or "-" for each echo request.
var fpingLine = regexp.MustCompile(`^(\S+)\s+:\s+([-\d.][-\d.\s]*)$`)

func parseFping(out []byte) map[string][]float64 {
	got := map[string][]float64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := fpingLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		rtts := []float64{}
		for _, f := range strings.Fields(m[2]) {
			if ms, err := strconv.ParseFloat(f, 64); err == nil && ms >= 0 {
				rtts = append(rtts, ms/1000)
			}
		}
		sort.Float64s(rtts)
		got[strings.ToLower(m[1])] = rtts
	}
	return got
}

// pingCommand is the system ping's line for one host: Pings requests a
// second apart, numeric output. A Mac pings IPv6 with ping6.
func pingCommand(bin, host string) (string, []string) {
	n := strconv.Itoa(Pings)
	if runtime.GOOS == "windows" {
		return bin, []string{"-n", n, host}
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() && runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("ping6"); err == nil {
			bin = p
		}
	}
	return bin, []string{"-n", "-c", n, "--", host}
}

// pingReply is the time on one answer: "time=12.3 ms" (Linux, Mac),
// "time=12ms" or "time<1ms" (Windows, in English).
var pingReply = regexp.MustCompile(`time[=<]([\d.]+) ?ms`)

func parsePing(out []byte) []float64 {
	rtts := []float64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "DUP!") {
			continue
		}
		if m := pingReply.FindStringSubmatch(line); m != nil {
			if ms, err := strconv.ParseFloat(m[1], 64); err == nil {
				rtts = append(rtts, ms/1000)
			}
		}
	}
	if len(rtts) > Pings {
		rtts = rtts[:Pings]
	}
	sort.Float64s(rtts)
	return rtts
}

// findTool is a program's path: on PATH, or where a package manager puts
// it, which a daemon started at boot may not have on its PATH (fping in
// /usr/sbin; Homebrew's /opt/homebrew/bin on a Mac).
func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	for _, d := range []string{"/usr/sbin", "/sbin", "/usr/local/bin", "/usr/local/sbin", "/opt/homebrew/bin", "/opt/homebrew/sbin", "/opt/local/bin", "/opt/local/sbin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func runTool(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// rrdtool and ping speak the C locale: a decimal point, English words
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// toolErr is a failed run told by what the program said.
func toolErr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if s := strings.TrimSpace(string(stderr)); s != "" {
		return errors.New(strings.TrimPrefix(s, "ERROR: "))
	}
	return err
}
