package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type logLine struct {
	ts     time.Time
	prio   int
	ident  string
	pid    string
	msg    string
	cursor string // journald's opaque position, used to page backwards
	meta   bool   // emitted by unitop itself, not by the journal
}

// logFilter narrows the journal at the source rather than hiding lines already
// fetched, so a search covers the whole log instead of the few hundred entries
// held in memory. journalctl applies it to the follow stream and to backwards
// paging alike.
type logFilter struct {
	grep string // -g: a PCRE on MESSAGE, case-insensitive while all-lowercase
	prio int    // -p: 0 shows everything, else the least severe level shown
}

func (f logFilter) args() []string {
	var a []string
	if f.grep != "" {
		a = append(a, "-g", f.grep)
	}
	if f.prio > 0 {
		a = append(a, "-p", strconv.Itoa(f.prio))
	}
	return a
}

func (f logFilter) empty() bool { return f.grep == "" && f.prio == 0 }

// label describes the filter for the pane header. It says what is being left
// out rather than naming the flags: a filtered log otherwise looks like a quiet
// one, and "-g nginx -p 4" does not explain itself to someone who did not type
// it.
func (f logFilter) label() string {
	var parts []string
	if f.grep != "" {
		parts = append(parts, "matching "+strconv.Quote(f.grep))
	}
	switch f.prio {
	case 4:
		parts = append(parts, "warning and above")
	case 3:
		parts = append(parts, "error and above")
	}
	return strings.Join(parts, ", ")
}

// nextPriority cycles everything → warning and above → error and above.
func nextPriority(p int) int {
	switch p {
	case 0:
		return 4
	case 4:
		return 3
	default:
		return 0
	}
}

// olderBatch is the answer to "what came before the oldest line we hold".
type olderBatch struct {
	gen   int
	lines []logLine // chronological order, ready to prepend
	atEnd bool      // the journal has nothing older
	err   string
}

// fetchOlder reads the page of entries immediately before cursor.
//
// The primitive is `--cursor=C --reverse`, which returns C first and then
// progressively older entries. `--after-cursor` is not the opposite of that: it
// moves forward in time whatever --reverse says. So we ask for one extra entry,
// drop the anchor, and flip the rest back into chronological order. Getting
// only the anchor back means we are at the start of the journal.
func fetchOlder(parent context.Context, r runner, unit, cursor string, f logFilter, n, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()

		args := append([]string{
			"-u", unit, "--cursor", cursor, "--reverse",
			"-n", strconv.Itoa(n + 1), "--no-pager", "-o", "json",
		}, f.args()...)
		out, err := r.command(ctx, "journalctl", args...).Output()
		if err != nil {
			return olderBatch{gen: gen, err: wrapExec(err).Error()}
		}

		var newestFirst []logLine
		for _, raw := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			if l, ok := parseJournalJSON([]byte(raw)); ok && !l.meta {
				newestFirst = append(newestFirst, l)
			}
		}
		// The first entry is the anchor we already have.
		if len(newestFirst) > 0 && newestFirst[0].cursor == cursor {
			newestFirst = newestFirst[1:]
		}
		if len(newestFirst) == 0 {
			return olderBatch{gen: gen, atEnd: true}
		}
		lines := make([]logLine, len(newestFirst))
		for i, l := range newestFirst {
			lines[len(newestFirst)-1-i] = l
		}
		return olderBatch{gen: gen, lines: lines, atEnd: len(lines) < n}
	}
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
	filter logFilter // what this stream was started with
	ch     chan journalBatch
	cancel context.CancelFunc
}

func (j *journalStream) stop() {
	if j == nil || j.cancel == nil {
		return
	}
	j.cancel()
}

// startJournal follows one unit's journal. Backlog lines arrive first, then it
// tails. Stderr is folded into the stream as meta lines so permission problems
// are visible in the pane instead of silently producing an empty log.
func startJournal(parent context.Context, r runner, unit string, f logFilter, backlog, gen int) *journalStream {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan journalBatch, 64)
	js := &journalStream{gen: gen, unit: unit, filter: f, ch: ch, cancel: cancel}

	args := append([]string{
		"-u", unit,
		"-n", strconv.Itoa(backlog),
		"-f", "--no-pager",
		"-o", "json",
		"--output-fields=MESSAGE,PRIORITY,SYSLOG_IDENTIFIER,_PID",
	}, f.args()...)
	cmd := r.command(ctx, "journalctl", args...)
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
	// Always present, even with --output-fields; it is what lets us page back.
	Cursor json.RawMessage `json:"__CURSOR"`
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
	l.cursor = jsonField(e.Cursor)
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
