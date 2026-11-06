//go:build linux

package hostmon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseStat(t *testing.T) {
	busy, total, ok := parseStat(strings.NewReader("cpu  408690183 216559 52384731 5248863620 5380860 0 944647 0 9717658 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n"))
	// guest (9717658) is inside user already: total is the first eight
	wantTotal := uint64(408690183 + 216559 + 52384731 + 5248863620 + 5380860 + 0 + 944647 + 0)
	if !ok || total != wantTotal || busy != wantTotal-5248863620-5380860 {
		t.Fatalf("busy %d total %d ok %v", busy, total, ok)
	}
	if _, _, ok := parseStat(strings.NewReader("intr 1 2 3\n")); ok {
		t.Error("read a cpu line from nothing")
	}
}

func TestParseNetDev(t *testing.T) {
	text := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9000 10 0 0 0 0 0 0 9000 10 0 0 0 0 0 0
enP7s7: 847248569841 600000 0 0 0 0 0 0 962038960753 700000 0 0 0 0 0 0
tap-vm1: 5 1 0 0 0 0 0 0 6 1 0 0 0 0 0 0
`
	got := parseNetDev(strings.NewReader(text), func(n string) bool { return n == "enP7s7" })
	if len(got) != 1 || got["enP7s7"] != [2]uint64{847248569841, 962038960753} {
		t.Fatalf("%v", got)
	}
}

func TestParseDiskstats(t *testing.T) {
	text := ` 259       0 nvme0n1 5000 10 80000 300 9000 20 160000 400 0 500 700 0 0 0 0 0 0
 259       1 nvme0n1p1 100 0 2000 3 0 0 0 0 0 4 3 0 0 0 0 0 0
   7       0 loop0 50 0 400 1 0 0 0 0 0 1 1 0 0 0 0 0 0
`
	got := parseDiskstats(strings.NewReader(text), func(n string) bool { return n == "nvme0n1" })
	if len(got) != 1 || got["nvme0n1"] != [2]uint64{80000 * 512, 160000 * 512} {
		t.Fatalf("%v", got)
	}
}

func TestParseSMI(t *testing.T) {
	g := parseSMI("NVIDIA GB10, 37, 46, 10.90\n")
	if g.name != "NVIDIA GB10" || *g.util != 37 || *g.temp != 46 || *g.power != 10.9 {
		t.Fatalf("%+v", g)
	}
	if g.names != nil || g.cards != nil {
		t.Fatalf("one GPU has no cards of its own: %+v", g)
	}
	g = parseSMI("A, 10, 50, [N/A]\nB, 30, 61, [N/A]\n")
	if g.name != "A + B" || *g.util != 20 || *g.temp != 61 || g.power != nil {
		t.Fatalf("two GPUs: %+v", g)
	}
	if len(g.cards) != 2 || *g.cards[0].Util != 10 || *g.cards[1].Temp != 61 || g.cards[1].Power != nil ||
		len(g.names) != 2 || g.names[1] != "B" {
		t.Fatalf("two GPUs, each: %+v", g)
	}
	g = parseSMI("NVIDIA RTX 6000 Ada Generation, 0, 34, 21.5\nNVIDIA RTX 6000 Ada Generation, 80, 52, 180.25\n")
	if g.name != "2 × NVIDIA RTX 6000 Ada Generation" || *g.util != 40 || *g.power != 201.75 ||
		*g.cards[0].Util != 0 || *g.cards[1].Power != 180.25 {
		t.Fatalf("two of a kind: %+v", g)
	}
	if g := parseSMI(""); g.util != nil || g.name != "" {
		t.Fatalf("no output: %+v", g)
	}
}

func TestCPUTempSensors(t *testing.T) {
	write := func(root, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(root+path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root+path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the GB10: seven ACPI zones, four of them the CPU clusters
	gb10 := t.TempDir()
	for i, z := range []struct{ path, temp string }{
		{`\_TZ_.TSOC`, "60900"}, {`\_TZ_.TS0E`, "45300"}, {`\_TZ_.TS0P`, "46700"}, {`\_TZ_.TS1E`, "45700"},
		{`\_TZ_.TS1P`, "58400"}, {`\_TZ_.TGPU`, "70000"}, {`\_TZ_.TUNC`, "45800"},
	} {
		z0 := "/class/thermal/thermal_zone" + strconv.Itoa(i)
		write(gb10, z0+"/type", "acpitz")
		write(gb10, z0+"/device/path", z.path)
		write(gb10, z0+"/temp", z.temp)
	}
	write(gb10, "/class/hwmon/hwmon1/name", "nvme")
	write(gb10, "/class/hwmon/hwmon1/temp1_input", "41850")
	got := cpuTempSensors(gb10)
	if len(got) != 4 {
		t.Fatalf("GB10 sensors: %v", got)
	}
	// an AMD desktop: Tctl counts, the dies' inputs do not
	amd := t.TempDir()
	write(amd, "/class/hwmon/hwmon2/name", "k10temp")
	write(amd, "/class/hwmon/hwmon2/temp1_input", "51000")
	write(amd, "/class/hwmon/hwmon2/temp1_label", "Tctl")
	write(amd, "/class/hwmon/hwmon2/temp3_input", "43000")
	write(amd, "/class/hwmon/hwmon2/temp3_label", "Tccd1")
	write(amd, "/class/thermal/thermal_zone0/type", "acpitz")
	write(amd, "/class/thermal/thermal_zone0/temp", "30000")
	if got := cpuTempSensors(amd); len(got) != 1 || !strings.HasSuffix(got[0], "hwmon2/temp1_input") {
		t.Fatalf("AMD sensors: %v", got)
	}
	// a Raspberry Pi: a thermal zone typed for the CPU
	pi := t.TempDir()
	write(pi, "/class/thermal/thermal_zone0/type", "cpu-thermal")
	write(pi, "/class/thermal/thermal_zone0/temp", "48000")
	if got := cpuTempSensors(pi); len(got) != 1 {
		t.Fatalf("Pi sensors: %v", got)
	}
	if got := cpuTempSensors(t.TempDir()); len(got) != 0 {
		t.Fatalf("a machine with no sensors: %v", got)
	}
}
