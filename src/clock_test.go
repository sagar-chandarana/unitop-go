package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The shift is uniform and nothing else: distinct stamps stay distinct and
// ordered — including stamps the shift lands in the future — because sort
// and the tree read the values, and collapsing them would scramble both.
func TestNormalizeClocksShiftsUniformly(t *testing.T) {
	c := NewCollector(runner{host: "root@h"})
	c.clockOff = time.Hour // the client runs an hour ahead of the remote

	base := time.Unix(1_723_000_000, 0)
	units := []Unit{
		{Name: "a.service", ActiveSince: base, StateChange: base.Add(time.Minute)},
		{Name: "b.service", ActiveSince: base.Add(30 * time.Minute)},
		{Name: "c.service"}, // zero stamps stay zero
	}
	c.normalizeClocks(units)

	if !units[0].ActiveSince.Equal(base.Add(time.Hour)) ||
		!units[0].StateChange.Equal(base.Add(61*time.Minute)) ||
		!units[1].ActiveSince.Equal(base.Add(90*time.Minute)) {
		t.Errorf("shift not uniform: %v %v %v",
			units[0].ActiveSince, units[0].StateChange, units[1].ActiveSince)
	}
	if !units[2].ActiveSince.IsZero() {
		t.Error("a zero stamp was invented")
	}
	if !units[0].ActiveSince.Before(units[1].ActiveSince) {
		t.Error("relative order lost")
	}

	// Local collectors are left strictly alone.
	local := NewCollector(runner{})
	local.clockOff = time.Hour // impossible in practice; the guard must hold
	was := base
	lu := []Unit{{Name: "x.service", ActiveSince: was}}
	local.normalizeClocks(lu)
	if !lu[0].ActiveSince.Equal(was) {
		t.Error("a local collector's stamps were shifted")
	}
}

// The clamp lives on the DISPLAYED age, not the stored stamp: a stamp a
// hair in the future reads as zero age, while two distinct future stamps
// remain distinct values underneath.
func TestAgeOfClampsDisplayOnly(t *testing.T) {
	future := time.Now().Add(2 * time.Second)
	if got := ageOf(future); got != 0 {
		t.Errorf("a future stamp aged %v, want 0", got)
	}
	past := time.Now().Add(-3 * time.Second)
	if got := ageOf(past); got < 2*time.Second || got > 4*time.Second {
		t.Errorf("an ordinary age came out %v", got)
	}
}

