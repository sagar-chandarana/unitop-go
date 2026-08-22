package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeFiniteJournalctl puts a scripted journalctl on PATH for the bounded
// finite-read tests, recording every invocation's argv.
func fakeFiniteJournalctl(t *testing.T, script string) (argv func() string) {
	t.Helper()
	bin := t.TempDir()
	full := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKEJ_DIR/argv\"\n" + script
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(full), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKEJ_DIR", bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(filepath.Join(bin, "argv"))
		return string(b)
	}
}

func pageStream() *journalStream {
	return &journalStream{ctx: context.Background(), done: nil}
}

func TestSliceJournalSelectorReachesBacklogAndPagingArgv(t *testing.T) {
	argv := fakeFiniteJournalctl(t, "exit 0\n")
	selector := sliceJournalSelector("system.slice", []string{
		"system.slice", "system-app.slice", "system-app-worker.slice",
	})

	if _, err := readBacklogSelector(context.Background(), runner{}, selector,
		logFilter{grep: "needle", prio: 4}, 25); err != nil {
		t.Fatalf("slice backlog failed: %v", err)
	}
	wantMatches := []string{
		"_SYSTEMD_SLICE=system.slice",
		"_SYSTEMD_SLICE=system-app.slice",
		"_SYSTEMD_SLICE=system-app-worker.slice",
	}
	assertSliceJournalArgv(t, argv(), wantMatches, "-n", "25", "-g", "needle", "-p", "4")

	msg := fetchOlderSelector(pageStream(), runner{}, selector, "anchor", logFilter{}, 10, 7)()
	if got := msg.(olderBatch); got.gen != 7 || got.err != "" {
		t.Fatalf("slice page result = %+v", got)
	}
	assertSliceJournalArgv(t, argv(), wantMatches, "--cursor", "anchor", "--reverse")
}

func assertSliceJournalArgv(t *testing.T, raw string, matches []string, other ...string) {
	t.Helper()
	args := strings.Split(strings.TrimSpace(raw), "\n")
	if slices.Contains(args, "-u") {
		t.Fatalf("slice journal unexpectedly used -u: %v", args)
	}
	for _, want := range append(append([]string(nil), matches...), other...) {
		if !slices.Contains(args, want) {
			t.Errorf("slice journal argv omitted %q: %v", want, args)
		}
	}
	for _, match := range matches {
		if n := strings.Count(raw, match+"\n"); n != 1 {
			t.Errorf("slice match %q occurred %d times in %q", match, n, raw)
		}
	}
}

// Twenty ~1MiB records, newest first, against the 16MiB page budget: the
// contiguous newest prefix is retained with its cursors, the boundary is a
// meta line saying so, and — the part that keeps the child alive — the rest
// is drained through EOF: the script writes all twenty and exits 0, which a
// reader that stopped at the budget would break with SIGPIPE, and that would
// surface here as an error.
func TestFiniteReadTruncatesHonestly(t *testing.T) {
	fakeFiniteJournalctl(t, `
payload=$(head -c 1100000 /dev/zero | tr '\0' a)
i=20
while [ $i -ge 1 ]; do
	printf '{"__CURSOR":"c%d","__REALTIME_TIMESTAMP":"%d","MESSAGE":"%s"}\n' $i $((1723000000000000 + i)) "$payload"
	i=$((i-1))
done
exit 0
`)
	lines, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 100)
	if err != nil {
		t.Fatalf("a truncated read is a partial success, got error: %v", err)
	}
	if len(lines) < 3 {
		t.Fatalf("kept %d lines", len(lines))
	}
	if !lines[0].meta || !strings.Contains(lines[0].msg, "budget") {
		t.Errorf("the boundary is not announced: %+v", lines[0])
	}
	if lines[0].cursor != "" {
		t.Error("the boundary notice must not carry a cursor")
	}
	// Contiguous newest: the last line is the newest record, and every
	// retained record follows its predecessor without a hole.
	if got := lines[len(lines)-1].cursor; got != "c20" {
		t.Errorf("newest retained = %q, want c20", got)
	}
	if got := lines[1].cursor; got == "" {
		t.Error("the oldest retained record must anchor the next page")
	}
	kept := len(lines) - 1
	if kept >= 20 || kept < 10 {
		t.Errorf("retained %d of 20 records; the 16MiB budget should hold roughly 15", kept)
	}
	first := 20 - kept + 1
	for i, l := range lines[1:] {
		want := "c" + strconv.Itoa(first+i)
		if l.cursor != want {
			t.Fatalf("retained records are not contiguous: line %d = %q, want %q", i, l.cursor, want)
		}
	}
}

