package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func pagingModel(lines int) *model {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 120, 24, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	m.focus = focusLogs
	m.journal = &journalStream{unit: "nginx.service", gen: m.logGen}
	for i := 0; i < lines; i++ {
		m.logs = append(m.logs, logLine{ts: time.Now(), prio: 6,
			msg: "line", cursor: "s=x;i=" + string(rune('a'+i%26))})
	}
	return &m
}

// Scrolling to the top asks for the page before what we hold, once.
func TestScrollingToTopLoadsOlder(t *testing.T) {
	m := pagingModel(200)

	if cmd := m.logKey("down"); cmd != nil {
		t.Error("scrolling down should not fetch anything")
	}
	var cmd interface{}
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		cmd = m.logKey("pgup")
	}
	if !m.atTopOfLog() {
		t.Fatal("never reached the top")
	}
	if cmd == nil {
		t.Fatal("reaching the top did not ask for earlier entries")
	}
	if !m.loadingOlder {
		t.Error("the loading state was not set")
	}

	// A second scroll while one is in flight must not fetch again.
	if again := m.logKey("pgup"); again != nil {
		t.Error("a fetch was already in flight; it should not start another")
	}
}

// The prepended page must not move what the reader is looking at.
func TestOlderBatchPrependsWithoutMovingTheView(t *testing.T) {
	m := pagingModel(200)
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		m.logKey("pgup")
	}
	scroll, held := m.logScroll, len(m.logs)

	older := make([]logLine, 100)
	for i := range older {
		older[i] = logLine{ts: time.Now(), prio: 6, msg: "older", cursor: "old"}
	}
	m.Update(olderBatch{gen: m.logGen, lines: older})

	if m.loadingOlder {
		t.Error("still marked as loading after the batch arrived")
	}
	if len(m.logs) != held+100 {
		t.Errorf("expected %d lines, got %d", held+100, len(m.logs))
	}
	if m.logs[0].msg != "older" {
		t.Error("the page was not prepended")
	}
	if m.logScroll != scroll {
		t.Errorf("the view moved: scroll %d -> %d", scroll, m.logScroll)
	}
	// And there is now more to scroll into.
	if m.atTopOfLog() {
		t.Error("still at the top after 100 earlier lines were added")
	}
}

// A batch for a unit we have left must be dropped.
func TestOlderBatchFromStaleGenerationIsIgnored(t *testing.T) {
	m := pagingModel(50)
	m.loadingOlder = true
	m.Update(olderBatch{gen: m.logGen - 1, lines: []logLine{{msg: "stale"}}})
	if len(m.logs) != 50 {
		t.Error("a stale page was prepended")
	}
	if !m.loadingOlder {
		t.Error("a stale page cleared the loading state of the current one")
	}
}

// Each state has to be visible, or the top of the buffer looks like the start
// of the journal.
func TestTopMarkerReportsPagingState(t *testing.T) {
	m := pagingModel(200)
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		m.logKey("pgup")
	}

	m.loadingOlder = true
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "loading earlier entries") {
		t.Errorf("loading marker = %q", got)
	}
	if !strings.Contains(stripANSI(strings.Join(m.renderLogWindow(120, m.logHeight()), "\n")), "loading earlier") {
		t.Error("the loading marker is not drawn in the log window")
	}

	m.loadingOlder = false
	m.logAtStart = true
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "beginning of this unit's journal") {
		t.Errorf("end marker = %q", got)
	}

	m.logAtStart = false
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "keep scrolling") {
		t.Errorf("more-available marker = %q", got)
	}

	m.logLoadErr = "journalctl: exit status 1"
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "could not load") {
		t.Errorf("error marker = %q", got)
	}
}

// Switching units throws the buffer away, paging state included.
func TestSelectionChangeResetsPagingState(t *testing.T) {
	m := pagingModel(50)
	m.loadingOlder, m.logAtStart, m.logLoadErr = true, true, "boom"
	m.selected = ""
	m.cursor = 1
	m.afterCursorMove()
	if m.loadingOlder || m.logAtStart || m.logLoadErr != "" {
		t.Errorf("paging state survived a unit change: loading=%v atStart=%v err=%q",
			m.loadingOlder, m.logAtStart, m.logLoadErr)
	}
}

// Nothing to page from means nothing is attempted.
func TestLoadOlderNeedsACursor(t *testing.T) {
	m := pagingModel(0)
	if cmd := m.loadOlder(); cmd != nil {
		t.Error("an empty buffer should not fetch")
	}
	m.logs = []logLine{{msg: "unitop says something", meta: true}}
	if cmd := m.loadOlder(); cmd != nil {
		t.Error("meta lines carry no cursor; there is nothing to page from")
	}
	m.logAtStart = true
	m.logs = append(m.logs, logLine{msg: "real", cursor: "s=1"})
	if cmd := m.loadOlder(); cmd != nil {
		t.Error("at the start of the journal there is nothing more to fetch")
	}
}

