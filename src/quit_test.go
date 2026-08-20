package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Ctrl-C means quit in every state the UI has — the menu used to swallow it,
// the confirmation treated it as cancel, and the filter editors quit without
// stopping the journal. Every path returns tea.Quit AND stops the stream.
func TestCtrlCQuitsFromEveryState(t *testing.T) {
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}
	states := []struct {
		name string
		prep func(m *model)
	}{
		{"unit list", func(m *model) {}},
		{"unit filter editor", func(m *model) { m.handleKey(keyOf("/")) }},
		{"log filter editor", func(m *model) { m.focus = focusLogs; m.handleKey(keyOf("/")) }},
		{"action menu", func(m *model) { m.handleKey(keyOf("x")) }},
		{"confirmation", func(m *model) {
			m.handleKey(keyOf("x"))
			m.handleKey(keyOf("down")) // "stop", which asks first
			m.handleKey(keyOf("\r"))
		}},
		{"too small", func(m *model) { m.width, m.height = 20, 5 }},
		{"disconnected", func(m *model) { m.connected = false }},
	}
	for _, st := range states {
		mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m := &mm
		m.width, m.height, m.ready, m.connected = 140, 30, true, true
		m.units = testUnits()
		m.rebuild()
		st.prep(m)
		var stopped bool
		m.journal = &journalStream{cancel: func() { stopped = true }}

		_, cmd := m.handleKey(ctrlC)
		if cmd == nil {
			t.Errorf("%s: ctrl-c returned no command", st.name)
			continue
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: ctrl-c did not quit", st.name)
		}
		if !stopped {
			t.Errorf("%s: the journal stream was left running", st.name)
		}
	}

	// A pasted ETX is data, not a gesture: it stays in the editor.
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 140, 30, true, true
	m.units = testUnits()
	m.rebuild()
	m.handleKey(keyOf("/"))
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune{'a', 0x03, 'b'}})
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("a pasted ETX quit the program")
		}
	}
	if m.filter != "a␃b" {
		t.Errorf("pasted ETX not sanitized into the editor: %q", m.filter)
	}
}

// q leaves through the same exit as ctrl-c, journal stop included.
func TestQQuitsThroughTheSameExit(t *testing.T) {
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 140, 30, true, true
	m.units = testUnits()
	m.rebuild()
	var stopped bool
	m.journal = &journalStream{cancel: func() { stopped = true }}
	_, cmd := m.handleKey(keyOf("q"))
	if cmd == nil {
		t.Fatal("q returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || !stopped {
		t.Errorf("q: quit=%v stopped=%v", cmd != nil, stopped)
	}
}

// The whole point, end to end: a real quiet FOLLOW — the backlog phase
// exits cleanly, and only the -f invocation writes its pid and sleeps, so
// the pid provably belongs to the follow child rooted at
// context.Background(). Ctrl-c must have reaped it and closed the channel
// BEFORE handleKey returns: quit() waits, it does not ask and hope.
func TestCtrlCKillsTheJournalChild(t *testing.T) {
	followPid, pagePid := fakeFollowJournalctl(t)

	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 140, 30, true, true
	m.units = testUnits()
	m.rebuild()
	if cmd := m.syncJournal(); cmd == nil {
		t.Fatal("no journal stream started")
	}
	ch := m.journal.ch
	pid := waitPid(t, followPid, "follow")

	// A page fetch in flight, too: its child hangs on a --cursor read.
	page := fetchOlder(m.journal, runner{}, m.journal.unit, "c1", logFilter{}, 10, m.logGen)
	pageDone := make(chan tea.Msg, 1)
	go func() { pageDone <- page() }()
	paging := waitPid(t, pagePid, "page")

	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl-c returned no command")
	}

	// handleKey has returned; the guarantee is already due: both children
	// reaped — ESRCH exactly — and the channel closed, right now.
	assertReaped(t, pid, "the follow child")
	assertReaped(t, paging, "the page-fetch child")
	select {
	case <-pageDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the page fetch never returned")
	}
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed, with only already-buffered batches before it
			}
		default:
			t.Fatal("the journal channel is still open after handleKey returned")
		}
	}
}

// fakeFollowJournalctl scripts a journalctl whose backlog exits cleanly with
// one seed entry (so paging has a cursor to hang off), whose -f invocation
// records its pid and sleeps, and whose --cursor invocation does the same
// under a second pid file — a quiet follow and a blocked page, each a real
// child rooted at the stream's context.
func fakeFollowJournalctl(t *testing.T) (followPid, pagePid func() int) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
has_f=0; has_cursor=0
for a in "$@"; do
	[ "$a" = -f ] && has_f=1
	case "$a" in --*cursor*|--cursor) has_cursor=1 ;; esac
done
if [ $has_f = 1 ]; then echo $$ >> "$FAKEJ_DIR/pids.follow"; exec sleep 300; fi
if [ $has_cursor = 1 ]; then echo $$ >> "$FAKEJ_DIR/pids.page"; exec sleep 300; fi
printf '%s\n' '{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000000","MESSAGE":"seed","PRIORITY":"6"}'
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKEJ_DIR", bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	last := func(name string) func() int {
		return func() int {
			b, err := os.ReadFile(filepath.Join(bin, name))
			if err != nil {
				return 0
			}
			f := strings.Fields(string(b))
			if len(f) == 0 {
				return 0
			}
			n, _ := strconv.Atoi(f[len(f)-1])
			return n
		}
	}
	return last("pids.follow"), last("pids.page")
}

func waitPid(t *testing.T, get func() int, what string) int {
	t.Helper()
	for i := 0; i < 250; i++ {
		if pid := get(); pid != 0 {
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the fake %s never started", what)
	return 0
}

// gone means really gone: reaped, not zombie — ESRCH exactly.
func assertReaped(t *testing.T, pid int, what string) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("%s (pid %d) was not reaped: kill(0) = %v", what, pid, err)
	}
}

// Replacing a stream — a selection or filter change — drops its only
// pointer, so the replacement must reap the old follow and any page in
// flight BEFORE letting go, exactly like quitting.
func TestStreamReplacementReapsTheOldChildren(t *testing.T) {
	followPid, pagePid := fakeFollowJournalctl(t)

	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 140, 30, true, true
	m.units = testUnits()
	m.rebuild()
	if cmd := m.syncJournal(); cmd == nil {
		t.Fatal("no journal stream started")
	}
	oldFollow := waitPid(t, followPid, "follow")

	page := fetchOlder(m.journal, runner{}, m.journal.unit, "c1", logFilter{}, 10, m.logGen)
	pageDone := make(chan tea.Msg, 1)
	go func() { pageDone <- page() }()
	oldPage := waitPid(t, pagePid, "page")

	m.logFilt.grep = "different"
	if cmd := m.syncJournal(); cmd == nil {
		t.Fatal("the filter change did not restart the stream")
	}
	assertReaped(t, oldFollow, "the replaced follow")
	assertReaped(t, oldPage, "the replaced page fetch")
	select {
	case <-pageDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the page fetch never returned")
	}

	m.quit() // leave nothing behind for the next test
}
