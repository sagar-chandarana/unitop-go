package main

import "github.com/charmbracelet/lipgloss"

// The palette is the terminal's own sixteen ANSI colours, as htop uses. Naming
// a colour by index rather than by hex means it is whatever the user's theme
// says it is, so unitop matches the rest of their terminal and needs no
// light/dark handling of its own.
//
// Colour is only ever *semantic* here. Five hues carry meaning:
//
//	green   healthy, running
//	yellow  in transition, or a number worth watching
//	red     failed, or a number that is too high
//	cyan    finished cleanly, and rates
//	blue    keys and interactive hints
//
// Structure — headings, the sorted column, the focused frame, the filter —
// is carried by *weight* instead: bold against faint, both on the terminal's
// own foreground. That is not a stylistic preference, it is the only thing
// that survives an arbitrary theme. Measured across two real ones:
//
//	                 duskfox  latte
//	magenta (5)         7.47   1.65   was headings and the focused frame
//	yellow (3)          9.55   1.73   was the filter
//	grey (8)            1.71   4.37   was every rule, frame and idle value
//	default foreground 11.86   7.06   what all three became
//
// Contrast against each theme's own background. A palette entry can fail on
// either side — pale colours meant for a dark ground wash out on a light one,
// and 8 is dark enough to be a background shade on a dark one — so anything
// that must be *read* takes the foreground the user already reads everything
// else in. Anything below notice is that same foreground, dimmed.
//
// Never write a hex value or a 256-colour index: it would override the theme
// and force the light/dark problem back in.
var (
	colDefault = lipgloss.NoColor{}   // the terminal's own foreground
	colRed     = lipgloss.Color("1")  //
	colGreen   = lipgloss.Color("2")  //
	colYellow  = lipgloss.Color("3")  //
	colBlue    = lipgloss.Color("4")  //
	colCyan    = lipgloss.Color("6")  //
	colOrange  = lipgloss.Color("11") // bright yellow: the heat step above yellow
	colSelBg   = lipgloss.Color("6")  // the selected row: black on cyan, as htop
	colSelFg   = lipgloss.Color("0")  // the selected row's text, against that
)

// Secondary text — timestamps, labels, footer hints, rules and the unfocused
// frame — is the terminal's own foreground dimmed. Faint asks the terminal to
// dim whatever its foreground happens to be, which no theme can get wrong,
// where colour 8 turned all of it into a violet smudge on duskfox. A terminal
// that ignores SGR 2 loses the hierarchy but keeps every word legible, which is
// the right way round to fail.
var (
	stBase    = lipgloss.NewStyle()
	stFaint   = lipgloss.NewStyle().Faint(true)
	stHeader  = lipgloss.NewStyle().Bold(true)
	stColHead = lipgloss.NewStyle() // plain: the sorted column is the one with weight
	stSortCol = lipgloss.NewStyle().Bold(true)
	stAccent  = lipgloss.NewStyle().Foreground(colCyan)
	stKey     = lipgloss.NewStyle().Foreground(colBlue)
	stAlert   = lipgloss.NewStyle().Foreground(colRed)
	stBad     = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	stWarn    = lipgloss.NewStyle().Foreground(colYellow)
	stGood    = lipgloss.NewStyle().Foreground(colGreen)
	stBorder  = lipgloss.NewStyle().Faint(true)
	stFilter  = lipgloss.NewStyle().Bold(true)
)

// heat ramps a magnitude across five steps, coolest to hottest:
//
//	dim     idle, or below the point of caring
//	green   normal
//	yellow  worth a glance
//	orange  high
//	red     wrong
//
// The four hues come from the terminal's own palette — orange is bright yellow,
// which every theme renders warmer than plain yellow. Four thresholds mark the
// boundaries, so a caller says where each step begins in its own units.
// The heat ramp's five steps. Plain foregrounds: a magnitude is read as a
// number, so it must not also shout in bold.
var (
	heatQuiet = stFaint
	heatOK    = stGood
	heatWarn  = stWarn
	heatHigh  = lipgloss.NewStyle().Foreground(colOrange)
	heatHot   = stAlert
)

// heat maps a magnitude onto the ramp, as a style rather than a colour: the
// quiet step has no colour of its own. It is the terminal's own foreground,
// dimmed — see the palette note above. Naming colour 8 here made every idle
// "0.0" unreadable on a theme entitled to treat 8 as a background shade.
func heat(v, quiet, warn, high, hot float64) lipgloss.Style {
	switch {
	case v < quiet:
		return heatQuiet
	case v < warn:
		return heatOK
	case v < high:
		return heatWarn
	case v < hot:
		return heatHigh
	default:
		return heatHot
	}
}

// stateStyle picks a colour for a unit's state. Among the inactive units it
// separates the three cases that systemd renders identically as dead: ran and
// finished (grey), skipped (dimmer still), and never ran.
func stateStyle(u Unit) lipgloss.Style {
	switch u.Active {
	case "failed":
		return stAlert
	case "activating", "deactivating", "reloading":
		return stWarn
	case "active":
		if u.Sub == "exited" {
			return stAccent
		}
		return stGood
	case "inactive":
		switch {
		case u.Skipped():
			return stFaint
		case u.Result != "" && u.Result != "success":
			return stWarn
		case u.ExecCode == execExited && u.ExecStatus != 0:
			return stWarn
		case u.Ran():
			return stAccent
		}
		return stFaint
	default:
		return stFaint
	}
}

// prioStyle maps a syslog priority to a colour for the log pane. Only the
// levels worth reacting to are coloured; ordinary output stays the terminal's
// own foreground, as a pager would leave it.
func prioStyle(p int) lipgloss.Style {
	switch {
	case p <= 3: // emerg, alert, crit, err
		return stAlert
	case p == 4: // warning
		return stWarn
	case p == 5: // notice
		return stAccent
	case p == 6: // info
		return stBase
	default: // debug and below
		return stFaint
	}
}
