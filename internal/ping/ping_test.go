package ping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCleanHost(t *testing.T) {
	good := map[string]string{
		"1.1.1.1":           "1.1.1.1",
		" 192.168.1.1 ":     "192.168.1.1",
		"::1":               "::1",
		"[2606:4700::1111]": "2606:4700::1111",
		"2001:DB8::0:1":     "2001:db8::1",
		"Example.COM":       "example.com",
		"example.com.":      "example.com",
		"router":            "router",
		"a-b.c-d.example":   "a-b.c-d.example",
	}
	for in, want := range good {
		if got, err := CleanHost(in); err != nil || got != want {
			t.Errorf("CleanHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-x", "--help", "a b", "http://example.com", "example.com/x", "a..b", "x_y.com",
		"-example.com", "example-.com", "a;b", "$(id)", strings.Repeat("a.", 127) + "aa", strings.Repeat("a", 64) + ".com"} {
		if got, err := CleanHost(in); !errors.Is(err, ErrBadHost) {
			t.Errorf("CleanHost(%q) = %q, %v; want ErrBadHost", in, got, err)
		}
	}
}

func TestCleanTitle(t *testing.T) {
	if got := cleanTitle("  Home\trouter\n "); got != "Home router" {
		t.Errorf("got %q", got)
	}
	if got := cleanTitle(strings.Repeat("é", 70)); len([]rune(got)) != titleMax {
		t.Errorf("got %d runes", len([]rune(got)))
	}
}

func TestParseFping(t *testing.T) {
	out := "1.1.1.1 : 2.66 3.07 - 2.89\n" +
		"10.255.255.1 : - - - -\n" +
		"::1     : 0.042 0.056 0.050 0.054\n" +
		"Example.com : 12.1 11.9 12.0 12.2\n" +
		"nowhere.invalid: Name or service not known\n" +
		"\n"
	got := parseFping([]byte(out))
	want := map[string][]float64{
		"1.1.1.1":      {0.00266, 0.00289, 0.00307},
		"10.255.255.1": {},
		"::1":          {0.000042, 0.00005, 0.000054, 0.000056},
		"example.com":  {0.0119, 0.012, 0.0121, 0.0122},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, w := range want {
		g := got[k]
		if len(g) != len(w) {
			t.Fatalf("%s: got %v, want %v", k, g, w)
		}
		for i := range w {
			if d := g[i] - w[i]; d > 1e-12 || d < -1e-12 {
				t.Fatalf("%s: got %v, want %v", k, g, w)
			}
		}
	}
}

func TestParsePing(t *testing.T) {
	linux := `PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.
64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=2.54 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=3.57 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=9.99 ms (DUP!)
64 bytes from 1.1.1.1: icmp_seq=3 ttl=57 time=1.02 ms

--- 1.1.1.1 ping statistics ---
3 packets transmitted, 3 received, 0% packet loss, time 2003ms
rtt min/avg/max/mdev = 1.020/2.376/3.570/1.040 ms
`
	if got := parsePing([]byte(linux)); !reflect.DeepEqual(got, []float64{0.00102, 0.00254, 0.00357}) {
		t.Errorf("linux: %v", got)
	}
	windows := "Reply from 1.1.1.1: bytes=32 time=3ms TTL=57\r\nReply from 1.1.1.1: bytes=32 time<1ms TTL=57\r\nRequest timed out.\r\n"
	if got := parsePing([]byte(windows)); !reflect.DeepEqual(got, []float64{0.001, 0.003}) {
		t.Errorf("windows: %v", got)
	}
}

func TestUpdateArgs(t *testing.T) {
	rtts := make([]float64, 17)
	for i := range rtts {
		rtts[i] = float64(i+1) / 1000
	}
	args := updateArgs("/x.rrd", 1000, rtts)
	if args[0] != "update" || args[1] != "/x.rrd" || args[2] != "--template" {
		t.Fatal(args)
	}
	if !strings.HasPrefix(args[3], "uptime:loss:median:ping1:") || !strings.HasSuffix(args[3], ":ping20") {
		t.Fatal(args[3])
	}
	v := strings.Split(args[4], ":")
	if len(v) != 1+3+Pings {
		t.Fatalf("%d values: %v", len(v), v)
	}
	// three lost: one unknown below the times, two above; the median is the
	// middle answer
	if v[0] != "1000" || v[1] != "U" || v[2] != "3" || v[3] != num(0.009) || v[4] != "U" || v[5] != num(0.001) ||
		v[21] != num(0.017) || v[22] != "U" || v[23] != "U" {
		t.Fatalf("values: %v", v)
	}
	// all lost: the count, and nothing else known
	v = strings.Split(updateArgs("/x.rrd", 1000, nil)[4], ":")
	if len(v) != 24 || v[2] != "20" || strings.Count(args[4], "U") != 4 || strings.Count(strings.Join(v, ":"), "U") != 22 {
		t.Fatalf("all lost: %v", v)
	}
}

func TestLossColors(t *testing.T) {
	var got []string
	for _, lc := range lossColors {
		got = append(got, fmt.Sprintf("%d %s %s", lc.upTo, lc.label, lc.color))
	}
	// SmokePing's legend for twenty pings
	want := []string{"0 0 #26ff00", "1 1 #00b8ff", "2 2 #0059ff", "3 3 #7e00ff", "5 4-5 #ff00ff",
		"10 6-10 #ff5500", "19 11-19 #ff0000", "20 20/20 #a00000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestParseLastUpdate(t *testing.T) {
	out := " uptime loss median ping1 ping2\n\n1793191200: U 3 1.669865e-02 1.1e-02 1.2e-02\n"
	r, ok := parseLastUpdate([]byte(out))
	if !ok || r.T != 1793191200 || r.Loss != 3 || r.Median == nil || *r.Median != 0.01669865 {
		t.Fatalf("%+v %v", r, ok)
	}
	r, ok = parseLastUpdate([]byte(" uptime loss median\n\n1793191200: U 20 U\n"))
	if !ok || r.Loss != 20 || r.Median != nil {
		t.Fatalf("all lost: %+v %v", r, ok)
	}
	if _, ok := parseLastUpdate([]byte("ERROR: no such file")); ok {
		t.Fatal("an error read as an update")
	}
}

// fake is a Monitor whose programs are stand-ins: fping answers from
// answers, rrdtool is the real one when realRRD and a recorder otherwise.
type fake struct {
	*Monitor
	mu      sync.Mutex
	calls   []string
	answers map[string]string // host -> fping's times
}

func newFake(t *testing.T, realRRD bool) *fake {
	t.Helper()
	f := &fake{Monitor: New(t.TempDir()), answers: map[string]string{}}
	rrd := "/fake/rrdtool"
	if realRRD {
		if rrd = findTool("rrdtool"); rrd == "" {
			t.Skip("rrdtool is not installed")
		}
	}
	f.tool = func(name string) string {
		switch name {
		case "rrdtool":
			return rrd
		case "fping":
			return "/fake/fping"
		}
		return ""
	}
	f.exec = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		f.mu.Lock()
		f.calls = append(f.calls, filepath.Base(name)+" "+strings.Join(args, " "))
		f.mu.Unlock()
		switch {
		case name == "/fake/fping":
			var b strings.Builder
			for _, h := range args[7:] {
				if a, ok := f.answers[h]; ok {
					fmt.Fprintf(&b, "%s : %s\n", h, a)
				}
			}
			return nil, []byte(b.String()), nil
		case realRRD:
			return runTool(ctx, name, args...)
		}
		return nil, nil, nil
	}
	return f
}

func (f *fake) called(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var got []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			got = append(got, c)
		}
	}
	return got
}

func TestTargetsAddEditDelete(t *testing.T) {
	f := newFake(t, false)
	a, err := f.Add(" 1.1.1.1 ", "  Cloudflare  ")
	if err != nil || a.Host != "1.1.1.1" || a.Title != "Cloudflare" || len(a.ID) != 8 {
		t.Fatalf("%+v %v", a, err)
	}
	b, err := f.Add("Example.com", "")
	if err != nil || b.Title != "example.com" {
		t.Fatalf("a destination with no name is called by its host: %+v %v", b, err)
	}
	if _, err := f.Add("EXAMPLE.com", "again"); !errors.Is(err, ErrExists) {
		t.Fatalf("the same host twice: %v", err)
	}
	if _, err := f.Add("-f /etc/passwd", ""); !errors.Is(err, ErrBadHost) {
		t.Fatalf("a flag for a host: %v", err)
	}
	select {
	case <-f.poke:
	default:
		t.Fatal("an added destination asks for a round")
	}

	title := "Router"
	if e, err := f.Edit(b.ID, nil, &title); err != nil || e.Title != "Router" || e.Host != "example.com" {
		t.Fatalf("%+v %v", e, err)
	}
	host := "1.1.1.1"
	if _, err := f.Edit(b.ID, &host, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("moved onto another's host: %v", err)
	}
	if _, err := f.Edit("nope", nil, &title); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	// the list outlives the monitor
	again := New(f.dir)
	if st := again.Status(); len(st.Targets) != 2 || st.Targets[1].Title != "Router" || st.Step != 300 || st.Pings != 20 {
		t.Fatalf("%+v", st)
	}

	os.WriteFile(f.rrdPath(a.ID), []byte("x"), 0o644)
	if err := f.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.rrdPath(a.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the history stays behind its destination")
	}
	if err := f.Delete(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var file targetsFile
	b2, _ := os.ReadFile(filepath.Join(f.dir, "targets.json"))
	if json.Unmarshal(b2, &file) != nil || len(file.Targets) != 1 || file.Targets[0].ID != b.ID {
		t.Fatalf("targets.json: %s", b2)
	}
}

func TestUnreadableListIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "targets.json"), []byte("{not json"), 0o644)
	m := New(dir)
	if _, err := m.Add("1.1.1.1", ""); err == nil {
		t.Fatal("added over a list it could not read")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "targets.json")); string(b) != "{not json" {
		t.Fatalf("the list was written over: %s", b)
	}
}

