package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLogFilterArgs(t *testing.T) {
	if got := (logFilter{}).args(); len(got) != 0 {
		t.Errorf("an empty filter should add no arguments: %v", got)
	}
	got := strings.Join(logFilter{grep: "timed out", prio: 3}.args(), " ")
	if got != "-g timed out -p 3" {
		t.Errorf("args = %q", got)
	}
	if !(logFilter{}).empty() {
		t.Error("the zero filter is empty")
	}
	if (logFilter{prio: 4}).empty() || (logFilter{grep: "x"}).empty() {
		t.Error("either half makes it non-empty")
	}
	// The label says what is being left out, in words — the flags that do it
	// explain nothing to someone who did not type them.
	if got := (logFilter{grep: "boom", prio: 4}).label(); got != `matching "boom", warning and above` {
		t.Errorf("label = %q", got)
	}
}

func TestPriorityCycle(t *testing.T) {
	p := 0
	for _, want := range []int{4, 3, 0, 4} {
		if p = nextPriority(p); p != want {
			t.Fatalf("cycle reached %d, want %d", p, want)
		}
	}
}

// The filter is applied by journalctl, so changing it has to rerun it — both
// for the follow stream and for backwards paging.
func TestLogFilterRestartsTheStream(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	stopJournalOnCleanup(t, &m)

	if cmd := m.syncJournal(); cmd == nil {
		t.Fatal("no stream started for the selected unit")
	}
	first := m.journal
	if cmd := m.syncJournal(); cmd != nil || m.journal != first {
		t.Error("an unchanged filter should not restart the stream")
	}

	m.logFilt.prio = 3
	if cmd := m.syncJournal(); cmd == nil {
		t.Fatal("a priority change should restart the stream")
	}
	if m.journal == first {
		t.Error("the stream was not replaced")
	}
	if m.journal.filter.prio != 3 {
		t.Errorf("the new stream carries filter %+v", m.journal.filter)
	}

	m.logFilt.grep = "session"
	before := m.journal
	if cmd := m.syncJournal(); cmd == nil || m.journal == before {
		t.Error("a grep change should restart the stream too")
	}
	if m.journal.filter.grep != "session" {
		t.Errorf("grep not carried: %+v", m.journal.filter)
	}
}

// "/" targets whichever pane is being read, and the two filters stay separate.
func TestSlashTargetsTheFocusedPane(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	stopJournalOnCleanup(t, &m)

	m.handleKey(keyOf("/"))
	if m.filterLogs {
		t.Error("with the table focused, / should filter units")
	}
	for _, r := range "ngin" {
		m.handleKey(keyOf(string(r)))
	}
	m.handleKey(keyOf("\r"))
	if m.filter != "ngin" || m.logFilt.grep != "" {
		t.Errorf("typed into the wrong filter: units=%q log=%q", m.filter, m.logFilt.grep)
	}

	m.focus = focusLogs
	m.handleKey(keyOf("/"))
	if !m.filterLogs {
		t.Error("with the log focused, / should search the log")
	}
	for _, r := range "boom" {
		m.handleKey(keyOf(string(r)))
	}
	// The keystrokes land in the log editor's draft, not the table filter.
	if m.logDraft != "boom" {
		t.Errorf("log search draft = %q", m.logDraft)
	}
	if m.filter != "ngin" {
		t.Errorf("the unit filter was disturbed: %q", m.filter)
	}
	// Esc cancels the draft; the applied log filter (empty here) and the table
	// filter are both left alone.
	m.handleKey(escKey())
	if m.logFilt.grep != "" || m.filter != "ngin" {
		t.Errorf("esc cleared the wrong filter: units=%q log=%q", m.filter, m.logFilt.grep)
	}
}

// An active filter has to be visible, or a quiet log looks like a broken one.
func TestActiveLogFilterIsShown(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	m.logFilt = logFilter{grep: "denied", prio: 3}

	// It rides in the log pane's own title, next to the unit it belongs to.
	title := stripANSI(m.logTitle(80))
	if !strings.Contains(title, `matching "denied"`) || !strings.Contains(title, "error and above") {
		t.Errorf("the active filter is not shown in the log pane title: %q", title)
	}
	if !strings.Contains(stripANSI(m.View()), `matching "denied"`) {
		t.Error("the active filter did not reach the screen")
	}
}

