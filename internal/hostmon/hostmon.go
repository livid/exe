// Package hostmon samples this machine's activity — CPU, memory, GPU,
// network and disk — every couple of seconds for the desktop's Control
// Strip. It samples only while someone is reading: the first read starts
// it, and it stops a while after the last one, keeping what it has.
package hostmon

import (
	"sync"
	"time"
)

// Sample is one moment's activity. Rates are bytes a second since the
// sample before; a figure the host cannot give is nil.
type Sample struct {
	T         int64    `json:"t"`         // unix milliseconds
	CPU       *float64 `json:"cpu"`       // percent of all cores busy
	CPUTemp   *float64 `json:"cpu_temp"`  // °C, the hottest CPU sensor
	Mem       *float64 `json:"mem"`       // percent of memory in use (total − available)
	MemUsed   uint64   `json:"mem_used"`  // bytes
	GPU       *float64 `json:"gpu"`            // percent utilisation, all GPUs averaged
	GPUTemp   *float64 `json:"gpu_temp"`       // °C, the hottest GPU
	GPUPower  *float64 `json:"gpu_power"`      // watts, all GPUs
	GPUs      []GPU    `json:"gpus,omitempty"` // each card, on a host with more than one
	NetIn     *float64 `json:"net_in"`    // over physical interfaces
	NetOut    *float64 `json:"net_out"`
	DiskRead  *float64 `json:"disk_read"` // over physical disks
	DiskWrite *float64 `json:"disk_write"`
}

// GPU is one card's figures, in nvidia-smi's order (its index).
type GPU struct {
	Util  *float64 `json:"util"`  // percent
	Temp  *float64 `json:"temp"`  // °C
	Power *float64 `json:"power"` // watts
}

// Info names what the samples cover.
type Info struct {
	Host        string   `json:"host"` // the machine's hostname
	Cores       int      `json:"cores"`
	MemoryTotal uint64   `json:"memory_total"`
	GPU         string   `json:"gpu"`            // "" without one; "2 × …" for two of a kind
	GPUs        []string `json:"gpus,omitempty"` // each card's name, on a host with more than one
	Net         []string `json:"net"`
	Disks       []string `json:"disks"`
}

// counters is one raw read of the host: running totals, from which two
// reads a moment apart make a Sample.
type counters struct {
	at                 time.Time
	cpuBusy, cpuTotal  uint64
	cpuOK              bool
	cpuTemp            *float64
	memTotal, memAvail uint64
	net, disk          map[string][2]uint64 // in/out, read/written bytes per device
	gpu                gpuReading
	info               Info
}

type gpuReading struct {
	name              string   // the cards' model, "2 × …" for two of a kind
	names             []string // each card's, with more than one
	util, temp, power *float64 // averaged, hottest, summed
	cards             []GPU    // each card's, with more than one
}

const (
	Interval = 2 * time.Second
	keep     = 300 // ten minutes of samples
	idle     = 10 * time.Minute
)

// Monitor holds the ring of samples and the sampling loop.
type Monitor struct {
	interval time.Duration
	idle     time.Duration
	read     func() counters

	mu      sync.Mutex
	ring    []Sample
	prev    *counters
	info    Info
	wanted  time.Time
	running bool
}

// New samples the real host. Supported says whether this platform can.
func New() *Monitor { return &Monitor{interval: Interval, idle: idle, read: readHost} }

// Read returns what the samples cover and every sample newer than after
// (unix ms; 0 for all), and keeps the sampling going: a cold monitor takes
// its first reading now, so the first sample lands one interval later.
func (m *Monitor) Read(after int64) (Info, []Sample) {
	m.mu.Lock()
	m.wanted = time.Now()
	start := !m.running
	m.running = true
	m.mu.Unlock()
	if start {
		c := m.read()
		m.mu.Lock()
		m.prev, m.info = &c, c.info
		m.mu.Unlock()
		go m.loop()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Sample{}
	for _, s := range m.ring {
		if s.T > after {
			out = append(out, s)
		}
	}
	return m.info, out
}

func (m *Monitor) loop() {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		if time.Since(m.wanted) > m.idle {
			m.running, m.prev = false, nil // the next reader starts afresh
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		c := m.read()
		m.mu.Lock()
		if m.prev != nil {
			if s, ok := between(m.prev, &c, m.interval); ok {
				m.ring = append(m.ring, s)
				if len(m.ring) > keep {
					m.ring = append([]Sample(nil), m.ring[len(m.ring)-keep:]...)
				}
			}
		}
		m.prev, m.info = &c, c.info
		m.mu.Unlock()
	}
}

// between makes the sample for the span from a to b: no sample at all when
// the span is not a plausible tick (a stalled host, a clock jump).
func between(a, b *counters, interval time.Duration) (Sample, bool) {
	dt := b.at.Sub(a.at).Seconds()
	if dt <= 0 || dt > 3*interval.Seconds() {
		return Sample{}, false
	}
	s := Sample{T: b.at.UnixMilli(), MemUsed: b.memTotal - min(b.memAvail, b.memTotal)}
	if a.cpuOK && b.cpuOK && b.cpuTotal > a.cpuTotal && b.cpuBusy >= a.cpuBusy {
		s.CPU = num(min(100, float64(b.cpuBusy-a.cpuBusy)/float64(b.cpuTotal-a.cpuTotal)*100))
	}
	if b.memTotal > 0 {
		s.Mem = num(float64(s.MemUsed) / float64(b.memTotal) * 100)
	}
	s.CPUTemp = b.cpuTemp
	s.GPU, s.GPUTemp, s.GPUPower, s.GPUs = b.gpu.util, b.gpu.temp, b.gpu.power, b.gpu.cards
	s.NetIn, s.NetOut = rates(a.net, b.net, dt)
	s.DiskRead, s.DiskWrite = rates(a.disk, b.disk, dt)
	return s, true
}

// rates sums each device's growth over dt seconds; a device whose counter
// went backwards (reset, or re-plugged) or that just appeared adds nothing.
func rates(a, b map[string][2]uint64, dt float64) (*float64, *float64) {
	if a == nil || b == nil {
		return nil, nil
	}
	var x, y float64
	for dev, cur := range b {
		prev, ok := a[dev]
		if !ok {
			continue
		}
		if cur[0] >= prev[0] {
			x += float64(cur[0] - prev[0])
		}
		if cur[1] >= prev[1] {
			y += float64(cur[1] - prev[1])
		}
	}
	return num(x / dt), num(y / dt)
}

func num(f float64) *float64 { return &f }
