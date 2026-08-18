package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testJournal builds a real journal file to run journalctl against: 1200
// entries whose only matches for NEEDLE are the oldest 100, far outside the
// last 500. That is the shape that exposed the bug — with -f, journalctl seeks
// back 500 *raw* entries and only then applies -g, so the one-command form
// `-n 500 -f -g NEEDLE` found none of the 100 matches that were there.
func testJournal(t *testing.T) string {
	t.Helper()
	const remote = "/run/current-system/sw/lib/systemd/systemd-journal-remote"
	if _, err := os.Stat(remote); err != nil {
		t.Skip("systemd-journal-remote not available")
	}
	if _, err := exec.LookPath("journalctl"); err != nil {
		t.Skip("journalctl not available")
	}

	const boot = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d"
	base := int64(1700000000000000)
	var b strings.Builder
	for i := 1; i <= 1200; i++ {
		msg := "routine chatter"
		if i <= 100 {
			msg = "NEEDLE old entry"
		}
		n, t64 := strconv.Itoa(i), base+int64(i)
		b.WriteString("__CURSOR=s=abc;i=" + strconv.FormatInt(int64(i), 16) +
			";b=" + boot + ";m=" + strconv.FormatInt(int64(i), 16) +
			";t=" + strconv.FormatInt(t64, 16) + ";x=0\n")
		b.WriteString("__REALTIME_TIMESTAMP=" + strconv.FormatInt(t64, 10) + "\n")
		b.WriteString("__MONOTONIC_TIMESTAMP=" + n + "\n")
		b.WriteString("_BOOT_ID=" + boot + "\n")
		b.WriteString("_SYSTEMD_UNIT=demo.service\nPRIORITY=6\n")
		b.WriteString("MESSAGE=" + msg + " " + n + "\n\n")
	}

	dir := t.TempDir()
	cmd := exec.Command(remote, "--output="+dir+"/system.journal", "-")
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not build a test journal: %v: %s", err, out)
	}
	return dir
}

// runJournalctl runs the arguments unitop would run, against the test journal
// rather than the host's: -D replaces the -u that a synthetic file has no use
// for. So this exercises the real argument construction, not a copy of it.
func runJournalctl(t *testing.T, dir string, args []string, wait time.Duration) []logLine {
	t.Helper()
	out := []string{"-D", dir, "-q"}
	for i := 0; i < len(args); i++ {
		if args[i] == "-u" {
			i++ // skip its value too
			continue
		}
		out = append(out, args[i])
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	raw, _ := exec.CommandContext(ctx, "journalctl", out...).Output()
	var lines []logLine
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if parsed, ok := parseJournalJSON([]byte(l)); ok {
			lines = append(lines, parsed)
		}
	}
	return lines
}

// reverseToChronological mirrors what readBacklog does with the output.
func reverseToChronological(in []logLine) []logLine {
	out := make([]logLine, len(in))
	for i, l := range in {
		out[len(in)-1-i] = l
	}
	return out
}

// The backlog reaches matches anywhere in the journal, not only those inside
// the follow window, and comes back oldest-first whatever the filter.
func TestBacklogReachesPastTheFollowWindow(t *testing.T) {
	dir := testJournal(t)

	// The bug, still reproducible: the old one-command form.
	old := []string{"-u", "demo.service", "-n", "500", "-f", "--no-pager", "-o", "json", "-g", "NEEDLE"}
	if got := runJournalctl(t, dir, old, 2*time.Second); len(got) != 0 {
		t.Logf("note: -n 500 -f -g NEEDLE returned %d entries here", len(got))
	} else {
		t.Log("confirmed: the one-command form finds 0 of the 100 matches")
	}

	lines := reverseToChronological(
		runJournalctl(t, dir, backlogArgs("demo.service", logFilter{grep: "NEEDLE"}, 500), 10*time.Second))
	if len(lines) != 100 {
		t.Fatalf("backlog found %d matches, want all 100", len(lines))
	}
	if !strings.HasSuffix(lines[0].msg, "entry 1") {
		t.Errorf("not oldest-first: starts with %q", lines[0].msg)
	}
	if !strings.HasSuffix(lines[len(lines)-1].msg, "entry 100") {
		t.Errorf("not oldest-first: ends with %q", lines[len(lines)-1].msg)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i].ts.Before(lines[i-1].ts) {
			t.Fatalf("entry %d goes backwards in time", i)
		}
	}
	if lines[len(lines)-1].cursor == "" {
		t.Error("no cursor on the newest entry, so the tail cannot resume from it")
	}
}

