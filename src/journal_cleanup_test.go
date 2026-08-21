package main

import (
	"testing"
	"time"
)

// stopJournalOnCleanup reaps whatever follow stream the model holds when the
// test ends. syncJournal starts follows on context.Background(), so without this
// a journalctl child outlives the test wherever the binary exists (CI, and any
// dev host) and can contaminate a later test that shadows journalctl.
//
// Register it immediately after the model is set up: the closure reads
// m.journal at cleanup time, so a stream the test later replaces is still
// reaped, and it protects assertion-failure (Fatal) returns as well as normal
// ones. stopAndWait is nil-safe and idempotent, and returns promptly for the
// inert synthetic streams these tests hold (nil cancel/done, no in-flight
// pages), so this is safe even for tests that already stop their own stream.
func stopJournalOnCleanup(t *testing.T, m *model) {
	t.Cleanup(func() { m.journal.stopAndWait() })
}

// The helper must actually reap the follow child a test owns, even though the
// fake journalctl execs `sleep 300` (so the child's name is no longer
// journalctl — a name-based scan would miss it). The owner subtest registers
// the cleanup while m.journal is still nil, starts a real follow, records its
// pid, and returns WITHOUT stopping it; once t.Run returns its cleanup has run,
// so the pid must be gone. This locks both the late-bound final-pointer
// ownership and synchronous reaping by pid (kill(0) → ESRCH), independent of
// process name.
func TestStopJournalOnCleanupReapsTheFollowChild(t *testing.T) {
	followPid, _ := fakeFollowJournalctl(t)

	var pid int
	t.Run("owner", func(t *testing.T) {
		m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m.width, m.height, m.ready, m.connected = 140, 30, true, true
		m.units = testUnits()
		m.rebuild()
		stopJournalOnCleanup(t, &m) // registered while m.journal is still nil

		m.cursor = firstUnitRow(t, &m)
		if cmd := m.afterCursorMove(); cmd == nil {
			t.Fatal("no follow stream started")
		}
		pid = waitPid(t, followPid, "follow")
		// Return normally, with no explicit stop — the cleanup must reap it.
	})

	assertReaped(t, pid, "the follow child the cleanup owned")
}
