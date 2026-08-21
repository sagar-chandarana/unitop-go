package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func startupModel(t *testing.T) model {
	// The label is what main passes: the -H value itself. The screen renders
	// only the (sanitized) label; the raw host stays with the transport.
	m := newModel(testRunner(t, "root@server1"), "root@server1", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 100, 30, true
	return m
}

// Until the first poll lands there is no table worth drawing, and the reason
// must not be a single line at the bottom of an empty screen.
func TestStartupScreenReplacesTheUI(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	out := m.View()

	if !strings.Contains(out, "connecting to root@server1") {
		t.Errorf("no connecting message:\n%s", out)
	}
	if strings.Contains(out, "UNIT") || strings.Contains(out, "no matching units") {
		t.Errorf("the empty table was drawn before connecting:\n%s", out)
	}
	if strings.Contains(out, "0/0 units") {
		t.Errorf("host stats were drawn before any poll:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n != 29 {
		t.Errorf("startup screen should fill 30 lines, got %d", n+1)
	}
}

func TestStartupSpinnerAnimatesThenStops(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	first := m.View()
	if _, cmd := m.Update(spinnerTickMsg{}); cmd == nil {
		t.Error("spinner did not re-arm while connecting")
	}
	if m.View() == first {
		t.Error("spinner frame did not change")
	}

	// Once connected it must stop re-arming, or it burns a wakeup forever.
	m.connected = true
	if _, cmd := m.Update(spinnerTickMsg{}); cmd != nil {
		t.Error("spinner kept animating after connecting")
	}
}

// Init starts a poll asynchronously. It must mark that request in flight
// before the first refresh tick has a chance to launch another one.
func TestInitMarksInitialPollInFlight(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	if m.polling {
		t.Fatal("test setup unexpectedly has a poll in flight")
	}
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init did not schedule the first poll")
	}
	if !m.polling {
		t.Fatal("Init did not mark the initial poll in flight")
	}
}

func TestStartupFailureShowsReasonAndNextSteps(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	m.Update(unitsMsg{err: errors.New("remote poll: exit status 255: Permission denied (publickey)")})

	out := m.View()
	if !strings.Contains(out, "cannot reach root@server1") {
		t.Errorf("failure headline missing:\n%s", out)
	}
	if !strings.Contains(out, "Permission denied") {
		t.Errorf("the underlying error was not shown:\n%s", out)
	}
	if !strings.Contains(out, "try:") {
		t.Errorf("no next steps offered:\n%s", out)
	}
	if !strings.Contains(out, "ssh-copy-id") {
		t.Errorf("auth failure did not suggest installing a key:\n%s", out)
	}
	if !strings.Contains(out, "attempt 1") || !strings.Contains(out, "retry now") {
		t.Errorf("retry state not shown:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n != 29 {
		t.Errorf("failure screen should fill 30 lines, got %d", n+1)
	}

	// A second failure counts up rather than replacing the screen.
	m.Update(unitsMsg{err: errors.New("remote poll: exit status 255: Permission denied (publickey)")})
	if !strings.Contains(m.View(), "attempt 2") {
		t.Error("attempts are not counted")
	}
}

// A first poll that succeeds hands the screen over to the real UI.
func TestStartupClearsOnFirstSuccess(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	m.Update(unitsMsg{err: errors.New("boom")})
	m.Update(unitsMsg{units: testUnits(), host: HostStats{OK: true, NCPU: 8}})

	if !m.connected {
		t.Fatal("a successful poll did not mark the model connected")
	}
	out := m.View()
	if strings.Contains(out, "connecting to") || strings.Contains(out, "cannot reach") {
		t.Errorf("startup screen survived a successful poll:\n%s", out)
	}
	if !strings.Contains(out, "UNIT") {
		t.Errorf("the table did not appear:\n%s", out)
	}

	// A later failure is a footer message, not a takeover: there is data to show.
	m.Update(unitsMsg{err: errors.New("transient")})
	out = m.View()
	if strings.Contains(out, "cannot reach") {
		t.Error("a failure after connecting should not replace the whole UI")
	}
	if !strings.Contains(out, "transient") {
		t.Error("a failure after connecting should still be reported in the footer")
	}
}

// An empty host is a legitimate success, not a reason to sit on the spinner.
func TestStartupTreatsZeroUnitsAsConnected(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	m.Update(unitsMsg{units: nil, host: HostStats{OK: true}})
	if !m.connected {
		t.Error("a poll with no units should still count as connected")
	}
}

func TestStartupKeysAreLimited(t *testing.T) {
	m := startupModel(t)
	stopJournalOnCleanup(t, &m)
	m.Update(unitsMsg{err: errors.New("boom")})

	// Keys that act on the table must not fire while there is no table.
	for _, k := range []string{"t", "a", "s", "x", "l"} {
		before := m
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if m.tree != before.tree || m.showAll != before.showAll || m.sortBy != before.sortBy ||
			m.menu.open || m.showLogs != before.showLogs {
			t.Errorf("%q changed state while disconnected", k)
		}
	}

	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}); cmd == nil {
		t.Error("R should retry while disconnected")
	}
	m.polling = false
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Error("q should quit while disconnected")
	}
}

func TestTroubleshootMatchesRealFailures(t *testing.T) {
	cases := []struct {
		err, want string
	}{
		{"exit status 255: Permission denied (publickey).", "ssh-copy-id"},
		{"Host key verification failed.", "accept it"},
		{"ssh: Could not resolve hostname nope: Name or service not known", "does not resolve"},
		{"ssh: connect to host x port 22: Connection timed out", "not answering on port 22"},
		{"ssh: connect to host x port 22: Connection refused", "port 22 is closed"},
		{"kex_exchange_identification: Connection closed by remote host", "dropped during setup"},
		{"bash: line 1: systemctl: command not found", "is it actually a systemd host?"},
		{"something nobody has seen before", "reproduce it by hand"},
	}
	for _, c := range cases {
		got := strings.Join(troubleshoot(c.err, "root@server1"), " ")
		if !strings.Contains(got, c.want) {
			t.Errorf("troubleshoot(%q)\n  = %q\n  want it to mention %q", c.err, got, c.want)
		}
		if len(troubleshoot(c.err, "root@server1")) == 0 {
			t.Errorf("troubleshoot(%q) offered nothing", c.err)
		}
	}

	// The local case must not suggest ssh things.
	local := strings.Join(troubleshoot("something broke", ""), " ")
	if strings.Contains(local, "ssh") {
		t.Errorf("local failure suggested ssh: %q", local)
	}
}
