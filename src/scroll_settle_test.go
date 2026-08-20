package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A quick mouse-wheel scroll through the unit list used to restart journalctl
// on every notch, for units the pointer only flew past. Now the wheel moves
// the selection at once but the journal follows only after the scroll settles.
func TestWheelScrollDefersTheJournalFetch(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	// Land on a unit and let its stream open, the deliberate way.
	m.cursor = firstUnitRow(t, &m)
	if cmd := m.afterCursorMove(); cmd == nil {
		t.Fatal("no stream started for the first selected unit")
	}
	opened := m.journal
	if opened == nil {
		t.Fatal("afterCursorMove did not open a journal")
	}
	restUnit := opened.unit

	// Scroll several notches. The selection must track the wheel, but the
	// journal must NOT be torn down and respawned on each notch.
	var lastCmd tea.Cmd
	for i := 0; i < 6; i++ {
		_, lastCmd = m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, X: 0})
		if m.journal != opened {
			t.Fatalf("notch %d restarted journalctl mid-scroll; the fetch should wait for the scroll to settle", i)
		}
	}
	if m.selected == restUnit {
		t.Fatal("the selection did not follow the wheel")
	}
	if lastCmd == nil {
		t.Fatal("a wheel notch should schedule a settle")
	}

	// A settle from an earlier notch is stale and must do nothing.
	if _, _ = m.Update(journalSettleMsg{gen: m.journalSettleGen - 1}); m.journal != opened {
		t.Fatal("a superseded settle restarted the journal")
	}

	// The latest notch's settle finally lets the journal follow the unit the
	// wheel came to rest on.
	_, cmd := m.Update(journalSettleMsg{gen: m.journalSettleGen})
	if cmd == nil {
		t.Fatal("the settling notch did not start the resting unit's stream")
	}
	if m.journal == nil || m.journal == opened {
		t.Fatal("the journal did not switch to the unit the scroll settled on")
	}
	if m.journal.unit != m.selected {
		t.Errorf("settled journal follows %q, selection is %q", m.journal.unit, m.selected)
	}

	m.journal.stopAndWait()
}

// A deliberate move (a click or keyboard navigation) during a pending wheel
// settle must switch the journal at once and leave the earlier settle inert —
// the settle must not later restart journalctl for the unit the wheel had been
// heading toward.
func TestDeliberateMoveCancelsAPendingWheelSettle(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	m.cursor = firstUnitRow(t, &m)
	if cmd := m.afterCursorMove(); cmd == nil {
		t.Fatal("no stream started for the first selected unit")
	}
	opened := m.journal
	if opened == nil {
		t.Fatal("afterCursorMove did not open a journal")
	}

	// A wheel notch schedules settle gen N; the journal has not moved yet.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, X: 0})
	genN := m.journalSettleGen
	if m.journal != opened {
		t.Fatal("the wheel notch restarted the journal instead of deferring")
	}

	// A deliberate keyboard move runs afterCursorMove: it switches the journal
	// immediately and bumps the generation, cancelling settle N.
	if cmd := m.listKey("up"); cmd == nil {
		t.Fatal("keyboard navigation did not restart the stream for its new unit")
	}
	if m.journal == opened {
		t.Fatal("the deliberate move did not switch the journal immediately")
	}
	switched := m.journal
	if m.journalSettleGen == genN {
		t.Fatal("the deliberate move did not supersede the pending wheel settle")
	}

	// The stale settle N is now inert.
	if _, _ = m.Update(journalSettleMsg{gen: genN}); m.journal != switched {
		t.Fatal("a superseded settle fired after a deliberate move switched the journal")
	}

	m.journal.stopAndWait()
}

// Wheeling over the log pane scrolls the log at once and is not part of the
// debounce: it must not schedule a settle or touch journalSettleGen.
func TestWheelOverLogDoesNotTouchTheSettle(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	m.cursor = firstUnitRow(t, &m)
	m.afterCursorMove()
	stream := m.journal
	gen := m.journalSettleGen

	// Seed more log lines than the pane is tall, so there is scrollback to move
	// into. WheelDown from the bottom clamps back to zero and would prove
	// nothing; WheelUp actually lifts the offset.
	for i := 0; i < 200; i++ {
		m.logs = append(m.logs, logLine{msg: "a log line"})
	}
	m.logFollow, m.logScroll = true, 0
	m.logEpoch++ // invalidate the height memo so the seeded lines count

	// Over the log pane the wheel scrolls the log directly.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp, X: m.tableWidth() + 4})
	if m.logScroll == 0 {
		t.Fatal("a wheel over the log did not scroll it")
	}
	if m.logFollow {
		t.Error("scrolling the log up should stop following")
	}
	if m.journalSettleGen != gen {
		t.Errorf("a wheel over the log changed journalSettleGen: %d → %d", gen, m.journalSettleGen)
	}
	if m.journal != stream {
		t.Error("a wheel over the log restarted the journal")
	}

	if stream != nil {
		stream.stopAndWait()
	}
}

// firstUnitRow returns the index of the first rowUnit in the table.
func firstUnitRow(t *testing.T, m *model) int {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == rowUnit {
			return i
		}
	}
	t.Fatal("no unit row in the table")
	return 0
}
