package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The remote poll is one shell one-liner. A marker starting with '#' would
// comment out everything after it, silently returning zero units.
func TestRemoteScriptMarkerIsShellSafe(t *testing.T) {
	if strings.HasPrefix(procMarker, "#") {
		t.Fatalf("procMarker %q starts a shell comment", procMarker)
	}
	if strings.ContainsAny(procMarker, "'\"$`\\;&|<>()") {
		t.Fatalf("procMarker %q contains shell metacharacters", procMarker)
	}
}

// Run the real script through a real shell and check both halves survive. This
// is the only place the remote code path is exercised without an ssh host.
func TestRemoteScriptRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no shell available")
	}
	script := "grep -H '' " + strings.Join(procFiles, " ") + " 2>/dev/null; " +
		"echo '" + procMarker + "'; echo 'fake.service loaded active running Fake'"

	out, err := exec.Command(sh, "-c", script).Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	head, tail, found := strings.Cut(string(out), procMarker+"\n")
	if !found {
		t.Fatalf("marker did not survive the shell:\n%s", out)
	}

	// The tail must still be there: this is exactly what the '#' marker broke.
	names := parseUnitList(tail)
	if len(names) != 1 || names[0] != "fake.service" {
		t.Errorf("unit list lost after the marker: %v", names)
	}

	proc := parseProcDump(head)
	for _, f := range []string{"/proc/stat", "/proc/meminfo", "/proc/loadavg"} {
		if proc[f] == "" {
			t.Errorf("%s came back empty", f)
		}
	}
	if !strings.Contains(proc["/proc/meminfo"], "MemTotal") {
		t.Errorf("meminfo lost its keys: %q", firstLine(proc["/proc/meminfo"]))
	}
	// /proc/net/dev lines contain their own colon; only the first one is the
	// filename separator.
	if dev := proc["/proc/net/dev"]; dev != "" && !strings.Contains(dev, ":") {
		t.Errorf("interface names lost their colon: %q", firstLine(dev))
	}

	// And the whole thing must parse into a usable host summary.
	c := NewCollector(runner{})
	h := c.deriveHost(proc, timeZero())
	if !h.OK || h.MemTotal == 0 || h.NCPU == 0 {
		t.Errorf("host summary not derived: %+v", h)
	}
}

func TestDeriveHostOnEmptyDumpIsNotOK(t *testing.T) {
	c := NewCollector(runner{})
	if h := c.deriveHost(nil, timeZero()); h.OK {
		t.Error("an empty /proc dump should not report OK")
	}
}

func TestSSHOptsIncludeMultiplexing(t *testing.T) {
	r := newRunner("root@example")
	joined := strings.Join(r.sshOpts(), " ")
	for _, want := range []string{"BatchMode=yes", "ControlMaster=auto", "ControlPath=", "ControlPersist="} {
		if !strings.Contains(joined, want) {
			t.Errorf("ssh opts missing %q: %s", want, joined)
		}
	}
	if local := newRunner(""); local.ctlPath != "" {
		t.Error("a local runner should not set up an ssh control socket")
	}
}

func timeZero() time.Time { return time.Unix(1785434347, 0) }
