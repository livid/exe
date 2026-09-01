//go:build linux

package hostinfo

import (
	"strings"
	"testing"
)

func TestParseMeminfo(t *testing.T) {
	const sample = `MemTotal:       131072000 kB
MemFree:         1000000 kB
MemAvailable:   120000000 kB
Buffers:          123456 kB
HugePages_Total:       0
`
	m := parseMeminfo(strings.NewReader(sample))
	if m.Total != 131072000*1024 {
		t.Errorf("Total = %d", m.Total)
	}
	if m.Available != 120000000*1024 {
		t.Errorf("Available = %d", m.Available)
	}
	if got := readMem(); got.Total == 0 || got.Available == 0 {
		t.Errorf("readMem() = %+v, want live figures", got)
	}
}
