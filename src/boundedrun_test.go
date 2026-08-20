package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// shCmd builds a `sh -c script` command; the primitive is transport-agnostic,
// so a local shell exercises it exactly as a remote ssh would.
func shCmd(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}

// stdout is capped at exactly maxCmdOutput: below and at the cap the whole
// output survives untruncated; one byte over comes back as errOversized with
// the buffer held at the cap. cap±1 fixtures are ~1 MiB — never GB.
func TestBoundedRunStdoutCapBoundary(t *testing.T) {
	for _, c := range []struct {
		n       int
		wantErr error
		wantLen int
	}{
		{maxCmdOutput - 1, nil, maxCmdOutput - 1},
		{maxCmdOutput, nil, maxCmdOutput},
		{maxCmdOutput + 1, errOversized, maxCmdOutput},
	} {
		script := fmt.Sprintf("head -c %d /dev/zero | tr '\\0' a", c.n)
		out, _, err := boundedRun(shCmd(context.Background(), script))
		if !errors.Is(err, c.wantErr) && err != c.wantErr {
			t.Errorf("n=%d: err=%v, want %v", c.n, err, c.wantErr)
		}
		if len(out) != c.wantLen {
			t.Errorf("n=%d: kept %d bytes, want %d", c.n, len(out), c.wantLen)
		}
	}
}

// A stderr flood alone is not oversized: stdout still arrives, exit 0 is not
// an error, and the stderr transcript is bounded with the one suppression
// marker (the existing stderrPump caps).
func TestBoundedRunStderrIsBoundedNotFatal(t *testing.T) {
	script := `printf 'the answer\n'
i=0
while [ $i -lt 4000 ]; do echo "noise $i xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" >&2; i=$((i+1)); done
exit 0`
	out, serr, err := boundedRun(shCmd(context.Background(), script))
	if err != nil {
		t.Fatalf("a stderr flood made the run fail: %v", err)
	}
	if strings.TrimSpace(string(out)) != "the answer" {
		t.Errorf("stdout lost under the stderr flood: %q", out)
	}
	if len(serr) > maxStderrBytes+256 {
		t.Errorf("stderr transcript is %d bytes, cap is %d", len(serr), maxStderrBytes)
	}
	if !strings.Contains(serr, "diagnostics suppressed") {
		t.Errorf("no suppression marker on a stderr flood: %q", serr)
	}
}

// The child is drained past the cap, so it never blocks on a full pipe: a
// command that floods 8 MiB and THEN writes a sentinel and exits must reach
// both — if boundedRun stopped reading at the cap the child would wedge
// forever. And its PID is reaped (ESRCH) by the time boundedRun returns.
func TestBoundedRunDrainsSoChildNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "finished")
	pidFile := filepath.Join(dir, "pid")
	script := fmt.Sprintf(`echo $$ > %s
head -c 8388608 /dev/zero | tr '\0' a
: > %s`, pidFile, sentinel)

	_, _, err := boundedRun(shCmd(context.Background(), script))
	if !errors.Is(err, errOversized) {
		t.Fatalf("8 MiB should be oversized: %v", err)
	}
	if _, e := os.Stat(sentinel); e != nil {
		t.Errorf("the child never finished — it blocked on a full pipe: %v", e)
	}
	b, _ := os.ReadFile(pidFile)
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid)
	if pid != 0 {
		if e := syscall.Kill(pid, 0); !errors.Is(e, syscall.ESRCH) {
			t.Errorf("the child (pid %d) was not reaped by return: %v", pid, e)
		}
	}
}

