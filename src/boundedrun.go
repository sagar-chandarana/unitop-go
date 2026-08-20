package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// maxCmdOutput bounds the stdout a single poll or action command may return.
// Journal reads have their own budgets (runFinite); this is for the
// systemctl / date / ssh commands, whose real output is a few kilobytes at
// most. Cmd.Output()/CombinedOutput() buffered the whole stream, so a
// hostile or broken monitored host answering a poll or action with an
// unbounded flood could OOM the client — timeouts bound duration, not bytes.
const maxCmdOutput = 1 << 20 // 1 MiB — orders of magnitude over any real poll

// errOversized is returned when a command's stdout passes the cap. It is an
// ordinary retryable error, not an UnsupportedError, so a flooding host is
// re-probed rather than treated as fatally unusable.
var errOversized = errors.New("command produced more output than unitop will read")

// boundedRun executes cmd and returns up to maxCmdOutput bytes of its stdout.
// Everything past the cap is drained (so the child never blocks on a full
// pipe) and the result flagged: a truncated response is a hostile or broken
// remote, not a parseable answer, so it comes back as errOversized. stderr is
// pumped concurrently under its own small byte/line cap for a diagnostic, and
// the child is always waited and reaped before returning. A nonzero exit
// folds the captured stderr into the error, the way wrapExec used to fold
// ExitError.Stderr — which a pipe leaves empty.
func boundedRun(cmd *exec.Cmd) (stdout []byte, stderr string, err error) {
	sout, e := cmd.StdoutPipe()
	if e != nil {
		return nil, "", e
	}
	serr, e := cmd.StderrPipe()
	if e != nil {
		return nil, "", e
	}
	if e := cmd.Start(); e != nil {
		return nil, "", e
	}
	pump := pumpStderr(serr)

	buf := make([]byte, 0, 64<<10)
	chunk := make([]byte, 32<<10)
	truncated := false
	var readErr error
	for {
		n, rerr := sout.Read(chunk)
		if n > 0 {
			switch room := maxCmdOutput - len(buf); {
			case room <= 0:
				truncated = true
			case n > room:
				buf = append(buf, chunk[:room]...)
				truncated = true
			default:
				buf = append(buf, chunk[:n]...)
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				readErr = rerr
			}
			break
		}
	}
	<-pump.done
	werr := cmd.Wait()
	if werr == nil && readErr != nil {
		// The exit status says fine, but the output was not fully readable:
		// a partial response must not parse as a whole one.
		werr = readErr
	}
	stderr = strings.Join(pump.take(), "; ")

	switch {
	case werr != nil:
		if stderr != "" {
			return buf, stderr, fmt.Errorf("%v: %s", werr, stderr)
		}
		return buf, stderr, werr
	case truncated:
		return buf, stderr, errOversized
	default:
		return buf, stderr, nil
	}
}
