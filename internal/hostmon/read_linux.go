//go:build linux

package hostmon

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"exe/internal/hostinfo"
)

// Supported: Linux reads all of it from /proc, /sys and nvidia-smi.
const Supported = true

func readHost() counters {
	c := counters{at: time.Now()}
	if f, err := os.Open("/proc/stat"); err == nil {
		c.cpuBusy, c.cpuTotal, c.cpuOK = parseStat(f)
		f.Close()
	}
	c.cpuTemp = readCPUTemp()
	mem := hostinfo.Mem()
	c.memTotal, c.memAvail = mem.Total, mem.Available
	if f, err := os.Open("/proc/net/dev"); err == nil {
		c.net = parseNetDev(f, physical("/sys/class/net/"))
		f.Close()
	}
	if f, err := os.Open("/proc/diskstats"); err == nil {
		c.disk = parseDiskstats(f, physical("/sys/block/"))
		f.Close()
	}
	c.gpu = readGPU()
	host, _ := os.Hostname()
	c.info = Info{Host: host, Cores: runtime.NumCPU(), MemoryTotal: c.memTotal, GPU: c.gpu.name, GPUs: c.gpu.names, Net: names(c.net), Disks: names(c.disk)}
	return c
}

// physical keeps the devices backed by hardware — a NIC or a disk has a
// `device` link under /sys; loopback, bridges, VM taps, Docker veths,
// tailscale0, loop and device-mapper devices have none. Counting only these
// counts each byte once: a VM's traffic crosses its tap, the bridge and the
// NIC, a filesystem's its dm device and its disk.
func physical(dir string) func(string) bool {
	return func(name string) bool {
		_, err := os.Stat(dir + name + "/device")
		return err == nil
	}
}

// parseStat reads the aggregate "cpu" line: busy is every jiffy but idle
// and iowait; guest time is already inside user and nice.
func parseStat(r io.Reader) (busy, total uint64, ok bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var v [8]uint64
		for i := 0; i < 8 && i+1 < len(f); i++ {
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return 0, 0, false
			}
			v[i] = n
		}
		for _, n := range v {
			total += n
		}
		return total - v[3] - v[4], total, true
	}
	return 0, 0, false
}

// parseNetDev reads /proc/net/dev: received bytes are the first figure
// after the name, transmitted the ninth.
func parseNetDev(r io.Reader, keep func(string) bool) map[string][2]uint64 {
	out := map[string][2]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		name = strings.TrimSpace(name)
		if !ok || !keep(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 == nil && err2 == nil {
			out[name] = [2]uint64{rx, tx}
		}
	}
	return out
}

// parseDiskstats reads /proc/diskstats: sectors read are the sixth field,
// sectors written the tenth, and a sector there is always 512 bytes.
func parseDiskstats(r io.Reader, keep func(string) bool) map[string][2]uint64 {
	out := map[string][2]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || !keep(f[2]) {
			continue
		}
		rd, err1 := strconv.ParseUint(f[5], 10, 64)
		wr, err2 := strconv.ParseUint(f[9], 10, 64)
		if err1 == nil && err2 == nil {
			out[f[2]] = [2]uint64{rd * 512, wr * 512}
		}
	}
	return out
}

var cpuTempOnce sync.Once
var cpuTempFiles []string

// readCPUTemp is the hottest of the CPU's sensors in °C, or nil when the
// machine names none. The sensors are found once (cpuTempSensors).
func readCPUTemp() *float64 {
	cpuTempOnce.Do(func() { cpuTempFiles = cpuTempSensors("/sys") })
	var hottest *float64
	for _, f := range cpuTempFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		milli, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil || milli <= 0 {
			continue
		}
		if c := milli / 1000; hottest == nil || c > *hottest {
			hottest = num(c)
		}
	}
	return hottest
}

