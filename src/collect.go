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
}

func newRunner(host string) runner {
	r := runner{host: host}
	if host != "" {
		r.ctlPath = filepath.Join(os.TempDir(), fmt.Sprintf(".unitop-%d.sock", os.Getpid()))
	}
	return r
}

func (r runner) sshOpts() []string {
	opts := []string{
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
// ControlPersist.
func (r runner) close() {
	if r.ctlPath == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "ssh", "-o", "ControlPath="+r.ctlPath, "-O", "exit", r.host).Run()
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
	version  int // systemd major on the target, 0 until the first poll
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

// minSystemd is the oldest systemd unitop works with. v247 (December 2020) is
// the first that accepts `systemctl show --timestamp=unix`; below it the
// timestamps come back locale-formatted and `journalctl --output-fields` does
// not exist either. Rather than carry a second code path for releases that old,
// unitop checks the version up front and says so.
const minSystemd = 247

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

// Markers separating the sections of the one-line remote poll script. Neither
// may start with '#', which would comment out the rest of the line, and
// neither may appear in /proc output.
const (
	verMarker  = "@@unitop-proc@@"
	procMarker = "@@unitop-units@@"
)

// pollBase fetches the systemd version, the /proc files and the unit list.
// Locally that is a few file reads plus an exec; remotely it is a single ssh
// round trip, which matters because the poll interval is short and a distant
// host is not. The version check happens here so an unusable systemd is
// reported before anything else is attempted.
func (c *Collector) pollBase(ctx context.Context) (map[string]string, []string, error) {
	if c.r.host == "" {
		if c.version == 0 {
			out, _ := c.r.command(ctx, "systemctl", "--version").Output()
			c.version = parseSystemdVersion(firstLineOf(string(out)))
		}
		if err := checkVersion(c.version, c.r.target()); err != nil {
			return nil, nil, err
		}
		proc := readProcLocal()
		out, err := c.r.command(ctx, "systemctl", listUnitsArgs...).Output()
		if err != nil {
			return proc, nil, fmt.Errorf("systemctl list-units: %w", wrapExec(err))
		}
		return proc, parseUnitList(string(out)), nil
	}

	script := "systemctl --version 2>/dev/null | head -1; echo '" + verMarker + "'; " +
		"grep -H '' " + strings.Join(procFiles, " ") + " 2>/dev/null; " +
		"echo '" + procMarker + "'; systemctl " + strings.Join(listUnitsArgs, " ")
	out, err := c.r.command(ctx, "sh", "-c", script).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("remote poll: %w", wrapExec(err))
	}
	ver, rest, _ := strings.Cut(string(out), verMarker+"\n")
	proc, units, _ := strings.Cut(rest, procMarker+"\n")
	c.version = parseSystemdVersion(firstLineOf(ver))
	if err := checkVersion(c.version, c.r.target()); err != nil {
		return nil, nil, err
	}
	return parseProcDump(proc), parseUnitList(units), nil
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
