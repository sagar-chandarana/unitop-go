package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A settle firing while the log filter is being typed must not restart
// journalctl with the half-typed filter: the editor owns that sync and defers
// it to Enter/Esc.
func TestSettleDoesNotRestartJournalWhileEditingLogFilter(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	m.cursor = firstUnitRow(t, &m)
	if cmd := m.afterCursorMove(); cmd == nil {
		t.Fatal("no stream started for the first selected unit")
	}
	streamed := m.journal

	// Schedule a settle without moving off the streamed unit: at the top the
	// wheel clamps, so the selection stays put but a settle is still pending.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp, X: 0})
	if m.journal != streamed {
		t.Fatal("the wheel notch restarted the journal")
	}
	if sel, _ := m.selectedUnit(); sel.Name != streamed.unit {
		t.Fatal("the selection moved; this test isolates the filter seam, not the unit seam")
	}

	// Start typing into the log filter.
	m.focus = focusLogs
	m.handleKey(keyOf("/"))
	if !m.filterInput || !m.filterLogs {
		t.Fatal("the log filter editor did not open")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.logFilt.grep != "x" {
		t.Fatalf("the filter char was not captured: %q", m.logFilt.grep)
	}

	// The pending settle fires mid-edit. It must not restart the stream.
	m.Update(journalSettleMsg{gen: m.journalSettleGen})
	if m.journal != streamed {
		t.Fatal("the settle restarted journalctl with the half-typed log filter mid-edit")
	}

	// Closing the editor is what applies the filter.
	m.handleKey(keyOf("\r"))
	if m.filterInput {
		t.Fatal("Enter did not close the filter editor")
	}
	if m.journal == streamed {
		t.Error("closing the filter editor did not apply the filter")
	}
	if m.journal != nil {
		m.journal.stopAndWait()
	}
}

// Mouse events bypass handleKey's editor branch, so while EITHER filter is
// being typed the editor must own the mouse too: a table click must not reach
// afterCursorMove (restarting the journal, moving the selection), the list
// wheel must not move the selection or schedule a settle, and a right-click
// must not open the action menu over the editor. The one exception is the wheel
// over the log pane, which still scrolls the log being read.
func TestFilterEditorOwnsMouseInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		logMode bool
	}{
		{"table filter", false},
		{"log filter", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
			m.width, m.height, m.ready = 140, 30, true
			m.connected = true
			m.units = testUnits()
			m.rebuild()

			m.cursor = firstUnitRow(t, &m)
			if cmd := m.afterCursorMove(); cmd == nil {
				t.Fatal("no stream started for the first selected unit")
			}
			streamed := m.journal
			for i := 0; i < 200; i++ {
				m.logs = append(m.logs, logLine{ts: time.Now(), prio: 6, msg: "line"})
			}
			m.logFollow, m.logScroll = true, 0
			m.logEpoch++

			// Open the chosen filter with a char that keeps every row (".",
			// present in every ".service" name), then type it.
			if tc.logMode {
				m.focus = focusLogs
			} else {
				m.focus = focusList
			}
			m.handleKey(keyOf("/"))
			m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")})
			if !m.filterInput || m.filterLogs != tc.logMode {
				t.Fatalf("wrong editor state: input=%v logs=%v want logs=%v", m.filterInput, m.filterLogs, tc.logMode)
			}

			cursor0, gen0 := m.cursor, m.journalSettleGen
			rowY := m.headerLines() + 3 + 2 // a different row

			// Left-click: inert.
			m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 2, Y: rowY})
			if m.journal != streamed {
				t.Error("a table click restarted the journal mid-edit")
			}
			if m.cursor != cursor0 {
				t.Error("a table click moved the selection mid-edit")
			}
			if !m.filterInput {
				t.Error("a table click closed the editor")
			}

			// Right-click: no menu.
			m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonRight, Action: tea.MouseActionPress, X: 2, Y: rowY})
			if m.menu.open {
				t.Error("a right-click opened the action menu over the editor")
			}
			if m.journal != streamed {
				t.Error("a right-click restarted the journal mid-edit")
			}

			// List wheel: no selection move, no settle scheduled.
			m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, X: 0})
			if m.cursor != cursor0 {
				t.Error("the list wheel moved the selection mid-edit")
			}
			if m.journalSettleGen != gen0 {
				t.Error("the list wheel scheduled a settle mid-edit")
			}

			// Log wheel: still scrolls the log being read.
			before := m.logScroll
			m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp, X: m.tableWidth() + 4})
			if m.logScroll == before {
				t.Error("the log wheel did not scroll the log mid-edit")
			}

			if streamed != nil {
				streamed.stopAndWait()
			}
		})
	}
}
