package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const showFixture = `MainPID=1421
Result=success
NRestarts=2
MemoryCurrent=5308416
MemoryPeak=31526912
CPUUsageNSec=851888654000
TasksCurrent=1
IPIngressBytes=112744226
IPEgressBytes=136447772
IOReadBytes=93208576
IOWriteBytes=1196032
IPAccounting=yes
Id=sshd.service
Description=SSH Daemon
LoadState=loaded
ActiveState=active
SubState=running
StateChangeTimestamp=@1785434347
ActiveEnterTimestamp=@1785434347

MainPID=0
Result=exit-code
NRestarts=[not set]
MemoryCurrent=[not set]
CPUUsageNSec=[not set]
TasksCurrent=[not set]
IPIngressBytes=[not set]
IPEgressBytes=[not set]
IOReadBytes=[not set]
IOWriteBytes=[not set]
IPAccounting=no
ExecMainStatus=1
ExecMainCode=1
ConditionResult=yes
Id=broken.service
Description=Broken thing
LoadState=loaded
ActiveState=failed
SubState=failed
StateChangeTimestamp=@1785430000
ActiveEnterTimestamp=@0

MainPID=0
Result=success
Type=oneshot
NRestarts=[not set]
MemoryCurrent=[not set]
CPUUsageNSec=[not set]
IPAccounting=no
ExecMainStatus=0
ExecMainCode=1
ConditionResult=yes
Id=fetch-certs.service
Description=Fetch certs
LoadState=loaded
ActiveState=inactive
SubState=dead
StateChangeTimestamp=@1785430000
ActiveEnterTimestamp=@0
`

func TestParseShow(t *testing.T) {
	units := parseShow(showFixture)
	if len(units) != 3 {
		t.Fatalf("got %d units, want 3", len(units))
	}

	a := units[0]
	if a.Name != "sshd.service" || a.Desc != "SSH Daemon" {
		t.Errorf("identity: %+v", a)
	}
	if a.NRestarts != 2 || a.MemCurrent != 5308416 || a.CPUNSec != 851888654000 {
		t.Errorf("counters: %+v", a)
	}
	if !a.IPAccount || a.IPIn != 112744226 || a.IPOut != 136447772 {
		t.Errorf("ip accounting: %+v", a)
	}
	if !a.ActiveSince.Equal(time.Unix(1785434347, 0)) {
		t.Errorf("ActiveSince = %v", a.ActiveSince)
	}

	b := units[1]
	if !b.Failed() {
		t.Errorf("broken.service should read as failed: %+v", b)
	}
	if b.MemCurrent != unsetU64 || b.NRestarts != unsetU64 || b.CPUNSec != unsetU64 {
		t.Errorf("[not set] must become the sentinel: %+v", b)
	}
	if b.IPAccount {
		t.Errorf("IPAccounting=no must disable the net columns")
	}
	if !b.ActiveSince.IsZero() {
		t.Errorf("@0 must parse as the zero time, got %v", b.ActiveSince)
	}
	if code, ok := b.ExitCode(); !ok || code != 1 {
		t.Errorf("ExitCode() = %d/%v, want 1/true", code, ok)
	}
}

func TestParseUnitListCanSkipHiddenInactiveUnits(t *testing.T) {
	list := `live.service loaded active running Live service
done.service loaded active exited Finished oneshot
failed.service loaded failed failed Failed service
dead.service loaded inactive dead Inactive service
dangling.service not-found inactive dead Missing service
`

	all := parseUnitList(list)
	if got, want := strings.Join(all, ","), "live.service,done.service,failed.service,dead.service"; got != want {
		t.Fatalf("all units = %q, want %q", got, want)
	}
	visible := parseUnitListFiltered(list, false)
	if got, want := strings.Join(visible, ","), "live.service,done.service,failed.service"; got != want {
		t.Fatalf("visible units = %q, want %q", got, want)
	}
	visible, total := parseUnitListScope(list, false)
	if got, want := strings.Join(visible, ","), "live.service,done.service,failed.service"; got != want || total != 4 {
		t.Fatalf("visible scope = %q with total %d, want %q with total 4", got, total, want)
	}
}

