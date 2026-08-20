package main

import (
	"strconv"
	"testing"
	"time"
)

// benchUnits is a realistic fleet: 120 services, the mix of running, exited and
// failed a host actually has, with names as long as systemd really makes them.
func benchUnits(n int) []Unit {
	out := make([]Unit, 0, n)
	for i := 0; i < n; i++ {
		u := Unit{
			Name:        "service-with-a-fairly-long-name-" + strconv.Itoa(i) + ".service",
			Desc:        "Description of service number " + strconv.Itoa(i),
			Active:      "active",
			Sub:         "running",
			CPUPct:      float64(i%400) / 3,
			MemCurrent:  uint64(i) << 20,
			Tasks:       uint64(i % 50),
			MainPID:     uint64(1000 + i),
			NRestarts:   uint64(i % 5),
			Slice:       "system.slice",
			ActiveSince: time.Now().Add(-time.Hour),
			HasRates:    true,
			IPAccount:   true,
			NetInRate:   float64(i * 128),
			NetOutRate:  float64(i * 64),
		}
		switch i % 7 {
		case 3:
			u.Active, u.Sub, u.MemCurrent, u.Tasks = "inactive", "dead", unsetU64, unsetU64
		case 5:
			u.Active, u.Sub, u.Result = "failed", "failed", "exit-code"
			u.MemCurrent, u.Tasks = unsetU64, unsetU64
		}
		if i%11 == 0 {
			u.Slice = "user-1000.slice"
		}
		out = append(out, u)
	}
	return out
}

func benchLogs(n int) []logLine {
	out := make([]logLine, 0, n)
	now := time.Now()
	for i := 0; i < n; i++ {
		out = append(out, logLine{
			ts: now.Add(time.Duration(i) * time.Second), prio: 6 - i%4,
			ident: "some-service", pid: "1234",
			msg: "a log line of roughly the length these things really are, number " + strconv.Itoa(i),
		})
	}
	return out
}

func benchModel(w, h, units, logs int, tree bool) *model {
	m := newModel(runner{}, "server1.local", time.Second, sortCPU, false, false, tree, "")
	m.width, m.height, m.ready = w, h, true
	m.connected = true
	m.units = benchUnits(units)
	m.rebuild()
	m.logs = benchLogs(logs)
	return &m
}

// primeBuffer counts the buffer once, before the timer starts, and hands back
// the memo value that describes it — the snapshot holdBufferAt restores.
func primeBuffer(m *model) logTotals {
	m.logEpoch++
	_ = m.logDisplayTotal()
	return *m.totals
}

// holdBufferAt pins a benchmark's buffer, off the timer: back to its starting
// length, with the memo describing it again. Left to grow four lines per
// iteration, the light-traffic buffers crossed the trim threshold around
// iteration ~5400 and the full one mixed append and trim frames in whatever
// ratio -benchtime happened to buy — so short and long runs measured
// different workloads. The memo is restored from the one snapshot primeBuffer
// took, not recounted: an off-timer recount of a 20k buffer per iteration is
// hidden work that allocates, drives GC, and warms exactly the entries the
// timed frame reads (measured: ~7s of wall clock for 0.5s of timed work).
func holdBufferAt(b *testing.B, m *model, logs int, snap logTotals) {
	b.StopTimer()
	m.logs = m.logs[:logs]
	m.logEpoch = snap.epoch
	*m.totals = snap
	b.StartTimer()
}

// BenchmarkView is the loop that actually runs: a batch of log lines arrives,
// the model takes it, and the frame is redrawn. Feeding the batch through
// Update rather than poking logEpoch matters — poking it forces the full
// buffer recount that the memo now avoids, so the benchmark would measure a
// path production no longer takes.
func BenchmarkView(b *testing.B) {
	m := benchModel(132, 40, 120, 500, false)
	arriving := benchLogs(4)
	m.logs = append(m.logs, arriving...)[:500] // pre-grow the backing array off the timer
	snap := primeBuffer(m)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		holdBufferAt(b, m, 500, snap)
		m.Update(journalBatch{gen: m.logGen, lines: arriving})
		_ = m.View()
	}
}