// An oversized record becomes a cursorless placeholder without taking its
// neighbours with it — including when it is the page anchor itself, which is
// dropped by position, not by cursor equality.
func TestOversizedRecordsBecomePlaceholders(t *testing.T) {
	// The anchor is oversized; five ordinary records follow it.
	fakeFiniteJournalctl(t, `
big=$(head -c 5000000 /dev/zero | tr '\0' b)
printf '{"__CURSOR":"anchor","__REALTIME_TIMESTAMP":"1723000000000006","MESSAGE":"%s"}\n' "$big"
i=5
while [ $i -ge 1 ]; do
	printf '{"__CURSOR":"o%d","__REALTIME_TIMESTAMP":"%d","MESSAGE":"m%d"}\n' $i $((1723000000000000 + i)) $i
	i=$((i-1))
done
exit 0
`)
	msg := fetchOlder(pageStream(), runner{}, "u.service", "anchor", logFilter{}, 10, 7)()
	ob, ok := msg.(olderBatch)
	if !ok || ob.err != "" {
		t.Fatalf("page failed: %+v", msg)
	}
	if len(ob.lines) != 5 {
		t.Fatalf("kept %d of 5 records after the oversized anchor", len(ob.lines))
	}
	if got := ob.lines[len(ob.lines)-1].cursor; got != "o5" {
		t.Errorf("newest retained = %q, want o5 (the fixture emits o5 first)", got)
	}
	if got := ob.lines[0].cursor; got != "o1" {
		t.Errorf("oldest retained = %q, want o1", got)
	}
	if !ob.atEnd {
		t.Error("five records for a ten-record ask is the end of the journal")
	}

	// An oversized record in the middle: the entries older than it survive.
	fakeFiniteJournalctl(t, `
big=$(head -c 5000000 /dev/zero | tr '\0' b)
printf '{"__CURSOR":"anchor","__REALTIME_TIMESTAMP":"1723000000000009","MESSAGE":"the anchor"}\n'
printf '{"__CURSOR":"n1","__REALTIME_TIMESTAMP":"1723000000000008","MESSAGE":"newer"}\n'
printf '{"__CURSOR":"huge","__REALTIME_TIMESTAMP":"1723000000000007","MESSAGE":"%s"}\n' "$big"
printf '{"__CURSOR":"n2","__REALTIME_TIMESTAMP":"1723000000000006","MESSAGE":"older survivor"}\n'
exit 0
`)
	msg = fetchOlder(pageStream(), runner{}, "u.service", "anchor", logFilter{}, 10, 7)()
	ob = msg.(olderBatch)
	if ob.err != "" || len(ob.lines) != 3 {
		t.Fatalf("middle-oversize page: %+v", ob)
	}
	if ob.lines[0].cursor != "n2" {
		t.Errorf("the record older than the oversized one did not survive: %+v", ob.lines[0])
	}
	if ob.lines[1].cursor != "" || !strings.Contains(ob.lines[1].msg, "dropped a journal entry") {
		t.Errorf("the oversized middle is not a placeholder: %+v", ob.lines[1])
	}
	if ob.lines[2].cursor != "n1" {
		t.Errorf("order lost: %+v", ob.lines[2])
	}
}

// The page argv carries the field allowlist, exactly like the backlog: the
// full record set is what let a single page balloon.
func TestPageArgvCarriesJournalFields(t *testing.T) {
	argv := fakeFiniteJournalctl(t, "exit 0\n")
	fetchOlder(pageStream(), runner{}, "u.service", "c1", logFilter{}, 10, 1)()
	if !strings.Contains(argv(), journalFields) {
		t.Errorf("page argv lacks %q:\n%s", journalFields, argv())
	}
}

// journalctl's one non-failure: exit 1 with silence is a -g that matched
// nothing. Exit 1 with words, or with partial output, is a real failure —
// classified from the captured result, since custom pipes leave
// ExitError.Stderr permanently empty.
func TestExitOneClassification(t *testing.T) {
	fakeFiniteJournalctl(t, "exit 1\n")
	lines, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{grep: "x"}, 10)
	if err != nil || len(lines) != 0 {
		t.Errorf("silent exit 1 must be no-matches: lines=%d err=%v", len(lines), err)
	}
	msg := fetchOlder(pageStream(), runner{}, "u.service", "c1", logFilter{}, 10, 1)()
	if ob := msg.(olderBatch); !ob.atEnd || ob.err != "" {
		t.Errorf("silent exit 1 page must be atEnd: %+v", ob)
	}

	fakeFiniteJournalctl(t, "echo 'permission denied for the journal' >&2\nexit 1\n")
	if _, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 10); err == nil ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Errorf("stderr exit 1 must fail with the diagnostic: %v", err)
	}

	fakeFiniteJournalctl(t, `printf '{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"partial"}\n'`+"\nexit 1\n")
	if _, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 10); err == nil {
		t.Error("partial output with exit 1 is a real failure, not no-matches")
	}
}

