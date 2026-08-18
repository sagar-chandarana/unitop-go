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
