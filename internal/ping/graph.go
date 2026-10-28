package ping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The database, as SmokePing's config.dist sets it up: a data source a
// value, each a gauge with a heartbeat of two steps, and seven archives —
// 100 days of every round, 400 days of hours (average, low, high), 800
// days of half-days. About 12 MB a destination, all of it made at once.
var archives = []string{
	"RRA:AVERAGE:0.5:1:28800",
	"RRA:AVERAGE:0.5:12:9600",
	"RRA:MIN:0.5:12:9600",
	"RRA:MAX:0.5:12:9600",
	"RRA:AVERAGE:0.5:144:2400",
	"RRA:MAX:0.5:144:2400",
	"RRA:MIN:0.5:144:2400",
}

func createArgs(path string, start int64) []string {
	hb := strconv.Itoa(2 * Step)
	args := []string{"create", path, "--step", strconv.Itoa(Step), "--start", strconv.FormatInt(start, 10), "--no-overwrite",
		"DS:uptime:GAUGE:" + hb + ":0:U",
		"DS:loss:GAUGE:" + hb + ":0:" + strconv.Itoa(Pings),
		"DS:median:GAUGE:" + hb + ":0:U",
	}
	for i := 1; i <= Pings; i++ {
		args = append(args, fmt.Sprintf("DS:ping%d:GAUGE:%s:0:U", i, hb))
	}
	return append(args, archives...)
}

// updateArgs writes one round as SmokePing's rrdupdate_string does: no
// uptime (that is for its SNMP probes), the count lost, the median, then
// the sorted times with the lost ones as unknowns split evenly above and
// below, so the smoke stays centred on the median.
func updateArgs(path string, t int64, rtts []float64) []string {
	tmpl := []string{"uptime", "loss", "median"}
	for i := 1; i <= Pings; i++ {
		tmpl = append(tmpl, "ping"+strconv.Itoa(i))
	}
	vals := []string{strconv.FormatInt(t, 10), "U"}
	n := len(rtts)
	if n == 0 {
		vals = append(vals, strconv.Itoa(Pings))
		for i := 0; i <= Pings; i++ {
			vals = append(vals, "U")
		}
	} else {
		loss := Pings - n
		vals = append(vals, strconv.Itoa(loss), num(rtts[n/2]))
		for i := 0; i < loss/2; i++ {
			vals = append(vals, "U")
		}
		for _, v := range rtts {
			vals = append(vals, num(v))
		}
		for i := 0; i < loss-loss/2; i++ {
			vals = append(vals, "U")
		}
	}
	return []string{"update", path, "--template", strings.Join(tmpl, ":"), strings.Join(vals, ":")}
}

func num(v float64) string { return strconv.FormatFloat(v, 'e', 10, 64) }

// parseLastUpdate reads rrdtool lastupdate: the names, a blank line, then
// "t: uptime loss median ping1…".
func parseLastUpdate(out []byte) (Result, bool) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := lines[len(lines)-1]
	ts, rest, ok := strings.Cut(last, ":")
	if !ok {
		return Result{}, false
	}
	t, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	f := strings.Fields(rest)
	if err != nil || t <= 0 || len(f) < 3 {
		return Result{}, false
	}
	r := Result{T: t}
	if loss, err := strconv.ParseFloat(f[1], 64); err == nil {
		r.Loss = int(loss)
	} else {
		r.Loss = Pings
	}
	if med, err := strconv.ParseFloat(f[2], 64); err == nil && !math.IsNaN(med) {
		r.Median = &med
	}
	return r, true
}

// Range is one of the detail graphs, SmokePing's table of them.
type Range struct {
	Title   string `json:"title"`
	Seconds int64  `json:"seconds"`
}

var Ranges = []Range{
	{"Last 3 Hours", 3 * 3600},
	{"Last 30 Hours", 30 * 3600},
	{"Last 10 Days", 10 * 86400},
	{"Last 360 Days", 360 * 86400},
}

// OverviewRange is the span of the small graph each destination has in
// the list, SmokePing's overview.
const OverviewRange = 10 * 3600

