package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// recordPageArgv shadows journalctl and records the argv of BACKWARDS-PAGE
// invocations only (those carrying --cursor). Follow streams leaked from other
// tests in the same `go test ./...` process also exec journalctl and would
// clobber a shared argv file (they carry -f, not --cursor); the guard keeps
// this recorder immune to that contamination.
func recordPageArgv(t *testing.T) func() string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--cursor\" ]; then\n" +
		"    printf '%s\\n' \"$@\" > \"$RECORD_DIR/argv\"\n" +
		"    break\n" +
		"  fi\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECORD_DIR", bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(filepath.Join(bin, "argv"))
		return string(b)
	}
}

// loadOlder must build its backwards page from the stream's applied filter, not
// the model's m.logFilt. Proven by the recorded journalctl argv (not by reading
// a field): the applied model filter is set to a DIFFERENT value, so a
// regression to it would show up in the argv.
func TestPagingUsesTheStreamFilterNotTheDraft(t *testing.T) {
	readArgv := recordPageArgv(t)

	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	const streamFilter, modelFilter = "boom", "crash"
	m.journal = &journalStream{
		ctx:    context.Background(),
		unit:   "nginx.service",
		filter: logFilter{grep: streamFilter},
	}
	m.logs = []logLine{{cursor: "c1", msg: "seed", ts: time.Now(), prio: 6}}
	m.logFilt = logFilter{grep: modelFilter} // deliberately != the stream's filter

	cmd := m.loadOlder()
	if cmd == nil {
		t.Fatal("loadOlder returned no command for a cursor-bearing buffer")
	}
	applyCmd(t, &m, cmd)

	argv := readArgv()
	if !strings.Contains(argv, "-g\n"+streamFilter) {
		t.Errorf("the page did not carry the stream's applied filter %q: argv=%q", streamFilter, argv)
	}
	if strings.Contains(argv, modelFilter) {
		t.Errorf("the page leaked the model filter %q into journalctl: argv=%q", modelFilter, argv)
	}
}

// The applied log filter (m.logFilt) is what every journal path reads. While
// the editor holds a draft, no path — poll (success OR failure), resize
// hide→show, paging, settle, title/empty-notice — may adopt it. This drives the
// full matrix with a NONEMPTY applied filter A and a distinct draft B, so every
// assertion distinguishes the two rather than "draft vs empty".
func TestLogFilterDraftNeverLeaksIntoJournalWork(t *testing.T) {
	const applied, draft = "boom", "crash"

	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	// Start a stream on applied filter A.
	m.focus = focusLogs
	m.logFilt = logFilter{grep: applied}
	m.cursor = firstUnitRow(t, &m)
	if cmd := m.afterCursorMove(); cmd == nil {
		t.Fatal("no stream started for the first selected unit")
	}
	streamed := m.journal
	if streamed == nil || streamed.filter.grep != applied {
		t.Fatalf("stream should carry applied filter %q, got %+v", applied, streamed)
	}

	// Open the editor and replace the draft with B.
	m.handleKey(keyOf("/"))
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear the seeded A
	for _, r := range draft {
		m.handleKey(keyOf(string(r)))
	}
	if m.logDraft != draft {
		t.Fatalf("draft not captured: %q", m.logDraft)
	}
	if m.logFilt.grep != applied || m.journal.filter.grep != applied {
		t.Fatalf("draft leaked into applied (%q) or stream (%q)", m.logFilt.grep, m.journal.filter.grep)
	}

	// Title and empty-notice describe A; the footer shows B.
	if title := m.logTitle(80); !strings.Contains(title, applied) || strings.Contains(title, draft) {
		t.Errorf("title should show applied %q not draft %q: %q", applied, draft, title)
	}
	m.logs = nil            // force the empty-log notice
	m.logBacklogDone = true // ...past the "reading the journal…" phase
	m.logEpoch++
	if notice := strings.Join(m.renderLogWindow(80, m.logHeight()), "\n"); !strings.Contains(notice, applied) || strings.Contains(notice, draft) {
		t.Errorf("empty notice should show applied %q not draft %q: %q", applied, draft, notice)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, draft) {
		t.Errorf("the footer should show the draft %q being typed", draft)
	}

	// A successful poll leaves the stream on A.
	m.Update(unitsMsg{units: testUnits()})
	if m.journal != streamed || m.journal.filter.grep != applied {
		t.Fatal("a successful poll restarted the stream with the draft")
	}
	// A failed poll too.
	m.Update(unitsMsg{err: errors.New("poll failed")})
	if m.journal != streamed || m.journal.filter.grep != applied {
		t.Fatal("a failed poll restarted the stream with the draft")
	}

	// (Paging provenance is proved by executed argv in
	// TestPagingUsesTheStreamFilterNotTheDraft below.)

	// Resize hide→show: hiding tears the stream down, showing restarts it — on
	// the applied filter A, never the draft B.
	m.Update(tea.WindowSizeMsg{Width: 83, Height: 30}) // < 84: log pane hidden
	if m.logPaneVisible() {
		t.Fatal("the log pane should be hidden at width 83")
	}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30}) // shown again
	if m.journal == nil {
		t.Fatal("showing the pane did not restart the stream")
	}
	if m.journal.filter.grep != applied {
		t.Fatalf("the restarted stream used %q, not the applied %q", m.journal.filter.grep, applied)
	}
	shownStream := m.journal

	// Esc discards the draft; the applied filter and the live stream are intact.
	m.handleKey(escKey())
	if m.logFilt.grep != applied {
		t.Errorf("esc changed the applied filter: %q", m.logFilt.grep)
	}
	if m.journal != shownStream {
		t.Error("esc restarted the stream")
	}

	// A fresh edit + Enter is the only thing that applies B. (The resize forced
	// focus back to the list when the pane hid; re-aim at the log.)
	m.focus = focusLogs
	m.handleKey(keyOf("/"))
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlU})
	for _, r := range draft {
		m.handleKey(keyOf(string(r)))
	}
	m.handleKey(keyOf("\r"))
	if m.logFilt.grep != draft {
		t.Fatalf("Enter did not apply the draft: %q", m.logFilt.grep)
	}
	if m.journal == shownStream {
		t.Error("committing the new filter did not restart the stream")
	}
	if m.journal != nil {
		m.journal.stopAndWait()
	}
}

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
	// The char lands in the draft; the applied filter is untouched until Enter.
	if m.logDraft != "x" {
		t.Fatalf("the filter char was not captured in the draft: %q", m.logDraft)
	}
	if m.logFilt.grep != "" {
		t.Fatalf("the draft leaked into the applied filter mid-edit: %q", m.logFilt.grep)
	}

	// The pending settle fires mid-edit. Because the applied filter is unchanged,
	// syncJournal short-circuits and the stream is not restarted — no special
	// case in the settle handler required.
	m.Update(journalSettleMsg{gen: m.journalSettleGen})
	if m.journal != streamed {
		t.Fatal("the settle restarted journalctl with the half-typed log filter mid-edit")
	}

	// Closing the editor with Enter is what applies the draft.
	m.handleKey(keyOf("\r"))
	if m.filterInput {
		t.Fatal("Enter did not close the filter editor")
	}
	if m.logFilt.grep != "x" {
		t.Fatalf("Enter did not apply the draft to the filter: %q", m.logFilt.grep)
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
