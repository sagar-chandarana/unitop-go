package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func recordedPid(t *testing.T, pidFile string) int {
	t.Helper()
	for i := 0; i < 250; i++ {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, e := strconv.Atoi(strings.TrimSpace(string(b))); e == nil && n != 0 {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the blocking child never recorded its pid")
	return 0
}

// A remote that connects but never answers must not pin the pane forever:
// phase one has a deadline. When it fires, the stream dies with a visible
// terminal batch (so the dead-stream recovery can retry), and the direct
// child is reaped by the time stopAndWait returns.
func TestPhaseOneDeadlineOnASilentRemote(t *testing.T) {
	defer func(o time.Duration) { backlogTimeout = o }(backlogTimeout)
	backlogTimeout = 300 * time.Millisecond

	bin := t.TempDir()
	pidFile := filepath.Join(bin, "ssh.pid")
	// The ssh session connects (records its pid) then blocks before any output.
	writeExe(t, bin, "ssh", "#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 300\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A mux-less runner: no ctlDir, so no 3s ssh -O exit cleanup against the
	// blocking fake ssh — the phase-one deadline is what this test measures.
	js := startJournal(context.Background(), runner{host: "root@silent"}, "u.service", logFilter{}, 50, 1)
	pid := recordedPid(t, pidFile)

	start := time.Now()
	var done bool
	var metas []string
	deadline := time.After(3 * time.Second)
	for !done {
		select {
		case b, ok := <-js.ch:
			if !ok {
				done = true
				break
			}
			for _, l := range b.lines {
				if l.meta {
					metas = append(metas, l.msg)
				}
			}
			if b.done {
				done = true
			}
		case <-deadline:
			t.Fatal("phase one never hit its deadline — the pane would spin forever")
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("phase one took %v, well past the %v deadline", elapsed, backlogTimeout)
	}
	if !strings.Contains(strings.Join(metas, "\n"), "clock probe") {
		t.Errorf("no visible terminal batch explaining the stall: %v", metas)
	}
	js.stopAndWait()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the silent-remote child (pid %d) outlived teardown: %v", pid, err)
	}
}

// A local backlog that blocks is bounded the same way — the direct
// journalctl child is killed at the deadline and the stream dies visibly.
func TestPhaseOneDeadlineOnABlockingBacklog(t *testing.T) {
	defer func(o time.Duration) { backlogTimeout = o }(backlogTimeout)
	backlogTimeout = 300 * time.Millisecond

	bin := t.TempDir()
	pidFile := filepath.Join(bin, "j.pid")
	// The follow invocation exits; the backlog invocation blocks.
	writeExe(t, bin, "journalctl", "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = -f ] && exit 0; done\n"+
		"echo $$ > "+pidFile+"\nexec sleep 300\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	js := startJournal(context.Background(), runner{}, "u.service", logFilter{}, 50, 1)
	pid := recordedPid(t, pidFile)

	got := false
	deadline := time.After(3 * time.Second)
	for !got {
		select {
		case b, ok := <-js.ch:
			if !ok || b.done {
				got = true
			}
		case <-deadline:
			t.Fatal("the blocking backlog never hit its deadline")
		}
	}
	js.stopAndWait()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the blocking backlog child (pid %d) outlived teardown: %v", pid, err)
	}
}

// A healthy but slow response UNDER the deadline still succeeds: the backlog
// lands and the follow tail begins and stays long-lived (its own phase, not
// bounded by the phase-one deadline).
func TestSlowButHealthyPhaseOneSucceeds(t *testing.T) {
	defer func(o time.Duration) { backlogTimeout = o }(backlogTimeout)
	backlogTimeout = time.Second

	bin := t.TempDir()
	writeExe(t, bin, "journalctl", "#!/bin/sh\n"+
		"for a in \"$@\"; do [ \"$a\" = -f ] && exec sleep 300; done\n"+
		"sleep 0.1\n"+ // well under the 1s deadline
		"printf '{\"__CURSOR\":\"c1\",\"__REALTIME_TIMESTAMP\":\"1723000000000001\",\"MESSAGE\":\"seed\"}\\n'\n"+
		"exit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	js := startJournal(context.Background(), runner{}, "u.service", logFilter{}, 50, 1)
	defer js.stopAndWait()

	seen, backlogDone := false, false
	deadline := time.After(5 * time.Second)
	for !backlogDone {
		select {
		case b, ok := <-js.ch:
			if !ok {
				t.Fatal("the stream ended before the backlog landed")
			}
			for _, l := range b.lines {
				if !l.meta && strings.Contains(l.msg, "seed") {
					seen = true
				}
			}
			if b.done {
				t.Fatal("a healthy slow stream died instead of following")
			}
			if b.backlogDone {
				backlogDone = true
			}
		case <-deadline:
			t.Fatal("the slow-but-healthy backlog never arrived")
		}
	}
	if !seen {
		t.Error("the backlog entry was not delivered")
	}
	// The follow phase is alive (the fake -f blocks); a moment later the
	// stream is still not done.
	select {
	case b := <-js.ch:
		if b.done {
			t.Error("the follow tail died — phase one's deadline leaked into phase two")
		}
	case <-time.After(200 * time.Millisecond):
	}
}