func TestShowAllQueuesAFullPollWhenOneIsAlreadyRunning(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.connected = true
	m.polling = true

	m.handleKey(keyOf("a"))
	if !m.showAll || !m.pollQueued {
		t.Fatalf("showAll=%v pollQueued=%v, want both true", m.showAll, m.pollQueued)
	}
}

func TestShowAllImmediatelyPollsWhenIdle(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.connected = true

	_, cmd := m.handleKey(keyOf("a"))
	if !m.showAll || !m.polling || cmd == nil {
		t.Fatalf("showAll=%v polling=%v cmd nil=%v, want true, true, false", m.showAll, m.polling, cmd == nil)
	}
}

func TestShowAllDoesNotPollWhileStopped(t *testing.T) {
	for _, state := range []string{"paused", "fatal"} {
		t.Run(state, func(t *testing.T) {
			m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
			m.connected = true
			m.paused = state == "paused"
			m.fatal = state == "fatal"

			m.handleKey(keyOf("a"))
			if !m.showAll || m.polling || m.pollQueued {
				t.Fatalf("showAll=%v polling=%v queued=%v, want true, false, false", m.showAll, m.polling, m.pollQueued)
			}
		})
	}
}

func TestPollAddsOnlyTheSelectedSliceToTheDetailBatch(t *testing.T) {
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "show-args")
	script := `#!/bin/sh
case "$1" in
list-units)
	printf 'live.service loaded active running Live service\n'
	;;
show)
	printf '%s\n' "$*" >> "$UNITOP_SHOW_ARGS"
	unitop_sep=0
	for unitop_arg in "$@"; do
		[ "$unitop_arg" = -- ] && unitop_sep=1
		if [ "$unitop_arg" = -.slice ] && [ "$unitop_sep" = 0 ]; then
			exit 64
		fi
		case "$unitop_arg" in
		live.service)
			printf 'Id=live.service\nLoadState=loaded\nActiveState=active\nSubState=running\nMemoryCurrent=1024\nCPUUsageNSec=1000\n\n'
			;;
		system.slice)
			printf 'Id=system.slice\nLoadState=loaded\nActiveState=active\nSubState=active\nTasksCurrent=12\nMemoryCurrent=1048576\nCPUUsageNSec=2000000000\nIPAccounting=yes\nIPIngressBytes=300\nIPEgressBytes=400\nIOReadBytes=500\nIOWriteBytes=600\n\n'
			;;
		-.slice)
			printf 'Id=-.slice\nLoadState=loaded\nActiveState=active\nSubState=active\nTasksCurrent=20\nMemoryCurrent=2097152\nCPUUsageNSec=3000000000\n\n'
			;;
		esac
	done
	;;
esac
`
	writeExe(t, bin, "systemctl", script)
	t.Setenv("UNITOP_SHOW_ARGS", argsFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	plain := NewCollector(runner{})
	plain.version = minSystemd
	if _, _, err := plain.PollVisible(context.Background(), false); err != nil {
		t.Fatalf("plain poll failed: %v", err)
	}
	plainArgs, _ := os.ReadFile(argsFile)
	if strings.Contains(string(plainArgs), ".slice") {
		t.Fatalf("unselected poll queried a slice: %q", plainArgs)
	}

	if err := os.WriteFile(argsFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	selected := NewCollector(runner{})
	selected.version = minSystemd
	units, _, slice, ok, err := selected.PollVisibleSlice(context.Background(), false, "system.slice")
	if err != nil {
		t.Fatalf("selected-slice poll failed: %v", err)
	}
	if len(units) != 1 || units[0].Name != "live.service" {
		t.Fatalf("service rows were contaminated by slice stats: %+v", units)
	}
	if !ok || slice.Name != "system.slice" || slice.Tasks != 12 || slice.MemCurrent != 1<<20 ||
		slice.IPIn != 300 || slice.IPOut != 400 || slice.IORead != 500 || slice.IOWrite != 600 {
		t.Fatalf("selected slice accounting = ok:%v %+v", ok, slice)
	}
	selectedArgs, _ := os.ReadFile(argsFile)
	line := strings.TrimSpace(string(selectedArgs))
	if strings.Count(line, "system.slice") != 1 || strings.Count(line, "live.service") != 1 ||
		strings.Contains(line, "other.slice") || !strings.Contains(line, " -- ") {
		t.Fatalf("selected detail batch = %q", line)
	}

	if err := os.WriteFile(argsFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewCollector(runner{})
	root.version = minSystemd
	_, _, rootStats, rootOK, err := root.PollVisibleSlice(context.Background(), false, "-.slice")
	if err != nil || !rootOK || rootStats.Name != "-.slice" {
		t.Fatalf("dash-prefixed root slice poll = ok:%v stats:%+v err:%v", rootOK, rootStats, err)
	}
	rootArgs, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(rootArgs), " -- live.service -.slice") {
		t.Fatalf("root slice was not protected by --: %q", rootArgs)
	}
}

func TestSelectedSliceJoinsOnlyTheFirstDetailBatch(t *testing.T) {
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "show-args")
	script := `#!/bin/sh
case "$1" in
list-units)
	unitop_i=0
	while [ "$unitop_i" -lt 401 ]; do
		printf 'u%03d.service loaded active running Fixture\n' "$unitop_i"
		unitop_i=$((unitop_i + 1))
	done
	;;
show)
	printf '%s\n' "$*" >> "$UNITOP_SHOW_ARGS"
	;;
esac
`
	writeExe(t, bin, "systemctl", script)
	t.Setenv("UNITOP_SHOW_ARGS", argsFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := NewCollector(runner{})
	c.version = minSystemd
	if _, _, _, _, err := c.PollVisibleSlice(context.Background(), false, "system.slice"); err != nil {
		t.Fatalf("multi-batch poll failed: %v", err)
	}
	raw, _ := os.ReadFile(argsFile)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || strings.Count(string(raw), "system.slice") != 1 ||
		!strings.Contains(lines[0], "system.slice") || strings.Contains(lines[1], "system.slice") {
		t.Fatalf("selected slice did not join only batch zero: %q", raw)
	}
}

func TestRateHandlesResetsAndUnset(t *testing.T) {
	if got := rate(3000, 1000, 2); got != 1000 {
		t.Errorf("rate = %v, want 1000", got)
	}
	// A restart resets the counter; that must read as idle, not as a spike.
	if got := rate(10, 5000, 2); got != 0 {
		t.Errorf("counter reset should give 0, got %v", got)
	}
	if got := rate(unsetU64, 5, 2); got != 0 {
		t.Errorf("unset should give 0, got %v", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"sshd.service":    "sshd.service",
		"--property=Id,X": "--property=Id,X",
		"a b":             "'a b'",
		"it's":            `'it'\''s'`,
		"":                "''",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollectorDerivesRates(t *testing.T) {
	c := NewCollector(runner{})
	now := time.Now()
	c.prev["a.service"] = sample{cpu: 1_000_000_000, ipIn: 1000, ipOut: 2000, when: now.Add(-2 * time.Second)}

	units := []Unit{{Name: "a.service", CPUNSec: 3_000_000_000, IPIn: 3000, IPOut: 2000, IPAccount: true}}
	// Mirror the derivation Poll performs.
	u := &units[0]
	p := c.prev[u.Name]
	dt := now.Sub(p.when).Seconds()
	u.HasRates = true
	u.CPUPct = float64(u.CPUNSec-p.cpu) / 1e9 / dt * 100
	u.NetInRate = rate(u.IPIn, p.ipIn, dt)

	if u.CPUPct < 99 || u.CPUPct > 101 {
		t.Errorf("2s of CPU over 2s wall clock should be ~100%%, got %v", u.CPUPct)
	}
	if u.NetInRate != 1000 {
		t.Errorf("NetInRate = %v, want 1000", u.NetInRate)
	}
}
