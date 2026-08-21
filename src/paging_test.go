package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	stopJournalOnCleanup(t, m)
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
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "unitop keeps the newest") {
		t.Errorf("the limit is not explained: %q", got)
	}

	// Live entries still arrive, and still trim the oldest to hold the line —
	// in blocks, so the buffer rides up to logTrimSlack over the cap between
	// trims rather than moving every retained entry on every arriving line.
	for i := 0; i < logTrimSlack*2; i++ {
		m.Update(journalBatch{gen: m.logGen, lines: []logLine{{msg: "new", cursor: "z"}}})
		if len(m.logs) > maxLogLines+logTrimSlack {
			t.Fatalf("live append blew the cap: %d lines", len(m.logs))
		}
		if len(m.logs) < maxLogLines {
			t.Fatalf("trimmed below the cap: %d lines", len(m.logs))
		}
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
	stopJournalOnCleanup(t, m)
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

// The memoised buffer height is grown incrementally as lines arrive, instead of
// being recomputed from scratch. It must agree with the full recount at every
// step: the pane's scroll arithmetic is built on it, so a drift here is a view
// that scrolls to the wrong place or thinks it has reached the top when it has
// not.
func TestIncrementalHeightAgreesWithRecount(t *testing.T) {
	m := pagingModel(0)
	m.focus = focusLogs

	check := func(what string) {
		t.Helper()
		got := m.logDisplayTotal()
		want := m.countDisplayLines(m.logs)
		if got != want {
			t.Fatalf("%s: memo says %d display lines, recount says %d", what, got, want)
		}
	}
	batch := func(n int, msg string) {
		lines := make([]logLine, 0, n)
		for i := 0; i < n; i++ {
			lines = append(lines, logLine{ts: time.Now(), prio: 6,
				msg: msg + " " + strconv.Itoa(i), cursor: "s=x;i=" + strconv.Itoa(i)})
		}
		m.Update(journalBatch{gen: m.logGen, lines: lines})
	}

	check("empty")
	batch(5, "short")
	check("after one batch")
	batch(3, strings.Repeat("a long line that will certainly wrap several times ", 4))
	check("after a batch of wrapping lines")
	for i := 0; i < 20; i++ {
		batch(7, "steady traffic")
	}
	check("after steady traffic")

	// A narrower pane rewraps everything; the memo must not be trusted across it.
	m.width = 90
	check("after a resize")
	batch(4, "more after the resize")
	check("after a batch at the new width")

	// Wrapping off and on again.
	m.logWrap = false
	check("with wrapping off")
	batch(2, strings.Repeat("wide ", 40))
	check("after a batch with wrapping off")
	m.logWrap = true
	check("with wrapping back on")

	// A page prepended at the top is not an append.
	older := []logLine{{ts: time.Now(), prio: 6, msg: "older", cursor: "s=x;i=old"}}
	m.Update(olderBatch{gen: m.logGen, lines: older})
	check("after paging backwards")
	batch(3, "after the prepend")
	check("after a batch following a prepend")

	// And the trim at the buffer cap drops lines whose heights were counted.
	for len(m.logs) < maxLogLines-10 {
		batch(500, "filling")
	}
	check("near the cap")
	batch(50, "over the cap")
	if len(m.logs) < maxLogLines || len(m.logs) > maxLogLines+logTrimSlack {
		t.Fatalf("buffer is %d, expected between %d and %d",
			len(m.logs), maxLogLines, maxLogLines+logTrimSlack)
	}
	check("after the trim")
}

// referenceLogWindow is the straightforward implementation renderLogWindow
// replaced: format every entry from the newest back, prepending, until there
// are enough lines, then slice out the window. It is quadratic and renders
// lines nobody sees, which is why it is only here — as the thing the fast one
// has to agree with, exactly.
func referenceLogWindow(m *model, width, height int) []string {
	need := height + m.logScroll
	var acc []string
	for i := len(m.logs) - 1; i >= 0 && len(acc) < need; i-- {
		acc = append(m.formatLog(m.logs[i], width), acc...)
	}
	if len(acc) == 0 {
		return nil
	}
	end := min(len(acc), len(acc)-m.logScroll)
	if end < 0 {
		end = 0
	}
	return append([]string(nil), acc[max(0, end-height):end]...)
}

func TestLogWindowMatchesTheReference(t *testing.T) {
	for _, wrap := range []bool{true, false} {
		for _, width := range []int{40, 60, 132} {
			m := pagingModel(0)
			m.logWrap = wrap
			// A mix of short lines, lines that wrap once, and lines that wrap
			// many times — the entry straddling the window's edge is where an
			// off-by-one hides.
			for i := 0; i < 400; i++ {
				msg := "short " + strconv.Itoa(i)
				switch i % 3 {
				case 1:
					msg = strings.Repeat("medium length line ", 4) + strconv.Itoa(i)
				case 2:
					msg = strings.Repeat("a much longer line that wraps repeatedly ", 6) + strconv.Itoa(i)
				}
				m.logs = append(m.logs, logLine{ts: time.Now(), prio: 6, msg: msg,
					cursor: "s=x;i=" + strconv.Itoa(i)})
			}
			m.logEpoch++

			for _, height := range []int{1, 5, 20} {
				// Measured at the width being rendered, not the model's own.
				total := 0
				for _, l := range m.logs {
					_, segs := m.logSegments(l, width)
					total += len(segs)
				}
				for _, scroll := range []int{0, 1, 2, 7, 50, 200, total - height} {
					// Clamp to this height, not the model's: a window scrolled
					// entirely past the buffer cannot happen in the app, and the
					// two implementations say different things about it.
					if scroll < 0 {
						continue
					}
					m.logScroll = min(scroll, max(0, total-height))

					got := m.renderLogWindow(width, height)
					want := referenceLogWindow(m, width, height)
					// The markers are painted on top by both paths equally;
					// compare what is underneath.
					if m.logScroll > 0 && len(want) > 0 {
						want[len(want)-1] = got[len(got)-1]
					}
					if m.atTopOfLog() && len(want) > 0 {
						want[0] = got[0]
					}
					if len(got) != len(want) {
						t.Fatalf("wrap=%v w=%d h=%d scroll=%d: %d lines, reference gives %d",
							wrap, width, height, m.logScroll, len(got), len(want))
					}
					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("wrap=%v w=%d h=%d scroll=%d line %d:\n got %q\nwant %q",
								wrap, width, height, m.logScroll, i, stripANSI(got[i]), stripANSI(want[i]))
						}
					}
				}
			}
		}
	}
}