func TestRoundFping(t *testing.T) {
	f := newFake(t, false)
	a, _ := f.Add("1.1.1.1", "")
	b, _ := f.Add("example.com", "")
	f.answers["1.1.1.1"] = strings.TrimSpace(strings.Repeat("2.5 ", 18) + "- -")
	f.answers["example.com"] = strings.TrimSpace(strings.Repeat("- ", 20))
	f.measure(context.Background(), 1800, false)

	pings := f.called("fping ")
	if len(pings) != 1 || pings[0] != "fping -C 20 -q -B1 -r1 -i10 -- 1.1.1.1 example.com" {
		t.Fatalf("one fping for every host, as SmokePing runs it: %v", pings)
	}
	if got := f.called("rrdtool create"); len(got) != 2 || !strings.Contains(got[0], "--start 1799 ") {
		t.Fatalf("an RRD for each, starting just before the round: %v", got)
	}
	st := f.Status()
	if st.Round != 1800 || st.Busy {
		t.Fatalf("%+v", st)
	}
	la, lb := st.Targets[0].Last, st.Targets[1].Last
	if la == nil || la.Loss != 2 || la.Median == nil || *la.Median != 0.0025 {
		t.Fatalf("1.1.1.1: %+v", la)
	}
	if lb == nil || lb.Loss != 20 || lb.Median != nil {
		t.Fatalf("example.com: %+v", lb)
	}

	// a round for new destinations measures only those
	c, _ := f.Add("9.9.9.9", "")
	f.answers["9.9.9.9"] = strings.TrimSpace(strings.Repeat("9 ", 20))
	f.measure(context.Background(), 1900, true)
	if pings := f.called("fping "); len(pings) != 2 || !strings.HasSuffix(pings[1], "-- 9.9.9.9") {
		t.Fatalf("%v", pings)
	}
	// and a second that is written already is not written again
	f.measure(context.Background(), 1900, false)
	if ups := f.called("rrdtool update"); len(ups) != 5 || len(f.called("rrdtool update "+f.rrdPath(c.ID))) != 1 ||
		len(f.called("rrdtool update "+f.rrdPath(a.ID))) != 2 || len(f.called("rrdtool update "+f.rrdPath(b.ID))) != 2 {
		t.Fatalf("updates: %v", ups)
	}
}