// Paging backwards must not grow the buffer without bound: the live path trims
// the oldest, but prepending cannot trim without discarding what is being read,
// so it stops instead and says so.
func TestBufferIsBounded(t *testing.T) {
	m := pagingModel(maxLogLines)
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		m.logKey("pgup")
	}
	if !m.logBufferFull() {
		t.Fatalf("a %d-line buffer should count as full", len(m.logs))
	}
	if cmd := m.loadOlder(); cmd != nil {
		t.Error("a full buffer kept fetching more history")
	}
	if m.loadingOlder {
		t.Error("a refused fetch left the loading flag set")
	}
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "the most unitop keeps") {
		t.Errorf("the limit is not explained: %q", got)
	}

	// Live entries still arrive, and still trim the oldest to hold the line.
	m.Update(journalBatch{gen: m.logGen, lines: []logLine{{msg: "new", cursor: "z"}}})
	if len(m.logs) > maxLogLines {
		t.Errorf("live append blew the cap: %d lines", len(m.logs))
	}
	if m.logs[len(m.logs)-1].msg != "new" {
		t.Error("the newest line was not kept")
	}

	// Below the cap it pages as normal. The scroll itself is what fetches, so
	// check its command rather than calling loadOlder again — by then one is
	// already in flight and refusing is the correct answer.
	small := pagingModel(600)
	var fetched bool
	for i := 0; i < 500 && !small.atTopOfLog(); i++ {
		if small.logKey("pgup") != nil {
			fetched = true
		}
	}
	if small.logBufferFull() {
		t.Error("600 lines should not count as full")
	}
	if !fetched {
		t.Error("a buffer under the cap should still page")
	}
}

// Switching units opens the new log where a log opens: at the live end. The
// old unit's scroll position carried over, so the view sat above an empty
// buffer — and because follow was still off, every batch that arrived pushed it
// further up instead of filling it in.
func TestSwitchingUnitsReturnsToTheLiveEnd(t *testing.T) {
	m := pagingModel(200)
	m.focus = focusLogs
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		m.logKey("pgup")
	}
	if m.logFollow || m.logScroll == 0 {
		t.Fatalf("expected to be scrolled back and not following: %d", m.logScroll)
	}

	// Move to a different unit, which restarts the stream.
	m.cursor = 0
	for m.cursor < len(m.rows)-1 {
		if r := m.rows[m.cursor]; r.kind == rowUnit && r.unit.Name != m.journal.unit {
			break
		}
		m.cursor++
	}
	m.afterCursorMove()

	if m.logScroll != 0 || !m.logFollow {
		t.Errorf("the new unit's log did not open at the live end: scroll=%d follow=%v",
			m.logScroll, m.logFollow)
	}

	// And batches arriving on the fresh stream fill the pane rather than
	// scrolling away from it.
	lines := make([]logLine, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, logLine{ts: time.Now(), prio: 6,
			msg: "line " + strconv.Itoa(i) + " of the new unit"})
	}
	m.Update(journalBatch{gen: m.logGen, lines: lines})
	if m.logScroll != 0 {
		t.Errorf("an arriving batch moved the view: scroll=%d", m.logScroll)
	}
	win := strings.Join(m.renderLogWindow(m.logInnerWidth(), m.logHeight()), "\n")
	if !strings.Contains(win, "line 29 of the new unit") {
		t.Errorf("the new unit's newest line is not on screen:\n%s", win)
	}
}

// Even with follow deliberately off, the view never floats above the buffer.
func TestScrollPositionStaysInsideTheBuffer(t *testing.T) {
	m := pagingModel(3)
	m.focus = focusLogs
	m.logFollow, m.logScroll = false, 0
	for i := 0; i < 5; i++ {
		m.Update(journalBatch{gen: m.logGen, lines: []logLine{
			{ts: time.Now(), prio: 6, msg: "line"},
		}})
	}
	if maxScroll := max(0, m.logDisplayTotal()-m.logHeight()); m.logScroll > maxScroll {
		t.Errorf("logScroll = %d, past the end of a %d-line buffer", m.logScroll, maxScroll)
	}
}

// F is the top and G the bottom, in the log as in the table, and the keys that
// were only ever aliases no longer answer. Binding two keys to one motion is
// cheap to add and expensive to keep explaining, so this pins what is left.
func TestMotionKeysHaveOneMeaningEach(t *testing.T) {
	m := pagingModel(200)
	m.logKey("F")
	if !m.atTopOfLog() {
		t.Error("F did not go to the top of the log")
	}
	m.logKey("end")
	if m.logScroll != 0 {
		t.Errorf("end did not return to the live end: logScroll = %d", m.logScroll)
	}
	if !m.logFollow {
		t.Error("resting at the live end should follow")
	}

	m.focus = focusList
	m.cursor = 3
	m.listKey("home")
	if m.cursor != 0 {
		t.Errorf("home did not go to the top of the table: cursor = %d", m.cursor)
	}

	// F and f are the log's two ends, and belong to no other pane.
	m.cursor = 3
	for _, k := range []string{"F", "f", "j", "k", "h", "g", "G", "ctrl+b", "ctrl+f"} {
		m.listKey(k)
		if m.cursor != 3 {
			t.Errorf("%q still moves the cursor; it was removed", k)
			m.cursor = 3
		}
	}

	d := m.interval
	for _, k := range []string{"=", "_"} {
		m.handleKey(keyOf(k))
		if m.interval != d {
			t.Errorf("%q still changes the interval; it was removed", k)
			m.interval = d
		}
	}
}
