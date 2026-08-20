package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// runner executes systemctl/journalctl either locally or on a remote host
// over ssh. Everything else in the program goes through it, so -H works for
// the log stream exactly as it does for the metric poll.
type runner struct {
	host string
	// ctlPath multiplexes every ssh invocation over one connection. Without
	// it each poll pays a full TCP + handshake per command, which on a distant
	// host costs more than the poll interval.
	ctlPath string
	// ctlDir is the private parent that makes the socket safe to share a
	// machine with: OpenSSH wants ControlPath somewhere others cannot write,
	// and the old predictable /tmp name let anyone pre-bind it — a squatted
	// socket made every real connection hang out its ControlMaster attempt.
	// Owned here, removed by close.
	ctlDir string
}

func newRunner(host string) runner {
	r := runner{host: host}
	if host != "" {
		// MkdirTemp: unique and mode 0700. If it cannot be made, unitop adds
		// no mux options of its own — slower, and the user's ssh config may
		// still share safely — but never a predictable public socket.
		if dir, err := os.MkdirTemp("", "unitop-mux-"); err == nil {
			r.ctlDir = dir
			r.ctlPath = filepath.Join(dir, "mux.sock")
		}
	}
	return r
}

func (r runner) sshOpts() []string {
	opts := []string{
		// -T refuses a remote pty even against a RequestTTY=force user
		// config: a forced tty turns every \n into \r\n and swallows the
		// command's output framing.
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
	}
	if r.ctlPath != "" {
		opts = append(opts,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+r.ctlPath,
			"-o", "ControlPersist=30s")
	}
	return opts
}

func (r runner) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if r.host == "" {
		return exec.CommandContext(ctx, name, args...)
	}
	remote := make([]string, 0, len(args)+1)
	remote = append(remote, shellQuote(name))
	for _, a := range args {
		remote = append(remote, shellQuote(a))
	}
	sshArgs := append(r.sshOpts(), r.host, "--", strings.Join(remote, " "))
	return exec.CommandContext(ctx, "ssh", sshArgs...)
}

// close tears down the multiplexed connection instead of leaving it to
// ControlPersist, then removes the private socket directory it owns — and
// only that. Idempotent on purpose: main can reach it twice, and RemoveAll
// of a directory already gone is a no-op.
func (r runner) close() {
	if r.ctlPath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = exec.CommandContext(ctx, "ssh", "-T", "-o", "ControlPath="+r.ctlPath, "-O", "exit", r.host).Run()
		cancel()
	}
	if r.ctlDir != "" {
		_ = os.RemoveAll(r.ctlDir)
	}
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("@%_-+=:,./", c)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Unit is one service plus the rates derived from the previous poll.
type Unit struct {
	Name        string
	Desc        string
	Slice       string
	Load        string
	Active      string
	Sub         string
	Result      string
	Type        string
	Fragment    string
	FileState   string // UnitFileState: enabled, disabled, static, masked…
	RestartPol  string // Restart=: no, always, on-failure…
	ExecStart   string // the argv of the first ExecStart=
	StatusText  string // what the service reports of itself via sd_notify
	User        string // empty means root
	TriggeredBy string // the socket or timer that activates it
	MemMax      uint64 // MemoryMax, unset when infinity
	TasksLimit  uint64 // TasksMax, unset when infinity
	NRestarts   uint64
	MainPID     uint64
	ExecStatus  uint64 // ExecMainStatus: exit code, or signal number when killed
	ExecCode    uint64 // ExecMainCode: the wait(2) si_code, 0 until a main process exits
	CondResult  string // ConditionResult: "no" when the unit was skipped
	Tasks       uint64
	MemCurrent  uint64
	MemPeak     uint64
	CPUNSec     uint64
	IPIn        uint64
	IPOut       uint64
	IORead      uint64
	IOWrite     uint64
	IPAccount   bool
	ActiveSince time.Time
	StateChange time.Time

	// Derived from the delta against the previous sample.
	CPUPct     float64
	NetInRate  float64
	NetOutRate float64
	IORRate    float64
	IOWRate    float64
	HasRates   bool
}

func (u Unit) Failed() bool { return u.Active == "failed" }

func (u Unit) NetRate() float64 {
	if !u.IPAccount {
		return -1
	}
	return u.NetInRate + u.NetOutRate
}

func (u Unit) IORate() float64 { return u.IORRate + u.IOWRate }

