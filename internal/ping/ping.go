// Package ping measures the round trip to a list of destinations the way
// SmokePing does, and keeps and draws what it measures with RRDtool.
//
// Every five minutes, on the clock's five minutes, one fping run sends
// twenty ICMP echo requests to each destination. The sorted round-trip
// times, their median and the count lost go into the destination's
// round-robin database, laid out as SmokePing lays out its own (data
// sources uptime, loss, median, ping1…ping20, and the archives of its
// config.dist), so a file from either reads in the other. The graphs are
// SmokePing's too, drawn by rrdtool graph: the grey smoke of the twenty
// pings, the median coloured by what was lost, and its legend (graph.go).
//
// Without fping it falls back on the system's ping, one process a
// destination (probe.go). Without rrdtool it measures nothing, having
// nowhere to keep it, and says so; both are looked for at every round, so
// installing either needs no restart.
//
// What is watched is the daemon's own: targets.json in the folder, beside
// each destination's <id>.rrd. Nothing of it is app data, so it does not
// travel to other nodes: each machine measures from where it stands.
package ping

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	Step  = 300 // seconds from one round to the next
	Pings = 20  // echo requests to each destination in a round

	titleMax = 64
)

var (
	ErrBadHost  = errors.New("that is not an IP address or a host name")
	ErrExists   = errors.New("that destination is in the list already")
	ErrNotFound = errors.New("no such destination")
	ErrNoRRD    = errors.New("rrdtool is not installed")
)

// Target is one destination.
type Target struct {
	ID      string `json:"id"`
	Host    string `json:"host"`  // an IP address or a host name, as fping takes it
	Title   string `json:"title"` // what the list calls it
	Created int64  `json:"created"`
}

// Result is one round's answer from a destination.
type Result struct {
	T      int64    `json:"t"`      // the round's start, unix seconds
	Median *float64 `json:"median"` // seconds; nil when every ping was lost
	Loss   int      `json:"loss"`   // of Pings
	Note   string   `json:"note,omitempty"`
}

// TargetStatus is a destination with its last round.
type TargetStatus struct {
	Target
	Last *Result `json:"last"`
}

// Status is how the monitor stands.
type Status struct {
	RRDtool string         `json:"rrdtool"` // its path; "" when it is missing
	Probe   string         `json:"probe"`   // "fping", "ping", or "" for neither
	Step    int            `json:"step"`
	Pings   int            `json:"pings"`
	From    string         `json:"from"`  // this machine's name: where the pings start
	Round   int64          `json:"round"` // the last round's start, 0 before the first
	Next    int64          `json:"next"`  // when the next one starts
	Busy    bool           `json:"busy"`  // a round is out now
	Targets []TargetStatus `json:"targets"`
}