// A scroll offset that outlives the geometry it was clamped against — the
// terminal widened, the pane went full-view — can point past the whole
// re-wrapped buffer. The window must land on the buffer's top, never on the
// empty-pane notice, which asserts the unit has written nothing while
// thousands of lines are held.
func TestOverScrolledWindowShowsTheBufferNotTheEmptyNotice(t *testing.T) {
	m := pagingModel(50)
	m.logScroll = 100000 // as left behind by a much narrower wrapping

	win := m.renderLogWindow(80, 5)
	if len(win) == 0 {
		t.Fatal("an over-scrolled window rendered nothing")
	}
	for _, line := range win {
		if strings.Contains(stripANSI(line), "written nothing") {
			t.Fatalf("over-scrolled window shows the empty-log notice: %q", stripANSI(line))
		}
	}
	// The paused marker reports the offset the window was actually drawn
	// at (50 lines, height 5, marker row: 46), not the stale 100000.
	if joined := stripANSI(strings.Join(win, "\n")); !strings.Contains(joined, "paused, 46 lines below") {
		t.Errorf("paused marker not corrected:\n%s", joined)
	}

	// It aims at the top: the same lines a properly clamped scroll shows.
	m.logScroll = 0
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		m.logKey("pgup")
	}
	m.loadingOlder = false // pgup asked for older entries; the marker differs
	top := m.renderLogWindow(80, 5)
	for i := 1; i < len(win)-1; i++ { // [0] and the last line carry markers
		if win[i] != top[i] {
			t.Errorf("line %d: over-scrolled %q, scrolled-to-top %q",
				i, stripANSI(win[i]), stripANSI(top[i]))
		}
	}
}

// Geometry changes re-wrap the buffer, so every one of them must re-clamp the
// scroll offset: the resize handler and both full-view transitions.
func TestGeometryChangesReclampTheLogScroll(t *testing.T) {
	limit := func(m *model) int {
		return max(0, m.logDisplayTotal()+1-m.logHeight()) // +1: the marker row
	}
	deepScroll := func(m *model) {
		m.logScroll = limit(m) + 100000
	}

	m := pagingModel(50)
	deepScroll(m)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	if got, want := m.logScroll, limit(m); got > want {
		t.Errorf("resize left logScroll=%d, limit is %d", got, want)
	}

	m = pagingModel(50)
	deepScroll(m)
	if m.activateRow(); !m.fullView {
		t.Fatal("enter did not open the full view")
	}
	if got, want := m.logScroll, limit(m); got > want {
		t.Errorf("entering full view left logScroll=%d, limit is %d", got, want)
	}

	deepScroll(m)
	m.escape()
	if m.fullView {
		t.Fatal("esc did not close the full view")
	}
	if got, want := m.logScroll, limit(m); got > want {
		t.Errorf("leaving full view left logScroll=%d, limit is %d", got, want)
	}
}

// Once trimming has discarded the oldest retained entries, the beginning of
// the journal — even if we once held it — is gone, and the top marker must
// say the buffer is full rather than claim the history is complete.
func TestTrimmingForgetsTheJournalBeginning(t *testing.T) {
	m := pagingModel(maxLogLines + logTrimSlack)
	m.logAtStart = true

	m.Update(journalBatch{gen: m.logGen, lines: []logLine{{msg: "one more", cursor: "z"}}})
	if len(m.logs) != maxLogLines {
		t.Fatalf("expected the batch to trim to the cap, buffer holds %d", len(m.logs))
	}
	if m.logAtStart {
		t.Error("the beginning fell off the front, but logAtStart still claims it")
	}
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "buffer full") {
		t.Errorf("top marker after trimming: %q", got)
	}
}