var showProperties = strings.Join([]string{
	"Id", "Description", "Slice", "LoadState", "ActiveState", "SubState", "Result",
	"Type", "FragmentPath", "NRestarts", "MainPID",
	"ExecMainStatus", "ExecMainCode", "ConditionResult",
	"TasksCurrent", "MemoryCurrent", "MemoryPeak", "CPUUsageNSec",
	"IPAccounting", "IPIngressBytes", "IPEgressBytes",
	"IOReadBytes", "IOWriteBytes",
	"ActiveEnterTimestamp", "StateChangeTimestamp",
	"UnitFileState", "Restart", "ExecStart", "StatusText",
	"User", "TriggeredBy", "MemoryMax", "TasksMax",
}, ",")

type sample struct {
	cpu, ipIn, ipOut, ioR, ioW uint64
	when                       time.Time
}

// Collector polls systemd and turns monotonic counters into rates.
type Collector struct {
	r        runner
	prev     map[string]sample
	prevHost *hostSample
	// version is the cached systemd major. Locally it holds only a version
	// that passed the gate — failed or rejected probes leave it 0, so the
	// next poll probes again; remotely every poll re-reports it anyway.
	version int
	// clockOff is client-minus-remote, re-sampled every remote poll. The
	// remote's realtime unit stamps are shifted by it before anything
	// client-side calls time.Since on them: the two clocks owe each other
	// nothing, and skew rendered uptimes negative or inflated.
	clockOff time.Duration
}

func NewCollector(r runner) *Collector {
	return &Collector{r: r, prev: map[string]sample{}}
}

// Poll returns every loaded service unit plus the machine-wide summary. Units
// systemd knows about only as a dangling reference (LoadState=not-found) are
// dropped: they are noise, not services.
func (c *Collector) Poll(ctx context.Context) ([]Unit, HostStats, error) {
	proc, names, err := c.pollBase(ctx)
	host := c.deriveHost(proc, time.Now())
	if err != nil {
		return nil, host, err
	}
	if len(names) == 0 {
		return nil, host, nil
	}

	now := time.Now()
	var units []Unit
	// One batch covers any realistic host; the split only guards against a
	// command line long enough to bother execve.
	const batch = 400
	for i := 0; i < len(names); i += batch {
		end := min(i+batch, len(names))
		args := append([]string{"show", "--timestamp=unix", "--property=" + showProperties}, names[i:end]...)
		out, err := c.r.command(ctx, "systemctl", args...).Output()
		if err != nil {
			return nil, host, fmt.Errorf("systemctl show: %w", wrapExec(err))
		}
		units = append(units, parseShow(string(out))...)
	}
	c.normalizeClocks(units)

	seen := make(map[string]sample, len(units))
	for i := range units {
		u := &units[i]
		cur := sample{cpu: u.CPUNSec, ipIn: u.IPIn, ipOut: u.IPOut, ioR: u.IORead, ioW: u.IOWrite, when: now}
		seen[u.Name] = cur
		p, ok := c.prev[u.Name]
		if !ok {
			continue
		}
		dt := now.Sub(p.when).Seconds()
		if dt <= 0 {
			continue
		}
		u.HasRates = true
		if u.CPUNSec != unsetU64 && p.cpu != unsetU64 && u.CPUNSec >= p.cpu {
			u.CPUPct = float64(u.CPUNSec-p.cpu) / 1e9 / dt * 100
		}
		u.NetInRate = rate(u.IPIn, p.ipIn, dt)
		u.NetOutRate = rate(u.IPOut, p.ipOut, dt)
		u.IORRate = rate(u.IORead, p.ioR, dt)
		u.IOWRate = rate(u.IOWrite, p.ioW, dt)
	}
	c.prev = seen
	return units, host, nil
}