// Monitor watches the destinations.
type Monitor struct {
	dir string

	mu      sync.Mutex
	targets []Target
	loadErr error // targets.json could not be read: nothing is written over it
	last    map[string]Result
	wrote   map[string]int64 // each RRD's last update
	round   int64
	next    int64
	busy    bool
	maxes   map[string]map[int64]float64 // findmax's answer, until the next update

	// fileMu is held while an RRD is created, updated or removed, so a
	// destination taken out mid-round does not come back as a new file.
	fileMu sync.Mutex
	// one findmax at a time for a destination, so the four graphs of a
	// window opening share one answer
	maxMu sync.Map // id -> *sync.Mutex

	poke chan struct{}
	gate chan struct{} // rrdtool graph runs at a time

	// seams for tests
	now  func() time.Time
	tool func(name string) string // a program's path, "" without it
	exec func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// New opens the monitor kept in dir. It starts nothing: Run does.
func New(dir string) *Monitor {
	m := &Monitor{
		dir:   dir,
		last:  map[string]Result{},
		wrote: map[string]int64{},
		maxes: map[string]map[int64]float64{},
		poke:  make(chan struct{}, 1),
		gate:  make(chan struct{}, 4),
		now:   time.Now,
		tool:  findTool,
		exec:  runTool,
	}
	m.load()
	return m
}

type targetsFile struct {
	Targets []Target `json:"targets"`
}

func (m *Monitor) load() {
	b, err := os.ReadFile(filepath.Join(m.dir, "targets.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var f targetsFile
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err != nil {
		m.loadErr = fmt.Errorf("targets.json: %w", err)
		log.Printf("ping: %v; the list is left as it is", m.loadErr)
		return
	}
	m.targets = f.Targets
}

// save writes the list; m.mu is held.
func (m *Monitor) save() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(targetsFile{Targets: m.targets}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.dir, ".targets-*.json")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(m.dir, "targets.json"))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func (m *Monitor) rrdPath(id string) string { return filepath.Join(m.dir, id+".rrd") }

// Status says how the monitor stands, every destination with its last round.
func (m *Monitor) Status() Status {
	from, _ := os.Hostname()
	probe, _ := m.prober()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		RRDtool: m.tool("rrdtool"), Probe: probe, Step: Step, Pings: Pings, From: from,
		Round: m.round, Next: m.next, Busy: m.busy, Targets: []TargetStatus{},
	}
	if st.Next == 0 {
		st.Next = nextRound(m.now())
	}
	for _, t := range m.targets {
		ts := TargetStatus{Target: t}
		if r, ok := m.last[t.ID]; ok {
			ts.Last = &r
		}
		st.Targets = append(st.Targets, ts)
	}
	return st
}

func (m *Monitor) find(id string) int {
	for i, t := range m.targets {
		if t.ID == id {
			return i
		}
	}
	return -1
}

func (m *Monitor) hostTaken(host, but string) bool {
	for _, t := range m.targets {
		if t.ID != but && strings.EqualFold(t.Host, host) {
			return true
		}
	}
	return false
}

// Add puts a destination on the list and has it measured at once, without
// waiting for the round.
func (m *Monitor) Add(host, title string) (Target, error) {
	host, err := CleanHost(host)
	if err != nil {
		return Target{}, err
	}
	if title = cleanTitle(title); title == "" {
		title = host
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return Target{}, m.loadErr
	}
	if m.hostTaken(host, "") {
		return Target{}, ErrExists
	}
	t := Target{ID: m.newID(), Host: host, Title: title, Created: m.now().Unix()}
	m.targets = append(m.targets, t)
	if err := m.save(); err != nil {
		m.targets = m.targets[:len(m.targets)-1]
		return Target{}, err
	}
	select {
	case m.poke <- struct{}{}:
	default:
	}
	return t, nil
}

// newID is a fresh destination id; m.mu is held.
func (m *Monitor) newID() string {
	for {
		var b [4]byte
		rand.Read(b[:])
		if id := hex.EncodeToString(b[:]); m.find(id) < 0 {
			return id
		}
	}
}

// Edit renames a destination or points it somewhere else; nil leaves a
// field as it is. A new address keeps the history: the graphs go on.
func (m *Monitor) Edit(id string, host, title *string) (Target, error) {
	var h string
	if host != nil {
		var err error
		if h, err = CleanHost(*host); err != nil {
			return Target{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return Target{}, m.loadErr
	}
	i := m.find(id)
	if i < 0 {
		return Target{}, ErrNotFound
	}
	t := m.targets[i]
	if host != nil {
		if m.hostTaken(h, id) {
			return Target{}, ErrExists
		}
		t.Host = h
	}
	if title != nil {
		if t.Title = cleanTitle(*title); t.Title == "" {
			t.Title = t.Host
		}
	}
	old := m.targets[i]
	m.targets[i] = t
	if err := m.save(); err != nil {
		m.targets[i] = old
		return Target{}, err
	}
	if t.Host != old.Host {
		delete(m.last, id)
		select {
		case m.poke <- struct{}{}:
		default:
		}
	}
	return t, nil
}

// Delete takes a destination off the list, and its history with it.
func (m *Monitor) Delete(id string) error {
	m.mu.Lock()
	if m.loadErr != nil {
		m.mu.Unlock()
		return m.loadErr
	}
	i := m.find(id)
	if i < 0 {
		m.mu.Unlock()
		return ErrNotFound
	}
	gone := m.targets[i]
	m.targets = append(m.targets[:i:i], m.targets[i+1:]...)
	if err := m.save(); err != nil {
		m.targets = append(m.targets[:i], append([]Target{gone}, m.targets[i:]...)...)
		m.mu.Unlock()
		return err
	}
	delete(m.last, id)
	delete(m.wrote, id)
	delete(m.maxes, id)
	m.mu.Unlock()
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	if err := os.Remove(m.rrdPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Run measures every Step seconds until ctx ends, and a new destination
// as soon as it is added.
func (m *Monitor) Run(ctx context.Context) {
	m.recall(ctx)
	m.mu.Lock()
	n := len(m.targets)
	m.mu.Unlock()
	probe, _ := m.prober()
	switch {
	case m.tool("rrdtool") == "":
		log.Printf("ping: %d destinations; rrdtool is not installed, so nothing is measured until it is", n)
	case probe == "":
		log.Printf("ping: %d destinations; neither fping nor ping is here, so nothing is measured", n)
	default:
		log.Printf("ping: %d destinations, %d pings each every %d s with %s", n, Pings, Step, probe)
	}
	next := nextRound(m.now())
	for ctx.Err() == nil {
		m.mu.Lock()
		m.next = next
		m.mu.Unlock()
		// a round for new destinations that runs past the next mark leaves
		// that mark's round to start the moment it is done
		wait := time.NewTimer(time.Until(time.Unix(next, 0)))
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-m.poke:
			wait.Stop()
			m.measure(ctx, m.now().Unix(), true)
		case <-wait.C:
			m.measure(ctx, next, false)
			next = nextRound(m.now())
		}
	}
}

// nextRound is the first whole Step after now.
func nextRound(now time.Time) int64 { return (now.Unix()/Step + 1) * Step }

// recall reads each destination's last update back out of its RRD, so a
// restarted daemon's list is not blank until the next round.
func (m *Monitor) recall(ctx context.Context) {
	rrd := m.tool("rrdtool")
	if rrd == "" {
		return
	}
	m.mu.Lock()
	ids := make([]string, 0, len(m.targets))
	for _, t := range m.targets {
		ids = append(ids, t.ID)
	}
	m.mu.Unlock()
	for _, id := range ids {
		out, _, err := m.exec(ctx, rrd, "lastupdate", m.rrdPath(id))
		if err != nil {
			continue
		}
		if r, ok := parseLastUpdate(out); ok {
			m.mu.Lock()
			m.last[id] = r
			m.wrote[id] = r.T
			if r.T > m.round {
				m.round = r.T
			}
			m.mu.Unlock()
		}
	}
}

// measure runs one round at t: every destination, or with fresh only those
// with no answer yet (one just added or pointed elsewhere).
func (m *Monitor) measure(ctx context.Context, t int64, fresh bool) {
	rrd := m.tool("rrdtool")
	probe, bin := m.prober()
	if rrd == "" || probe == "" {
		return
	}
	m.mu.Lock()
	var todo []Target
	for _, tg := range m.targets {
		if _, ok := m.last[tg.ID]; !fresh || !ok {
			todo = append(todo, tg)
		}
	}
	if len(todo) == 0 {
		m.mu.Unlock()
		return
	}
	m.busy = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.busy = false
		m.mu.Unlock()
	}()

	hosts := make([]string, 0, len(todo))
	for _, tg := range todo {
		hosts = append(hosts, tg.Host)
	}
	rctx, cancel := context.WithTimeout(ctx, (Step-30)*time.Second)
	got := m.ping(rctx, probe, bin, hosts)
	cancel()

	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	for _, tg := range todo {
		m.mu.Lock()
		i := m.find(tg.ID)
		still := i >= 0 && m.targets[i].Host == tg.Host
		prev := m.wrote[tg.ID]
		m.mu.Unlock()
		if !still || t <= prev {
			continue // gone, moved, or this second is written already
		}
		s := got[strings.ToLower(tg.Host)]
		if err := m.write(ctx, rrd, tg.ID, t, s.rtts); err != nil {
			log.Printf("ping: %s (%s): %v", tg.Title, tg.Host, err)
			continue
		}
		r := s.result(t)
		m.mu.Lock()
		m.last[tg.ID] = r
		m.wrote[tg.ID] = t
		delete(m.maxes, tg.ID)
		if t > m.round {
			m.round = t
		}
		m.mu.Unlock()
	}
}

// write adds one round to a destination's RRD, making the file first if it
// has none; m.fileMu is held.
func (m *Monitor) write(ctx context.Context, rrd, id string, t int64, rtts []float64) error {
	p := m.rrdPath(id)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(m.dir, 0o755); err != nil {
			return err
		}
		if _, stderr, err := m.exec(ctx, rrd, createArgs(p, t-1)...); err != nil {
			return toolErr(err, stderr)
		}
	}
	_, stderr, err := m.exec(ctx, rrd, updateArgs(p, t, rtts)...)
	return toolErr(err, stderr)
}

// CleanHost is host as fping takes it: an IP address, written the usual
// way, or a host name in lower case. Anything else — a flag, a URL, a
// space — is ErrBadHost; nothing past this reaches a command line.
func CleanHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")); err == nil {
		return a.String(), nil
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 || !hostRE.MatchString(host) {
		return "", ErrBadHost
	}
	return strings.ToLower(host), nil
}

var hostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// cleanTitle is a name for the list: one line, no control characters,
// at most titleMax characters.
func cleanTitle(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(s); len(r) > titleMax {
		s = strings.TrimSpace(string(r[:titleMax]))
	}
	return s
}
