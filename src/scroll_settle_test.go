package main

import (
	"strings"
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
	stopJournalOnCleanup(t, &m)

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
	stopJournalOnCleanup(t, &m)

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
	stopJournalOnCleanup(t, &m)

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

// During the settle window the selection has moved but the log lines still
// belong to the streamed unit, so the log pane title must name the streamed
// unit — not the not-yet-loaded selection — or it labels one unit's logs with
// another's name.
func TestLogTitleNamesTheStreamedUnitDuringSettle(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	stopJournalOnCleanup(t, &m)

	m.cursor = firstUnitRow(t, &m)
	if cmd := m.afterCursorMove(); cmd == nil {
		t.Fatal("no stream started for the first selected unit")
	}
	streamed := m.journal
	if streamed == nil {
		t.Fatal("afterCursorMove did not open a journal")
	}

	// Seed an unmistakable line that belongs to the streamed unit, so the test
	// checks a real rendered line under the title, not the helper over an empty
	// buffer.
	const oldLine = "STREAMED-UNIT-LINE-42"
	m.logs = append(m.logs, logLine{ts: time.Now(), prio: 6, msg: oldLine})
	m.logFollow, m.logScroll = true, 0
	m.logEpoch++

	// Wheel to a different unit; the journal is deferred, so the lines on screen
	// still belong to the streamed unit.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, X: 0})
	if m.journal != streamed {
		t.Fatal("the wheel notch restarted the journal instead of deferring")
	}
	sel, ok := m.selectedUnit()
	if !ok || sel.Name == streamed.unit {
		t.Fatalf("the selection did not move to a different unit: %+v", sel)
	}

	// The title names the streamed unit, not the unloaded selection...
	title := m.logTitle(80)
	if !strings.Contains(title, shortUnit(streamed.unit)) {
		t.Errorf("log title should name the streamed unit %q, got %q", streamed.unit, title)
	}
	if strings.Contains(title, shortUnit(sel.Name)) {
		t.Errorf("log title named the unloaded selection %q; its logs are not on screen yet: %q", sel.Name, title)
	}
	// ...and the streamed unit's actual line is what is rendered beneath it.
	rendered := strings.Join(m.renderLogWindow(80, m.logHeight()), "\n")
	if !strings.Contains(rendered, oldLine) {
		t.Errorf("the streamed unit's line should be on screen under its own title, rendered:\n%s", rendered)
	}

	m.journal.stopAndWait()
}

// A stream that ends on its own retains its lines and records its identity in
// journalDiedUnit. If the wheel then moves the selection before recovery, the
// log title must still name the dead unit those retained lines came from — not
// the new selection.
func TestLogTitleNamesTheDeadUnitWhoseLinesRemain(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	stopJournalOnCleanup(t, &m)

	m.cursor = firstUnitRow(t, &m)
	if cmd := m.afterCursorMove(); cmd == nil {
		t.Fatal("no stream started for the first selected unit")
	}
	deadUnit := m.journal.unit

	// A distinctive line arrives, then the stream ends on its own.
	const deadLine = "DEAD-STREAM-LINE-7"
	m.logs = append(m.logs, logLine{ts: time.Now(), prio: 6, msg: deadLine})
	m.logFollow, m.logScroll = true, 0
	m.logEpoch++
	m.Update(journalBatch{gen: m.logGen, done: true})
	if m.journal != nil {
		t.Fatal("the done batch did not retire the stream")
	}
	if m.journalDiedUnit != deadUnit {
		t.Fatalf("the dead unit identity was not recorded: %q", m.journalDiedUnit)
	}
	if len(m.logs) == 0 {
		t.Fatal("the dead stream's lines should be retained")
	}

	// The wheel moves the selection while the dead lines are still on screen.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, X: 0})
	sel, _ := m.selectedUnit()
	if sel.Name == deadUnit {
		t.Fatal("the selection did not move off the dead unit")
	}

	if name, _ := m.logUnitName(); name != deadUnit {
		t.Errorf("the retained lines should be named by their dead unit %q, got %q", deadUnit, name)
	}
	title := m.logTitle(80)
	if !strings.Contains(title, shortUnit(deadUnit)) || strings.Contains(title, shortUnit(sel.Name)) {
		t.Errorf("title should name the dead unit %q, not the selection %q: %q", deadUnit, sel.Name, title)
	}
	rendered := strings.Join(m.renderLogWindow(80, m.logHeight()), "\n")
	if !strings.Contains(rendered, deadLine) {
		t.Errorf("the dead unit's line should still be on screen under its name, rendered:\n%s", rendered)
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
