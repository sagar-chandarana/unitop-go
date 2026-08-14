package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type logLine struct {
	ts    time.Time
	prio  int
	ident string
	pid   string
	msg   string
	meta  bool // emitted by unitop itself, not by the journal
}

// journalBatch carries whatever the reader goroutine had ready. gen lets the
// model discard lines belonging to a unit the user already navigated away from.
type journalBatch struct {
	gen   int
	lines []logLine
	done  bool
}

type journalStream struct {
	gen    int
	unit   string
	ch     chan journalBatch
	cancel context.CancelFunc
}

func (j *journalStream) stop() {
	if j == nil {
		return
	}
	j.cancel()
}

// startJournal follows one unit's journal. Backlog lines arrive first, then it
// tails. Stderr is folded into the stream as meta lines so permission problems
// are visible in the pane instead of silently producing an empty log.
func startJournal(parent context.Context, r runner, unit string, backlog, gen int) *journalStream {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan journalBatch, 64)
	js := &journalStream{gen: gen, unit: unit, ch: ch, cancel: cancel}

	cmd := r.command(ctx, "journalctl",
		"-u", unit,
		"-n", strconv.Itoa(backlog),
		"-f", "--no-pager",
		"-o", "json",
		"--output-fields=MESSAGE,PRIORITY,SYSLOG_IDENTIFIER,_PID",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		go emitMeta(ctx, ch, gen, "cannot open journalctl stdout: "+err.Error())
		return js
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		go emitMeta(ctx, ch, gen, "cannot run journalctl: "+err.Error())
		return js
	}

	go func() {
		defer close(ch)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		pending := make([]logLine, 0, 64)
		flush := func() bool {
			if len(pending) == 0 {
				return true
			}
			select {
			case ch <- journalBatch{gen: gen, lines: pending}:
				pending = make([]logLine, 0, 64)
				return true
			case <-ctx.Done():
				return false
			}
		}
		for sc.Scan() {
			line, ok := parseJournalJSON(sc.Bytes())
			if !ok {
				continue
			}
			pending = append(pending, line)
			// Flush when the reader is caught up, or when the burst gets big
			// enough that holding it back would look like a stall.
			if len(pending) >= 200 || len(ch) == 0 {
				if !flush() {
					return
				}
			}
		}
		flush()
		_ = cmd.Wait()
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "journal stream ended"
		}
		select {
		case ch <- journalBatch{gen: gen, lines: []logLine{{ts: time.Now(), prio: 4, msg: msg, meta: true}}, done: true}:
		case <-ctx.Done():
		}
	}()
	return js
}

func emitMeta(ctx context.Context, ch chan journalBatch, gen int, msg string) {
	select {
	case ch <- journalBatch{gen: gen, lines: []logLine{{ts: time.Now(), prio: 3, msg: msg, meta: true}}, done: true}:
	case <-ctx.Done():
	}
	close(ch)
}

type rawEntry struct {
	Message  json.RawMessage `json:"MESSAGE"`
	Priority json.RawMessage `json:"PRIORITY"`
	Ident    json.RawMessage `json:"SYSLOG_IDENTIFIER"`
	PID      json.RawMessage `json:"_PID"`
	Realtime json.RawMessage `json:"__REALTIME_TIMESTAMP"`
}

func parseJournalJSON(b []byte) (logLine, bool) {
	var e rawEntry
	if err := json.Unmarshal(b, &e); err != nil {
		// Not JSON (journalctl occasionally prints a plain notice line).
		s := strings.TrimSpace(string(b))
		if s == "" {
			return logLine{}, false
		}
		return logLine{ts: time.Now(), prio: 6, msg: s, meta: true}, true
	}
	l := logLine{prio: 6}
	l.msg = jsonField(e.Message)
	l.ident = jsonField(e.Ident)
	l.pid = jsonField(e.PID)
	if p := jsonField(e.Priority); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			l.prio = n
		}
	}
	if ts := jsonField(e.Realtime); ts != "" {
		if usec, err := strconv.ParseInt(ts, 10, 64); err == nil {
			l.ts = time.UnixMicro(usec)
		}
	}
	if l.ts.IsZero() {
		l.ts = time.Now()
	}
	return l, true
}

// jsonField decodes the three shapes a journal field can take: a string, a
// byte array (non-UTF-8 payloads), or an array of either (repeated fields).
func jsonField(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var nums []int
	if err := json.Unmarshal(raw, &nums); err == nil {
		buf := make([]byte, 0, len(nums))
		for _, n := range nums {
			buf = append(buf, byte(n))
		}
		return string(buf)
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			out = append(out, jsonField(p))
		}
		return strings.Join(out, " ")
	}
	return strings.Trim(string(raw), `"`)
}