// A successful read's stderr is a warning the user would otherwise never
// see; it lands in the pane as meta lines.
func TestSuccessfulReadSurfacesStderr(t *testing.T) {
	fakeFiniteJournalctl(t, `printf '{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"fine"}\n'`+"\necho 'lines were skipped by rate limiting' >&2\nexit 0\n")
	lines, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, l := range lines {
		if l.meta {
			joined += l.msg + "\n"
		}
	}
	if !strings.Contains(joined, "rate limiting") {
		t.Errorf("the warning vanished: %q", joined)
	}
}

// A stderr flood cannot starve stdout or flood the pane: the transcript is
// capped by bytes AND lines, one marker says so, and the record still lands.
func TestStderrFloodIsBoundedAndMarked(t *testing.T) {
	fakeFiniteJournalctl(t, `
i=0
while [ $i -lt 500 ]; do
	printf 'warning %04d: %s\n' $i 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' >&2
	i=$((i+1))
done
printf '{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"still made it"}\n'
exit 0
`)
	lines, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var record, marker, warnings int
	for _, l := range lines {
		switch {
		case !l.meta:
			record++
		case strings.Contains(l.msg, "diagnostics suppressed"):
			marker++
		default:
			warnings++
		}
	}
	if record != 1 {
		t.Errorf("stdout was starved: %d records", record)
	}
	if marker != 1 {
		t.Errorf("suppression marker count = %d, want exactly 1", marker)
	}
	if warnings > maxStderrLines {
		t.Errorf("%d warning lines surfaced; the cap is %d", warnings, maxStderrLines)
	}
}

// A warning a live -f prints once must land in the pane while the process
// still runs — it used to sit in a buffer until the stream died, which on a
// healthy unit is never.
func TestFollowWarningSurfacesWhileAlive(t *testing.T) {
	fakeFiniteJournalctl(t, `
for a in "$@"; do
	if [ "$a" = -f ]; then
		echo 'live-warning-marker' >&2
		exec sleep 300
	fi
done
printf '{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"seed"}\n'
exit 0
`)
	js := startJournal(context.Background(), runner{}, "u.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case b := <-js.ch:
			for _, l := range b.lines {
				if l.meta && strings.Contains(l.msg, "live-warning-marker") {
					if b.done {
						t.Fatal("the warning arrived only with the stream's end")
					}
					return
				}
			}
			if b.done {
				t.Fatal("the stream ended without surfacing the live warning")
			}
		case <-deadline:
			t.Fatal("the live warning never surfaced")
		}
	}
}

// A process can close stdout and keep talking on stderr. The tail keeps
// listening until the pump finishes too, so nothing said after stdout's EOF
// is lost, and only then does the stream report its end.
func TestStderrOutlivesStdout(t *testing.T) {
	fakeFiniteJournalctl(t, `
for a in "$@"; do
	if [ "$a" = -f ]; then
		exec 1>&-
		echo 'after-stdout-warning-one' >&2
		sleep 0.3
		echo 'after-stdout-warning-two' >&2
		exit 0
	fi
done
exit 0
`)
	js := startJournal(context.Background(), runner{}, "u.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	var got []string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case b, ok := <-js.ch:
			if !ok {
				t.Fatal("channel closed before the done batch")
			}
			for _, l := range b.lines {
				if l.meta {
					got = append(got, l.msg)
				}
			}
			if b.done {
				joined := strings.Join(got, "\n")
				for _, w := range []string{"after-stdout-warning-one", "after-stdout-warning-two"} {
					if !strings.Contains(joined, w) {
						t.Errorf("%q was lost after stdout closed; got:\n%s", w, joined)
					}
				}
				return
			}
		case <-deadline:
			t.Fatal("the stream never ended")
		}
	}
}

