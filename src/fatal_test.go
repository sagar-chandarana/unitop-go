package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func fatalModel(t *testing.T) *model {
	t.Helper()
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	return &m
}

// polled reports whether a tick actually launched a poll. The tick always
// returns a Batch — it re-arms itself either way — so the flag is the signal.
func polled(m *model) bool {
	m.polling = false
	m.Update(tickMsg(time.Now()))
	return m.polling
}

// A fatal verdict arriving after we are already connected used to latch on
// forever: the tick suppresses itself while fatal, and nothing ever cleared it,
// so the display froze and only manual refreshes moved it.
func TestFatalAfterConnectingDoesNotLatch(t *testing.T) {
	m := fatalModel(t)
	if !polled(m) {
		t.Fatal("a healthy model should poll on a tick")
	}

	m.Update(unitsMsg{err: &UnsupportedError{msg: "systemd 229 on h is older than 247"}})
	if !m.fatal {
		t.Fatal("an unsupported host should stop the polling")
	}
	if polled(m) {
		t.Error("the tick should not poll a host we have judged unusable")
	}

	// A poll that works is the evidence that it is usable after all.
	m.polling = false
	m.Update(unitsMsg{units: testUnits()})
	if m.fatal {
		t.Error("a successful poll did not clear the fatal verdict")
	}
	if !polled(m) {
		t.Error("polling did not resume after a successful poll")
	}
}

// Asking explicitly clears it too, on both screens — the host may have been
// upgraded since.
func TestExplicitRefreshClearsFatal(t *testing.T) {
	for _, connected := range []bool{true, false} {
		m := fatalModel(t)
		m.connected = connected
		m.Update(unitsMsg{err: &UnsupportedError{msg: "systemd 229 on h is older than 247"}})
		if !m.fatal {
			t.Fatalf("connected=%v: not fatal to begin with", connected)
		}
		m.polling = false
		_, cmd := m.handleKey(keyOf("R"))
		if cmd == nil {
			t.Errorf("connected=%v: R did not poll", connected)
		}
		if m.fatal {
			t.Errorf("connected=%v: R left the fatal verdict set", connected)
		}
	}
}

// A frozen screen has to say it is frozen. The footer shows the error instead
// of the key hints while one is set, so the host bar carries it.
func TestFrozenPollingIsVisible(t *testing.T) {
	m := fatalModel(t)
	m.Update(unitsMsg{err: &UnsupportedError{msg: "systemd 229 on h is older than 247"}})

	screen := stripANSI(m.View())
	if !strings.Contains(screen, "NOT POLLING") {
		t.Errorf("the screen does not say polling has stopped:\n%s", screen)
	}
	if !strings.Contains(screen, "R to retry") {
		t.Error("the screen does not name the key that restarts it")
	}

	// And it goes away once polling resumes.
	m.Update(unitsMsg{units: testUnits()})
	if strings.Contains(stripANSI(m.View()), "NOT POLLING") {
		t.Error("the notice outlived the condition")
	}
}

// Only an unsupported host is fatal. An ordinary failure — the host is down,
// ssh refused — must keep retrying, or a reboot would need a restart.
func TestOrdinaryFailuresKeepRetrying(t *testing.T) {
	m := fatalModel(t)
	m.Update(unitsMsg{err: errors.New("ssh: connect to host h port 22: Connection refused")})
	if m.fatal {
		t.Fatal("a connection failure is not fatal")
	}
	if !polled(m) {
		t.Error("polling stopped after an ordinary failure")
	}
}
