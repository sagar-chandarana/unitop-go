package main

import (
	"strings"
	"testing"
	"time"
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
	if got := (logFilter{grep: "boom", prio: 4}).label(); got != "/boom warning+" {
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
	if m.logFilt.grep != "boom" {
		t.Errorf("log search = %q", m.logFilt.grep)
	}
	if m.filter != "ngin" {
		t.Errorf("the unit filter was disturbed: %q", m.filter)
	}
	// Esc clears only the one being edited.
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

	got := stripANSI(strings.Join(m.unitDetail(testUnits()[0], 140), "\n"))
	if !strings.Contains(got, "/denied") || !strings.Contains(got, "error+") {
		t.Errorf("the active filter is not shown in the detail block:\n%s", got)
	}
}
