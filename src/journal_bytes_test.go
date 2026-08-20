package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// A per-entry display cap is applied at parse, so every downstream buffer —
// the follow channel, the batch, the model — inherits the bound. It cuts on
// a grapheme boundary and names what it dropped.
func TestCapMessageBoundary(t *testing.T) {
	defer func(o int) { maxLineBytes = o }(maxLineBytes)
	maxLineBytes = 64

	for _, n := range []int{maxLineBytes - 1, maxLineBytes, maxLineBytes + 1, 100000} {
		got := capMessage(strings.Repeat("a", n))
		if n <= maxLineBytes {
			if got != strings.Repeat("a", n) {
				t.Errorf("n=%d: capped a message within budget", n)
			}
			continue
		}
		if len(got) > maxLineBytes {
			t.Errorf("n=%d: capped result %d exceeds the hard cap %d", n, len(got), maxLineBytes)
		}
		body := strings.SplitN(got, " ⟨unitop", 2)[0]
		if body != strings.Repeat("a", maxLineBytes-elisionReserve) {
			t.Errorf("n=%d: body is %d a's, want %d (cap minus marker reserve)", n, len(body), maxLineBytes-elisionReserve)
		}
		if !strings.Contains(got, "bytes elided") {
			t.Errorf("n=%d: no elision marker", n)
		}
	}
	// A multibyte grapheme straddling the boundary is dropped whole, never
	// split into invalid UTF-8.
	wide := strings.Repeat("世", 100) // 3 bytes each, well over the 64 cap
	got := capMessage(wide)
	body := strings.SplitN(got, " ⟨unitop", 2)[0]
	if len(body)%3 != 0 || !strings.HasSuffix(body, "世") {
		t.Errorf("a wide cluster was split: kept %d bytes", len(body))
	}
	if len(got) > maxLineBytes {
		t.Errorf("wide-cluster cap exceeded the hard limit: %d", len(got))
	}

	// Parsed entries inherit the cap (proves the queue/batch bound).
	raw := `{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"` + strings.Repeat("x", 5000) + `"}`
	l, ok := parseJournalJSON([]byte(raw))
	if !ok || len(l.msg) > maxLineBytes+64 {
		t.Errorf("parseJournalJSON did not cap the message: len=%d", len(l.msg))
	}
}

// trimCut drops oldest until BOTH budgets are satisfied, and no more; it is
// idle below the slack high-water.
func TestTrimCutByteBudget(t *testing.T) {
	defer func(b, s int) { maxLogBytes, logByteSlack = b, s }(maxLogBytes, logByteSlack)
	maxLogBytes, logByteSlack = 1000, 0

	line := func(b int) logLine { return logLine{msg: strings.Repeat("a", b)} }
	// Uneven sizes: 200, 200, 700, 50, 50 = 1200 bytes, over the 1000 cap.
	logs := []logLine{line(200), line(200), line(700), line(50), line(50)}
	total := 0
	for _, l := range logs {
		total += lineBytes(l)
	}
	cut, dropped := trimCut(logs, total)
	if total-dropped > maxLogBytes {
		t.Errorf("remaining %d bytes exceeds cap %d", total-dropped, maxLogBytes)
	}
	// Dropping the two 200s (400) leaves 800 <= 1000; dropping only one 200
	// leaves 1000 which is exactly at cap and acceptable, so cut should be the
	// smallest prefix that fits: dropping index 0 (200) leaves 1000 == cap.
	if cut != 1 || dropped != 200 {
		t.Errorf("cut=%d dropped=%d, want the smallest prefix (1, 200)", cut, dropped)
	}
	// Under both budgets → no work.
	if c, _ := trimCut(logs[:2], 400); c != 0 {
		t.Errorf("trimCut trimmed under budget: %d", c)
	}
}

