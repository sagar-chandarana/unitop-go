package main

import (
	"errors"
	"time"

	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseSystemdVersion(t *testing.T) {
	cases := map[string]int{
		"systemd 229":                 229, // Ubuntu 16.04
		"systemd 247 (247.3-7+deb11)": 247,
		"systemd 257 (257.7)":         257,
		"systemd 258 (258.3)":         258,
		"systemd 252.4-1":             252,
		"systemd 250~rc1":             250,
		"":                            0,
		"garbage":                     0,
		"notsystemd 300":              0,
		"systemd vNext":               0,
	}
	for in, want := range cases {
		if got := parseSystemdVersion(in); got != want {
			t.Errorf("parseSystemdVersion(%q) = %d, want %d", in, got, want)
		}
	}
}

// The point of the check is that an unusable systemd is named as such, rather
// than surfacing later as `unrecognized option '--timestamp=unix'`.
func TestCheckVersion(t *testing.T) {
	if err := checkVersion(229, "root@old"); err == nil {
		t.Fatal("systemd 229 should be rejected")
	} else {
		msg := err.Error()
		for _, want := range []string{"229", "too old", "247", "root@old"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message should mention %q: %s", want, msg)
			}
		}
	}

	if err := checkVersion(0, "this host"); err == nil {
		t.Error("a missing systemd should be rejected")
	} else if !strings.Contains(err.Error(), "no systemd") {
		t.Errorf("unclear message for a missing systemd: %s", err)
	}

	// The floor itself and anything above it are fine.
	for _, v := range []int{minSystemd, 250, 257, 258, 300} {
		if err := checkVersion(v, "h"); err != nil {
			t.Errorf("systemd %d should be accepted: %v", v, err)
		}
	}
}

func TestTroubleshootExplainsOldSystemd(t *testing.T) {
	got := strings.Join(troubleshoot(
		"remote poll: systemd 229 on root@server1 is too old — unitop needs systemd 247 or newer",
		"root@server1"), " ")
	if !strings.Contains(got, "247") {
		t.Errorf("advice should name the required version: %q", got)
	}
	if !strings.Contains(got, "ssh root@server1 systemctl --version") {
		t.Errorf("advice should give the command to check: %q", got)
	}

	// Locally the same advice must not tell you to ssh anywhere.
	local := strings.Join(troubleshoot("systemd 229 on this host is too old", ""), " ")
	if strings.Contains(local, "ssh ") {
		t.Errorf("local advice should not mention ssh: %q", local)
	}
	if !strings.Contains(local, "systemctl --version") {
		t.Errorf("local advice should still give the check command: %q", local)
	}

	missing := strings.Join(troubleshoot("no systemd on root@x: `systemctl --version` did not report one", "root@x"), " ")
	if !strings.Contains(missing, "systemd running") {
		t.Errorf("a missing systemd should be explained: %q", missing)
	}
}

func TestFirstLineOf(t *testing.T) {
	if got := firstLineOf("systemd 257 (257.7)\n+PAM +AUDIT\n"); got != "systemd 257 (257.7)" {
		t.Errorf("firstLineOf = %q", got)
	}
	if got := firstLineOf("only one line"); got != "only one line" {
		t.Errorf("firstLineOf = %q", got)
	}
	if got := firstLineOf(""); got != "" {
		t.Errorf("firstLineOf = %q", got)
	}
}

// A too-old systemd is a verdict, not a transient failure: polling must stop,
// and the screen must not claim the host was unreachable.
func TestUnsupportedIsFatalAndStopsPolling(t *testing.T) {
	m := newModel(newRunner("root@old"), "old", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 100, 24, true

	m.Update(unitsMsg{err: checkVersion(229, "root@old")})
	if !m.fatal {
		t.Fatal("a version failure should be fatal")
	}

	// A tick must not start another poll.
	if _, cmd := m.Update(tickMsg(time.Now())); cmd == nil {
		t.Fatal("the ticker should keep running")
	}
	if m.polling {
		t.Error("polling continued after a fatal error")
	}

	out := m.View()
	if strings.Contains(out, "cannot reach") {
		t.Errorf("we did reach it; the headline should not say otherwise:\n%s", out)
	}
	if !strings.Contains(out, "cannot be watched") {
		t.Errorf("headline missing:\n%s", out)
	}
	if !strings.Contains(out, "not retrying") {
		t.Errorf("should say it has stopped retrying:\n%s", out)
	}
	if !strings.Contains(out, "systemd 229") || !strings.Contains(out, "247") {
		t.Errorf("both versions should be named:\n%s", out)
	}

	// An explicit R clears the verdict — systemd may have just been upgraded.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if m.fatal {
		t.Error("R should clear the fatal verdict and retry")
	}
}

// An ordinary connection failure must stay retryable.
func TestOrdinaryFailureKeepsRetrying(t *testing.T) {
	m := newModel(newRunner("root@x"), "x", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 100, 24, true
	m.Update(unitsMsg{err: errors.New("ssh: connect to host x port 22: Connection timed out")})
	if m.fatal {
		t.Error("a timeout is not fatal")
	}
	if !strings.Contains(m.View(), "retrying every") {
		t.Error("a retryable failure should still say it is retrying")
	}
}
