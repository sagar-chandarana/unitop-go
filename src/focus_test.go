package main

import (
	"strings"
	"testing"
	"time"
)

func focusModel(t *testing.T) *model {
	t.Helper()
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	if !m.logPaneVisible() {
		t.Fatal("the log pane should be visible at this width")
	}
	return &m
}

// A key that belongs to the other pane does nothing, and is not offered. The
// alternative is worse than either: pressing s while reading the log resorted
// the table silently behind it.
func TestKeysBelongToTheFocusedPane(t *testing.T) {
	m := focusModel(t)

	// Table focused: the log's own keys are inert — including F and f, which
	// are the log's two ends and belong to no other pane.
	follow, wrap, prio, scroll := m.logFollow, m.logWrap, m.logFilt.prio, m.logScroll
	cursor := m.cursor
	for _, k := range []string{"f", "F", "e", "w"} {
		m.handleKey(keyOf(k))
	}
	if m.logFollow != follow || m.logWrap != wrap || m.logFilt.prio != prio || m.logScroll != scroll {
		t.Error("log keys acted while the table had focus")
	}
	if m.cursor != cursor {
		t.Errorf("a log key moved the table cursor: %d -> %d", cursor, m.cursor)
	}

	// Log focused: the table's are.
	m.focus = focusLogs
	sortBy, reverse, tree, showAll := m.sortBy, m.reverse, m.tree, m.showAll
	for _, k := range []string{"s", "S", "i", "t", "a"} {
		m.handleKey(keyOf(k))
	}
	if m.sortBy != sortBy || m.reverse != reverse || m.tree != tree || m.showAll != showAll {
		t.Error("table keys acted while the log had focus")
	}

	// And the log's now work.
	m.handleKey(keyOf("w"))
	if m.logWrap == wrap {
		t.Error("w did nothing with the log focused")
	}

	// tab crosses between them, but only when there are two panes to cross.
	m.handleKey(keyOf("tab"))
	if m.focus != focusList {
		t.Error("tab did not return focus to the table")
	}
	m.showLogs = false
	m.handleKey(keyOf("tab"))
	if m.focus != focusList {
		t.Error("tab moved focus to a hidden pane")
	}
}

// The footer names what the next keystroke can do, and only that.
func TestFooterFollowsTheFocus(t *testing.T) {
	m := focusModel(t)

	foot := stripANSI(m.viewFooter())
	for _, want := range []string{"/ filter units", "s sort", "i invert", "t tree", "a all"} {
		if !strings.Contains(foot, want) {
			t.Errorf("table footer is missing %q: %s", want, foot)
		}
	}
	for _, gone := range []string{"f follow", "F/f top", "e level", "w wrap", "/ search"} {
		if strings.Contains(foot, gone) {
			t.Errorf("table footer offers the log's %q: %s", gone, foot)
		}
	}

	m.focus = focusLogs
	foot = stripANSI(m.viewFooter())
	for _, want := range []string{"/ search log", "F/f top/bottom", "e level", "f follow", "w wrap"} {
		if !strings.Contains(foot, want) {
			t.Errorf("log footer is missing %q: %s", want, foot)
		}
	}
	for _, gone := range []string{"s sort", "i invert", "t tree", "a all", "filter units"} {
		if strings.Contains(foot, gone) {
			t.Errorf("log footer offers the table's %q: %s", gone, foot)
		}
	}

	// Every hint offered must actually be live, and every live key offered.
	for _, k := range []string{"f", "F", "e", "w"} {
		if !m.keyApplies(k) {
			t.Errorf("%q is offered but inert", k)
		}
	}
}

// Focus is drawn on the whole pane, not on a divider: the focused box is heavy,
// the other light. Both are always present, so nothing moves when focus does.
func TestFocusedPaneIsFramed(t *testing.T) {
	m := focusModel(t)

	body := func() []string { return m.viewBody() }
	tableFocused := body()
	if !strings.Contains(tableFocused[0], "┏") {
		t.Errorf("the focused table is not drawn heavy: %q", stripANSI(tableFocused[0]))
	}
	if !strings.Contains(tableFocused[0], "╭") {
		t.Errorf("the unfocused log is not drawn light: %q", stripANSI(tableFocused[0]))
	}

	m.focus = focusLogs
	logFocused := body()
	if strings.Index(stripANSI(logFocused[0]), "┏") <= strings.Index(stripANSI(logFocused[0]), "╭") {
		t.Errorf("focus did not move to the log pane: %q", stripANSI(logFocused[0]))
	}

	// Same geometry either way — focus must not shift the layout.
	for i := range tableFocused {
		if a, b := lipglossWidth(tableFocused[i]), lipglossWidth(logFocused[i]); a != b {
			t.Fatalf("line %d is %d wide focused on the table and %d on the log", i, a, b)
		}
	}
	for _, l := range tableFocused {
		if w := lipglossWidth(l); w > m.width {
			t.Fatalf("a framed line is %d wide, screen is %d", w, m.width)
		}
	}

	// The full view is one pane, and it always has the focus.
	m.fullView = true
	full := body()
	if !strings.Contains(full[0], "┏") || strings.Contains(full[0], "╭") {
		t.Errorf("the full view should be one focused box: %q", stripANSI(full[0]))
	}
}
