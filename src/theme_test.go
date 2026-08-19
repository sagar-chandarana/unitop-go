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