// Every filter shape comes back the same way round.
func TestBacklogShapes(t *testing.T) {
	dir := testJournal(t)
	for _, c := range []struct {
		name  string
		f     logFilter
		n     int
		want  int
		first string
	}{
		{"unfiltered", logFilter{}, 500, 500, "chatter 701"},
		{"level", logFilter{prio: 6}, 500, 500, "chatter 701"},
		{"grep matching nothing", logFilter{grep: "zzz-no-such-thing"}, 500, 0, ""},
		{"grep with fewer matches than n", logFilter{grep: "NEEDLE"}, 500, 100, "entry 1"},
		{"grep with more matches than n", logFilter{grep: "chatter"}, 50, 50, "chatter 1151"},
	} {
		t.Run(c.name, func(t *testing.T) {
			lines := reverseToChronological(
				runJournalctl(t, dir, backlogArgs("demo.service", c.f, c.n), 10*time.Second))
			if len(lines) != c.want {
				t.Fatalf("got %d entries, want %d", len(lines), c.want)
			}
			if c.want > 0 && !strings.HasSuffix(lines[0].msg, c.first) {
				t.Errorf("first entry %q does not end with %q", lines[0].msg, c.first)
			}
		})
	}
}

// The tail resumes exactly after the backlog: nothing replayed, nothing skipped.
func TestFollowResumesAfterTheBacklog(t *testing.T) {
	dir := testJournal(t)
	backlog := reverseToChronological(
		runJournalctl(t, dir, backlogArgs("demo.service", logFilter{}, 20), 10*time.Second))
	if len(backlog) != 20 {
		t.Fatalf("backlog is %d entries", len(backlog))
	}
	newest := backlog[len(backlog)-1]

	after := runJournalctl(t, dir,
		followArgs("demo.service", logFilter{}, newest.cursor, time.Now()), 2*time.Second)
	if len(after) != 0 {
		t.Errorf("following from the newest backlog cursor replayed %d entries", len(after))
	}

	// And from one entry earlier it replays exactly that one — so the cursor is
	// being honoured, and the empty result above is not a silent failure.
	after = runJournalctl(t, dir,
		followArgs("demo.service", logFilter{}, backlog[len(backlog)-2].cursor, time.Now()), 2*time.Second)
	if len(after) != 1 || after[0].msg != newest.msg {
		t.Errorf("resuming one entry earlier gave %d entries, want just %q", len(after), newest.msg)
	}
}

// -n 0 reads as "start with nothing", but journalctl takes it as "replay
// nothing" and it silently defeats --after-cursor. Losing that replay would
// drop whatever the unit wrote between the backlog command and the tail.
func TestFollowDoesNotSuppressItsOwnReplay(t *testing.T) {
	dir := testJournal(t)
	backlog := reverseToChronological(
		runJournalctl(t, dir, backlogArgs("demo.service", logFilter{}, 20), 10*time.Second))
	secondNewest := backlog[len(backlog)-2].cursor

	args := followArgs("demo.service", logFilter{}, secondNewest, time.Now())
	for i, a := range args {
		if a == "-n" && i+1 < len(args) && args[i+1] == "0" {
			t.Fatal("followArgs passes -n 0, which suppresses the replay it depends on")
		}
	}
	if got := runJournalctl(t, dir, args, 2*time.Second); len(got) != 1 {
		t.Errorf("resuming one entry back replayed %d entries, want 1", len(got))
	}
}