// logBytes stays exactly in sync with the buffer across append+trim, prepend,
// and clear; retention never exceeds the byte budget after a trim.
func TestModelByteAccountingAndTrim(t *testing.T) {
	defer func(b, s int) { maxLogBytes, logByteSlack = b, s }(maxLogBytes, logByteSlack)
	maxLogBytes, logByteSlack = 4000, 1000

	m := pagingModel(0)
	sum := func() int {
		n := 0
		for _, l := range m.logs {
			n += lineBytes(l)
		}
		return n
	}
	// Flood uneven sizes well past the budget.
	for i := 0; i < 200; i++ {
		size := 100 + (i%5)*80 // 100..420
		m.Update(journalBatch{gen: m.logGen, lines: []logLine{
			{ts: time.Now(), msg: strings.Repeat("a", size), cursor: "c" + strings.Repeat("z", i%3)},
		}})
		if m.logBytes != sum() {
			t.Fatalf("iter %d: logBytes=%d out of sync with buffer sum=%d", i, m.logBytes, sum())
		}
		if m.logBytes > maxLogBytes+logByteSlack {
			t.Fatalf("iter %d: logBytes=%d rode past the high-water %d", i, m.logBytes, maxLogBytes+logByteSlack)
		}
	}
	// After the flood the buffer sits under the byte cap (a trim brought it
	// down to <= maxLogBytes, then it rides the slack).
	if m.logBytes > maxLogBytes+logByteSlack {
		t.Errorf("final logBytes=%d exceeds cap+slack", m.logBytes)
	}
	// The newest entry is retained; the oldest were trimmed.
	if m.logs[len(m.logs)-1].msg == "" {
		t.Error("the newest entry was trimmed")
	}
	if m.logFollow && m.logScroll != 0 {
		t.Errorf("following, but logScroll=%d", m.logScroll)
	}

	// The clear path (a selection change) zeroes the accounting.
	mc := pagingModel(3)
	mc.logBytes = 999 // stale; the clear must zero it
	for i, r := range mc.rows {
		if r.kind == rowUnit && r.unit.Name != mc.journal.unit {
			mc.cursor = i
			break
		}
	}
	mc.syncJournal()
	if mc.logBytes != 0 || mc.logs != nil {
		t.Errorf("clear left logBytes=%d logs=%d", mc.logBytes, len(mc.logs))
	}
}

// A single maximum-size entry does not make a frame slow: with the entry cap
// it wraps to a bounded number of display lines, not the ~50k a 4 MiB entry
// produced. Safe fixture — one maxLineBytes entry, not gigabytes.
func BenchmarkViewLargestEntry(b *testing.B) {
	m := benchModel(132, 40, 120, 500, false)
	m.focus = focusLogs
	huge := logLine{ts: time.Now(), prio: 6, ident: "svc", pid: "1",
		msg: strings.Repeat("word ", maxLineBytes/5)}
	m.logs = append(m.logs, huge)
	m.logEpoch++
	_ = m.View() // prime the memo off the timer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
	// Sanity: the max entry wraps to a bounded height, not tens of thousands.
	if h := len(m.formatLog(huge, m.logInnerWidth())); h > ansi.StringWidth(huge.msg) {
		b.Fatalf("entry wrapped to %d lines — unbounded", h)
	}
}