const (
	// SmokePing draws its graphs 600 wide; below MinWidth its legend no
	// longer fits, so a narrower window scrolls the graph sideways.
	MinWidth = 560
	maxWidth = 2400
)

// GraphOpts asks for one picture.
type GraphOpts struct {
	Overview      bool    // the list's small graph, not a detail one
	Range         int64   // seconds back from now
	Start, End    int64   // or a span of its own: SmokePing's navigator
	Width, Height int     // the whole picture, in CSS pixels
	Zoom          float64 // device pixels to a CSS pixel
}

// Plot is where the graph's plotting area sits in the picture, in CSS
// pixels, and what time its edges stand for.
type Plot struct {
	Left, Top, Width, Height int
	Start, End               int64
}

// Graph draws one destination with rrdtool graph, as a PNG.
func (m *Monitor) Graph(ctx context.Context, id string, o GraphOpts) ([]byte, Plot, error) {
	rrd := m.tool("rrdtool")
	if rrd == "" {
		return nil, Plot{}, ErrNoRRD
	}
	m.mu.Lock()
	ok := m.find(id) >= 0
	m.mu.Unlock()
	if !ok {
		return nil, Plot{}, ErrNotFound
	}
	path := m.rrdPath(id)
	if _, err := os.Stat(path); err != nil {
		return nil, Plot{}, ErrNotFound
	}
	o.Width = min(max(o.Width, MinWidth), maxWidth)
	o.Height = min(max(o.Height, 80), 1200)
	if !(o.Zoom >= 1) {
		o.Zoom = 1
	}
	o.Zoom = math.Min(o.Zoom, 4)
	probe, _ := m.prober()

	var args []string
	when := m.now()
	if o.Start > 0 && o.End > o.Start {
		when = time.Unix(o.End, 0)
	}
	if o.Overview {
		args = overviewArgs(path, o, probeDesc(probe), when)
	} else {
		var top float64
		if o.Start > 0 && o.End > o.Start {
			v, err := m.maxMedian(ctx, rrd, path, strconv.FormatInt(o.Start, 10), strconv.FormatInt(o.End, 10), "")
			if err != nil {
				return nil, Plot{}, err
			}
			if v == 0 {
				v = 1
			}
			top = v * 1.5
		} else {
			maxes, err := m.findmax(ctx, rrd, id, path)
			if err != nil {
				return nil, Plot{}, err
			}
			if top = maxes[o.Range]; top == 0 {
				top = maxes[Ranges[0].Seconds]
			}
		}
		args = detailArgs(path, o, top, probe, when)
	}
	select {
	case m.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, Plot{}, ctx.Err()
	}
	defer func() { <-m.gate }()
	out, stderr, err := m.exec(ctx, rrd, args...)
	if err != nil {
		return nil, Plot{}, toolErr(err, stderr)
	}
	return parseGraphv(out)
}