// cpuTempSensors finds the files holding CPU temperatures (millidegrees)
// under a sysfs root: an hwmon chip the kernel names for a CPU (Intel's
// coretemp, AMD's k10temp and zenpower, the ARM boards' cpu_thermal) —
// AMD's Tctl/Tdie only, its other inputs being the dies'; else thermal
// zones typed for a CPU (x86_pkg_temp, cpu-thermal and the like); else
// ACPI zones the firmware names for a CPU cluster — the GB10's TS0E/TS0P
// and TS1E/TS1P (each cluster's efficiency and performance cores), not
// its SoC, GPU or uncore zones.
func cpuTempSensors(root string) []string {
	var out []string
	hw, _ := filepath.Glob(root + "/class/hwmon/hwmon*")
	for _, h := range hw {
		name := readTrim(h + "/name")
		switch name {
		case "coretemp", "k10temp", "zenpower", "cpu_thermal":
		default:
			continue
		}
		inputs, _ := filepath.Glob(h + "/temp*_input")
		for _, in := range inputs {
			label := readTrim(strings.TrimSuffix(in, "_input") + "_label")
			if (name == "k10temp" || name == "zenpower") && label != "" && label != "Tctl" && label != "Tdie" {
				continue
			}
			out = append(out, in)
		}
	}
	if len(out) > 0 {
		return out
	}
	zones, _ := filepath.Glob(root + "/class/thermal/thermal_zone*")
	var acpi []string
	for _, z := range zones {
		t := strings.ToLower(readTrim(z + "/type"))
		if t == "x86_pkg_temp" || strings.Contains(t, "cpu") {
			out = append(out, z+"/temp")
		}
		if t == "acpitz" && acpiCPUZone.MatchString(readTrim(z+"/device/path")) {
			acpi = append(acpi, z+"/temp")
		}
	}
	if len(out) > 0 {
		return out
	}
	return acpi
}

var acpiCPUZone = regexp.MustCompile(`^\\_TZ_\.TS\d[EP]$`)

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

var smiOnce sync.Once
var smiPath string

// readGPU asks nvidia-smi (about 25 ms on the GB10). A host without it, or
// one where it hangs or fails, reports no GPU figures.
func readGPU() gpuReading {
	smiOnce.Do(func() { smiPath, _ = exec.LookPath("nvidia-smi") })
	if smiPath == "" {
		return gpuReading{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, smiPath, "--query-gpu=name,utilization.gpu,temperature.gpu,power.draw",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuReading{}
	}
	return parseSMI(string(out))
}

// parseSMI reads one "name, util, temp, power" line per GPU: utilisation
// averaged, the hottest temperature, power summed, and with more than one
// card each card's own. "[N/A]" is no figure — the GB10 has no memory of
// its own to report, and some boards no power.
func parseSMI(s string) gpuReading {
	var g gpuReading
	var util, power float64
	var nUtil, nPower int
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 4 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		var card GPU
		if v, ok := smiNum(f[1]); ok {
			card.Util = num(v)
			util += v
			nUtil++
		}
		if v, ok := smiNum(f[2]); ok {
			card.Temp = num(v)
			if g.temp == nil || v > *g.temp {
				g.temp = num(v)
			}
		}
		if v, ok := smiNum(f[3]); ok {
			card.Power = num(v)
			power += v
			nPower++
		}
		g.names = append(g.names, f[0])
		g.cards = append(g.cards, card)
	}
	if nUtil > 0 {
		g.util = num(util / float64(nUtil))
	}
	if nPower > 0 {
		g.power = num(power)
	}
	switch {
	case len(g.names) == 1:
		g.name, g.names, g.cards = g.names[0], nil, nil
	case len(g.names) > 1 && slices.Equal(g.names, slices.Repeat(g.names[:1], len(g.names))):
		g.name = strconv.Itoa(len(g.names)) + " × " + g.names[0]
	case len(g.names) > 1:
		g.name = strings.Join(g.names, " + ")
	}
	return g
}

func smiNum(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func names(m map[string][2]uint64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
