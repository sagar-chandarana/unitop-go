package main

import "github.com/charmbracelet/lipgloss"

// The palette is the terminal's own sixteen ANSI colours, as htop uses. Naming
// a colour by index rather than by hex means it is whatever the user's theme
// says it is, so unitop matches the rest of their terminal and needs no
// light/dark handling of its own.
//
// Colour never carries something you have to *read*. Five hues carry meaning:
//
//	green   healthy, running — and a column sorted low to high
//	yellow  in transition, a number worth watching, and the active filter
//	red     failed, a number that is too high — and sorted high to low
//	cyan    finished cleanly, and rates
//	blue    keys and interactive hints
//
// The host name is cyan: it is the one heading that answers "which machine am I
// looking at", it is short, bold and always in the same corner, so the hue is
// worth the risk that a theme renders cyan weakly. Every other heading is
// colourless.
//
// The host name is cyan: it is the one heading that answers "which machine am
// I looking at", it is short, bold, and always in the same corner, so the hue
// is worth what a weak cyan costs. Every other heading is colourless.
//
// Two more are decoration only, never text: magenta draws the focused frame,
// the menu box, the spinners and the editor caret, and colour 8 draws the
// rules and the unfocused frame. Both may be nearly invisible on some theme
// and nothing is lost when they are — a frame is also a heavier glyph than
// the unfocused one, and the sort arrow says the direction the colour shows.
//
// What must be read — headings, the host name, column titles, labels,
// timestamps, idle values, unit and slice names, log lines — is the
// terminal's own foreground: bold for emphasis, faint for below notice.
// That is not a stylistic preference, it is the only thing that survives an
// arbitrary theme. Measured across two real ones:
//
//	                 duskfox  latte
//	magenta (5)         7.47   1.65   frames and spinners — decoration
//	yellow (3)          9.55   1.73   warnings, and the filter
//	grey (8)            1.71   4.37   rules and the unfocused frame
//	default foreground 11.86   7.06   everything there is to read
//
// Contrast against each theme's own background. A palette entry can fail on
// either side — pale colours meant for a dark ground wash out on a light one,
// and 8 is dark enough to be a background shade on a dark one — which is why
// what must be read takes the foreground the user already reads everything
// else in, and why the two weakest hues are only ever decoration.
//
// The filter is the deliberate exception: it is yellow and it is words, so on
// a light theme carrying a dark theme's palette it will be pale. That is a
// choice — a filter is a mode you have to notice you are in, the pane it
// applies to is titled with it, and it is bold besides.
//
// Never write a hex value or a 256-colour index: it would override the theme
// and force the light/dark problem back in.
var (
	colDefault = lipgloss.NoColor{}   // the terminal's own foreground
	colGrey    = lipgloss.Color("8")  // rules and frames only — never text
	colRed     = lipgloss.Color("1")  //
	colGreen   = lipgloss.Color("2")  //
	colYellow  = lipgloss.Color("3")  //
	colBlue    = lipgloss.Color("4")  //
	colMagenta = lipgloss.Color("5")  // frames and spinners only — never text
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
	stBase     = lipgloss.NewStyle()
	stFaint    = lipgloss.NewStyle().Faint(true)
	stHeader   = lipgloss.NewStyle().Bold(true)
	stHost     = lipgloss.NewStyle().Foreground(colCyan).Bold(true)
	stColHead  = lipgloss.NewStyle() // plain: the sorted column is the one with weight
	stSortDesc = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	stSortAsc  = lipgloss.NewStyle().Foreground(colGreen).Bold(true)
	stAccent   = lipgloss.NewStyle().Foreground(colCyan)
	stKey      = lipgloss.NewStyle().Foreground(colBlue)
	stAlert    = lipgloss.NewStyle().Foreground(colRed)
	stBad      = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	stWarn     = lipgloss.NewStyle().Foreground(colYellow)
	stGood     = lipgloss.NewStyle().Foreground(colGreen)
	stBorder   = lipgloss.NewStyle().Foreground(colGrey).Faint(true)
	stFrame    = lipgloss.NewStyle().Foreground(colMagenta)
	stFilter   = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
)

// sortStyle colours the sorted column's title by which way it is sorted —
// red for high to low, green for low to high — so the direction is legible
// from the colour as well as from the arrow beside it. This is the one place
// red and green are a direction rather than health; everywhere else they keep
// their usual meaning, and nothing here is worse off if the colours are
// missed, because the arrow already says it.
func sortStyle(reverse bool) lipgloss.Style {
	if reverse {
		return stSortAsc
	}
	return stSortDesc
}

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
