package main

import "github.com/charmbracelet/lipgloss"

// Catppuccin-derived palette, adaptive so the TUI stays legible on light and
// dark terminals alike. Dark value first, light value second.
var (
	colText    = lipgloss.AdaptiveColor{Dark: "#cdd6f4", Light: "#4c4f69"}
	colSubtle  = lipgloss.AdaptiveColor{Dark: "#6c7086", Light: "#9ca0b0"}
	colFaint   = lipgloss.AdaptiveColor{Dark: "#45475a", Light: "#bcc0cc"}
	colGreen   = lipgloss.AdaptiveColor{Dark: "#a6e3a1", Light: "#40a02b"}
	colYellow  = lipgloss.AdaptiveColor{Dark: "#f9e2af", Light: "#df8e1d"}
	colPeach   = lipgloss.AdaptiveColor{Dark: "#fab387", Light: "#fe640b"}
	colRed     = lipgloss.AdaptiveColor{Dark: "#f38ba8", Light: "#d20f39"}
	colMauve   = lipgloss.AdaptiveColor{Dark: "#cba6f7", Light: "#8839ef"}
	colBlue    = lipgloss.AdaptiveColor{Dark: "#89b4fa", Light: "#1e66f5"}
	colTeal    = lipgloss.AdaptiveColor{Dark: "#94e2d5", Light: "#179299"}
	colSelBg   = lipgloss.AdaptiveColor{Dark: "#313244", Light: "#dce0e8"}
	colPanelBd = lipgloss.AdaptiveColor{Dark: "#45475a", Light: "#ccd0da"}
)

var (
	stBase    = lipgloss.NewStyle().Foreground(colText)
	stSubtle  = lipgloss.NewStyle().Foreground(colSubtle)
	stFaint   = lipgloss.NewStyle().Foreground(colFaint)
	stHeader  = lipgloss.NewStyle().Foreground(colMauve).Bold(true)
	stColHead = lipgloss.NewStyle().Foreground(colSubtle).Bold(true)
	stSortCol = lipgloss.NewStyle().Foreground(colMauve).Bold(true).Underline(true)
	stAccent  = lipgloss.NewStyle().Foreground(colTeal)
	stKey     = lipgloss.NewStyle().Foreground(colBlue)
	stBad     = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	stWarn    = lipgloss.NewStyle().Foreground(colYellow)
	stGood    = lipgloss.NewStyle().Foreground(colGreen)
	stSel     = lipgloss.NewStyle().Background(colSelBg).Bold(true)
	stBorder  = lipgloss.NewStyle().Foreground(colPanelBd)
	stFilter  = lipgloss.NewStyle().Foreground(colPeach).Bold(true)
)

// heat maps a magnitude onto the palette: quiet things stay out of the way,
// busy things escalate green -> yellow -> peach -> red.
func heat(v float64, t1, t2, t3, t4 float64) lipgloss.AdaptiveColor {
	switch {
	case v <= 0:
		return colFaint
	case v < t1:
		return colSubtle
	case v < t2:
		return colGreen
	case v < t3:
		return colYellow
	case v < t4:
		return colPeach
	default:
		return colRed
	}
}

// stateColor picks a colour for a unit's state. Among the inactive units it
// separates the three cases that systemd renders identically as dead: ran and
// finished (quiet), skipped (quieter still), and never ran.
func stateColor(u Unit) lipgloss.AdaptiveColor {
	switch u.Active {
	case "failed":
		return colRed
	case "activating", "deactivating", "reloading":
		return colYellow
	case "active":
		if u.Sub == "exited" {
			return colTeal
		}
		return colGreen
	case "inactive":
		switch {
		case u.Skipped():
			return colFaint
		case u.Result != "" && u.Result != "success":
			return colPeach
		case u.ExecCode == execExited && u.ExecStatus != 0:
			return colPeach
		case u.Ran():
			return colSubtle
		}
		return colFaint // never ran
	default:
		return colSubtle
	}
}

// prioColor maps a syslog priority to a colour for the log pane.
func prioColor(p int) lipgloss.AdaptiveColor {
	switch {
	case p <= 2: // emerg, alert, crit
		return colRed
	case p == 3: // err
		return colRed
	case p == 4: // warning
		return colYellow
	case p == 5: // notice
		return colTeal
	case p == 6: // info
		return colText
	default: // debug and below
		return colSubtle
	}
}