// findmax is SmokePing's top of the scale for each detail graph: the
// highest median over its span, the spans that agree within a factor of
// two drawn to one scale, each with a fifth of room above. The smoke does
// not set it: an outlier is cut off at the top instead of flattening the
// rest. The answer holds until the next round is written.
func (m *Monitor) findmax(ctx context.Context, rrd, id, path string) (map[int64]float64, error) {
	mu, _ := m.maxMu.LoadOrStore(id, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	m.mu.Lock()
	have := m.maxes[id]
	m.mu.Unlock()
	if have != nil {
		return have, nil
	}
	vals := make([]float64, len(Ranges))
	errs := make([]error, len(Ranges))
	var wg sync.WaitGroup
	for i, r := range Ranges {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vals[i], errs[i] = m.maxMedian(ctx, rrd, path, "-"+strconv.FormatInt(r.Seconds, 10),
				"-"+strconv.FormatInt(r.Seconds/600, 10), "600")
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	med := sorted[(len(sorted)-1)/2]
	unison := 0.000001
	agree := make([]bool, len(vals))
	for i, v := range vals {
		if v <= 2*med && v >= med/2 {
			agree[i] = true
			unison = math.Max(unison, v)
		}
	}
	got := map[int64]float64{}
	for i, r := range Ranges {
		if agree[i] {
			got[r.Seconds] = unison * 1.2
		} else {
			got[r.Seconds] = vals[i] * 1.2
		}
	}
	m.mu.Lock()
	if m.find(id) >= 0 {
		m.maxes[id] = got
	}
	m.mu.Unlock()
	return got, nil
}

// maxMedian is the highest median from start to end; 0 when there is none.
func (m *Monitor) maxMedian(ctx context.Context, rrd, path, start, end, width string) (float64, error) {
	args := []string{"graphv", os.DevNull, "--start", start, "--end", end}
	if width != "" {
		args = append(args, "--width", width)
	}
	args = append(args, "DEF:maxping="+defPath(path)+":median:AVERAGE", "PRINT:maxping:MAX:%le")
	out, stderr, err := m.exec(ctx, rrd, args...)
	if err != nil {
		return 0, toolErr(err, stderr)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, " = "); ok && k == "print[0]" {
			f, err := strconv.ParseFloat(strings.Trim(v, `"`), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return 0, nil
			}
			return f, nil
		}
	}
	return 0, nil
}

// defPath is a file name as a DEF takes it: a colon would end it.
func defPath(p string) string { return strings.ReplaceAll(p, ":", `\:`) }

// rrdText is text for a COMMENT or GPRINT, where a colon is a separator.
func rrdText(s string) string { return strings.ReplaceAll(s, ":", `\:`) }

// probeDesc says what a round sends, as SmokePing's probe does: fping's
// and a Unix ping's echo requests carry 56 bytes, Windows' 32.
func probeDesc(probe string) string {
	if probe == "ping" && runtime.GOOS == "windows" {
		return "ICMP Echo Pings (32 Bytes)"
	}
	return "ICMP Echo Pings (56 Bytes)"
}

// stamp is the date under a graph, as SmokePing writes it (Perl's
// scalar localtime).
func stamp(t time.Time) string { return t.Format("Mon Jan _2 15:04:05 2006") }

// common is what every graph says first: its size, its time, SmokePing's
// look without borders (graphborders = no), and the PNG format.
func common(o GraphOpts) []string {
	args := []string{"graphv", "-", "--full-size-mode",
		"--width", strconv.Itoa(o.Width), "--height", strconv.Itoa(o.Height),
		"--zoom", strconv.FormatFloat(o.Zoom, 'f', -1, 64),
		"--imgformat", "PNG", "--border", "0",
		"--color", "BACK#ffffff00", "--color", "CANVAS#ffffff00",
	}
	if o.Start > 0 && o.End > o.Start {
		return append(args, "--start", strconv.FormatInt(o.Start, 10), "--end", strconv.FormatInt(o.End, 10))
	}
	return append(args, "--start", "-"+strconv.FormatInt(o.Range, 10))
}

// lossColors is SmokePing's default scale for the median: green with
// nothing lost, through blues and purples to red, dark red for all.
// Each colour covers the counts above the previous one up to its own.
var lossColors = func() []struct {
	upTo  int
	label string
	color string
} {
	p := Pings
	per01 := max(int(0.01*float64(p)), 1)
	per05 := max(int(0.05*float64(p)), 2)
	per10 := max(int(0.10*float64(p)), 3)
	per25 := max(int(0.25*float64(p)), 4)
	per50 := max(int(0.50*float64(p)), 5)
	span := func(lo, hi int) string {
		if hi < lo {
			return strconv.Itoa(hi)
		}
		if hi > lo {
			return fmt.Sprintf("%d-%d", lo, hi)
		}
		return strconv.Itoa(lo)
	}
	return []struct {
		upTo  int
		label string
		color string
	}{
		{0, "0", "#26ff00"},
		{per01, span(1, per01), "#00b8ff"},
		{per05, span(per01+1, per05), "#0059ff"},
		{per10, span(per05+1, per10), "#7e00ff"},
		{per25, span(per10+1, per25), "#ff00ff"},
		{per50, span(per25+1, per50), "#ff5500"},
		{p - 1, span(per50+1, p-1), "#ff0000"},
		{p, fmt.Sprintf("%d/%d", p, p), "#a00000"},
	}
}()

// detailArgs is SmokePing's detail graph (get_detail): the twenty pings as
// smoke, darker toward the median; the median as a band two pixels tall
// coloured by loss; and the legend under it.
func detailArgs(path string, o GraphOpts, top float64, probe string, when time.Time) []string {
	rrd := defPath(path)
	topS := num(top)
	args := append(common(o), "--rigid", "--upper-limit", topS, "--lower-limit", "0", "--vertical-label", "Seconds")
	for i := 1; i <= Pings; i++ {
		args = append(args, fmt.Sprintf("DEF:ping%d=%s:ping%d:AVERAGE", i, rrd, i))
	}
	// a ping over the top is drawn to the top, not left out
	for i := 1; i <= Pings; i++ {
		args = append(args, fmt.Sprintf("CDEF:cp%d=ping%d,%s,LT,ping%d,INF,IF", i, i, topS, i))
	}
	args = append(args, "DEF:loss="+rrd+":loss:AVERAGE")
	// smokecol: each pair of pings, outermost first, as a grey band from the
	// lower to the upper, lighter the further out
	half := float64(Pings) / 2
	for top, bot := Pings, 1; top > bot; top, bot = top-1, bot+1 {
		c := int(190/half*(half-float64(bot))) + 50
		args = append(args,
			fmt.Sprintf("CDEF:smoke%d=cp%d,UN,UNKN,cp%d,cp%d,-,IF", bot, bot, top, bot),
			fmt.Sprintf("AREA:cp%d", bot),
			fmt.Sprintf("STACK:smoke%d#%02x%02x%02x", bot, c, c, c))
	}
	// the median's band is a pixel either side of it at the graph's scale;
	// the plotting area is the picture less the legend, about 100 pixels
	swidth := num(top / math.Max(float64(o.Height-100), 40))
	args = append(args,
		"DEF:median="+rrd+":median:AVERAGE",
		fmt.Sprintf("CDEF:ploss=loss,%d,/,100,*", Pings),
		"VDEF:avmed=median,AVERAGE",
		"VDEF:sdmed=median,STDEV",
		"CDEF:mesd=median,POP,avmed,sdmed,/",
		`GPRINT:avmed:median rtt\:  %.1lf %ss avg`,
		`GPRINT:median:MAX:%.1lf %ss max`,
		`GPRINT:median:MIN:%.1lf %ss min`,
		`GPRINT:median:LAST:%.1lf %ss now`,
		`GPRINT:sdmed:%.1lf %ss sd`,
		`GPRINT:mesd:AVERAGE:%.1lf %s am/s\l`,
		"LINE1:median#202020",
		`GPRINT:ploss:AVERAGE:packet loss\: %.2lf %% avg`,
		`GPRINT:ploss:MAX:%.2lf %% max`,
		`GPRINT:ploss:MIN:%.2lf %% min`,
		`GPRINT:ploss:LAST:%.2lf %% now\l`,
		`COMMENT:loss color\:`,
	)
	last := -1
	for _, lc := range lossColors {
		args = append(args,
			fmt.Sprintf("CDEF:me%d=loss,%d,GT,loss,%d,LE,*,1,UNKN,IF,median,*", lc.upTo, last, lc.upTo),
			fmt.Sprintf("CDEF:meL%d=me%d,%s,-", lc.upTo, lc.upTo, swidth),
			fmt.Sprintf("CDEF:meH%d=me%d,0,*,%s,2,*,+", lc.upTo, lc.upTo, swidth),
			fmt.Sprintf("AREA:meL%d", lc.upTo),
			fmt.Sprintf("STACK:meH%d%s:%s", lc.upTo, lc.color, lc.label))
		last = lc.upTo
	}
	every := fmt.Sprintf(`COMMENT:probe\:       %d %s every %ds`, Pings, probeDesc(probe), Step)
	args = append(args, `COMMENT: \l`, "HRULE:0#000000")
	// SmokePing puts the date at the end of the probe's line; under 640
	// pixels the two do not fit side by side, and the date takes a line
	if o.Width < 640 {
		return append(args, every+`\l`, "COMMENT:"+rrdText(stamp(when))+`\r`)
	}
	return append(args, every, "COMMENT:"+rrdText(stamp(when))+`\j`)
}

// overviewArgs is SmokePing's overview graph (get_overview): the median
// as a line in a band of its standard deviation over the twenty pings.
func overviewArgs(path string, o GraphOpts, desc string, when time.Time) []string {
	rrd := defPath(path)
	args := append(common(o), "--vertical-label", "Seconds", "--alt-autoscale-max", "--alt-y-grid", "--rigid", "--lower-limit", "0",
		"DEF:median1="+rrd+":median:AVERAGE",
		"DEF:loss1="+rrd+":loss:AVERAGE",
		fmt.Sprintf("CDEF:ploss1=loss1,%d,/,100,*", Pings),
		"CDEF:dm1=median1,0,100000,LIMIT",
	)
	// calc_stddev: the pings answered, their mean, their deviation
	var count, sum, sq strings.Builder
	for i := 1; i <= Pings; i++ {
		args = append(args,
			fmt.Sprintf("DEF:pin1p%d=%s:ping%d:AVERAGE", i, rrd, i),
			fmt.Sprintf("CDEF:p1p%d=pin1p%d,UN,0,pin1p%d,IF", i, i, i))
		if i == 1 {
			fmt.Fprintf(&count, "CDEF:pings1=%d,p1p1,UN", Pings)
			sum.WriteString("CDEF:m1=p1p1")
			sq.WriteString("CDEF:sdev1=p1p1,m1,-,DUP,*")
			continue
		}
		fmt.Fprintf(&count, ",p1p%d,UN,+", i)
		fmt.Fprintf(&sum, ",p1p%d,+", i)
		fmt.Fprintf(&sq, ",p1p%d,m1,-,DUP,*,+", i)
	}
	count.WriteString(",-")
	sum.WriteString(",pings1,/")
	sq.WriteString(",pings1,/,SQRT")
	return append(args, count.String(), sum.String(), sq.String(),
		"CDEF:dmlow1=dm1,sdev1,2,/,-",
		"CDEF:s2d1=sdev1",
		"AREA:dmlow1",
		"AREA:s2d1#00458630::STACK",
		"LINE1:dm1#004586:med RTT",
		"VDEF:avmed1=median1,AVERAGE",
		"VDEF:avsd1=sdev1,AVERAGE",
		"CDEF:msr1=median1,POP,avmed1,avsd1,/",
		"VDEF:avmsr1=msr1,AVERAGE",
		`GPRINT:avmed1:%5.1lf %ss av md `,
		`GPRINT:ploss1:AVERAGE:%5.1lf %% av ls`,
		`GPRINT:avsd1:%5.1lf %ss av sd`,
		`GPRINT:avmsr1:%5.1lf %s am/as\l`,
		"COMMENT:"+rrdText(desc),
		"COMMENT:"+rrdText(stamp(when))+`\j`,
	)
}

// parseGraphv splits rrdtool graphv's answer: "key = value" lines, then
// "image = BLOB_SIZE:n" and the picture's n bytes.
func parseGraphv(out []byte) ([]byte, Plot, error) {
	var p Plot
	head, rest, ok := bytes.Cut(out, []byte("image = BLOB_SIZE:"))
	if !ok {
		return nil, p, errors.New("rrdtool drew no picture")
	}
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return nil, p, errors.New("rrdtool's picture is cut short")
	}
	n, err := strconv.Atoi(string(rest[:nl]))
	if err != nil || n <= 0 || nl+1+n > len(rest) {
		return nil, p, errors.New("rrdtool's picture is cut short")
	}
	png := rest[nl+1 : nl+1+n]
	for _, line := range strings.Split(string(head), "\n") {
		k, v, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		i, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		switch k {
		case "graph_left":
			p.Left = int(i)
		case "graph_top":
			p.Top = int(i)
		case "graph_width":
			p.Width = int(i)
		case "graph_height":
			p.Height = int(i)
		case "graph_start":
			p.Start = i
		case "graph_end":
			p.End = i
		}
	}
	return png, p, nil
}
