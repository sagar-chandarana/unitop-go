package main

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// procStat builds a /proc/stat cpu line. The fields are, in order:
// user nice system idle iowait irq softirq steal guest guest_nice.
func procStat(user, nice, system, idle, iowait, irq, softirq, steal, guest, guestNice uint64) string {
	return fmt.Sprintf("cpu  %d %d %d %d %d %d %d %d %d %d\ncpu0 0 0 0 0 0 0 0 0 0 0\n",
		user, nice, system, idle, iowait, irq, softirq, steal, guest, guestNice)
}

func hostSampleAt(t *testing.T, c *Collector, stat string, at time.Time) HostStats {
	t.Helper()
	return c.deriveHost(map[string]string{
		"/proc/stat":    stat,
		"/proc/meminfo": "MemTotal: 1024 kB\nMemAvailable: 512 kB\n",
		"/proc/loadavg": "0.00 0.00 0.00 1/1 1",
		"/proc/uptime":  "100.0 100.0",
	}, at)
}

// Guest time is counted twice in /proc/stat on purpose: the kernel's
// account_guest_time() adds each unit to CPUTIME_USER (or CPUTIME_NICE) *and*
// to CPUTIME_GUEST (or CPUTIME_GUEST_NICE). Summing all ten fields therefore
// counts a hypervisor's guest time twice, inflating both the busy time and the
// total — and since busy < total, that pushes the reported percentage up.
func TestHostCPUDoesNotDoubleCountGuestTime(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(time.Second)

	// One second of wall clock on a single CPU, all of it spent running a
	// guest: user advances 100 ticks and guest advances the same 100, because
	// they are the same 100. Idle does not move. The host is 100% busy.
	c := NewCollector(runner{})
	hostSampleAt(t, c, procStat(0, 0, 0, 0, 0, 0, 0, 0, 0, 0), t0)
	h := hostSampleAt(t, c, procStat(100, 0, 0, 0, 0, 0, 0, 0, 100, 0), t1)
	if math.Abs(h.CPUPct-100) > 0.01 {
		t.Errorf("all-guest second: CPU%% = %.2f, want 100", h.CPUPct)
	}

	// Half busy with guest, half idle. Counting guest twice would report
	// 200/300 = 66.7% instead of the true 50%.
	c = NewCollector(runner{})
	hostSampleAt(t, c, procStat(0, 0, 0, 0, 0, 0, 0, 0, 0, 0), t0)
	h = hostSampleAt(t, c, procStat(100, 0, 0, 100, 0, 0, 0, 0, 100, 0), t1)
	if math.Abs(h.CPUPct-50) > 0.01 {
		t.Errorf("half-guest second: CPU%% = %.2f, want 50 (66.67 means guest was counted twice)", h.CPUPct)
	}

	// guest_nice is the same story against nice.
	c = NewCollector(runner{})
	hostSampleAt(t, c, procStat(0, 0, 0, 0, 0, 0, 0, 0, 0, 0), t0)
	h = hostSampleAt(t, c, procStat(0, 100, 0, 100, 0, 0, 0, 0, 0, 100), t1)
	if math.Abs(h.CPUPct-50) > 0.01 {
		t.Errorf("half-guest_nice second: CPU%% = %.2f, want 50", h.CPUPct)
	}
}

// The ordinary cases must be untouched by that change.
func TestHostCPUOnAHostWithoutGuests(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(time.Second)

	for _, c := range []struct {
		name       string
		user, idle uint64
		iowait     uint64
		steal      uint64
		want       float64
	}{
		{"fully idle", 0, 100, 0, 0, 0},
		{"fully busy", 100, 0, 0, 0, 100},
		{"quarter busy", 25, 75, 0, 0, 25},
		{"iowait counts as idle", 25, 50, 25, 0, 25},
		{"steal counts as busy", 25, 50, 0, 25, 50},
	} {
		t.Run(c.name, func(t *testing.T) {
			col := NewCollector(runner{})
			hostSampleAt(t, col, procStat(0, 0, 0, 0, 0, 0, 0, 0, 0, 0), t0)
			h := hostSampleAt(t, col, procStat(c.user, 0, 0, c.idle, c.iowait, 0, 0, c.steal, 0, 0), t1)
			if math.Abs(h.CPUPct-c.want) > 0.01 {
				t.Errorf("CPU%% = %.2f, want %.2f", h.CPUPct, c.want)
			}
		})
	}
}

// idle includes iowait, which the kernel documents as able to go backwards.
// Subtracted as uint64 that wrapped, and one glitched sample reported an
// enormous negative CPU percentage. Such a sample is rejected — exactly
// zero, not a plausible-looking clamp — but only its CPU figure: the same
// sample's network rates come through untouched, and the baseline advances
// so the next well-formed sample measures correctly. Both guards get a turn:
// a backwards idle component, and a monotonic idle that outruns the total.
func TestHostCPUSurvivesBackwardsIowait(t *testing.T) {
	t0 := time.Now()
	dev := func(rx, tx uint64) string {
		return fmt.Sprintf("Inter-|Receive|Transmit\n face |bytes|bytes\n  eth0: %d 0 0 0 0 0 0 0 %d 0 0 0 0 0 0 0\n", rx, tx)
	}
	sample := func(c *Collector, stat string, rx, tx uint64, at time.Time) HostStats {
		return c.deriveHost(map[string]string{
			"/proc/stat":    stat,
			"/proc/net/dev": dev(rx, tx),
			"/proc/meminfo": "MemTotal: 1024 kB\nMemAvailable: 512 kB\n",
			"/proc/loadavg": "0.00 0.00 0.00 1/1 1",
			"/proc/uptime":  "100.0 100.0",
		}, at)
	}

	c := NewCollector(runner{})
	sample(c, procStat(0, 0, 0, 100, 50, 0, 0, 0, 0, 0), 1000, 2000, t0)

	// user works 100 ticks while iowait falls from 50 to 20.
	h := sample(c, procStat(100, 0, 0, 100, 20, 0, 0, 0, 0, 0), 2000, 4000, t0.Add(time.Second))
	if h.CPUPct != 0 {
		t.Errorf("backwards-idle sample: CPU%% = %.2f, want exactly 0", h.CPUPct)
	}
	if h.NetIn != 1000 || h.NetOut != 2000 {
		t.Errorf("the rejected CPU figure disturbed the network rates: in=%.0f out=%.0f, want 1000/2000", h.NetIn, h.NetOut)
	}

	// Against the advanced baseline: 50 busy ticks of 100 total.
	h = sample(c, procStat(150, 0, 0, 150, 20, 0, 0, 0, 0, 0), 3000, 6000, t0.Add(2*time.Second))
	if math.Abs(h.CPUPct-50) > 0.01 {
		t.Errorf("recovery sample: CPU%% = %.2f, want 50", h.CPUPct)
	}

	// The other guard: idle climbs monotonically but outruns the total,
	// because the busy components ran backwards instead.
	h = sample(c, procStat(100, 0, 0, 250, 20, 0, 0, 0, 0, 0), 4000, 8000, t0.Add(3*time.Second))
	if h.CPUPct != 0 {
		t.Errorf("idle-outruns-total sample: CPU%% = %.2f, want exactly 0", h.CPUPct)
	}
	h = sample(c, procStat(150, 0, 0, 300, 20, 0, 0, 0, 0, 0), 5000, 10000, t0.Add(4*time.Second))
	if math.Abs(h.CPUPct-50) > 0.01 {
		t.Errorf("recovery after the second guard: CPU%% = %.2f, want 50", h.CPUPct)
	}
}