// BenchmarkViewStatic is the same frame with nothing arriving — a quiet
// service — and BenchmarkViewRecount is the worst case, a buffer measured
// from scratch, as a resize or a page prepended at the top forces.
func BenchmarkViewStatic(b *testing.B) {
	m := benchModel(132, 40, 120, 500, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkViewRecount(b *testing.B) {
	m := benchModel(132, 40, 120, 500, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.logEpoch++ // as a resize or a page prepended at the top does
		_ = m.View()
	}
}

func BenchmarkViewWide(b *testing.B) {
	m := benchModel(200, 60, 120, 500, false)
	arriving := benchLogs(4)
	m.logs = append(m.logs, arriving...)[:500] // pre-grow the backing array off the timer
	snap := primeBuffer(m)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		holdBufferAt(b, m, 500, snap)
		m.Update(journalBatch{gen: m.logGen, lines: arriving})
		_ = m.View()
	}
}

func BenchmarkViewTree(b *testing.B) {
	m := benchModel(132, 40, 120, 500, true)
	arriving := benchLogs(4)
	m.logs = append(m.logs, arriving...)[:500] // pre-grow the backing array off the timer
	snap := primeBuffer(m)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		holdBufferAt(b, m, 500, snap)
		m.Update(journalBatch{gen: m.logGen, lines: arriving})
		_ = m.View()
	}
}

// BenchmarkRebuild is the filter/sort/group pass that runs on every poll.
func BenchmarkRebuild(b *testing.B) {
	m := benchModel(132, 40, 120, 0, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.rebuild()
	}
}

// BenchmarkParseShow is the poll's parsing half.
func BenchmarkParseShow(b *testing.B) {
	var blocks string
	for i := 0; i < 120; i++ {
		n := strconv.Itoa(i)
		blocks += "Id=service-" + n + ".service\nDescription=Service number " + n +
			"\nLoadState=loaded\nActiveState=active\nSubState=running\nSlice=system.slice\n" +
			"MemoryCurrent=" + n + "0485760\nCPUUsageNSec=" + n + "000000000\nTasksCurrent=" + n +
			"\nNRestarts=0\nMainPID=" + n + "\nType=simple\nUnitFileState=enabled\n" +
			"ExecStart={ path=/usr/bin/service-" + n + " ; argv[]=/usr/bin/service-" + n + " }\n\n"
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parseShow(blocks)
	}
}

// At the cap almost every frame is an append: the buffer rides from the cap
// toward cap+slack four lines at a time, the memo shifting at its end. This
// is the steady state anyone watching a chatty service lives in. The
// periodic block trim is a different workload, measured on its own below —
// mixed in here, short and long runs bought different fractions of the
// 512-append:1-trim cycle and measured different things.
func BenchmarkViewFullBuffer(b *testing.B) {
	m := benchModel(132, 40, 120, maxLogLines, false)
	arriving := benchLogs(4)
	m.logs = append(m.logs, arriving...)[:maxLogLines] // pre-grow off the timer
	snap := primeBuffer(m)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		holdBufferAt(b, m, maxLogLines, snap)
		m.Update(journalBatch{gen: m.logGen, lines: arriving})
		_ = m.View()
	}
}

// And the trim frame itself: the batch that carries the buffer past
// cap+slack, measuring the heights of the oldest ~2k lines, dropping them,
// and shifting the memo by both ends.
func BenchmarkViewFullBufferTrim(b *testing.B) {
	m := benchModel(132, 40, 120, maxLogLines+logTrimSlack, false)
	arriving := benchLogs(4)
	template := append([]logLine(nil), m.logs...)
	m.logs = append(m.logs, arriving...)[:len(template)] // pre-grow off the timer
	snap := primeBuffer(m)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m.logs = append(m.logs[:0], template...) // a trim rearranges the slice; restore it
		m.logEpoch = snap.epoch
		*m.totals = snap
		b.StartTimer()
		m.Update(journalBatch{gen: m.logGen, lines: arriving})
		_ = m.View()
	}
}

// And scrolling in a full buffer, which asks for the total on every keypress.
// The position is reset each iteration — two stores, nothing against a
// millisecond frame — so every iteration measures the same keypress: the
// first pgup from the live end. Left to accumulate, the walk hit the clamp
// after ~1500 iterations and measured the deepest window from then on, so
// ns/op depended on -benchtime.
func BenchmarkScrollFullBuffer(b *testing.B) {
	m := benchModel(132, 40, 120, maxLogLines, false)
	m.focus = focusLogs
	// The first scroll arithmetic recounts all 20k entries to prime the memo;
	// pay that once, off the timer, or ns/op depends on how many iterations
	// the one-time cost is amortised over.
	_ = m.logDisplayTotal()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.logScroll, m.logFollow = 0, true
		m.logKey("pgup")
		_ = m.View()
	}
}

// Scrolled far back in a full buffer. renderLogWindow walks from the newest
// entry until it has enough display lines, so the further back the view is, the
// more it touches — and it prepends each entry's lines to the accumulator,
// which copies what it has already built every time.
func BenchmarkScrollDeep(b *testing.B) {
	for _, depth := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			m := benchModel(132, 40, 120, maxLogLines, false)
			m.focus = focusLogs
			m.logFollow, m.logScroll = false, depth
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.renderLogWindow(m.logInnerWidth(), m.logHeight())
			}
		})
	}
}
