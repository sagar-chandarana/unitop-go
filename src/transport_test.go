package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every remote runner owns a private, unique, mode-0700 parent for its mux
// socket: a predictable public /tmp path let anyone pre-bind the socket, and
// a squatted ControlPath makes every real connection hang out its
// ControlMaster attempt (measured: rc=124 at three seconds).
func TestRunnerMuxSocketLivesInAPrivateParent(t *testing.T) {
	r1 := newRunner("root@h")
	defer r1.close()
	r2 := newRunner("root@h")
	defer r2.close()

	if r1.ctlDir == "" || r1.ctlPath == "" {
		t.Fatal("no private mux directory was created")
	}
	if filepath.Dir(r1.ctlPath) != r1.ctlDir {
		t.Errorf("socket %q does not live in its owned dir %q", r1.ctlPath, r1.ctlDir)
	}
	if r1.ctlDir == r2.ctlDir {
		t.Error("two runners share one mux directory")
	}
	fi, err := os.Stat(r1.ctlDir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mux dir mode = %o, want 700", fi.Mode().Perm())
	}
}

// close removes only the owned directory, and twice is as safe as once —
// main reaches it on both the error and the normal path.
func TestRunnerCloseIsIdempotent(t *testing.T) {
	r := newRunner("root@h")
	dir := r.ctlDir
	r.close()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("close did not remove the owned dir: %v", err)
	}
	r.close() // and again, without incident
}

// A local runner needs no socket, no directory, no cleanup.
func TestLocalRunnerCreatesNothing(t *testing.T) {
	r := newRunner("")
	if r.ctlDir != "" || r.ctlPath != "" {
		t.Errorf("local runner created transport state: dir=%q path=%q", r.ctlDir, r.ctlPath)
	}
	r.close()
}

// The ssh argv: -T (a forced-tty user config would CRLF the framing),
// the mux options pointing into the owned dir, and the valid
// host / -- / single-shell-command tail with quoting intact.
func TestRemoteArgvShape(t *testing.T) {
	r := newRunner("root@h")
	defer r.close()
	cmd := r.command(context.Background(), "journalctl", "-u", "a b.service", "-g", "can't")
	args := cmd.Args
	if args[0] != "ssh" {
		t.Fatalf("argv[0] = %q", args[0])
	}
	flat := strings.Join(args, " ")
	if !strings.Contains(flat, " -T ") && args[1] != "-T" {
		t.Errorf("-T missing: %q", flat)
	}
	if !strings.Contains(flat, "ControlPath="+r.ctlPath) {
		t.Errorf("mux path not used: %q", flat)
	}
	if args[len(args)-3] != "root@h" || args[len(args)-2] != "--" {
		t.Errorf("tail is not host -- command: %v", args[len(args)-3:])
	}
	if want := `journalctl -u 'a b.service' -g 'can'\''t'`; args[len(args)-1] != want {
		t.Errorf("remote command = %q, want %q", args[len(args)-1], want)
	}
}

