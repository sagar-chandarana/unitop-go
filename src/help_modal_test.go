package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// helpSnap is every mutable field a hidden command could disturb while the
// help screen covers the panes. helpScroll and help itself are excluded —
// scrolling and closing are help's own documented actions.
type helpSnap struct {
	cursor, topRow                 int
	focus                          focusArea
	filter, filterWas              string
	filterInput, filterLogs        bool
	logFilt                        logFilter
	logScroll                      int
	logFollow, logWrap             bool
	menu                           ctxMenu
	sortBy                         sortKey
	reverse, tree, showAll, paused bool
	interval                       time.Duration
	showLogs                       bool
	journal                        *journalStream
}

func snapshot(m *model) helpSnap {
	return helpSnap{
		cursor: m.cursor, topRow: m.topRow, focus: m.focus,
		filter: m.filter, filterWas: m.filterWas,
		filterInput: m.filterInput, filterLogs: m.filterLogs,
		logFilt: m.logFilt, logScroll: m.logScroll,
		logFollow: m.logFollow, logWrap: m.logWrap,
		menu: m.menu, sortBy: m.sortBy, reverse: m.reverse,
		tree: m.tree, showAll: m.showAll, paused: m.paused,
		interval: m.interval, showLogs: m.showLogs, journal: m.journal,
	}
}

func helpModel(t *testing.T, w, h int) *model {
	t.Helper()
	mm := newModel(runner{}, "hostname", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = w, h, true, true
	m.units = testUnits()
	m.rebuild()
	m.journal = &journalStream{unit: "nginx.service", gen: m.logGen}
	m.help = true
	return m
}

// With help open, no command key touches the hidden panes: every key that is
// not close/quit/scroll returns no command and changes nothing but is fully
// swallowed. Across too-short and full-height help at min and wide widths.
func TestHelpSwallowsEveryCommandKey(t *testing.T) {
	// Every table/log/global command key that would otherwise act.
	cmdKeys := []string{
		"enter", "x", "/", "s", "r", "t", "a", "p", "R", "+", "-",
		"l", "w", "e", "f", "F", "tab", "left", "right",
	}
	for _, g := range [][2]int{{40, 10}, {40, 30}, {140, 10}, {140, 40}} {
		for _, k := range cmdKeys {
			m := helpModel(t, g[0], g[1])
			before := snapshot(m)
			beforeScroll, beforeHelp := m.helpScroll, m.help
			_, cmd := m.handleKey(keyOf(k))
			if cmd != nil {
				t.Errorf("%v key %q returned a command behind the help screen", g, k)
			}
			if snapshot(m) != before {
				t.Errorf("%v key %q mutated hidden state behind the help screen", g, k)
			}
			if m.helpScroll != beforeScroll {
				t.Errorf("%v key %q moved the help scroll (not a scroll key)", g, k)
			}
			if m.help != beforeHelp {
				t.Errorf("%v key %q closed help", g, k)
			}
		}
	}
}

// Its own keys still work: ? and esc close, q quits, and the scroll keys move
// helpScroll (clamped) without touching the panes.
func TestHelpOwnKeysStillAct(t *testing.T) {
	for _, k := range []string{"?", "esc"} {
		m := helpModel(t, 140, 40)
		m.handleKey(keyOf(k))
		if m.help {
			t.Errorf("%q did not close help", k)
		}
	}
	// q quits through the shared exit.
	m := helpModel(t, 140, 40)
	if _, cmd := m.handleKey(keyOf("q")); cmd == nil {
		t.Error("q did not quit from help")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q from help did not return tea.Quit")
	}

	// Scroll keys move helpScroll, and clamp — on a short help screen there is
	// somewhere to scroll; the panes stay put.
	ms := helpModel(t, 40, 10) // short: help content exceeds 10 rows
	if ms.helpScrollMax() == 0 {
		t.Skip("help fits at 40x10; no scroll to exercise")
	}
	before := snapshot(ms)
	ms.handleKey(keyOf("down"))
	if ms.helpScroll != 1 {
		t.Errorf("down did not scroll help: %d", ms.helpScroll)
	}
	if snapshot(ms) != before {
		t.Error("scrolling help disturbed the hidden panes")
	}
	ms.handleKey(keyOf("up"))
	ms.handleKey(keyOf("up")) // clamp at 0
	if ms.helpScroll != 0 {
		t.Errorf("help scroll did not clamp at 0: %d", ms.helpScroll)
	}
}

// The mouse is help's too: the wheel scrolls it (clamped), and every click —
// left, right, header — is inert.
func TestHelpOwnsTheMouse(t *testing.T) {
	m := helpModel(t, 40, 10)
	before := snapshot(m)
	press := func(b tea.MouseButton, x, y int) {
		m.handleMouse(tea.MouseMsg{Button: b, Action: tea.MouseActionPress, X: x, Y: y})
	}
	// Clicks anywhere are inert: content, header row, right-click.
	press(tea.MouseButtonLeft, 5, 5)
	press(tea.MouseButtonLeft, 10, 0) // header row
	press(tea.MouseButtonRight, 5, 5)
	if snapshot(m) != before || m.menu.open {
		t.Error("a click acted behind the help screen")
	}
	if m.helpScroll != 0 {
		t.Errorf("a click moved the help scroll: %d", m.helpScroll)
	}

	// The wheel scrolls help (clamped), nothing else.
	if m.helpScrollMax() > 0 {
		m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
		if m.helpScroll != 1 {
			t.Errorf("wheel-down did not scroll help: %d", m.helpScroll)
		}
		if snapshot(m) != before {
			t.Error("the wheel disturbed the hidden panes")
		}
		for i := 0; i < m.helpScrollMax()+5; i++ {
			m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
		}
		if m.helpScroll != m.helpScrollMax() {
			t.Errorf("wheel scroll did not clamp at max: %d/%d", m.helpScroll, m.helpScrollMax())
		}
		for i := 0; i < m.helpScrollMax()+5; i++ {
			m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
		}
		if m.helpScroll != 0 {
			t.Errorf("wheel-up did not clamp at 0: %d", m.helpScroll)
		}
	}
}
