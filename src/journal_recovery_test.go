package main

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// deadStream retires a live-looking stream the way a real terminal batch
// does, and hands back the model.
func deadStream(t *testing.T) *model {
	t.Helper()
	m := pagingModel(3)
	_, cmd := m.Update(journalBatch{gen: m.logGen, done: true, backlogDone: true,
		lines: []logLine{{ts: time.Now(), prio: 4, msg: "journal stream ended", meta: true}}})
	if cmd != nil {
		t.Fatal("the terminal batch must not answer with a restart command")
	}
	if m.journal != nil {
		t.Fatal("the dead stream was not retired")
	}
	stopJournalOnCleanup(t, m)
	return m
}

// A terminal batch retires the stream — final lines kept, no restart command
// returned — instead of leaving a corpse syncJournal mistakes for live.
func TestDeadStreamRetiresWithoutRestart(t *testing.T) {
	m := deadStream(t)
	if m.journalDiedAt.IsZero() {
		t.Error("the death was not recorded; nothing will ever restart")
	}
	if m.loadingOlder {
		t.Error("the loading state outlived its stream")
	}
	if last := m.logs[len(m.logs)-1]; !last.meta || last.msg != "journal stream ended" {
		t.Errorf("the final lines were lost: %+v", last)
	}
}

// The next SUCCESSFUL poll starts exactly one replacement — and a second
// poll right after starts nothing, because the replacement is alive.
func TestDeadStreamRestartsOnNextSuccessfulPoll(t *testing.T) {
	fakeFollowJournalctl(t)
	m := deadStream(t)
	m.journalDiedAt = time.Now().Add(-2 * time.Second) // past the gate

	m.Update(unitsMsg{units: testUnits()})
	if m.journal == nil {
		t.Fatal("a successful poll past the gate did not restart the stream")
	}
	gen := m.logGen
	defer m.journal.stopAndWait()

	m.Update(unitsMsg{units: testUnits()})
	if m.logGen != gen {
		t.Error("a second poll churned the healthy replacement")
	}
}

// Two successful polls inside the gate window start nothing: a hot poll
// interval must not churn children.
func TestDeadStreamGateHoldsUnderFastPolls(t *testing.T) {
	fakeFollowJournalctl(t)
	m := deadStream(t)
	m.journalDiedAt = time.Now() // fresh death, gate closed

	m.Update(unitsMsg{units: testUnits()})
	m.Update(unitsMsg{units: testUnits()})
	if m.journal != nil {
		t.Fatal("the retry gate did not hold")
	}
}

// A failed poll never restarts the SAME dead target, however old the death
// — while a target that changed or vanished reconciles regardless, which
// TestRemovedUnitReconcilesImmediately proves separately.
func TestFailedPollDoesNotRestartDeadTarget(t *testing.T) {
	fakeFollowJournalctl(t)
	m := deadStream(t)
	m.journalDiedAt = time.Now().Add(-time.Minute)

	m.Update(unitsMsg{err: errors.New("ssh: connect timed out")})
	if m.journal != nil {
		t.Fatal("a failed poll restarted the stream")
	}
}

// Explicit R is the prompt way back: straight through syncJournal, past the
// automatic gate, however fresh the death.
func TestExplicitRetryBypassesTheGate(t *testing.T) {
	fakeFollowJournalctl(t)
	m := deadStream(t)
	m.journalDiedAt = time.Now() // gate would hold an automatic retry
	m.polling = false

	m.handleKey(keyOf("R"))
	if m.journal == nil {
		t.Fatal("R did not restart the dead stream")
	}
	m.journal.stopAndWait()
}

// A stale-generation terminal batch — a unit navigated away from — cannot
// retire the CURRENT stream.
func TestStaleDoneCannotKillTheLiveStream(t *testing.T) {
	m := pagingModel(3)
	live := m.journal
	m.Update(journalBatch{gen: m.logGen - 1, done: true})
	if m.journal != live {
		t.Fatal("a stale terminal batch disturbed the live stream")
	}
	if !m.journalDiedAt.IsZero() {
		t.Error("a stale terminal batch recorded a death")
	}
}

// With the pane hidden no journal is wanted: a successful poll past the gate
// starts nothing, and the next deliberate pane transition recovers instead.
func TestNoRestartWhilePaneHidden(t *testing.T) {
	fakeFollowJournalctl(t)
	m := deadStream(t)
	m.journalDiedAt = time.Now().Add(-2 * time.Second)
	m.showLogs = false

	m.Update(unitsMsg{units: testUnits()})
	if m.journal != nil {
		t.Fatal("a hidden pane grew a journal stream")
	}

	// The deliberate transition brings it back, gate or no gate.
	m.handleKey(keyOf("l"))
	if m.journal == nil {
		t.Fatal("reopening the pane did not restart the stream")
	}
	m.journal.stopAndWait()
}

