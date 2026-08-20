package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		for _, want := range []string{"229", "too old", "251", "root@old"} {
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

	// 247–250 look plausible — their systemctl has --timestamp — but the
	// unix choice arrives in 251, and every detailed poll depends on it.
	for _, v := range []int{229, 247, 250} {
		if err := checkVersion(v, "h"); err == nil {
			t.Errorf("systemd %d should be rejected", v)
		}
	}
	// The floor itself and anything above it are fine.
	for _, v := range []int{minSystemd, 257, 258, 300} {
		if err := checkVersion(v, "h"); err != nil {
			t.Errorf("systemd %d should be accepted: %v", v, err)
		}
	}
}

func TestTroubleshootExplainsOldSystemd(t *testing.T) {
	got := strings.Join(troubleshoot(
		"remote poll: systemd 229 on root@server1 is too old — unitop needs systemd 251 or newer",
		"root@server1"), " ")
	if !strings.Contains(got, "251") {
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
	if !strings.Contains(out, "systemd 229") || !strings.Contains(out, "251") {
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

// fakeSystemctl puts a scripted systemctl first on PATH. setState chooses its
// behaviour ("fail" exits 127 with stderr, "old" reports 250, anything else
// 251), probes counts how many times --version has actually run, and
// showArgs returns the argv the last `show` invocation received.
func fakeSystemctl(t *testing.T) (setState func(string), probes func() int, showArgs func() []string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	script := `#!/bin/sh
case "$1" in
--version)
	printf 'probe\n' >> "$UNITOP_TEST_STATE.calls"
	case "$(cat "$UNITOP_TEST_STATE" 2>/dev/null)" in
	fail) echo "cannot open shared object file" >&2; exit 127 ;;
	old) echo "systemd 250 (250.5)" ;;
	*) echo "systemd 251 (251.4)" ;;
	esac ;;
list-units)
	printf 'dummy.service loaded active running A dummy\n' ;;
show)
	printf '%s\n' "$@" > "$UNITOP_TEST_STATE.show"
	printf 'Id=dummy.service\nActiveState=active\nSubState=running\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNITOP_TEST_STATE", state)
	// Prepend, not replace: the script itself needs cat from the real PATH,
	// and the fake shadows any real systemctl by coming first.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(mode string) {
			if err := os.WriteFile(state, []byte(mode), 0o644); err != nil {
				t.Fatal(err)
			}
		}, func() int {
			b, _ := os.ReadFile(state + ".calls")
			return strings.Count(string(b), "probe")
		}, func() []string {
			b, err := os.ReadFile(state + ".show")
			if err != nil {
				t.Fatalf("systemctl show was never invoked: %v", err)
			}
			return strings.Split(strings.TrimSpace(string(b)), "\n")
		}
}

// The local probe, executed for real against a scripted systemctl on PATH.
// Four claims, in sequence, against ONE collector — no restart anywhere:
// a probe that cannot run is an ordinary retryable error carrying its stderr;
// a parsed too-old version is an UnsupportedError and is NOT cached, so the
// next explicit poll re-probes and finds the upgrade; an accepted version IS
// cached, so ordinary timer polls stop paying for the probe — proven with the
// stub set back to failing, where an accidental re-probe would be loud; and
// the detailed poll still asks for exactly --timestamp=unix.
func TestLocalProbeRetriesAndCachesThroughUpgrades(t *testing.T) {
	setState, probes, showArgs := fakeSystemctl(t)

	ctx := context.Background()
	c := NewCollector(runner{})
	var unsup *UnsupportedError

	setState("fail")
	_, _, err := c.pollBase(ctx)
	if err == nil || errors.As(err, &unsup) {
		t.Fatalf("a probe that could not run must be an ordinary error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot open shared object") {
		t.Errorf("the probe's stderr was lost: %v", err)
	}

	setState("old")
	if _, _, err = c.pollBase(ctx); err == nil || !errors.As(err, &unsup) {
		t.Fatalf("systemd 250 must be an UnsupportedError, got %v", err)
	}

	setState("new")
	if _, units, err := c.pollBase(ctx); err != nil || len(units) != 1 {
		t.Fatalf("the upgraded host was still refused: units=%v err=%v", units, err)
	}

	setState("fail") // a re-probe would now fail loudly; the cache must carry it
	before := probes()
	for i := 0; i < 2; i++ {
		if _, _, err := c.pollBase(ctx); err != nil {
			t.Fatalf("poll %d after acceptance: %v", i, err)
		}
	}
	if got := probes(); got != before {
		t.Errorf("an accepted version was re-probed on ordinary polls: %d → %d", before, got)
	}

	// The whole point of the floor: the detailed poll's argv.
	if _, _, err := c.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	argv := showArgs()
	if len(argv) < 2 || argv[0] != "show" || argv[1] != "--timestamp=unix" {
		t.Errorf("show argv starts %v, want [show --timestamp=unix …]", argv)
	}
}

// The advice for a missing executable names the binary that failed: a local
// systemctl gone missing used to be blamed on the ssh client, which is not
// even involved.
func TestTroubleshootNamesTheMissingBinary(t *testing.T) {
	localCtl := strings.Join(troubleshoot(
		`systemctl --version: exec: "systemctl": executable file not found in $PATH`, ""), " ")
	if !strings.Contains(localCtl, "systemctl is missing") || strings.Contains(localCtl, "ssh client") {
		t.Errorf("local systemctl advice: %q", localCtl)
	}

	remoteCtl := strings.Join(troubleshoot(
		"remote poll: exit status 127: sh: line 1: systemctl: command not found", "root@h"), " ")
	if !strings.Contains(remoteCtl, "systemctl is missing on root@h") {
		t.Errorf("remote systemctl advice: %q", remoteCtl)
	}

	noSSH := strings.Join(troubleshoot(
		`exec: "ssh": executable file not found in $PATH`, "root@h"), " ")
	if !strings.Contains(noSSH, "ssh client") {
		t.Errorf("missing ssh client advice: %q", noSSH)
	}
}

// The user-visible half of the retry story. An UnsupportedError stops the
// timer, so nothing recovers "by itself": the recovery gesture is R, and it
// must work on the SAME model — receive the verdict, upgrade the host, press
// R, run the command it returns, and watch the model connect.
func TestExplicitRetryRecoversAfterUpgrade(t *testing.T) {
	setState, _, _ := fakeSystemctl(t)

	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 100, 24, true

	setState("old")
	cmd := m.pollCmd()
	m.Update(cmd())
	if !m.fatal {
		t.Fatal("systemd 250 did not produce the fatal verdict")
	}

	setState("new") // the host is upgraded under the running unitop
	_, retry := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if m.fatal {
		t.Fatal("R did not clear the verdict")
	}
	if retry == nil {
		t.Fatal("R returned no poll command")
	}
	m.Update(retry())
	if m.fatal || !m.connected || m.err != "" {
		t.Errorf("after R on the upgraded host: fatal=%v connected=%v err=%q",
			m.fatal, m.connected, m.err)
	}
	if len(m.units) != 1 {
		t.Errorf("the retry poll delivered %d units, want 1", len(m.units))
	}
}
