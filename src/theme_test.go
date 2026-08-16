package main

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The ramp must actually ramp: five distinct steps, each reached in order, all
// from the terminal's own palette.
func TestHeatRamps(t *testing.T) {
	const q, w, h, x = 10, 20, 30, 40
	steps := []struct {
		v    float64
		want lipgloss.TerminalColor
		name string
	}{
		{0, colGrey, "idle"},
		{q - 1, colGrey, "below notice"},
		{q, colGreen, "normal"},
		{w, colYellow, "worth a glance"},
		{h, colOrange, "high"},
		{x, colRed, "wrong"},
		{x * 100, colRed, "far past wrong"},
	}
	seen := map[lipgloss.TerminalColor]bool{}
	for _, s := range steps {
		got := heat(s.v, q, w, h, x)
		if got != s.want {
			t.Errorf("%s (v=%v): got %v, want %v", s.name, s.v, got, s.want)
		}
		seen[got] = true
	}
	if len(seen) != 5 {
		t.Errorf("the ramp collapsed to %d steps, want 5", len(seen))
	}
	// Every step is one of the sixteen; nothing invented.
	for c := range seen {
		switch c {
		case colGrey, colGreen, colYellow, colOrange, colRed:
		default:
			t.Errorf("heat reached outside the palette: %v", c)
		}
	}
}
