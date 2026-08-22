package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// procFiles are read every poll to build the host summary. They are cheap
// enough locally to read directly, and small enough to fetch over ssh in one
// grep when -H is in play.
var procFiles = []string{
	"/proc/stat",
	"/proc/meminfo",
	"/proc/loadavg",
	"/proc/uptime",
	"/proc/net/dev",
}

// HostStats is the machine-wide summary drawn above the unit table.
type HostStats struct {
	OK           bool
	UnitTotal    int
	NCPU         int
	Uptime       time.Duration
	Load         [3]float64
	CPUPct       float64
	MemTotal     uint64
	MemUsed      uint64
	SwapTotal    uint64
	SwapUsed     uint64
	NetIn        float64
	NetOut       float64
	NetOK        bool
	NetInTotal   uint64
	NetOutTotal  uint64
	IOOK         bool
	IOReadTotal  uint64
	IOWriteTotal uint64
}

func (h HostStats) MemPct() float64 {
	if h.MemTotal == 0 {
		return 0
	}
	return float64(h.MemUsed) / float64(h.MemTotal) * 100
}

func (h HostStats) SwapPct() float64 {
	if h.SwapTotal == 0 {
		return 0
	}
	return float64(h.SwapUsed) / float64(h.SwapTotal) * 100
}

// LoadPct expresses the 1-minute load average against the core count, which is
// what makes it comparable between a 2-core VPS and a 48-core box.
func (h HostStats) LoadPct() float64 {
	if h.NCPU == 0 {
		return 0
	}
	return h.Load[0] / float64(h.NCPU) * 100
}

type hostSample struct {
	cpuTotal, cpuIdle uint64
	netRx, netTx      uint64
	when              time.Time
}

