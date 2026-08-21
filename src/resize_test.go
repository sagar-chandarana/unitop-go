package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// resizeModel is a connected model at the given width with a spy stream: the
// counter says how many times a resize tore the stream down.
func resizeModel(t *testing.T, width int) (*model, *int) {
	t.Helper()
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = width, 30, true, true
	m.units = testUnits()
	m.rebuild()
	cancels := 0
	m.journal = &journalStream{unit: "nginx.service", gen: m.logGen,
		cancel: func() { cancels++ }}
	return m, &cancels
}

func resize(m *model, w, h int) tea.Cmd {
	_, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return cmd
}

// Shrinking under the split: focus returns to the table, the stream is torn
// down exactly once, and the pane state is cleared — no journalctl runs on
// behind a pane that is not there.
func TestShrinkUnderSplitReconcilesFocusAndStream(t *testing.T) {
	m, cancels := resizeModel(t, 84) // the last visible column
	m.focus = focusLogs
	// Seeded pane state that must not survive the hide.
	m.logs = benchLogs(5)
	m.logScroll, m.logFollow = 3, false

	if cmd := resize(m, 83, 30); cmd != nil {
		t.Error("hiding returned a command; the teardown is synchronous")
	}
	if m.focus != focusList {
		t.Error("focus stayed on the invisible log")
	}
	if *cancels != 1 {
		t.Errorf("the stream was torn down %d times, want exactly once", *cancels)
	}
	if m.journal != nil {
		t.Error("the hidden pane kept its stream pointer")
	}
	if len(m.logs) != 0 || m.logScroll != 0 || !m.logFollow {
		t.Errorf("the hidden pane kept its state: logs=%d scroll=%d follow=%v",
			len(m.logs), m.logScroll, m.logFollow)
	}
	// And the visible table is genuinely live again: its keys apply and
	// the cursor moves.
	if !m.keyApplies("s") {
		t.Error("table keys do not apply after the shrink")
	}
	was := m.cursor
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != was+1 {
		t.Errorf("Down did not move the table cursor: %d → %d", was, m.cursor)
	}

	// The unfocused variant: focus was already on the table and stays there.
	m2, cancels2 := resizeModel(t, 84)
	m2.focus = focusList
	resize(m2, 83, 30)
	if m2.focus != focusList || *cancels2 != 1 {
		t.Errorf("unfocused shrink: focus=%v cancels=%d", m2.focus, *cancels2)
	}
}

// Expanding across the split starts exactly one stream for the selected
// unit — immediately, even paused or fatal: the pane transition is
// deliberate and owes the dead-stream poll gate nothing.
func TestExpandAcrossSplitStartsTheStream(t *testing.T) {
	fakeFollowJournalctl(t)
	for _, state := range []string{"paused", "fatal"} {
		mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m := &mm
		m.width, m.height, m.ready, m.connected = 83, 30, true, true
		m.units = testUnits()
		m.rebuild()
		stopJournalOnCleanup(t, m)
		switch state {
		case "paused":
			m.paused = true
		case "fatal":
			m.fatal = true
			m.polling = false
		}

		cmd := resize(m, 84, 30) // the exact first visible column
		if m.journal == nil {
			t.Fatalf("%s: expansion started no stream", state)
		}
		gen := m.logGen
		if cmd == nil {
			t.Errorf("%s: expansion returned no command to run the stream", state)
		}
		// And only one: a second, width-only resize churns nothing.
		live := m.journal
		if extra := resize(m, 160, 30); extra != nil || m.journal != live || m.logGen != gen {
			t.Errorf("%s: a visible→visible resize churned the stream", state)
		}
		m.journal.stopAndWait()
	}
}

// Height-only and visible→visible changes keep the pointer and return no
// command; a full-view pane is visible at any width, so crossing 84 while
// in it is no transition; a user-hidden pane crosses nothing either.
func TestNoFalsePaneTransitions(t *testing.T) {
	m, cancels := resizeModel(t, 140)
	live := m.journal
	if cmd := resize(m, 140, 20); cmd != nil || m.journal != live {
		t.Error("a height-only resize churned the stream")
	}
	if cmd := resize(m, 200, 20); cmd != nil || m.journal != live {
		t.Error("a visible→visible width change churned the stream")
	}
	if *cancels != 0 {
		t.Errorf("cancellations without a transition: %d", *cancels)
	}

	// Full view below the split: still visible — and untouched: content,
	// generation, focus and scroll all survive, which is the very reason
	// syncJournal must not run on an arbitrary resize.
	m.fullView = true
	m.focus = focusLogs
	// Enough lines that a scroll of 2 stays legal after the rewrap, so the
	// assertion can demand EXACT equality rather than a clamp tolerance.
	m.logs = benchLogs(40)
	m.logScroll, m.logFollow = 2, false
	gen := m.logGen
	if cmd := resize(m, 83, 20); cmd != nil || m.journal != live {
		t.Error("full view crossing 84 churned the stream")
	}
	if m.logGen != gen || m.focus != focusLogs || len(m.logs) != 40 || m.logScroll != 2 || m.logFollow {
		t.Errorf("full-view crossing disturbed pane state: gen=%d focus=%v logs=%d scroll=%d follow=%v",
			m.logGen, m.focus, len(m.logs), m.logScroll, m.logFollow)
	}
	if *cancels != 0 {
		t.Errorf("full-view crossing cancelled %d times", *cancels)
	}

	// Hidden by the user: no crossing in either direction.
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	h := &mm
	h.width, h.height, h.ready, h.connected = 83, 30, true, true
	h.showLogs = false
	h.units = testUnits()
	h.rebuild()
	if cmd := resize(h, 140, 30); cmd != nil || h.journal != nil {
		t.Error("a user-hidden pane grew a stream on expansion")
	}
}

// Expansion with nothing to follow — no unit selected — starts nothing.
func TestExpandWithNoSelectionStartsNothing(t *testing.T) {
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 83, 30, true, true
	m.rebuild() // no units, no rows, no selection

	if cmd := resize(m, 140, 30); cmd != nil || m.journal != nil {
		t.Errorf("an empty selection grew a stream: cmd=%v journal=%v", cmd, m.journal)
	}
}

// The healing is unconditional: an already-invalid focus (log focus while
// the pane is hidden, however it got there) is repaired by ANY resize, not
// only a threshold crossing.
func TestAnyResizeHealsInvalidFocus(t *testing.T) {
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 83, 30, true, true
	m.units = testUnits()
	m.rebuild()
	m.focus = focusLogs // invalid: the pane is not visible at 83

	if resize(m, 82, 30); m.focus != focusList {
		t.Error("a hidden→hidden resize did not heal the invalid focus")
	}
}

// The pane transition is deliberate, so it beats the automatic retry gate:
// a stream of the SAME target that died a moment ago — which postPollSync
// would still be deferring — restarts the instant the pane becomes visible.
func TestExpansionBypassesTheRetryGate(t *testing.T) {
	fakeFollowJournalctl(t)
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 83, 30, true, true
	m.units = testUnits()
	m.rebuild()
	stopJournalOnCleanup(t, m)
	// A fresh same-target death: the gate is closed for automatic retries.
	m.journalDiedAt = time.Now()
	m.journalDiedUnit = "nginx.service"
	m.journalDiedFilter = logFilter{}

	if cmd := resize(m, 84, 30); cmd == nil || m.journal == nil {
		t.Fatal("the deliberate pane transition deferred to the retry gate")
	}
	defer m.journal.stopAndWait()
	if !m.journalDiedAt.IsZero() {
		t.Error("the deliberate sync did not settle the debt")
	}
}