// The same for the table: a filtered list has to say so, or it reads as a
// machine with almost nothing running on it.
func TestActiveUnitFilterIsShown(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.filter = "ngin"
	m.rebuild()

	title := stripANSI(m.tableTitle(80))
	if !strings.Contains(title, `name or description contains "ngin"`) {
		t.Errorf("the unit filter is not shown in the table title: %q", title)
	}
	if !strings.Contains(title, "of "+strconv.Itoa(len(m.units))) {
		t.Errorf("the title should say how many units were filtered out: %q", title)
	}
}

// journalctl -f prints nothing at all when the filter matches nothing, so an
// empty pane used to sit on "waiting for journal…" as though it were stuck. It
// has to say which of the three it is.
func TestEmptyLogSaysWhy(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	// No stream at all — a slice row, say.
	if got := stripANSI(strings.Join(m.emptyLogNotice(), " ")); !strings.Contains(got, "no journal") {
		t.Errorf("with no stream: %q", got)
	}

	// A stream that has just started is still reading.
	m.journal = &journalStream{unit: "nginx.service", gen: m.logGen}
	if got := stripANSI(strings.Join(m.emptyLogNotice(), " ")); !strings.Contains(got, "reading the journal") {
		t.Errorf("just started: %q", got)
	}
	if !m.logStarting() {
		t.Error("a stream whose backlog has not landed should count as starting")
	}

	// The backlog command has finished and produced nothing. That is a fact the
	// stream reports, not a timeout.
	m.Update(journalBatch{gen: m.logGen, backlogDone: true})
	if m.logStarting() {
		t.Error("the backlog-done signal did not land")
	}
	if got := stripANSI(strings.Join(m.emptyLogNotice(), " ")); !strings.Contains(got, "nothing to the journal") {
		t.Errorf("empty and settled: %q", got)
	}
	m.logFilt = logFilter{grep: "boom", prio: 3}
	got := stripANSI(strings.Join(m.emptyLogNotice(), " "))
	for _, want := range []string{"no entries", `matching "boom"`, "error and above", "esc clears it"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered and empty: missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "waiting") {
		t.Errorf("still claiming to be waiting: %q", got)
	}

	// And that is what the pane actually renders, not just what the helper says.
	win := stripANSI(strings.Join(m.renderLogWindow(m.logInnerWidth(), m.logHeight()), " "))
	if !strings.Contains(win, "no entries") || !strings.Contains(win, `matching "boom"`) {
		t.Errorf("the pane does not show it: %q", win)
	}
}

// A real decoded space arrives as KeySpace carrying Runes == " ". Appending
// the runes and then a literal space put two spaces in per press, so typing
// "timed out" silently searched for "timed  out" — in the unit filter and in
// the journal grep alike. Each editor takes a real-shaped KeySpace, a
// synthetic rune-less one, and a bracketed paste, and each event must land
// as exactly what it carried.
func TestSpaceInsertsExactlyOneSpace(t *testing.T) {
	realSpace := tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	synthetic := tea.KeyMsg{Type: tea.KeySpace}
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("timed out"), Paste: true}

	editors := []struct {
		name string
		logs bool
		read func(m *model) string
	}{
		{"unit filter", false, func(m *model) string { return m.filter }},
		{"journal grep", true, func(m *model) string { return m.logDraft }},
	}
	for _, ed := range editors {
		mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m := &mm
		m.width, m.height, m.ready = 140, 30, true
		m.connected = true
		m.units = testUnits()
		m.rebuild()
		if ed.logs {
			m.focus = focusLogs
		}

		m.handleKey(keyOf("/"))
		if m.filterLogs != ed.logs {
			t.Fatalf("%s: / targeted the wrong editor", ed.name)
		}
		m.handleKey(keyOf("a"))
		m.handleKey(realSpace)
		m.handleKey(synthetic)
		m.handleKey(keyOf("b"))
		m.handleKey(paste)
		if want := "a  btimed out"; ed.read(m) != want {
			t.Errorf("%s = %q, want %q (one space per space event, paste verbatim)", ed.name, ed.read(m), want)
		}
	}
}
