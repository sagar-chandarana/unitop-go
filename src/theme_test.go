package main

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The ramp must actually ramp: five distinct steps, each reached in order. The
// four hot ones are named colours; the quiet one is deliberately not — it is
// the terminal's own foreground, dimmed, because colour 8 is not safe as text.
func TestHeatRamps(t *testing.T) {
	const q, w, h, x = 10, 20, 30, 40
	steps := []struct {
		v     float64
		want  lipgloss.TerminalColor
		faint bool
		name  string
	}{
		{0, lipgloss.NoColor{}, true, "idle"},
		{q - 1, lipgloss.NoColor{}, true, "below notice"},
		{q, colGreen, false, "normal"},
		{w, colYellow, false, "worth a glance"},
		{h, colOrange, false, "high"},
		{x, colRed, false, "wrong"},
		{x * 100, colRed, false, "far past wrong"},
	}
	seen := map[lipgloss.TerminalColor]bool{}
	for _, s := range steps {
		got := heat(s.v, q, w, h, x)
		if got.GetForeground() != s.want {
			t.Errorf("%s (v=%v): got %v, want %v", s.name, s.v, got.GetForeground(), s.want)
		}
		if got.GetFaint() != s.faint {
			t.Errorf("%s (v=%v): faint %v, want %v", s.name, s.v, got.GetFaint(), s.faint)
		}
		if got.GetBold() {
			t.Errorf("%s (v=%v): a magnitude must not be bold", s.name, s.v)
		}
		seen[got.GetForeground()] = true
	}
	if len(seen) != 5 {
		t.Errorf("the ramp collapsed to %d steps, want 5", len(seen))
	}
	// Every coloured step is one of the sixteen; nothing invented.
	for c := range seen {
		switch c {
		case lipgloss.NoColor{}, colGreen, colYellow, colOrange, colRed:
		default:
			t.Errorf("heat reached outside the palette: %v", c)
		}
	}
}

// The sorted column says which way it is sorted twice over — the arrow and the
// colour — but "high to low" is only a real direction on the magnitude columns,
// so the colour must track that and stay neutral where it would lie. Whichever
// key, the sorted title stays bold so it carries weight.
func TestSortStyleShowsDirection(t *testing.T) {
	// Every magnitude column: unreversed is high to low (red), reversed low to
	// high (green).
	for _, key := range []sortKey{
		sortCPU, sortMem, sortNetIn, sortNetOut,
		sortIORead, sortIOWrite, sortRestarts, sortTasks,
	} {
		if got := sortStyle(key, false).GetForeground(); got != colRed {
			t.Errorf("%s high to low should be red, got %v", key, got)
		}
		if got := sortStyle(key, true).GetForeground(); got != colGreen {
			t.Errorf("%s low to high should be green, got %v", key, got)
		}
	}

	// Uptime sorts on the start timestamp: unreversed shows the shortest age
	// first — low to high, green — and reversed the longest, high to low, red.
	// That is inverted from a magnitude key.
	if got := sortStyle(sortUptime, false).GetForeground(); got != colGreen {
		t.Errorf("uptime newest-first is shortest age first: should be green, got %v", got)
	}
	if got := sortStyle(sortUptime, true).GetForeground(); got != colRed {
		t.Errorf("uptime oldest-first is longest age first: should be red, got %v", got)
	}

	// Name is alphabetical and state is an attention rank, not magnitudes: no
	// colour at all either way — exactly the terminal's own foreground, so an
	// accidental yellow or cyan would fail here too, not only red or green.
	for _, key := range []sortKey{sortName, sortState} {
		for _, reverse := range []bool{false, true} {
			if fg := sortStyle(key, reverse).GetForeground(); fg != (lipgloss.NoColor{}) {
				t.Errorf("%s reverse=%v must claim no direction colour, got %v", key, reverse, fg)
			}
		}
	}

	// The sorted title is bold whichever column and whichever way it sorts.
	for _, key := range []sortKey{sortCPU, sortUptime, sortName, sortState} {
		if !sortStyle(key, false).GetBold() || !sortStyle(key, true).GetBold() {
			t.Errorf("%s sorted title should be bold both ways", key)
		}
	}
}