// One strict parser for every clock reply: exactly one nonempty line of
// decimal digits fitting int64.
func TestParseEpochLine(t *testing.T) {
	if ts, err := parseEpochLine("1723000000\n"); err != nil || ts.Unix() != 1723000000 {
		t.Errorf("a plain epoch was rejected: %v %v", ts, err)
	}
	if _, err := parseEpochLine("1723000000\r\n"); err != nil {
		t.Errorf("CRLF rejected: %v", err)
	}
	if _, err := parseEpochLine("1723000000"); err != nil {
		t.Errorf("no terminator rejected: %v", err)
	}
	bad := map[string]string{
		"empty":             "",
		"blank line":        "\n",
		"multi-line":        "1723000000\ntrailing payload\n",
		"double terminator": "1723000000\n\n",
		"bare CR":           "1723000000\r",
		"leading space":     " 1723000000\n",
		"trailing space":    "1723000000 \n",
		"nondecimal":        "17230x0000\n",
		"signed":            "-1723000000\n",
		"zero":              "0\n",
		"words":             "Tue Aug 20 05:00:00\n",
		"overflow":          strings.Repeat("9", 20) + "\n",
	}
	for name, in := range bad {
		if _, err := parseEpochLine(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// fakeRemoteRig wires a fake ssh (executes the joined remote command
// locally), a skewed fake date, and a recording fake journalctl.
func fakeRemoteRig(t *testing.T, skew time.Duration) (argvOf func(which string) string) {
	t.Helper()
	bin := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("ssh", "#!/bin/sh\nfor a in \"$@\"; do last=$a; done\nexec sh -c \"$last\"\n")
	write("date", fmt.Sprintf("#!/bin/sh\necho %d\n", time.Now().Add(skew).Unix()))
	write("journalctl", `#!/bin/sh
has_f=0
for a in "$@"; do [ "$a" = -f ] && has_f=1; done
if [ $has_f = 1 ]; then
	printf '%s\n' "$@" > "$FAKEJ_DIR/follow.argv"
	exit 0
fi
printf '%s\n' "$@" > "$FAKEJ_DIR/backlog.argv"
cat "$FAKEJ_DIR/backlog.out" 2>/dev/null
exit 0
`)
	write("systemctl", `#!/bin/sh
case "$1" in
--version) echo "systemd 251 (251.4)" ;;
*) printf 'dummy.service loaded active running A dummy\n' ;;
esac
`)
	t.Setenv("FAKEJ_DIR", bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(which string) string {
		b, _ := os.ReadFile(filepath.Join(bin, which+".argv"))
		return string(b)
	}
}

// drainUntilDone reads the stream to its terminal batch (or channel close).
func drainUntilDone(t *testing.T, js *journalStream) []string {
	t.Helper()
	var metas []string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case b, ok := <-js.ch:
			if !ok {
				return metas
			}
			for _, l := range b.lines {
				if l.meta {
					metas = append(metas, l.msg)
				}
			}
			if b.done {
				return metas
			}
		case <-deadline:
			t.Fatal("the stream never finished")
		}
	}
}

// With an empty backlog the follow's --since boundary is the REMOTE's now —
// under either sign of skew, and with a filter too. The client's clock
// appears nowhere.
func TestEmptyBacklogFollowsFromTheRemoteClock(t *testing.T) {
	for _, skew := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		argv := fakeRemoteRig(t, skew)
		want := fmt.Sprintf("@%d", time.Now().Add(skew).Unix())

		js := startJournal(context.Background(), runner{host: "root@fake"}, "dummy.service",
			logFilter{grep: "x"}, 50, 1)
		defer js.stopAndWait()
		drainUntilDone(t, js)
		js.stopAndWait()

		got := argv("follow")
		if !strings.Contains(got, "--since\n"+want) && !strings.Contains(got, "--since\n@") {
			t.Fatalf("skew %v: follow argv lacks --since: %q", skew, got)
		}
		// Exact: the probed remote epoch, floor seconds, ±1s of rig latency.
		lines := strings.Split(got, "\n")
		for i, l := range lines {
			if l == "--since" && i+1 < len(lines) {
				var e int64
				fmt.Sscanf(lines[i+1], "@%d", &e)
				d := e - time.Now().Add(skew).Unix()
				if d < -3 || d > 1 {
					t.Errorf("skew %v: --since %s is %ds from the remote clock", skew, lines[i+1], d)
				}
			}
		}
		if !strings.Contains(got, "-g\nx") {
			t.Errorf("the filter was lost: %q", got)
		}
	}
}

// A backlog WITH a cursor keeps the cursor handoff: --after-cursor, and no
// --since at all.
func TestCursorHandoffIsUnchanged(t *testing.T) {
	argv := fakeRemoteRig(t, -time.Hour)
	seed := `{"__CURSOR":"c9","__REALTIME_TIMESTAMP":"1723000000000001","MESSAGE":"seed"}` + "\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("FAKEJ_DIR"), "backlog.out"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	js := startJournal(context.Background(), runner{host: "root@fake"}, "dummy.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	drainUntilDone(t, js)
	js.stopAndWait()

	got := argv("follow")
	if !strings.Contains(got, "--after-cursor\nc9") {
		t.Errorf("cursor handoff lost: %q", got)
	}
	if strings.Contains(got, "--since") {
		t.Errorf("--since crept into the cursor path: %q", got)
	}
}

// A local stream never issues the clock probe.
func TestLocalStreamIssuesNoClockProbe(t *testing.T) {
	bin := t.TempDir()
	canary := filepath.Join(bin, "canary")
	if err := os.WriteFile(filepath.Join(bin, "date"),
		[]byte("#!/bin/sh\ntouch "+canary+"\necho 1723000000\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "journalctl"),
		[]byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	js := startJournal(context.Background(), runner{}, "dummy.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	drainUntilDone(t, js)
	js.stopAndWait()
	if _, err := os.Stat(canary); !errors.Is(err, os.ErrNotExist) {
		t.Error("a LOCAL stream ran the remote clock probe")
	}
}

// A probe that fails or babbles is a terminal, visible meta failure — the
// exact shape the dead-stream recovery already retries.
func TestBrokenClockProbeFailsVisiblyAndRetryably(t *testing.T) {
	argv := fakeRemoteRig(t, 0)
	_ = argv
	bin := os.Getenv("FAKEJ_DIR")
	if err := os.WriteFile(filepath.Join(bin, "date"),
		[]byte("#!/bin/sh\necho not-an-epoch\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	js := startJournal(context.Background(), runner{host: "root@fake"}, "dummy.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	metas := drainUntilDone(t, js)
	js.stopAndWait()
	joined := strings.Join(metas, "\n")
	if !strings.Contains(joined, "remote clock probe") {
		t.Errorf("the broken probe died silently: %q", joined)
	}
}

// The poll-side offset: a remote an hour behind yields clockOff ≈ +1h, ahead
// yields ≈ −1h; and a babbling clock section is a retryable poll error.
func TestPollClockOffsetBothSigns(t *testing.T) {
	for _, skew := range []time.Duration{-time.Hour, time.Hour} {
		fakeRemoteRig(t, skew)
		c := NewCollector(runner{host: "root@fake"})
		if _, _, err := c.pollBase(context.Background()); err != nil {
			t.Fatalf("skew %v: poll failed: %v", skew, err)
		}
		if d := c.clockOff + skew; d < -5*time.Second || d > 5*time.Second {
			t.Errorf("skew %v: clockOff = %v", skew, c.clockOff)
		}
	}

	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nfor a in \"$@\"; do last=$a; done\nexec sh -c \"$last\"\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "date"), []byte("#!/bin/sh\necho garbage\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho systemd 251\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := NewCollector(runner{host: "root@fake"})
	_, _, err := c.pollBase(context.Background())
	var unsup *UnsupportedError
	if err == nil || errors.As(err, &unsup) {
		t.Fatalf("a babbling clock must be a retryable poll error, got %v", err)
	}
}

// The user-visible halves, both skew signs: the table's UP cell and the
// detail's "up …" stat read a true age, and a stamp normalization lands a
// hair in the future clamps to the literal 0s — on its own row and in its
// own stats line, with both rows REQUIRED to appear so an empty or clipped
// View cannot pass vacuously.
func TestUptimeRendersTrueAgeUnderSkew(t *testing.T) {
	for _, skew := range []time.Duration{-time.Hour, time.Hour} { // remote minus client
		c := NewCollector(runner{host: "root@h"})
		c.clockOff = -skew // clockOff is client minus remote
		now := time.Now()
		units := []Unit{
			// 90 seconds old in the REMOTE's frame.
			{Name: "aged.service", Desc: "d", Active: "active", Sub: "running",
				ActiveSince: now.Add(skew).Add(-90 * time.Second), Slice: "system.slice"},
			// Activated "now" remotely plus rounding: future after the shift.
			{Name: "fresh.service", Desc: "d", Active: "active", Sub: "running",
				ActiveSince: now.Add(skew).Add(2 * time.Second), Slice: "system.slice"},
		}
		c.normalizeClocks(units)

		mm := newModel(runner{host: "root@h"}, "h", time.Second, sortName, false, false, false, "")
		m := &mm
		// Full-width table: the UP column only exists when the table has the
		// room, and the pane strips ".service" from names.
		m.width, m.height, m.ready, m.connected = 160, 30, true, true
		m.showLogs = false
		m.units = units
		m.rebuild()

		// The table cell: the aged unit's true ~90s, the fresh unit's clamped
		// literal 0s — on their own rows, and BOTH rows must be there.
		var agedSeen, freshSeen bool
		for line := range strings.SplitSeq(stripANSI(m.View()), "\n") {
			switch {
			case strings.Contains(line, " aged "):
				agedSeen = true
				if !strings.Contains(line, "1m30s") && !strings.Contains(line, "1m29s") {
					t.Errorf("skew %v: aged table cell lost the true age: %q", skew, line)
				}
			case strings.Contains(line, " fresh "):
				freshSeen = true
				// " 0s" present IS the clamp: a negative age renders "-" in
				// the UP cell and would lack it. Other columns legitimately
				// hold "-" placeholders, so no blanket dash check.
				if !strings.Contains(line, " 0s") {
					t.Errorf("skew %v: fresh table cell is not the literal 0s clamp: %q", skew, line)
				}
			}
		}
		if !agedSeen || !freshSeen {
			t.Fatalf("skew %v: rows missing from the view (aged=%v fresh=%v)", skew, agedSeen, freshSeen)
		}

		// And the detail pane's own stats line, not just the table.
		aged := strings.Join(m.unitStats(units[0]), " ")
		if !strings.Contains(aged, "up 1m30s") && !strings.Contains(aged, "up 1m29s") {
			t.Errorf("skew %v: detail lost the true age: %q", skew, aged)
		}
		fresh := strings.Join(m.unitStats(units[1]), " ")
		if !strings.Contains(fresh, "up 0s") {
			t.Errorf("skew %v: detail is not the literal up 0s: %q", skew, fresh)
		}
	}
}

// Ordering survives normalization end to end: sortUptime over stamps that
// include two distinct near-future values keeps them distinct and ordered,
// through the real rebuild.
func TestUptimeSortSurvivesNearFutureStamps(t *testing.T) {
	// Raw REMOTE-frame stamps through the real normalization: the remote
	// runs half an hour behind, so its freshest activations land in the
	// client's near future after the shift.
	c := NewCollector(runner{host: "root@h"})
	c.clockOff = 30 * time.Minute
	now := time.Now()
	remote := func(age time.Duration) time.Time { return now.Add(-30 * time.Minute).Add(-age) }
	units := []Unit{
		{Name: "old.service", Desc: "d", Active: "active", Sub: "running",
			ActiveSince: remote(time.Hour), Slice: "calm.slice"},
		// Whole seconds apart: the sort key is Unix()-grained by design, so
		// sub-second distinctions legitimately tie — what must NOT happen is
		// the collapse a value-clamp would cause across ANY distance.
		{Name: "newer.service", Desc: "d", Active: "active", Sub: "running",
			ActiveSince: remote(-1 * time.Second), Slice: "busy.slice"},
		{Name: "newest.service", Desc: "d", Active: "active", Sub: "running",
			ActiveSince: remote(-2 * time.Second), Slice: "busy.slice"},
	}
	c.normalizeClocks(units)
	if !units[1].ActiveSince.After(now) || !units[2].ActiveSince.After(units[1].ActiveSince) {
		t.Fatal("normalization did not land the fresh stamps distinctly in the future")
	}

	// Flat: strictly by stamp, newest first, nothing collapsed.
	flat := buildRows(units, sortUptime, false, false, nil)
	var order []string
	for _, r := range flat {
		order = append(order, r.unit.Name)
	}
	if got, want := strings.Join(order, ","), "newest.service,newer.service,old.service"; got != want {
		t.Errorf("flat uptime order = %v, want %v", got, want)
	}

	// Tree: the slice aggregate takes its NEWEST child's stamp, so the busy
	// slice outranks the calm one, and inside it the two future stamps stay
	// distinct and ordered.
	tree := buildRows(units, sortUptime, false, true, map[string]bool{})
	var seq []string
	for _, r := range tree {
		if r.kind == rowSlice {
			seq = append(seq, "slice:"+r.slice)
		} else {
			seq = append(seq, r.unit.Name)
		}
	}
	joined := strings.Join(seq, ",")
	bi, ci := strings.Index(joined, "slice:busy.slice"), strings.Index(joined, "slice:calm.slice")
	if bi < 0 || ci < 0 || bi > ci {
		t.Errorf("the busy slice (newest aggregate) should lead: %v", seq)
	}
	ni, wi := strings.Index(joined, "newest.service"), strings.Index(joined, "newer.service")
	if ni < 0 || wi < 0 || ni > wi {
		t.Errorf("in-slice order lost: %v", seq)
	}
}

// The whole point of the remote boundary, end to end: an entry written in
// the gap between backlog and follow is DELIVERED, under either skew sign,
// filtered or not. The fake follow honours --since like journalctl would —
// so if the client's clock had chosen the boundary, the ahead-skew case
// would drop the entry and this test would fail.
func TestGapEntryIsDeliveredUnderSkew(t *testing.T) {
	for _, skew := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		for _, filt := range []logFilter{{}, {grep: "gap"}} {
			bin := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			remoteNow := time.Now().Add(skew).Unix()
			gapTS := remoteNow // written the same second the backlog ended
			write("ssh", "#!/bin/sh\nfor a in \"$@\"; do last=$a; done\nexec sh -c \"$last\"\n")
			write("date", fmt.Sprintf("#!/bin/sh\necho %d\n", remoteNow))
			write("journalctl", fmt.Sprintf(`#!/bin/sh
has_f=0; since=""
prev=""
for a in "$@"; do
	[ "$a" = -f ] && has_f=1
	[ "$prev" = --since ] && since=$a
	prev=$a
done
if [ $has_f = 0 ]; then exit 0; fi
epoch=${since#@}
if [ -n "$epoch" ] && [ "$epoch" -le %d ]; then
	printf '{"__CURSOR":"g1","__REALTIME_TIMESTAMP":"%d000000","MESSAGE":"the gap entry"}\n'
fi
exit 0
`, gapTS, gapTS))
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			js := startJournal(context.Background(), runner{host: "root@fake"}, "dummy.service", filt, 50, 1)
			defer js.stopAndWait()
			got := false
			deadline := time.After(10 * time.Second)
		wait:
			for !got {
				select {
				case b, ok := <-js.ch:
					if !ok {
						break wait
					}
					for _, l := range b.lines {
						if strings.Contains(l.msg, "the gap entry") {
							got = true
						}
					}
					if b.done {
						break wait
					}
				case <-deadline:
					break wait
				}
			}
			js.stopAndWait()
			if !got {
				t.Fatalf("skew %v filter %+v: the gap entry was lost", skew, filt)
			}
		}
	}
}

// A clock probe that EXITS nonzero (not merely babbles) is retryable on
// both paths, with its stderr; and a local POLL runs no date at all.
func TestNonzeroClockProbeAndLocalPoll(t *testing.T) {
	bin := t.TempDir()
	canary := filepath.Join(bin, "canary")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("ssh", "#!/bin/sh\nfor a in \"$@\"; do last=$a; done\nexec sh -c \"$last\"\n")
	write("date", "#!/bin/sh\ntouch "+canary+"\necho 'clock is on fire' >&2\nexit 7\n")
	write("systemctl", "#!/bin/sh\necho systemd 251\n")
	write("journalctl", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := NewCollector(runner{host: "root@fake"})
	_, _, err := c.pollBase(context.Background())
	var unsup *UnsupportedError
	if err == nil || errors.As(err, &unsup) || !strings.Contains(err.Error(), "on fire") {
		t.Fatalf("nonzero poll clock probe: %v", err)
	}

	js := startJournal(context.Background(), runner{host: "root@fake"}, "d.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	metas := drainUntilDone(t, js)
	js.stopAndWait()
	if joined := strings.Join(metas, "\n"); !strings.Contains(joined, "remote clock probe") ||
		!strings.Contains(joined, "on fire") {
		t.Errorf("nonzero stream probe unnamed: %q", joined)
	}

	// The local poll never touches date.
	os.Remove(canary)
	lc := NewCollector(runner{})
	lc.pollBase(context.Background())
	if _, err := os.Stat(canary); !errors.Is(err, os.ErrNotExist) {
		t.Error("a LOCAL poll ran the remote clock probe")
	}
}

// A poll that dawdles after its clock sample must not fold the dawdling
// into the offset: the boundary is the launch instant, so a two-second
// list-units leaves clockOff within ordinary latency of the true skew.
func TestSlowPollDoesNotInflateTheOffset(t *testing.T) {
	bin := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("ssh", "#!/bin/sh\nfor a in \"$@\"; do last=$a; done\nexec sh -c \"$last\"\n")
	// Precomputed at write time — a fake that CALLS date would find itself
	// on PATH and recurse forever. The poll follows within milliseconds, so
	// the ±1.5s assertion window absorbs the staleness.
	write("date", fmt.Sprintf("#!/bin/sh\necho %d\n", time.Now().Unix()))
	write("systemctl", `#!/bin/sh
case "$1" in
--version) echo "systemd 251" ;;
*) sleep 2; printf 'dummy.service loaded active running A dummy\n' ;;
esac
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := NewCollector(runner{host: "root@fake"})
	if _, _, err := c.pollBase(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.clockOff < -1500*time.Millisecond || c.clockOff > 1500*time.Millisecond {
		t.Errorf("a 2s script inflated clockOff to %v; the launch boundary should bound it near zero", c.clockOff)
	}
}

// The probe is a real child born before journalctl: teardown mid-probe
// reaps it before returning, and neither backlog nor follow ever launches —
// UT-015's contract extends to the newest family member.
func TestClockProbeChildIsOwned(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "probe.pid")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The PID recorded is fake ssh's OWN: the direct child CommandContext
	// owns — not a grandchild behind sh — so kill-0/ESRCH speaks about
	// exactly the process the ownership contract governs. It blocks in
	// place of running the probe.
	write("ssh", `#!/bin/sh
for a in "$@"; do last=$a; done
case "$last" in
*"date +%s"*) echo $$ > `+pidFile+`; exec sleep 300 ;;
esac
exec sh -c "$last"
`)
	write("journalctl", "#!/bin/sh\ntouch "+filepath.Join(bin, "journalctl.ran")+"\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	js := startJournal(context.Background(), runner{host: "root@fake"}, "d.service", logFilter{}, 50, 1)
	defer js.stopAndWait()
	var pid int
	for i := 0; i < 250 && pid == 0; i++ {
		if b, err := os.ReadFile(pidFile); err == nil {
			f := strings.Fields(string(b))
			if len(f) > 0 {
				pid, _ = strconv.Atoi(f[0])
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the probe never started")
	}

	js.stopAndWait()
	assertReaped(t, pid, "the clock probe child")
	if _, err := os.Stat(filepath.Join(bin, "journalctl.ran")); !errors.Is(err, os.ErrNotExist) {
		t.Error("journalctl launched despite teardown mid-probe")
	}
}