// The strict framing parser: exactly one of each marker, in order, CRLF
// tolerated. Anything else is a retryable malformed-poll error — the old
// lenient Cut turned these into successful zero-unit polls or fatal
// version-0 verdicts.
func TestParseRemotePollFraming(t *testing.T) {
	good := "systemd 251 (251.4)\n+FLAGS\n" + verMarker + "\n/proc/stat:cpu  0 0 0 0 0 0 0 0 0 0\n" +
		procMarker + "\nfake.service loaded active running Fake\n"

	ver, proc, units, err := parseRemotePoll(good)
	if err != nil {
		t.Fatalf("LF framing rejected: %v", err)
	}
	if firstLineOf(ver) != "systemd 251 (251.4)" || !strings.Contains(proc, "/proc/stat") ||
		!strings.Contains(units, "fake.service") {
		t.Errorf("sections misassigned: ver=%q proc=%q units=%q", ver, proc, units)
	}

	crlf := strings.ReplaceAll(good, "\n", "\r\n")
	if _, _, u, err := parseRemotePoll(crlf); err != nil || !strings.Contains(u, "fake.service") {
		t.Errorf("CRLF framing not normalized: %v", err)
	}

	bad := map[string]string{
		"missing version marker": strings.Replace(good, verMarker+"\n", "", 1),
		"missing proc marker":    strings.Replace(good, procMarker+"\n", "", 1),
		"duplicated marker":      good + verMarker + "\n",
		"reversed markers":       procMarker + "\nproc\n" + verMarker + "\nsystemd 251\n",
	}
	for name, in := range bad {
		if _, _, _, err := parseRemotePoll(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The version command's own failure survives the round trip as a retryable
// error carrying its stderr — even though every later command would have
// succeeded. It used to vanish into `| head -1`, parse as version 0, and
// stop polling for good as a fatal "no systemd" verdict.
func TestRemoteVersionFailureIsRetryable(t *testing.T) {
	bin := t.TempDir()
	// A fake ssh: run the remote command string locally. The last argv
	// element is the single joined shell command, exactly as sshd would
	// hand it to the login shell.
	sshScript := `#!/bin/sh
for a in "$@"; do last=$a; done
exec sh -c "$last"
`
	ctlScript := `#!/bin/sh
case "$1" in
--version) echo "systemctl is having a bad day" >&2; exit 5 ;;
esac
printf 'fake.service loaded active running Fake\n'
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(sshScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(ctlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := newRunner("root@fake")
	defer r.close()
	c := NewCollector(r)
	_, _, err := c.pollBase(context.Background())
	if err == nil {
		t.Fatal("a failed remote version probe polled successfully")
	}
	var unsup *UnsupportedError
	if errors.As(err, &unsup) {
		t.Fatalf("a failed probe must be retryable, got the fatal verdict: %v", err)
	}
	if !strings.Contains(err.Error(), "bad day") {
		t.Errorf("the probe's stderr was lost: %v", err)
	}

	// And with a healthy fake, the same runner polls: the strict framing
	// accepts what the script actually emits.
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(`#!/bin/sh
case "$1" in
--version) echo "systemd 251 (251.4)" ;;
*) printf 'fake.service loaded active running Fake\n' ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	proc, units, err := c.pollBase(context.Background())
	if err != nil {
		t.Fatalf("healthy remote poll failed: %v", err)
	}
	if len(units) != 1 || len(proc) == 0 {
		t.Errorf("poll parsed units=%d procfiles=%d", len(units), len(proc))
	}
}

// testRunner is newRunner plus the cleanup every remote runner now owes: each
// one holds a private mux directory on disk until close.
func testRunner(t *testing.T, host string) runner {
	t.Helper()
	r := newRunner(host)
	t.Cleanup(r.close)
	return r
}

// If the private parent cannot be made at all, unitop adds no mux options
// of its own — never a predictable public path; the user's ssh config may
// still share safely. Proven, not commented: no dir, no socket, and no
// ControlMaster/ControlPath in the argv.
func TestUnusableTempDirOmitsUnitopMuxOptions(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing", "deeper"))
	r := newRunner("root@h")
	defer r.close()
	if r.ctlDir != "" || r.ctlPath != "" {
		t.Fatalf("a mux path appeared under an unusable TMPDIR: dir=%q path=%q", r.ctlDir, r.ctlPath)
	}
	opts := strings.Join(r.sshOpts(), " ")
	if strings.Contains(opts, "ControlMaster") || strings.Contains(opts, "ControlPath") {
		t.Errorf("multiplexing options present without a socket: %q", opts)
	}
}

// A unit description is arbitrary text, and it may CONTAIN a marker token —
// as a whole word or a line suffix. Data, not framing: the poll parses. Only
// a whole line equal to the marker delimits, and a duplicated one still
// fails.
func TestMarkerTokensInsideDataStayData(t *testing.T) {
	good := "systemd 251 (251.4)\n" + verMarker + "\n/proc/stat:cpu 0\n" + procMarker + "\n" +
		"evil.service loaded active running says " + verMarker + " often\n" +
		"worse.service loaded active running suffixed " + procMarker + "\n"
	_, _, units, err := parseRemotePoll(good)
	if err != nil {
		t.Fatalf("marker tokens inside descriptions broke the framing: %v", err)
	}
	if !strings.Contains(units, "evil.service") || !strings.Contains(units, "worse.service") {
		t.Errorf("data lines lost: %q", units)
	}

	dup := good + verMarker + "\n"
	if _, _, _, err := parseRemotePoll(dup); err == nil {
		t.Error("a duplicated standalone marker line was accepted")
	}
}