// rate is a counter delta per second, yielding 0 across a restart (when the
// kernel/systemd counter resets to a lower value) rather than a huge spike.
func rate(cur, prev uint64, dt float64) float64 {
	if cur == unsetU64 || prev == unsetU64 || cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

// minSystemd is the oldest systemd unitop works with. v251 (May 2022) is
// the first whose `systemctl show --timestamp=unix` exists at all: v250
// advertises --timestamp but knows only pretty/us/utc/us+utc, and the v251
// release notes introduce the unix choice — so on anything older every
// detailed poll fails on its own arguments. (The floor said 247 for a while,
// which merely looked right: 247–250 passed this gate and then failed every
// poll.) Rather than carry a second code path for releases that old, unitop
// checks the version up front and says so.
const minSystemd = 251

// parseSystemdVersion reads the major version out of the first line of
// `systemctl --version`: "systemd 229" or "systemd 257 (257.7)".
func parseSystemdVersion(s string) int {
	f := strings.Fields(s)
	if len(f) < 2 || f[0] != "systemd" {
		return 0
	}
	num := f[1] // may be "257", "252.4-1" or "250~rc1"
	for i, r := range num {
		if r < '0' || r > '9' {
			num = num[:i]
			break
		}
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0
	}
	return n
}

// UnsupportedError is a failure that retrying cannot fix: the far side is not
// a machine unitop can work with. The UI stops polling on it rather than
// hammering a host once a second forever.
type UnsupportedError struct{ msg string }

func (e *UnsupportedError) Error() string { return e.msg }

// checkVersion turns the reported version into the error the user sees, so a
// too-old or absent systemd fails immediately and legibly instead of surfacing
// as `unrecognized option '--timestamp=unix'` several commands later.
func checkVersion(version int, where string) error {
	if version == 0 {
		return &UnsupportedError{fmt.Sprintf(
			"no systemd on %s: `systemctl --version` did not report one", where)}
	}
	if version < minSystemd {
		return &UnsupportedError{fmt.Sprintf(
			"systemd %d on %s is too old — unitop needs systemd %d or newer",
			version, where, minSystemd)}
	}
	return nil
}

// target names the machine in error messages.
func (r runner) target() string {
	if r.host == "" {
		return "this host"
	}
	return r.host
}

var listUnitsArgs = []string{
	"list-units", "--type=service", "--all", "--plain", "--no-legend", "--no-pager",
}

// Markers separating the sections of the one-line remote poll script. None
// may start with '#', which would comment out the rest of the line, and
// none may appear in /proc output.
const (
	clockMarker = "@@unitop-ver@@" // ends the clock section; the version follows
	verMarker   = "@@unitop-proc@@"
	procMarker  = "@@unitop-units@@"
)

// pollBase fetches the systemd version, the /proc files and the unit list.
// Locally that is a few file reads plus an exec; remotely it is a single ssh
// round trip, which matters because the poll interval is short and a distant
// host is not. The version check happens here so an unusable systemd is
// reported before anything else is attempted.
func (c *Collector) pollBase(ctx context.Context) (map[string]string, []string, error) {
	if c.r.host == "" {
		if c.version == 0 {
			out, err := c.r.command(ctx, "systemctl", "--version").Output()
			if err != nil {
				// A probe that could not run is not a verdict about the
				// host's systemd — it is an ordinary, retryable failure.
				// Discarding it here turned "systemctl missing from PATH"
				// into a fatal "no systemd" that stopped polling for good.
				return nil, nil, fmt.Errorf("systemctl --version: %w", wrapExec(err))
			}
			// Validate before caching: only a version that passes is kept.
			// Caching a rejected one made the explicit-retry gestures
			// no-ops — the fatal verdict was re-issued from the cache even
			// after the host's systemd had been upgraded underneath us.
			v := parseSystemdVersion(firstLineOf(string(out)))
			if err := checkVersion(v, c.r.target()); err != nil {
				return nil, nil, err
			}
			c.version = v
		}
		proc := readProcLocal()
		out, err := c.r.command(ctx, "systemctl", listUnitsArgs...).Output()
		if err != nil {
			return proc, nil, fmt.Errorf("systemctl list-units: %w", wrapExec(err))
		}
		return proc, parseUnitList(string(out)), nil
	}

	// `|| exit` keeps each probe's own failure: piped through the old
	// `| head -1` the version's status vanished, later commands succeeded,
	// and an empty version parsed as a fatal "no systemd" verdict on a host
	// whose systemctl had merely hiccuped. Stderr rides the ssh exit error
	// and the whole thing stays retryable. The clock sample leads: remote
	// realtime stamps (unit uptimes) are normalized against it, since the
	// two machines' clocks owe each other nothing.
	script := "date +%s || exit; echo '" + clockMarker + "'; " +
		"systemctl --version || exit; echo '" + verMarker + "'; " +
		"grep -H '' " + strings.Join(procFiles, " ") + " 2>/dev/null; " +
		"echo '" + procMarker + "'; systemctl " + strings.Join(listUnitsArgs, " ")
	// The offset boundary is the LAUNCH instant, wall-only: `date` runs
	// first on the far side, so client-now-minus-remote-epoch measured
	// AFTER the round trip would fold the whole script and return latency
	// into every age. Anchored at launch, the error is bounded by the
	// one-way outbound latency plus the floor-second — ages overstate by at
	// most that, never by a loaded host's whole poll.
	launched := time.Now().Round(0)
	out, err := c.r.command(ctx, "sh", "-c", script).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("remote poll: %w", wrapExec(err))
	}
	clock, ver, proc, units, perr := parseRemotePoll(string(out))
	if perr != nil {
		return nil, nil, fmt.Errorf("remote poll: %w", perr)
	}
	remoteNow, perr := parseEpochLine(clock)
	if perr != nil {
		return nil, nil, fmt.Errorf("remote poll: %w", perr)
	}
	c.clockOff = launched.Sub(remoteNow) // positive when the client is ahead
	c.version = parseSystemdVersion(firstLineOf(ver))
	if err := checkVersion(c.version, c.r.target()); err != nil {
		return nil, nil, err
	}
	return parseProcDump(proc), parseUnitList(units), nil
}