// Every retained field is bounded at parse, not just msg: a hostile host
// sending a 4 MiB identifier, pid, or cursor cannot smuggle it into the
// buffer. An oversized cursor is dropped (the entry becomes cursorless), and
// capMessage's result never exceeds maxLineBytes even with its marker.
func TestParseBoundsEveryField(t *testing.T) {
	big := strings.Repeat("z", 100000)
	raw := `{"__CURSOR":"` + big + `","__REALTIME_TIMESTAMP":"1723000000000001",` +
		`"MESSAGE":"` + big + `","SYSLOG_IDENTIFIER":"` + big + `","_PID":"` + big + `"}`
	l, ok := parseJournalJSON([]byte(raw))
	if !ok {
		t.Fatal("parse failed")
	}
	if len(l.msg) > maxLineBytes {
		t.Errorf("msg %d bytes exceeds the hard cap %d (marker included)", len(l.msg), maxLineBytes)
	}
	if len(l.ident) > maxFieldBytes || len(l.pid) > maxFieldBytes {
		t.Errorf("ident/pid uncapped: %d/%d", len(l.ident), len(l.pid))
	}
	if l.cursor != "" {
		t.Errorf("an oversized cursor was retained (%d bytes) instead of dropped", len(l.cursor))
	}
	// The bounded per-entry cost the buffer accounts for.
	if lineBytes(l) > maxLineBytes+2*maxFieldBytes+maxCursorBytes {
		t.Errorf("lineBytes %d exceeds the per-entry ceiling", lineBytes(l))
	}

	// capMessage's marker-inclusive bound holds at a small cap too.
	defer func(o int) { maxLineBytes = o }(maxLineBytes)
	maxLineBytes = 80
	if got := capMessage(strings.Repeat("a", 100000)); len(got) > maxLineBytes {
		t.Errorf("capMessage returned %d bytes, over the %d cap", len(got), maxLineBytes)
	}
	// A cursor at exactly the limit is kept; one over is dropped.
	atLimit := `{"__CURSOR":"` + strings.Repeat("c", maxCursorBytes) + `","MESSAGE":"x"}`
	if l, _ := parseJournalJSON([]byte(atLimit)); l.cursor == "" {
		t.Error("a cursor at exactly the limit was dropped")
	}
	overLimit := `{"__CURSOR":"` + strings.Repeat("c", maxCursorBytes+1) + `","MESSAGE":"x"}`
	if l, _ := parseJournalJSON([]byte(overLimit)); l.cursor != "" {
		t.Errorf("a cursor one over the limit was retained (%d bytes)", len(l.cursor))
	}

	// A cap smaller than the marker still obeys the hard limit (no marker).
	func() {
		defer func(o int) { maxLineBytes = o }(maxLineBytes)
		maxLineBytes = 20 // < elisionReserve
		if got := capMessage(strings.Repeat("a", 1000)); len(got) > maxLineBytes {
			t.Errorf("tiny cap: capMessage returned %d bytes, over %d", len(got), maxLineBytes)
		}
	}()
}

// A backward page cannot bypass the aggregate byte budget: loadOlder refuses
// once the buffer is byte-full, so a prepend only runs with headroom, and the
// olderBatch accounting stays exact.
func TestBackwardPageRespectsTheByteBudget(t *testing.T) {
	defer func(b, s int) { maxLogBytes, logByteSlack = b, s }(maxLogBytes, logByteSlack)
	maxLogBytes, logByteSlack = 4000, 1000

	m := pagingModel(0)
	sum := func() int {
		n := 0
		for _, l := range m.logs {
			n += lineBytes(l)
		}
		return n
	}
	// Fill to byte-full.
	for i := 0; i < 60; i++ {
		m.Update(journalBatch{gen: m.logGen, lines: []logLine{
			{ts: time.Now(), msg: strings.Repeat("a", 300), cursor: "cur" + strings.Repeat("x", i%2)},
		}})
	}
	if !m.logBufferFull() {
		t.Fatalf("buffer not byte-full: logBytes=%d cap=%d", m.logBytes, maxLogBytes)
	}
	// loadOlder must refuse while byte-full.
	if cmd := m.loadOlder(); cmd != nil {
		t.Error("loadOlder fetched a page while byte-full — the budget can be bypassed")
	}

	// A prepend that does arrive keeps the accounting exact and overshoots by
	// at most the one page.
	before := m.logBytes
	page := make([]logLine, 20)
	for i := range page {
		page[i] = logLine{ts: time.Now(), msg: strings.Repeat("b", 200), cursor: "old"}
	}
	m.Update(olderBatch{gen: m.logGen, lines: page})
	if m.logBytes != sum() {
		t.Errorf("prepend desynced the accounting: logBytes=%d sum=%d", m.logBytes, sum())
	}
	// Absolute bound from the CAPS, not the page's actual sizes: the buffer
	// was under maxLogBytes (loadOlder refuses otherwise), and a page is at
	// most its entry count times the per-entry ceiling.
	perEntryCeiling := maxLineBytes + 2*maxFieldBytes + maxCursorBytes
	if m.logBytes > maxLogBytes+len(page)*perEntryCeiling {
		t.Errorf("prepend overshot the absolute one-page bound: %d > %d",
			m.logBytes, maxLogBytes+len(page)*perEntryCeiling)
	}
	_ = before
}