// deriveHost turns an already-fetched /proc dump into the host summary.
func (c *Collector) deriveHost(files map[string]string, now time.Time) HostStats {
	if len(files) == 0 {
		return HostStats{}
	}
	h := HostStats{OK: true}
	cur := hostSample{when: now}

	for _, line := range strings.Split(files["/proc/stat"], "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" && len(f) >= 5 {
			// The fields are user nice system idle iowait irq softirq steal
			// guest guest_nice. guest is *already counted inside* user, and
			// guest_nice inside nice — the kernel adds them twice on purpose,
			// so summing all ten counts guest time twice. On a hypervisor that
			// is not a rounding error: it inflates both the busy time and the
			// total, and pushes the reported CPU% above the truth.
			for i, v := range f[1:] {
				if i >= 8 { // guest, guest_nice
					continue
				}
				n, _ := strconv.ParseUint(v, 10, 64)
				cur.cpuTotal += n
				if i == 3 || i == 4 { // idle + iowait
					cur.cpuIdle += n
				}
			}
		} else if strings.HasPrefix(f[0], "cpu") {
			h.NCPU++
		}
	}

	mem := map[string]uint64{}
	for _, line := range strings.Split(files["/proc/meminfo"], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		mem[k] = n * 1024 // meminfo is in kB
	}
	h.MemTotal = mem["MemTotal"]
	if avail, ok := mem["MemAvailable"]; ok && h.MemTotal >= avail {
		h.MemUsed = h.MemTotal - avail
	} else {
		h.MemUsed = h.MemTotal - mem["MemFree"] - mem["Buffers"] - mem["Cached"]
	}
	h.SwapTotal = mem["SwapTotal"]
	if h.SwapTotal >= mem["SwapFree"] {
		h.SwapUsed = h.SwapTotal - mem["SwapFree"]
	}

	if f := strings.Fields(files["/proc/loadavg"]); len(f) >= 3 {
		for i := 0; i < 3; i++ {
			h.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	if f := strings.Fields(files["/proc/uptime"]); len(f) >= 1 {
		secs, _ := strconv.ParseFloat(f[0], 64)
		h.Uptime = time.Duration(secs) * time.Second
	}

	for _, line := range strings.Split(files["/proc/net/dev"], "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		// Loopback would double-count every local request.
		if name == "lo" || strings.HasPrefix(name, "veth") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		cur.netRx += rx
		cur.netTx += tx
		h.NetOK = true
	}
	h.NetInTotal, h.NetOutTotal = cur.netRx, cur.netTx
	h.IOReadTotal, h.IOWriteTotal, h.IOOK = parseBlockStats(files)

	if p := c.prevHost; p != nil {
		dt := now.Sub(p.when).Seconds()
		if dt > 0 {
			if cur.cpuTotal > p.cpuTotal {
				dTotal := float64(cur.cpuTotal - p.cpuTotal)
				// idle includes iowait, which the kernel documents as able to
				// go backwards. Subtracted as uint64 that wrapped, and one
				// glitched sample showed an enormous negative percentage. A
				// sample whose idle ran backwards — or somehow outran the
				// total — is rejected instead; the baseline below advances
				// regardless, so the next well-formed sample recovers.
				if cur.cpuIdle >= p.cpuIdle {
					if dIdle := float64(cur.cpuIdle - p.cpuIdle); dIdle <= dTotal {
						h.CPUPct = (dTotal - dIdle) / dTotal * 100
					}
				}
			}
			h.NetIn = rate(cur.netRx, p.netRx, dt)
			h.NetOut = rate(cur.netTx, p.netTx, dt)
		}
	}
	c.prevHost = &cur
	return h
}

func readProcLocal() map[string]string {
	out := make(map[string]string, len(procFiles)+4)
	for _, f := range procFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		out[f] = string(b)
	}
	// /sys/block contains whole devices rather than their partitions. Keep
	// physical leaf devices only: loop/dm/md layers otherwise count the same IO
	// again above or below the real device.
	stats, _ := filepath.Glob("/sys/block/*/stat")
	for _, f := range stats {
		dir := filepath.Dir(f)
		if _, err := os.Stat(filepath.Join(dir, "device")); err != nil {
			continue
		}
		if slaves, err := os.ReadDir(filepath.Join(dir, "slaves")); err == nil && len(slaves) > 0 {
			continue
		}
		if b, err := os.ReadFile(f); err == nil {
			out[f] = string(b)
		}
	}
	return out
}

// parseBlockStats sums the sector counters in physical /sys/block/*/stat
// entries. Linux reports these counters in 512-byte sectors regardless of the
// device's logical block size.
func parseBlockStats(files map[string]string) (read, written uint64, ok bool) {
	const sectorBytes = uint64(512)
	max := ^uint64(0)
	for name, raw := range files {
		rest, found := strings.CutPrefix(name, "/sys/block/")
		if !found || !strings.HasSuffix(rest, "/stat") || strings.Count(rest, "/") != 1 {
			continue
		}
		f := strings.Fields(raw)
		if len(f) < 7 {
			continue
		}
		r, er := strconv.ParseUint(f[2], 10, 64)
		w, ew := strconv.ParseUint(f[6], 10, 64)
		if er != nil || ew != nil || r > max/sectorBytes || w > max/sectorBytes {
			continue
		}
		r *= sectorBytes
		w *= sectorBytes
		if read > max-r || written > max-w {
			continue
		}
		read += r
		written += w
		ok = true
	}
	return read, written, ok
}

// parseProcDump splits the output of "grep -H ” /proc/a /proc/b …", which
// prefixes every line with its filename, back into per-file contents.
func parseProcDump(raw string) map[string]string {
	parts := map[string][]string{}
	for _, line := range strings.Split(raw, "\n") {
		// Only the first colon is the separator: /proc/net/dev and
		// /proc/meminfo both contain colons of their own.
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		parts[name] = append(parts[name], rest)
	}
	out := make(map[string]string, len(parts))
	for name, lines := range parts {
		out[name] = strings.Join(lines, "\n")
	}
	return out
}