func TestUnknownHostIsTold(t *testing.T) {
	f := newFake(t, false)
	f.Add("nowhere.invalid", "")
	f.measure(context.Background(), 1800, false)
	if l := f.Status().Targets[0].Last; l == nil || l.Loss != 20 || l.Note != "unknown host" {
		t.Fatalf("%+v", l)
	}
}

func TestNothingWithoutRRDtool(t *testing.T) {
	f := newFake(t, false)
	f.tool = func(name string) string {
		if name == "fping" {
			return "/fake/fping"
		}
		return ""
	}
	f.Add("1.1.1.1", "")
	f.measure(context.Background(), 1800, false)
	if len(f.called("")) != 0 {
		t.Fatalf("measured with nowhere to keep it: %v", f.calls)
	}
	if st := f.Status(); st.RRDtool != "" || st.Probe != "fping" {
		t.Fatalf("%+v", st)
	}
	if _, _, err := f.Graph(context.Background(), f.Status().Targets[0].ID, GraphOpts{Range: 10800}); !errors.Is(err, ErrNoRRD) {
		t.Fatal(err)
	}
}

func TestRunMeasuresAnAddedDestinationAtOnce(t *testing.T) {
	f := newFake(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	f.answers["1.1.1.1"] = "1 2 3"
	f.Add("1.1.1.1", "")
	deadline := time.Now().Add(5 * time.Second)
	for f.Status().Targets[0].Last == nil {
		if time.Now().After(deadline) {
			t.Fatal("not measured")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if st := f.Status(); st.Next%Step != 0 || st.Next <= time.Now().Unix() {
		t.Fatalf("next round %d", st.Next)
	}
}

// TestRRDAndGraphs runs the real rrdtool: SmokePing's layout, rounds
// written into it, and both kinds of graph drawn from it.
func TestRRDAndGraphs(t *testing.T) {
	f := newFake(t, true)
	tg, _ := f.Add("1.1.1.1", "One")
	now := time.Now().Unix() / Step * Step
	for i := int64(36); i >= 0; i-- {
		times := []string{}
		for j := 0; j < 20; j++ {
			if i%7 == 0 && j < 3 {
				times = append(times, "-")
			} else {
				times = append(times, fmt.Sprintf("%.2f", 10+float64(j)/4+float64(i%5)))
			}
		}
		f.answers["1.1.1.1"] = strings.Join(times, " ")
		f.measure(context.Background(), now-i*Step, false)
	}
	info, _, err := runTool(context.Background(), f.tool("rrdtool"), "info", f.rrdPath(tg.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"step = 300", "ds[uptime].type = \"GAUGE\"", "ds[loss].max = 2.0000000000e+01",
		"ds[median].minimal_heartbeat = 600", "ds[ping20].type", "rra[0].rows = 28800", "rra[3].cf = \"MAX\"", "rra[6].cf = \"MIN\""} {
		if !strings.Contains(string(info), want) {
			t.Errorf("rrdtool info lacks %q", want)
		}
	}
	if l := f.Status().Targets[0].Last; l == nil || l.T != now || l.Loss != 3 {
		t.Fatalf("last: %+v", l)
	}

	// a restarted monitor reads the last round back
	again := New(f.dir)
	again.tool, again.exec = f.tool, f.exec
	again.recall(context.Background())
	if l := again.Status().Targets[0].Last; l == nil || l.T != now || l.Loss != 3 || l.Median == nil {
		t.Fatalf("recalled: %+v", l)
	}

	for _, o := range []GraphOpts{
		{Range: 10800, Width: 697, Height: 300, Zoom: 1},
		{Range: 108000, Width: 400, Height: 380, Zoom: 1.5}, // a phone's: the narrow legend
		{Range: 864000, Width: 200, Height: 380, Zoom: 2},   // under MinWidth: drawn at it
		{Overview: true, Range: OverviewRange, Width: 697, Height: 120, Zoom: 2},
		{Start: now - 3600, End: now, Width: 800, Height: 320, Zoom: 1},
	} {
		pic, plot, err := f.Graph(context.Background(), tg.ID, o)
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		img, err := png.Decode(bytes.NewReader(pic))
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		w := max(o.Width, MinWidth)
		if b := img.Bounds(); b.Dx() != int(float64(w)*o.Zoom+0.5) || b.Dy() != int(float64(o.Height)*o.Zoom+0.5) {
			t.Errorf("%+v: picture %v", o, b)
		}
		if plot.Left <= 0 || plot.Width <= 0 || plot.Width >= w || plot.Height <= 0 || plot.End <= plot.Start {
			t.Errorf("%+v: plot %+v", o, plot)
		}
		if o.Start > 0 && (plot.Start != o.Start || plot.End != o.End) {
			t.Errorf("the navigator's span: %+v", plot)
		}
	}
	// the detail graphs' scale is worked out once until the next round
	before := len(f.called("rrdtool graphv /dev/null"))
	f.Graph(context.Background(), tg.ID, GraphOpts{Range: 864000, Width: 697, Height: 300})
	if after := len(f.called("rrdtool graphv /dev/null")); after != before {
		t.Fatalf("findmax ran again: %d -> %d", before, after)
	}
	if top := f.maxes[tg.ID][10800]; top < 0.0130*1.2 || top > 0.0200*1.2 {
		t.Fatalf("the top of the 3-hour scale: %v", f.maxes[tg.ID])
	}
	f.measure(context.Background(), now+Step, false)
	if f.maxes[tg.ID] != nil {
		t.Fatal("a new round leaves the old scale")
	}
	if _, _, err := f.Graph(context.Background(), "nope", GraphOpts{Range: 10800}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// TestLegendsFit draws every legend at the widths a window or a phone asks
// for, with wide figures (medians from 50 to 250 ms, losses), and holds each
// item rrdtool placed to the picture: SmokePing's lines from WideWidth, the
// narrow ones below it. At 586 to 655 SmokePing's first line ran off the
// edge before WideWidth was raised to 680.
func TestLegendsFit(t *testing.T) {
	f := newFake(t, true)
	tg, _ := f.Add("1.1.1.1", "")
	now := time.Now().Unix() / Step * Step
	for i := int64(12); i >= 0; i-- {
		v := map[bool]string{true: "250.25 ", false: "50.5 "}[i%2 == 0]
		f.answers["1.1.1.1"] = strings.TrimSpace(strings.Repeat(v, 17) + "- - -")
		if i == 3 {
			f.answers["1.1.1.1"] = strings.TrimSpace(strings.Repeat("- ", 20))
		}
		f.measure(context.Background(), now-i*Step, false)
	}
	path := f.rrdPath(tg.ID)
	for _, w := range []int{300, 304, 344, 359, 374, 414, 480, 560, 586, 620, 655, 679, 680, 697, 728} {
		for _, z := range []float64{1, 1.5, 3} {
			for _, ov := range []bool{false, true} {
				o := GraphOpts{Overview: ov, Range: 10800, Width: w, Height: 380, Zoom: z}
				var args []string
				if ov {
					o.Range, o.Height = OverviewRange, 150
					args = overviewArgs(path, o, probeDesc("fping"), time.Now())
				} else {
					args = detailArgs(path, o, 0.2, "fping", time.Now())
				}
				args[1] = os.DevNull // the numbers, not the picture
				out, stderr, err := runTool(context.Background(), f.tool("rrdtool"), args...)
				if err != nil {
					t.Fatalf("%+v: %v", o, toolErr(err, stderr))
				}
				items := 0
				for _, line := range strings.Split(string(out), "\n") {
					k, v, ok := strings.Cut(line, " = ")
					if !ok || !strings.HasPrefix(k, "coords[") {
						continue
					}
					var x1, y1, x2, y2 int
					fmt.Sscanf(strings.Trim(v, `"`), "%d,%d,%d,%d", &x1, &y1, &x2, &y2)
					items++
					if x1 < 0 || x2 > w || y2 > o.Height {
						t.Errorf("width %d zoom %g overview %v: legend item %s at %d..%d leaves the picture", w, z, ov, k, x1, x2)
					}
				}
				if items < 4 {
					t.Fatalf("%+v: %d legend items", o, items)
				}
			}
		}
	}
}