// A blocked page — nothing anchorable retained — is terminal: the model
// takes the error AND atEnd, so the next top-scroll launches nothing instead
// of refetching the identical page forever.
func TestBlockedPageDoesNotRelaunch(t *testing.T) {
	m := pagingModel(50)
	m.Update(olderBatch{gen: m.logGen, atEnd: true,
		err: "cannot page further back: every entry here is too large to anchor the next page on"})
	if m.logLoadErr == "" {
		t.Fatal("the blocked state lost its explanation")
	}
	if cmd := m.loadOlder(); cmd != nil {
		t.Error("a second scroll relaunched the blocked page")
	}
	if got := stripANSI(m.logTopMarker(120)); !strings.Contains(got, "too large") {
		t.Errorf("the top marker does not explain the block: %q", got)
	}
}

// After suppression begins, discarded flood lines must not keep the
// consumer's select loop hot: at most the cap plus one marker surface, and
// then the stream goes quiet even though the child keeps shouting.
func TestLiveFloodGoesQuietAfterSuppression(t *testing.T) {
	// The flood runs IN the direct child — no backgrounding: an orphaned
	// writer would hold both pipes open past the kill and hang stopAndWait.
	// A finite burst crosses both caps, then the same owned process keeps
	// shouting slowly, proving the quiet is suppression, not exhaustion.
	fakeFiniteJournalctl(t, `
for a in "$@"; do
	if [ "$a" = -f ]; then
		i=0
		while [ $i -lt 2000 ]; do echo "flood $i xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" >&2; i=$((i+1)); done
		while :; do echo "endless $i" >&2; i=$((i+1)); sleep 0.05; done
	fi
done
exit 0
`)
	js := startJournal(context.Background(), runner{}, "u.service", logFilter{}, 50, 1)
	defer js.stopAndWait()

	metas, marker := 0, 0
	settle := time.After(3 * time.Second)
collect:
	for {
		select {
		case b := <-js.ch:
			for _, l := range b.lines {
				if !l.meta {
					continue
				}
				metas++
				if strings.Contains(l.msg, "diagnostics suppressed") {
					marker++
				}
			}
			if b.done {
				t.Fatal("the stream ended under the flood")
			}
		case <-settle:
			break collect
		}
	}
	if marker != 1 {
		t.Errorf("suppression marker count = %d, want exactly 1", marker)
	}
	if metas > maxStderrLines+1 {
		t.Errorf("%d meta lines surfaced; the cap is %d plus the marker", metas, maxStderrLines)
	}
	// The child is still shouting into the void; the pane must now be quiet.
	select {
	case b := <-js.ch:
		if len(b.lines) > 0 {
			t.Errorf("a batch arrived after suppression went quiet: %+v", b.lines[0])
		}
	case <-time.After(500 * time.Millisecond):
	}
}

// A follow that dies with a nonzero status and nothing on stderr must say
// so, not hide behind the generic sign-off.
func TestSilentNonzeroFollowExitIsNamed(t *testing.T) {
	fakeFiniteJournalctl(t, `
for a in "$@"; do
	if [ "$a" = -f ]; then exit 3; fi
done
exit 0
`)
	js := startJournal(context.Background(), runner{}, "u.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case b := <-js.ch:
			if !b.done {
				continue
			}
			joined := ""
			for _, l := range b.lines {
				joined += l.msg + "\n"
			}
			if !strings.Contains(joined, "exit status 3") {
				t.Errorf("the silent nonzero exit is unnamed: %q", joined)
			}
			return
		case <-deadline:
			t.Fatal("the stream never reported its end")
		}
	}
}

// The aggregate discard latches: after the first over-budget record, a small
// older record that would have fit again must stay out, or the page stops
// being the contiguous newest prefix. Four 3.5MiB records fill ~14MiB, the
// fifth crosses 16MiB and latches, and the tiny sixth is the trap.
func TestAggregateTruncationLatches(t *testing.T) {
	fakeFiniteJournalctl(t, `
payload=$(head -c 3500000 /dev/zero | tr '\0' a)
i=6
while [ $i -ge 3 ]; do
	printf '{"__CURSOR":"c%d","__REALTIME_TIMESTAMP":"%d","MESSAGE":"%s"}\n' $i $((1723000000000000 + i)) "$payload"
	i=$((i-1))
done
printf '{"__CURSOR":"c2","__REALTIME_TIMESTAMP":"1723000000000002","MESSAGE":"%s"}\n' "$payload"
printf '{"__CURSOR":"tiny","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"small enough to sneak back in"}\n'
exit 0
`)
	lines, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.cursor == "tiny" {
			t.Fatal("a small record slipped in after the budget latched; the page is no longer the contiguous newest")
		}
	}
	if !lines[0].meta || !strings.Contains(lines[0].msg, "budget") {
		t.Errorf("no boundary notice: %+v", lines[0])
	}
	if got := lines[len(lines)-1].cursor; got != "c6" {
		t.Errorf("newest retained = %q, want c6", got)
	}
}

