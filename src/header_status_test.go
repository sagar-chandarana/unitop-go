package main

import (
	"strings"
	"testing"
	"time"
)

// statusModel is the worst crowd the header can draw: a long host label,
// live host stats, a long filter, tree/all/reverse, and failed units — every
// expendable competitor present at once.
func statusModel(t *testing.T, w, h int) *model {
	t.Helper()
	mm := newModel(runner{}, "a-very-long-sanitized-hostname.internal.example.com",
		time.Second, sortCPU, true, true, true, "a-long-unit-filter-string")
	m := &mm
	m.width, m.height, m.ready, m.connected = w, h, true, true
	m.units = testUnits()
	m.host = HostStats{OK: true, NCPU: 12, Uptime: 90 * 24 * time.Hour,
		Load: [3]float64{2.5, 2.6, 2.7}, CPUPct: 42,
		MemUsed: 15 << 30, MemTotal: 29 << 30, SwapTotal: 8 << 30, SwapUsed: 5 << 30,
		NetIn: 12000, NetOut: 1500}
	m.rebuild()
	return m
}

// The critical status survives every supported geometry, in all three header
// shapes: width-compact (<76), height-compact (wide but short), and normal.
// Identity, usage, sort/tree/all and the filter are all expendable first.
func TestCriticalStatusSurvivesEveryHeaderShape(t *testing.T) {
	sizes := [][2]int{
		{40, 12},  // width-compact at the minimum supported width
		{75, 30},  // width-compact at its widest
		{76, 30},  // the first normal three-line width
		{120, 19}, // height-compact: wide but short
		{200, 40}, // roomy normal
	}
	for _, size := range sizes {
		for _, state := range []string{"paused", "fatal"} {
			m := statusModel(t, size[0], size[1])
			switch state {
			case "paused":
				m.paused = true
			case "fatal":
				m.fatal = true
				m.polling = false
			}
			rows := strings.Split(stripANSI(m.View()), "\n")
			if len(rows) != m.height {
				t.Fatalf("%v %s: frame is %d rows of %d", size, state, len(rows), m.height)
			}
			header := strings.Join(rows[:m.headerLines()], "\n")
			for i, line := range rows {
				got := lipglossWidth(line)
				if got > m.width {
					t.Errorf("%v %s: row %d is %d cells of %d", size, state, i, got, m.width)
				}
				// The header composer owes EXACT width — reserving the status
				// must neither overflow nor leave the header ragged. The footer
				// and body pad themselves by their own rules and are left alone.
				if i < m.headerLines() && got != m.width {
					t.Errorf("%v %s: header row %d is %d cells, want exactly %d", size, state, i, got, m.width)
				}
			}
			switch state {
			case "paused":
				if !strings.Contains(header, "PAUSED") {
					t.Errorf("%v: PAUSED lost from the header:\n%s", size, header)
				}
			case "fatal":
				if !strings.Contains(header, "NOT POLLING") || !strings.Contains(header, "R to retry") {
					t.Errorf("%v: the full stopped phrase lost from the header:\n%s", size, header)
				}
			}
		}
	}
}

// A normal, healthy header invents no stopped status and keeps its shape.
func TestNormalHeaderInventsNoStatus(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {76, 30}, {200, 40}} {
		m := statusModel(t, size[0], size[1])
		out := stripANSI(m.View())
		if strings.Contains(out, "PAUSED") || strings.Contains(out, "NOT POLLING") {
			t.Errorf("%v: a healthy header claims a stopped status:\n%s", size, out)
		}
		rows := strings.Split(out, "\n")
		if len(rows) != m.height {
			t.Errorf("%v: frame is %d rows of %d", size, len(rows), m.height)
		}
	}
}
