package main

import "github.com/charmbracelet/lipgloss"

// The palette is the terminal's own sixteen ANSI colours, as htop uses. Naming
// a colour by index rather than by hex means it is whatever the user's theme
// says it is, so unitop matches the rest of their terminal and needs no
// light/dark handling of its own. Six hues carry meaning and nothing else is
// coloured:
//
//	green   healthy, running
//	yellow  in transition, or a number worth watching
//	red     failed, or a number that is too high
//	cyan    finished cleanly, and rates
//	blue    keys and interactive hints
//	magenta headings and the selected sort
//
// Anything that is merely context is grey, and anything below notice is dim.
var (
	colDefault = lipgloss.NoColor{}  // the terminal's own foreground
	colGrey    = lipgloss.Color("8") // bright black: present but secondary
	colRed     = lipgloss.Color("1") //
	colGreen   = lipgloss.Color("2") //
	colYellow  = lipgloss.Color("3") //
	colBlue    = lipgloss.Color("4") //
	colMagenta = lipgloss.Color("5") //
	colCyan    = lipgloss.Color("6") //
	colSelBg   = lipgloss.Color("6") // the selected row: black on cyan, as htop
	colSelFg   = lipgloss.Color("0") // the selected row's text, against that
)

var (
	stBase    = lipgloss.NewStyle()
	stSubtle  = lipgloss.NewStyle().Foreground(colGrey)
	stFaint   = lipgloss.NewStyle().Foreground(colGrey).Faint(true)
	stHeader  = lipgloss.NewStyle().Foreground(colMagenta).Bold(true)
	stColHead = lipgloss.NewStyle().Bold(true)
	stSortCol = lipgloss.NewStyle().Foreground(colMagenta).Bold(true)
	stAccent  = lipgloss.NewStyle().Foreground(colCyan)
	stKey     = lipgloss.NewStyle().Foreground(colBlue)
	stBad     = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	stWarn    = lipgloss.NewStyle().Foreground(colYellow)
	stGood    = lipgloss.NewStyle().Foreground(colGreen)
	stBorder  = lipgloss.NewStyle().Foreground(colGrey).Faint(true)
	stFilter  = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
)

// heat maps a magnitude onto four steps — dim, green, yellow, red. quiet is
// the value below which something is not worth looking at, warn where it
// starts to matter, and high where it is a problem.
func heat(v, quiet, warn, high float64) lipgloss.TerminalColor {
	switch {
	case v < quiet:
		return colGrey
	case v < warn:
		return colGreen
	case v < high:
		return colYellow
	default:
		return colRed
	}
}

// stateColor picks a colour for a unit's state. Among the inactive units it
// separates the three cases that systemd renders identically as dead: ran and
// finished (grey), skipped (dimmer still), and never ran.
func stateColor(u Unit) lipgloss.TerminalColor {
	switch u.Active {
	case "failed":
		return colRed
	case "activating", "deactivating", "reloading":
		return colYellow
	case "active":
		if u.Sub == "exited" {
			return colCyan
		}
		return colGreen
	case "inactive":
		switch {
		case u.Skipped():
			return colGrey
		case u.Result != "" && u.Result != "success":
			return colYellow
		case u.ExecCode == execExited && u.ExecStatus != 0:
			return colYellow
		case u.Ran():
			return colCyan
		}
		return colGrey
	default:
		return colGrey
	}
}

// prioColor maps a syslog priority to a colour for the log pane. Only the
// levels worth reacting to are coloured; ordinary output stays the terminal's
// own foreground, as a pager would leave it.
func prioColor(p int) lipgloss.TerminalColor {
	switch {
	case p <= 3: // emerg, alert, crit, err
		return colRed
	case p == 4: // warning
		return colYellow
	case p == 5: // notice
		return colCyan
	case p == 6: // info
		return colDefault
	default: // debug and below
		return colGrey
	}
}