// A page whose every retained record is an oversized placeholder has nothing
// to anchor the next fetch on: driven for real, it must come back blocked —
// terminal and explained — with any warnings intact.
func TestAllCursorlessPageIsBlocked(t *testing.T) {
	fakeFiniteJournalctl(t, `
big=$(head -c 5000000 /dev/zero | tr '\0' b)
printf '{"__CURSOR":"anchor","__REALTIME_TIMESTAMP":"1723000000000009","MESSAGE":"the anchor"}\n'
printf '{"__CURSOR":"h1","__REALTIME_TIMESTAMP":"1723000000000008","MESSAGE":"%s"}\n' "$big"
printf '{"__CURSOR":"h2","__REALTIME_TIMESTAMP":"1723000000000007","MESSAGE":"%s"}\n' "$big"
echo 'page warning to keep' >&2
exit 0
`)
	msg := fetchOlder(pageStream(), runner{}, "u.service", "anchor", logFilter{}, 10, 3)()
	ob := msg.(olderBatch)
	if ob.err == "" || !ob.atEnd {
		t.Fatalf("an unanchorable page must be blocked AND terminal: %+v", ob)
	}
	joined := ""
	for _, l := range ob.lines {
		joined += l.msg + "\n"
	}
	if !strings.Contains(joined, "page warning to keep") {
		t.Errorf("the blocked page dropped its warnings: %q", joined)
	}
}

// The 64 KiB byte cap binds on its own, before the line cap can: a hundred
// kilobyte-long lines stop surfacing around line 65.
func TestStderrByteCapBindsAlone(t *testing.T) {
	fakeFiniteJournalctl(t, `
line=$(head -c 1000 /dev/zero | tr '\0' y)
i=0
while [ $i -lt 100 ]; do echo "$line" >&2; i=$((i+1)); done
printf '{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"made it"}\n'
exit 0
`)
	lines, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var warnings, marker int
	for _, l := range lines {
		if l.meta {
			if strings.Contains(l.msg, "diagnostics suppressed") {
				marker++
			} else {
				warnings++
			}
		}
	}
	if warnings >= 100 || warnings < 30 {
		t.Errorf("%d of 100 kilobyte lines surfaced; the 64 KiB cap should bind around 65", warnings)
	}
	if marker != 1 {
		t.Errorf("marker count = %d", marker)
	}
}

// One diagnostic longer than the reader keeps is clipped, marked, and — the
// classification half — still counts as journalctl having spoken: 4 KiB of
// spaces before real text must not trim to silence and fake a no-match.
func TestClippedDiagnosticStillCounts(t *testing.T) {
	fakeFiniteJournalctl(t, `
pad=$(head -c 5000 /dev/zero | tr '\0' ' ')
echo "${pad}the real complaint" >&2
exit 1
`)
	_, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{grep: "x"}, 10)
	if err == nil {
		t.Fatal("a spoken exit 1 was classified as no-matches")
	}
}

// Exit 1 with a nonblank stdout NOTICE (not a record) is a real failure too:
// journalctl said something, whatever the channel.
func TestExitOneWithNoticeIsAnError(t *testing.T) {
	fakeFiniteJournalctl(t, "echo -- 'No entries, but shaped oddly' \nexit 1\n")
	if _, err := readBacklog(context.Background(), runner{}, "u.service", logFilter{}, 10); err == nil {
		t.Fatal("exit 1 with stdout output was classified as no-matches")
	}
}

// Cancellation racing a natural end of stream: whichever wins, the stream
// winds down cleanly — children reaped, channel closed — under the race
// detector. The suppression claim is deliberately narrow: a send STARTED
// after cancellation returns false (send pre-checks ctx), while one already
// racing the cancel may deliver either way; such stale batches are
// generation-checked by the consumer, so cleanliness, not emptiness, is
// what this proves.
func TestCancelRacingNaturalEOF(t *testing.T) {
	fakeFiniteJournalctl(t, `
for a in "$@"; do
	if [ "$a" = -f ]; then exit 0; fi
done
exit 0
`)
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		js := startJournal(ctx, runner{}, "u.service", logFilter{}, 50, i)
		cancel()
		js.stopAndWait()
		for range js.ch { // whatever slipped in before the cancel
		}
	}
}