// An endless flood returns promptly on cancellation with the child reaped —
// memory stays bounded because the read loop discards past the cap rather
// than buffering.
func TestBoundedRunCancellationReaps(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := fmt.Sprintf(`echo $$ > %s
while :; do head -c 65536 /dev/zero | tr '\0' a; done`, pidFile)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { boundedRun(shCmd(ctx, script)); close(done) }()

	var pid int
	for i := 0; i < 250 && pid == 0; i++ {
		b, _ := os.ReadFile(pidFile)
		fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid)
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the flood never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("boundedRun did not return after cancellation")
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the flooding child survived cancellation")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A nonzero exit folds the captured stderr into the error — the pipe leaves
// ExitError.Stderr empty, so boundedRun must carry the diagnostic itself.
func TestBoundedRunFoldsStderrOnFailure(t *testing.T) {
	_, _, err := boundedRun(shCmd(context.Background(), "echo 'permission denied for the journal' >&2; exit 7"))
	if err == nil || !strings.Contains(err.Error(), "permission denied for the journal") {
		t.Errorf("stderr not folded into the failure: %v", err)
	}
}

// Integration: an oversized reply to any poll path surfaces as an error, not
// a malformed zero-unit parse — local and over fake ssh.
func TestOversizedPollSurfacesAnError(t *testing.T) {
	flood := "head -c 2000000 /dev/zero | tr '\\0' a"
	// Local: a fake systemctl that floods on --version.
	bin := t.TempDir()
	writeExe(t, bin, "systemctl", "#!/bin/sh\ncase \"$1\" in --version) "+flood+" ;; esac\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := NewCollector(runner{})
	if _, _, err := c.pollBase(context.Background()); err == nil {
		t.Error("a flooding local version probe polled successfully")
	}

	// Remote: a fake ssh that runs the joined command locally, flooding the
	// framed base poll.
	bin2 := t.TempDir()
	writeExe(t, bin2, "ssh", "#!/bin/sh\nfor a in \"$@\"; do last=$a; done\nexec sh -c \"$last\"\n")
	writeExe(t, bin2, "date", "#!/bin/sh\necho 1723000000\n")
	writeExe(t, bin2, "systemctl", "#!/bin/sh\n"+flood+"\n")
	t.Setenv("PATH", bin2+string(os.PathListSeparator)+os.Getenv("PATH"))
	rc := NewCollector(testRunner(t, "root@fake"))
	if _, _, err := rc.pollBase(context.Background()); err == nil {
		t.Error("a flooding remote poll polled successfully")
	}
}

func writeExe(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Oversized coverage for the two remaining callers: the detailed `systemctl
// show` batch (via Poll) and a unit action. Both must expose a bounded
// failure, not parse/succeed on a truncated reply.
func TestOversizedShowAndActionSurfaceErrors(t *testing.T) {
	flood := "head -c 2000000 /dev/zero | tr '\\0' a"

	// Detailed show: valid version + one unit, then a flood on `show`.
	bin := t.TempDir()
	writeExe(t, bin, "systemctl", "#!/bin/sh\ncase \"$1\" in\n"+
		"--version) echo 'systemd 251 (251.4)' ;;\n"+
		"list-units) printf 'dummy.service loaded active running A dummy\\n' ;;\n"+
		"show) "+flood+" ;;\nesac\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := NewCollector(runner{})
	if _, _, err := c.Poll(context.Background()); err == nil {
		t.Error("a flooding detailed-show polled successfully")
	} else if !strings.Contains(err.Error(), "systemctl show") {
		t.Errorf("the show flood surfaced the wrong error: %v", err)
	}

	// Action: a flood on the action verb. The result must carry the bounded
	// failure, not a truncated success.
	bin2 := t.TempDir()
	writeExe(t, bin2, "systemctl", "#!/bin/sh\n"+flood+"\nexit 0\n")
	t.Setenv("PATH", bin2+string(os.PathListSeparator)+os.Getenv("PATH"))
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	msg := m.runAction("nginx.service", unitActions[0])()
	ar, ok := msg.(actionResult)
	if !ok {
		t.Fatalf("runAction returned %T", msg)
	}
	if !errors.Is(ar.err, errOversized) {
		t.Errorf("a flooding action did not surface errOversized: %v", ar.err)
	}
}