// The retirement wait proves an owned page RETURNED, not that its message
// was consumed: Bubble Tea may already hold the olderBatch. The generation
// dies with the stream, so that queued result bounces instead of
// resurrecting errors, atEnd, or page output from a retired stream.
func TestQueuedPageResultBouncesOffRetirement(t *testing.T) {
	m := pagingModel(3)
	pageGen := m.logGen
	held := len(m.logs)

	m.Update(journalBatch{gen: pageGen, done: true, backlogDone: true})
	m.Update(olderBatch{gen: pageGen, err: "context canceled", atEnd: true,
		lines: []logLine{{ts: time.Now(), msg: "stale page line", cursor: "z"}}})

	if len(m.logs) != held {
		t.Errorf("a retired stream's page mutated the buffer: %d → %d lines", held, len(m.logs))
	}
	if m.logLoadErr != "" || m.logAtStart || m.loadingOlder {
		t.Errorf("a retired stream's page disturbed paging state: err=%q atStart=%v loading=%v",
			m.logLoadErr, m.logAtStart, m.loadingOlder)
	}
}

// Retirement is idempotent, and not by scheduling luck: a duplicate terminal
// batch carries the dead generation and bounces, leaving the death time and
// any newer replacement untouched.
func TestDuplicateTerminalBatchIsInert(t *testing.T) {
	fakeFollowJournalctl(t)
	m := pagingModel(3)
	stopJournalOnCleanup(t, m)
	deadGen := m.logGen

	m.Update(journalBatch{gen: deadGen, done: true, backlogDone: true})
	died := m.journalDiedAt
	if died.IsZero() {
		t.Fatal("no death recorded")
	}

	time.Sleep(5 * time.Millisecond)
	m.Update(journalBatch{gen: deadGen, done: true, backlogDone: true})
	if !m.journalDiedAt.Equal(died) {
		t.Error("a duplicate terminal batch moved the death timestamp")
	}

	// Nor can it touch the replacement that R brought back.
	m.polling = false
	m.handleKey(keyOf("R"))
	if m.journal == nil {
		t.Fatal("R did not restart")
	}
	live := m.journal
	defer live.stopAndWait()
	m.Update(journalBatch{gen: deadGen, done: true, backlogDone: true})
	if m.journal != live {
		t.Error("a duplicate terminal batch killed the replacement")
	}
}

// The gate defers only the automatic restart of the SAME dead target. A
// selection that moved on inside the gate window reconciles immediately:
// the new unit's stream starts, and the dead unit's final lines stop
// masquerading under the new selection.
func TestGateDoesNotDeferReconciliation(t *testing.T) {
	fakeFollowJournalctl(t)
	m := pagingModel(3)
	stopJournalOnCleanup(t, m)
	deadUnit := m.journal.unit
	m.Update(journalBatch{gen: m.logGen, done: true, backlogDone: true})
	if m.journalDiedAt.IsZero() {
		t.Fatal("no death recorded")
	}

	// Move the selection to a different unit — through the real key path,
	// so the selection-by-name memory rebuild() restores moves with it —
	// then poll INSIDE the gate.
	m.focus = focusList // pagingModel focuses the log; down must move the table
	for i := 0; i < len(m.rows) && m.journalTarget() == deadUnit; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.journalTarget() == deadUnit {
		t.Fatal("could not move the selection off the dead unit")
	}
	m.Update(unitsMsg{units: testUnits()})
	if m.journal == nil {
		t.Fatal("the changed selection was left unreconciled inside the gate")
	}
	defer m.journal.stopAndWait()
	if m.journal.unit == deadUnit {
		t.Fatalf("the gate restarted the dead target %q instead of the new selection", deadUnit)
	}
	if !m.journalDiedAt.IsZero() {
		t.Error("the deliberate reconciliation did not settle the debt")
	}
}

// The most adversarial shape: the poll REMOVES the dead unit. The selection
// memory dangles, rebuild reanchors to a surviving row, and that new target
// must reconcile immediately — the gate protects nothing that is gone.
func TestRemovedUnitReconcilesImmediately(t *testing.T) {
	fakeFollowJournalctl(t)
	m := pagingModel(3)
	stopJournalOnCleanup(t, m)
	dead := m.journal.unit
	m.Update(journalBatch{gen: m.logGen, done: true, backlogDone: true})

	var survivors []Unit
	for _, u := range testUnits() {
		if u.Name != dead {
			survivors = append(survivors, u)
		}
	}
	m.Update(unitsMsg{units: survivors}) // inside the fresh gate
	if m.journal == nil {
		t.Fatal("the removed unit's death still gated the new selection")
	}
	defer m.journal.stopAndWait()
	if m.journal.unit == dead {
		t.Fatalf("a stream started for the removed unit %q", dead)
	}
	if !m.journalDiedAt.IsZero() {
		t.Error("reconciliation did not settle the debt")
	}
}
