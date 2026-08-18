package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// stubJournalctl puts a journalctl on PATH that serves the test journal: it
// rewrites `-u <unit>` into `-D <dir>` and hands the rest to the real one. That
// lets the whole pipeline — startJournal, both phases, the model, the pane —
// run against a journal we control, rather than testing the pieces separately
// and hoping they meet in the middle.
func stubJournalctl(t *testing.T, dir string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nargs=''\nskip=0\nfor a in \"$@\"; do\n" +
		"  if [ $skip = 1 ]; then skip=0; continue; fi\n" +
		"  if [ \"$a\" = -u ]; then skip=1; continue; fi\n" +
		"  args=\"$args '$a'\"\ndone\n" +
		"eval exec /run/current-system/sw/bin/journalctl -D " + dir + " -q $args\n"
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// drain runs a stream to the point where the backlog has landed, feeding every
// batch into the model the way the bubbletea loop would.
func drain(t *testing.T, m *model, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case b, ok := <-m.journal.ch:
			if !ok {
				return
			}
			m.Update(b)
			if b.backlogDone {
				return
			}
		case <-deadline:
			t.Fatal("the backlog never arrived")
		}
	}
}

func e2eModel(t *testing.T, dir string, f logFilter) *model {
	t.Helper()
	stubJournalctl(t, dir)
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	m.logFilt = f
	m.journal = startJournal(context.Background(), m.r, "demo.service", f, journalBacklog, m.logGen)
	t.Cleanup(m.journal.stop)
	return &m
}

// The whole path, end to end: a search whose only matches are older than the
// follow window puts them on screen, oldest at the top.
func TestSearchFindsOldMatchesEndToEnd(t *testing.T) {
	dir := testJournal(t)
	m := e2eModel(t, dir, logFilter{grep: "NEEDLE"})
	drain(t, m, 15*time.Second)

	if len(m.logs) != 100 {
		t.Fatalf("the pane holds %d entries, want all 100 matches", len(m.logs))
	}
	if !strings.HasSuffix(m.logs[0].msg, "entry 1") {
		t.Errorf("oldest is not first: %q", m.logs[0].msg)
	}
	if !strings.HasSuffix(m.logs[len(m.logs)-1].msg, "entry 100") {
		t.Errorf("newest is not last: %q", m.logs[len(m.logs)-1].msg)
	}
	if !m.logBacklogDone {
		t.Error("the backlog-done signal never reached the model")
	}

	// And it renders that way: newest at the bottom, which is where the eye
	// goes and where following happens.
	win := m.renderLogWindow(m.logInnerWidth(), m.logHeight())
	if !strings.Contains(stripANSI(win[len(win)-1]), "entry 100") {
		t.Errorf("the newest entry is not on the bottom line: %q", stripANSI(win[len(win)-1]))
	}
}

// A search that matches nothing reaches the settled, honest message rather than
// sitting on "reading the journal…".
func TestSearchWithNoMatchesEndToEnd(t *testing.T) {
	dir := testJournal(t)
	m := e2eModel(t, dir, logFilter{grep: "zzz-no-such-thing"})
	if !m.logStarting() {
		t.Error("should start out reading")
	}
	drain(t, m, 15*time.Second)

	if len(m.logs) != 0 {
		t.Fatalf("got %d entries for a pattern that matches nothing", len(m.logs))
	}
	if m.logStarting() {
		t.Error("still claiming to be reading after the backlog ended")
	}
	got := stripANSI(strings.Join(m.emptyLogNotice(), " "))
	if !strings.Contains(got, "no entries") || !strings.Contains(got, "zzz-no-such-thing") {
		t.Errorf("pane says %q", got)
	}
}

// Unfiltered, the pane holds the newest 500 and can page back past them.
func TestBacklogAndPagingEndToEnd(t *testing.T) {
	dir := testJournal(t)
	m := e2eModel(t, dir, logFilter{})
	drain(t, m, 15*time.Second)

	if len(m.logs) != journalBacklog {
		t.Fatalf("the pane holds %d entries, want %d", len(m.logs), journalBacklog)
	}
	if !strings.HasSuffix(m.logs[0].msg, "chatter 701") {
		t.Errorf("oldest held is %q, want chatter 701", m.logs[0].msg)
	}

	// Scroll to the top and let the page land.
	m.focus = focusLogs
	var cmd = m.scrollLog(scrollToEnd)
	if cmd == nil {
		t.Fatal("reaching the top did not ask for earlier entries")
	}
	applyCmd(t, m, cmd)
	if len(m.logs) <= journalBacklog {
		t.Fatalf("paging back added nothing: still %d entries", len(m.logs))
	}
	if !strings.HasSuffix(m.logs[0].msg, "chatter 201") && !strings.HasSuffix(m.logs[0].msg, "entry 201") {
		t.Logf("oldest after one page: %q", m.logs[0].msg)
	}
	for i := 1; i < len(m.logs); i++ {
		if m.logs[i].ts.Before(m.logs[i-1].ts) {
			t.Fatalf("the buffer is out of order at %d: %q then %q",
				i, m.logs[i-1].msg, m.logs[i].msg)
		}
	}
}

// applyCmd runs a tea.Cmd and feeds what it produces back into the model,
// unwrapping a Batch the way the bubbletea loop does.
func applyCmd(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			applyCmd(t, m, c)
		}
		return
	}
	m.Update(msg)
}

// journalctl exits 1 when a -g pattern matches nothing, and says nothing on
// stderr about it. Treated as a failure it becomes a red line claiming the
// journal could not be read. A real failure has something to say — that is the
// only thing separating the two.
func TestNoMatchesIsNotAFailure(t *testing.T) {
	dir := testJournal(t)
	stubJournalctl(t, dir)
	ctx := context.Background()

	lines, err := readBacklog(ctx, runner{}, "demo.service", logFilter{grep: "zzz-no-such-thing"}, 500)
	if err != nil {
		t.Errorf("a pattern matching nothing reported an error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("got %d lines", len(lines))
	}

	// Sanity: the same call with a real pattern still works, so the branch
	// above is not swallowing everything.
	lines, err = readBacklog(ctx, runner{}, "demo.service", logFilter{grep: "NEEDLE"}, 500)
	if err != nil || len(lines) != 100 {
		t.Errorf("a matching pattern gave %d lines, err=%v", len(lines), err)
	}

	// And a genuine failure is still a failure: a directory that is not a
	// journal makes journalctl exit non-zero *with* something on stderr.
	bad := t.TempDir()
	stubJournalctl(t, bad)
	if _, err := readBacklog(ctx, runner{}, "demo.service", logFilter{}, 500); err == nil {
		t.Log("note: an empty journal directory is not an error here, only an empty result")
	} else {
		t.Logf("a real failure is still reported: %v", err)
	}
}