// parseRemotePoll splits the remote round trip by its three marker LINES,
// strictly. CRLF is normalized first — command-line -T deterministically
// wins over any RequestTTY config (ssh -G proves it), but the remote OUTPUT
// itself may carry CRLF, and defensive framing costs nothing. A delimiter
// counts only when a whole line equals the marker: unit descriptions are
// arbitrary text in the final section, and a description merely CONTAINING
// a marker token must stay data, not framing. Exactly one of each, in
// order; anything else is a retryable malformed-poll error — the old
// lenient Cut ignored its booleans, so torn framing became a
// successful-looking zero-unit poll, or handed the version parser an empty
// string that turned into a fatal "no systemd" verdict.
func parseRemotePoll(out string) (clock, ver, proc, units string, err error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	markers := [3]string{clockMarker, verMarker, procMarker}
	idx := [3]int{-1, -1, -1}
	for i, l := range lines {
		for j, mk := range markers {
			if l != mk {
				continue
			}
			if idx[j] >= 0 {
				return "", "", "", "", errors.New("malformed poll framing: a marker line is duplicated")
			}
			idx[j] = i
		}
	}
	if idx[0] < 0 || idx[1] < 0 || idx[2] < 0 {
		return "", "", "", "", errors.New("malformed poll framing: a marker line is missing")
	}
	if !(idx[0] < idx[1] && idx[1] < idx[2]) {
		return "", "", "", "", errors.New("malformed poll framing: the marker lines are out of order")
	}
	return strings.Join(lines[:idx[0]], "\n"),
		strings.Join(lines[idx[0]+1:idx[1]], "\n"),
		strings.Join(lines[idx[1]+1:idx[2]], "\n"),
		strings.Join(lines[idx[2]+1:], "\n"), nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func parseUnitList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		if f[1] == "not-found" {
			continue
		}
		names = append(names, f[0])
	}
	return names
}

func parseShow(out string) []Unit {
	var units []Unit
	for _, block := range strings.Split(out, "\n\n") {
		u := Unit{
			NRestarts:  unsetU64,
			ExecStatus: unsetU64,
			ExecCode:   unsetU64,
			Tasks:      unsetU64,
			MemMax:     unsetU64,
			TasksLimit: unsetU64,
			MemCurrent: unsetU64,
			MemPeak:    unsetU64,
			CPUNSec:    unsetU64,
			IPIn:       unsetU64,
			IPOut:      unsetU64,
			IORead:     unsetU64,
			IOWrite:    unsetU64,
		}
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			// A description, a status line or an ExecStart is free text the
			// unit's author chose. See sanitize.go. The numeric properties are
			// unaffected by this.
			v = sanitizeText(v)
			switch k {
			case "Id":
				u.Name = v
			case "Description":
				u.Desc = v
			case "Slice":
				u.Slice = v
			case "LoadState":
				u.Load = v
			case "ActiveState":
				u.Active = v
			case "SubState":
				u.Sub = v
			case "Result":
				u.Result = v
			case "Type":
				u.Type = v
			case "FragmentPath":
				u.Fragment = v
			case "ExecMainStatus":
				u.ExecStatus = parseU64(v)
			case "ExecMainCode":
				u.ExecCode = parseU64(v)
			case "ConditionResult":
				u.CondResult = v
			case "NRestarts":
				u.NRestarts = parseU64(v)
			case "MainPID":
				u.MainPID = parseU64(v)
			case "TasksCurrent":
				u.Tasks = parseU64(v)
			case "MemoryCurrent":
				u.MemCurrent = parseU64(v)
			case "MemoryPeak":
				u.MemPeak = parseU64(v)
			case "CPUUsageNSec":
				u.CPUNSec = parseU64(v)
			case "IPAccounting":
				u.IPAccount = v == "yes"
			case "IPIngressBytes":
				u.IPIn = parseU64(v)
			case "IPEgressBytes":
				u.IPOut = parseU64(v)
			case "IOReadBytes":
				u.IORead = parseU64(v)
			case "IOWriteBytes":
				u.IOWrite = parseU64(v)
			case "UnitFileState":
				u.FileState = v
			case "Restart":
				u.RestartPol = v
			case "ExecStart":
				// A unit may declare several; the first is the one that ran.
				if u.ExecStart == "" {
					u.ExecStart = parseExecStart(v)
				}
			case "StatusText":
				u.StatusText = v
			case "User":
				u.User = v
			case "TriggeredBy":
				u.TriggeredBy = v
			case "MemoryMax":
				u.MemMax = parseU64(v) // "infinity" parses as unset, which is right
			case "TasksMax":
				u.TasksLimit = parseU64(v)
			case "ActiveEnterTimestamp":
				u.ActiveSince = parseUnixTS(v)
			case "StateChangeTimestamp":
				u.StateChange = parseUnixTS(v)
			}
		}
		if u.Name == "" {
			continue
		}
		if u.IPIn == unsetU64 && u.IPOut == unsetU64 {
			u.IPAccount = false
		}
		if u.Slice == "" {
			u.Slice = "system.slice"
		}
		units = append(units, u)
	}
	return units
}