// The top marker states the retention policy. A live count rides above the
// cap between trims — deliberately — so any exact "N lines held" is false
// most of the time and jitters between frames besides.
func TestBufferFullMarkerIsStable(t *testing.T) {
	at := func(lines int) string {
		m := pagingModel(lines)
		return stripANSI(m.logTopMarker(120))
	}
	capped, riding := at(maxLogLines), at(maxLogLines+logTrimSlack)
	if capped != riding {
		t.Errorf("marker changed with the buffer's momentary length:\n at cap %q\n riding %q", capped, riding)
	}
	if want := strconv.Itoa(maxLogLines); !strings.Contains(capped, want) {
		t.Errorf("marker %q does not state the %s-line contract", capped, want)
	}
}

// The top marker is a row of its own. Painted over the oldest visible line it
// made that line unreachable — the clamp allows no step that would bring it
// back — and a one-entry journal showed only the marker.
func TestTopMarkerDoesNotEatData(t *testing.T) {
	// A one-entry journal shows the entry and the marker.
	m := pagingModel(1)
	m.logAtStart = true
	win := m.renderLogWindow(120, 5)
	joined := stripANSI(strings.Join(win, "\n"))
	if !strings.Contains(joined, "beginning of this unit's journal") {
		t.Errorf("no top marker over a complete one-entry journal:\n%s", joined)
	}
	if !strings.Contains(joined, "line") {
		t.Errorf("the only entry is not shown:\n%s", joined)
	}

	// At the true top of a buffer taller than the window, the very first
	// display line sits beneath the marker instead of underneath it.
	m = pagingModel(200)
	for i := 0; i < 500 && !m.atTopOfLog(); i++ {
		m.logKey("pgup")
	}
	m.loadingOlder = false
	win = m.renderLogWindow(120, m.logHeight())
	if len(win) != m.logHeight() {
		t.Fatalf("top window has %d rows, the pane has %d", len(win), m.logHeight())
	}
	first := m.formatLog(m.logs[0], 120)[0]
	if win[1] != first {
		t.Errorf("row under the marker is %q, the buffer starts with %q",
			stripANSI(win[1]), stripANSI(first))
	}
}

// A stale offset between the valid maximum and the buffer's total used to
// slip through the retry: the walk found some lines, declared victory, and
// left blank rows while entries sat below the window — and the paused marker
// printed the stale offset rather than the one the window was drawn at.
func TestStaleScrollBetweenMaxAndTotalFillsTheWindow(t *testing.T) {
	m := pagingModel(100)
	// 100 one-line entries at height 20: the marker-aware maximum is 81. An
	// offset of 85 leaves only 15 lines above it.
	m.logScroll = 85
	win := m.renderLogWindow(120, 20)
	if len(win) != 20 {
		t.Fatalf("window has %d rows of 20", len(win))
	}
	joined := stripANSI(strings.Join(win, "\n"))
	if !strings.Contains(joined, "paused, 81 lines below") {
		t.Errorf("paused marker does not show the effective offset:\n%s", joined)
	}
}

// logHeight bottoms out at one row. A one-row window at the true top holds
// nothing but the top marker's slack, and painting the paused marker there
// indexed win[-1]; on two rows the two markers would consume both. The paused
// marker now needs more than one data line to paint over.
func TestOneAndTwoRowWindows(t *testing.T) {
	m := pagingModel(30)
	total := m.logDisplayTotal()

	// Every offset at height 1 draws exactly one row, including offsets past
	// the maximum, which the retry corrects rather than panics on.
	for skip := 0; skip <= total+5; skip++ {
		m.logScroll = skip
		if win := m.renderLogWindow(120, 1); len(win) != 1 {
			t.Fatalf("height 1, scroll %d: %d rows", skip, len(win))
		}
	}

	// The top step is the marker; one step below it is the oldest data line.
	first := stripANSI(m.formatLog(m.logs[0], 120)[0])
	m.logScroll = total
	if got := stripANSI(m.renderLogWindow(120, 1)[0]); !strings.Contains(got, "keep scrolling") {
		t.Errorf("top step at height 1 is not the top marker: %q", got)
	}
	m.logScroll = total - 1
	if got := stripANSI(m.renderLogWindow(120, 1)[0]); got != first {
		t.Errorf("one step below the top shows %q, not the oldest line %q", got, first)
	}

	// Height 2 at the true top: the marker and the oldest line, whatever the
	// stale offset was.
	m.logScroll = 1 << 20
	win := m.renderLogWindow(120, 2)
	if len(win) != 2 {
		t.Fatalf("height 2 at the top: %d rows", len(win))
	}
	if got := stripANSI(win[1]); got != first {
		t.Errorf("row under the marker is %q, not the oldest line %q", got, first)
	}
}
