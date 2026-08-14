package main

import "fmt"

// systemd reports the main process's wait(2) si_code in ExecMainCode. Zero
// means the unit has no main process to report on — it never ran.
const (
	execNotRun = 0
	execExited = 1 // CLD_EXITED
	execKilled = 2 // CLD_KILLED
	execDumped = 3 // CLD_DUMPED
)

// Ran reports whether the unit's main process actually executed. This is the
// only thing separating a oneshot that has finished from one that never
// started: systemd calls both of them inactive/dead.
func (u Unit) Ran() bool {
	return u.ExecCode != unsetU64 && u.ExecCode != execNotRun
}

// Skipped reports a unit systemd deliberately did not run because one of its
// conditions was not met.
func (u Unit) Skipped() bool {
	return u.Result == "exec-condition" || u.CondResult == "no"
}

// StateLabel is what the STATE column says. SubState alone is not enough:
// every oneshot that has run to completion sits in inactive/dead, which reads
// as "something is wrong" when it means "it finished". This distinguishes
// finished from never-started from skipped, and names the exit code when there
// is one.
func (u Unit) StateLabel() string {
	switch u.Active {
	case "failed":
		return u.failureLabel()

	case "inactive":
		if u.Result != "" && u.Result != "success" {
			return u.failureLabel()
		}
		switch {
		case u.Skipped():
			return "skipped"
		case u.ExecCode == execExited && u.ExecStatus == 0:
			return "exited"
		case u.ExecCode == execExited:
			// Ran and exited non-zero, but SuccessExitStatus= covers it.
			return fmt.Sprintf("exit %d", u.ExecStatus)
		case u.Ran():
			// Signalled, which for a success result means it was stopped.
			return "stopped"
		}
		return "dead"
	}
	return u.Sub
}

// failureLabel names why a unit is not running, preferring the exit code or
// signal over the generic "failed".
func (u Unit) failureLabel() string {
	switch u.Result {
	case "exit-code":
		if u.ExecCode == execExited && u.ExecStatus != unsetU64 {
			return fmt.Sprintf("exit %d", u.ExecStatus)
		}
	case "signal", "core-dump":
		if u.ExecStatus != unsetU64 && u.ExecStatus > 0 {
			return "sig " + signalName(u.ExecStatus)
		}
		return "signal"
	case "exec-condition":
		return "skipped"
	case "timeout":
		return "timeout"
	case "watchdog":
		return "watchdog"
	case "oom-kill":
		return "oom-kill"
	case "start-limit-hit":
		return "limit"
	case "resources":
		return "resources"
	case "protocol":
		return "protocol"
	}
	return "failed"
}

var signalNames = map[uint64]string{
	1: "HUP", 2: "INT", 3: "QUIT", 4: "ILL", 5: "TRAP", 6: "ABRT", 7: "BUS",
	8: "FPE", 9: "KILL", 10: "USR1", 11: "SEGV", 12: "USR2", 13: "PIPE",
	14: "ALRM", 15: "TERM", 17: "CHLD", 18: "CONT", 19: "STOP", 20: "TSTP",
	24: "XCPU", 25: "XFSZ", 31: "SYS",
}

func signalName(n uint64) string {
	if s, ok := signalNames[n]; ok {
		return s
	}
	return fmt.Sprint(n)
}

// ExitCode is the main process's exit status, and whether there is one worth
// showing.
func (u Unit) ExitCode() (uint64, bool) {
	if u.ExecCode != execExited || u.ExecStatus == unsetU64 {
		return 0, false
	}
	return u.ExecStatus, true
}
