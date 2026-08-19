package main

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Every case below is a real (ActiveState, SubState, Result, ExecMainCode,
// ExecMainStatus, ConditionResult) tuple observed on a live host. systemd
// renders the middle four of them identically as "dead".
func TestStateLabel(t *testing.T) {
	cases := []struct {
		name string
		u    Unit
		want string
	}{
		{"running service", Unit{
			Active: "active", Sub: "running", Result: "success",
			ExecCode: execNotRun, ExecStatus: 0, CondResult: "yes"}, "running"},

		{"oneshot with RemainAfterExit", Unit{
			Active: "active", Sub: "exited", Result: "success",
			ExecCode: execExited, ExecStatus: 0, CondResult: "yes"}, "exited"},

		// fetch-certs: ran to completion, then went away.
		{"finished oneshot", Unit{
			Active: "inactive", Sub: "dead", Result: "success",
			ExecCode: execExited, ExecStatus: 0, CondResult: "yes"}, "exited"},

		// systemd-boot-random-seed: never ran, condition not met.
		{"skipped by condition", Unit{
			Active: "inactive", Sub: "dead", Result: "success",
			ExecCode: execNotRun, ExecStatus: 0, CondResult: "no"}, "skipped"},

		// net-setup@...: ExecCondition returned non-zero.
		{"skipped by ExecCondition", Unit{
			Active: "inactive", Sub: "dead", Result: "exec-condition",
			ExecCode: execNotRun, ExecStatus: 0, CondResult: "no"}, "skipped"},

		// emergency.service: loaded, conditions fine, simply never started.
		{"never ran", Unit{
			Active: "inactive", Sub: "dead", Result: "success",
			ExecCode: execNotRun, ExecStatus: 0, CondResult: "yes"}, "dead"},

		{"stopped by signal", Unit{
			Active: "inactive", Sub: "dead", Result: "success",
			ExecCode: execKilled, ExecStatus: 15, CondResult: "yes"}, "stopped"},

		{"non-zero exit allowed by SuccessExitStatus", Unit{
			Active: "inactive", Sub: "dead", Result: "success",
			ExecCode: execExited, ExecStatus: 3, CondResult: "yes"}, "exit 3"},

		// deploy-config.service.
		{"failed with an exit code", Unit{
			Active: "failed", Sub: "failed", Result: "exit-code",
			ExecCode: execExited, ExecStatus: 1, CondResult: "yes"}, "exit 1"},

		{"failed on a signal", Unit{
			Active: "failed", Sub: "failed", Result: "signal",
			ExecCode: execKilled, ExecStatus: 9, CondResult: "yes"}, "sig KILL"},

		{"core dumped", Unit{
			Active: "failed", Sub: "failed", Result: "core-dump",
			ExecCode: execDumped, ExecStatus: 11, CondResult: "yes"}, "sig SEGV"},

		{"timed out", Unit{
			Active: "failed", Sub: "failed", Result: "timeout",
			ExecCode: execNotRun, CondResult: "yes"}, "timeout"},

		{"watchdog", Unit{
			Active: "failed", Sub: "failed", Result: "watchdog",
			ExecCode: execNotRun, CondResult: "yes"}, "watchdog"},

		{"start limit", Unit{
			Active: "failed", Sub: "failed", Result: "start-limit-hit",
			ExecCode: execNotRun, CondResult: "yes"}, "limit"},

		{"failed for an unnamed reason", Unit{
			Active: "failed", Sub: "failed", Result: "",
			ExecCode: execNotRun, CondResult: "yes"}, "failed"},

		{"starting", Unit{
			Active: "activating", Sub: "start", Result: "success",
			ExecCode: execNotRun, CondResult: "yes"}, "start"},

		// A unit whose properties systemd did not report at all.
		{"no exec data", Unit{
			Active: "inactive", Sub: "dead", Result: "success",
			ExecCode: unsetU64, ExecStatus: unsetU64}, "dead"},
	}

	for _, c := range cases {
		if got := c.u.StateLabel(); got != c.want {
			t.Errorf("%s: StateLabel() = %q, want %q", c.name, got, c.want)
		}
		// The column is narrow; a label that does not fit helps nobody.
		if len([]rune(c.u.StateLabel())) > 9 {
			t.Errorf("%s: label %q is wider than the STATE column", c.name, c.u.StateLabel())
		}
	}
}

// The three inactive cases must be visually distinguishable, or the label is
// the only thing carrying the difference. Styles are compared by what they
// declare rather than by what they render: the suite has no terminal, so
// lipgloss would strip every colour and make them all look alike.
func TestStateColorSeparatesInactiveCases(t *testing.T) {
	look := func(st lipgloss.Style) [3]any {
		return [3]any{st.GetForeground(), st.GetFaint(), st.GetBold()}
	}
	finished := Unit{Active: "inactive", Sub: "dead", Result: "success", ExecCode: execExited, ExecStatus: 0}
	never := Unit{Active: "inactive", Sub: "dead", Result: "success", ExecCode: execNotRun}
	skipped := Unit{Active: "inactive", Sub: "dead", Result: "success", ExecCode: execNotRun, CondResult: "no"}
	failed := Unit{Active: "failed", Sub: "failed", Result: "exit-code", ExecCode: execExited, ExecStatus: 1}
	running := Unit{Active: "active", Sub: "running"}

	if look(stateStyle(finished)) == look(stateStyle(never)) {
		t.Error("a finished oneshot and one that never ran look identical")
	}
	if stateStyle(failed).GetForeground() != colRed {
		t.Error("a failed unit should be red")
	}
	if stateStyle(running).GetForeground() != colGreen {
		t.Error("a running unit should be green")
	}
	// The quietest state has no colour of its own — colour 8 is not safe as
	// text, so it is the terminal's own foreground, dimmed.
	if st := stateStyle(skipped); !st.GetFaint() || st.GetForeground() != (lipgloss.NoColor{}) {
		t.Errorf("a skipped unit should be dimmed default, got fg=%v faint=%v", st.GetForeground(), st.GetFaint())
	}
	// The hues that carry meaning; anything else would be decoration.
	seen := map[lipgloss.TerminalColor]bool{}
	for _, u := range []Unit{finished, never, skipped, failed, running,
		{Active: "active", Sub: "exited"}, {Active: "activating", Sub: "start"}} {
		seen[stateStyle(u).GetForeground()] = true
	}
	for c := range seen {
		switch c {
		case colRed, colGreen, colYellow, colCyan, lipgloss.NoColor{}:
		default:
			t.Errorf("state colouring reached outside the palette: %v", c)
		}
	}
}

func TestRanAndSkipped(t *testing.T) {
	if (Unit{ExecCode: execExited}).Ran() == false {
		t.Error("an exited main process means the unit ran")
	}
	if (Unit{ExecCode: execNotRun}).Ran() {
		t.Error("code 0 means the unit never ran")
	}
	if (Unit{ExecCode: unsetU64}).Ran() {
		t.Error("an unreported code must not read as ran")
	}
	if !(Unit{CondResult: "no"}).Skipped() {
		t.Error("ConditionResult=no is a skip")
	}
	if !(Unit{Result: "exec-condition"}).Skipped() {
		t.Error("Result=exec-condition is a skip")
	}
	if (Unit{CondResult: "yes"}).Skipped() {
		t.Error("ConditionResult=yes is not a skip")
	}
}

func TestExitCode(t *testing.T) {
	if c, ok := (Unit{ExecCode: execExited, ExecStatus: 7}).ExitCode(); !ok || c != 7 {
		t.Errorf("ExitCode() = %d/%v", c, ok)
	}
	// A signal number is not an exit code.
	if _, ok := (Unit{ExecCode: execKilled, ExecStatus: 9}).ExitCode(); ok {
		t.Error("a killed process should not report an exit code")
	}
	if _, ok := (Unit{ExecCode: unsetU64, ExecStatus: unsetU64}).ExitCode(); ok {
		t.Error("missing exec data should not report an exit code")
	}
}

func TestSignalName(t *testing.T) {
	for n, want := range map[uint64]string{9: "KILL", 15: "TERM", 11: "SEGV", 6: "ABRT", 99: "99"} {
		if got := signalName(n); got != want {
			t.Errorf("signalName(%d) = %q, want %q", n, got, want)
		}
	}
}
