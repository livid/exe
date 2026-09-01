//go:build darwin

package hostinfo

import "testing"

func TestParseVMStat(t *testing.T) {
	const sample = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               10000.
Pages active:                            200000.
Pages inactive:                           20000.
Pages speculative:                         5000.
Pages throttled:                              0.
Pages wired down:                         50000.
`
	if got, want := parseVMStat(sample), uint64((10000+20000+5000)*16384); got != want {
		t.Errorf("parseVMStat = %d, want %d", got, want)
	}
}
