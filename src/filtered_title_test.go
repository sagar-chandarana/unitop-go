package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// At the exact boundary — a title budget equal to the styled suffix width — the
// indicator must still fit and never overrun width-4 (or framed would clip it).
// room==0 (suffix fills the budget) and room==1 (one cell left for the head) are
// the off-by-one cases.
func TestFitTitleReservesTheSuffixAtTheBoundary(t *testing.T) {
	head := stColHead.Render("log a-longer-head")
	suffixW := lipgloss.Width(stFaint.Render(" · ") + stFilter.Render("filtered"))
	for _, room := range []int{0, 1} {
		width := suffixW + 4 + room // budget = suffixW + room
		got := fitTitle(head, width, `matching "x"`, "filtered")
		if w := lipgloss.Width(got); w > width-4 {
			t.Errorf("room=%d: title is %d cells, over the %d budget: %q", room, w, width-4, stripANSI(got))
		}
		if !strings.Contains(stripANSI(got), "filtered") {
			t.Errorf("room=%d: the indicator was dropped: %q", room, stripANSI(got))
		}
	}
}

// A narrow pane with a long unit name and an active filter must still show the
// "· filtered" indicator: fitTitle used to hand framed a title wider than the
// budget, and framed truncates from the right, cutting the indicator off. The
// indicator now survives — the expendable head (unit name) is truncated instead
// — and every frame row stays exactly the terminal width.
func TestFilteredTitleKeepsItsIndicatorWhenNarrow(t *testing.T) {
	longName := "日本語-very-long-service-name-that-overflows-any-narrow-pane.service"
	units := []Unit{{Name: longName, Desc: "a long enough description to fill the pane too",
		Active: "active", Sub: "running"}}

	for _, tc := range []struct {
		name string
		w    int
		full bool
	}{
		{"84-col split pane", 84, false},
		{"40-col full view", 40, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
			m.width, m.height, m.ready = tc.w, 24, true
			m.connected = true
			m.units = units
			m.rebuild()
			m.cursor = firstUnitRow(t, &m)
			m.logFilt = logFilter{grep: "boom"} // an active applied filter
			if tc.full {
				m.fullView, m.showLogs, m.focus = true, true, focusLogs
			}

			out := stripANSI(m.View())
			if !strings.Contains(out, "filtered") {
				t.Errorf("the filtered indicator was cut off:\n%s", out)
			}
			// Every framed row (one carrying a box-drawing edge) is exactly the
			// terminal width; footer hint lines are not frame rows.
			for i, l := range strings.Split(out, "\n") {
				if strings.ContainsAny(l, "┃│┏┓┗┛╭╮╰╯") {
					if w := lipglossWidth(l); w != tc.w {
						t.Errorf("frame line %d is %d cells of %d: %q", i, w, tc.w, l)
					}
				}
			}
		})
	}
}