// parseExecStart pulls the command out of systemd's structured rendering:
//
//	{ path=/usr/bin/caddy ; argv[]=caddy run --config /etc/caddy ; flags=… }
func parseExecStart(v string) string {
	i := strings.Index(v, "argv[]=")
	if i < 0 {
		return ""
	}
	argv := v[i+len("argv[]="):]
	if j := strings.Index(argv, " ; "); j >= 0 {
		argv = argv[:j]
	}
	return strings.TrimSpace(argv)
}

// parseU64 treats systemd's "[not set]" and the u64 sentinel alike.
func parseU64(v string) uint64 {
	if v == "" || v == "[not set]" {
		return unsetU64
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return unsetU64
	}
	return n
}

// parseUnixTS reads the "@1785434347" form produced by --timestamp=unix.
func parseUnixTS(v string) time.Time {
	v = strings.TrimPrefix(v, "@")
	if v == "" || v == "0" {
		return time.Time{}
	}
	// Newer systemd appends a fractional part on some fields.
	if i := strings.IndexAny(v, ". "); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// wrapExec surfaces the stderr of a failed command, which is the only place
// systemctl/ssh explain themselves.
func wrapExec(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		msg := strings.TrimSpace(string(ee.Stderr))
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		return fmt.Errorf("%v: %s", err, msg)
	}
	return err
}

// normalizeClocks shifts the remote's realtime unit stamps into the client's
// frame — ActiveEnterTimestamp and StateChangeTimestamp are the remote wall
// clock's opinion, and everything client-side computes ages from them. The
// shift is UNIFORM and nothing else: clamping individual values here would
// collapse distinct near-future stamps and scramble sort and tree order.
// A stamp the shift lands slightly in the future (mid-poll activation,
// floor-second rounding) is the age helper's problem, at display time.
// The stamps are wall-only time.Times throughout — parseUnixTS builds them
// from time.Unix, so no Go monotonic reading is ever attached. Local
// collectors share one clock and are left alone.
func (c *Collector) normalizeClocks(units []Unit) {
	if c.r.host == "" || c.clockOff == 0 {
		return
	}
	for i := range units {
		for _, ts := range []*time.Time{&units[i].ActiveSince, &units[i].StateChange} {
			if !ts.IsZero() {
				*ts = ts.Add(c.clockOff)
			}
		}
	}
}

// ageOf is the one place an elapsed duration is derived from a unit stamp.
// A normalized remote stamp can land a hair in the future; the clamp lives
// HERE, on the displayed duration, so the stored stamps keep their order.
func ageOf(ts time.Time) time.Duration {
	if d := time.Since(ts); d > 0 {
		return d
	}
	return 0
}

// parseEpochLine reads a clock probe's reply, no more forgivingly than
// `date +%s` prints it: decimal digits and the command's single trailing
// LF or CRLF — no surrounding whitespace, no extra blank lines, no sign —
// fitting a positive int64. Shared by the poll's clock section and the
// journal boundary probe, so the two cannot drift apart in what they
// accept.
func parseEpochLine(s string) (time.Time, error) {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "\n"):
		s = s[:len(s)-1]
	}
	// A bare carriage return is not a terminator; it falls through to the
	// digit check and is rejected like any other stray byte.
	if s == "" {
		return time.Time{}, errors.New("the clock reply is empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return time.Time{}, errors.New("the clock reply is not a bare decimal epoch")
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, errors.New("the clock reply overflows an epoch")
	}
	if n <= 0 {
		return time.Time{}, errors.New("the clock reply is not a positive epoch")
	}
	return time.Unix(n, 0), nil
}
