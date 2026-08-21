package main

import (
	"strings"
	"testing"
	"time"
)

// Full view fixes the detail block at seven rows, but at short supported heights
// the pane is only a few rows tall — the detail block plus its rule overflow and
// framed clips every log row, so the pane is titled "log" and takes log controls
// while showing zero log lines. At least one log row and the separator must
// survive at every supported height. Pure rendering: no stream is started.
func TestFullViewKeepsALogRowAtShortHeights(t *testing.T) {
	const line = "FULLVIEW-LINE-9"
	for h := minHeight; h <= 14; h++ {
		m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m.width, m.height, m.ready = 100, h, true
		m.connected = true
		m.units = testUnits()
		m.rebuild()
		m.cursor = firstUnitRow(t, &m)

		// Enter full view directly — activateRow would start a real journalctl
		// follow, which a pure rendering test has no business launching.
		m.fullView, m.showLogs, m.focus = true, true, focusLogs
		m.logs = []logLine{{ts: time.Now(), prio: 6, msg: line, cursor: "c1"}}
		m.logBacklogDone = true
		m.logFollow, m.logScroll = true, 0
		m.logEpoch++

		lines := strings.Split(stripANSI(m.View()), "\n")

		// Frame geometry: the view is exactly h logical rows.
		if len(lines) != h {
			t.Errorf("h=%d: View() has %d rows, want %d", h, len(lines), h)
		}

		// The sentinel is on screen, and it sits below a separator rule — so it
		// is genuinely in the log region, not surfaced elsewhere by a reflow.
		sentinel := -1
		for i, l := range lines {
			if strings.Contains(l, line) {
				sentinel = i
				break
			}
		}
		if sentinel < 0 {
			t.Errorf("h=%d: the full-view log pane shows no rows (%q missing)", h, line)
			continue
		}
		ruleBefore := false
		for _, l := range lines[:sentinel] {
			if strings.Count(l, "─") >= 3 {
				ruleBefore = true
				break
			}
		}
		if !ruleBefore {
			t.Errorf("h=%d: no separator rule precedes the log line; it is not in the log region", h)
		}
	}
}
