package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
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
			// Nothing older matched, which is the start of the journal as far
			// as this filter is concerned — not a failure to read it.
			if noMatches(err) {
				return olderBatch{gen: gen, atEnd: true}
			}
			return olderBatch{gen: gen, err: sanitizeText(wrapExec(err).Error())}
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
	// backlogDone marks the end of the first phase: everything after it is
	// live. It is what lets an empty pane say "nothing matches" instead of
	// "still reading", without guessing at how long a first batch may take.
	backlogDone bool
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

// journalFields is what we read of each entry. __CURSOR comes back regardless,
// which is what makes paging backwards possible.
const journalFields = "--output-fields=MESSAGE,PRIORITY,SYSLOG_IDENTIFIER,_PID"

// startJournal reads one unit's journal in two phases: a backlog that ends, and
// then a tail that begins exactly where it left off.
//
// It used to be one command — `journalctl -n 500 -f <filter>` — which was wrong
// for `-g`. With `-f`, journalctl seeks back N *raw* entries and only then
// applies the pattern, so a search matched nothing older than the last 500
// lines however much of it was in the journal. Measured on a 1200-entry
// journal whose 100 matches were the oldest: the one-command form found none of
// them, and the boundary sat exactly at the 500th entry from the end. `-p` was
// never affected, because PRIORITY is indexed and journalctl can seek by it.
//
// Splitting the phases also means the backlog *ends*, so an empty pane knows
// whether it is still reading or has genuinely found nothing.
//
// Stderr is folded into the stream as meta lines so permission problems are
// visible in the pane instead of silently producing an empty log.
func startJournal(parent context.Context, r runner, unit string, f logFilter, backlog, gen int) *journalStream {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan journalBatch, 64)
	js := &journalStream{gen: gen, unit: unit, filter: f, ch: ch, cancel: cancel}

	go func() {
		defer close(ch)
		send := func(b journalBatch) bool {
			select {
			case ch <- b:
				return true
			case <-ctx.Done():
				return false
			}
		}
		// backlogDone rides on every terminal message too. It is what stops the
		// pane saying "reading the journal…", and the spinner it drives re-arms
		// itself every 120ms for as long as that is true — a stream that failed
		// before reporting one would tick forever.
		meta := func(msg string) {
			send(journalBatch{gen: gen, done: true, backlogDone: true, lines: []logLine{
				{ts: time.Now(), prio: 3, msg: sanitizeText(msg), meta: true}}})
		}

		// Phase one. Note the time first: with nothing in the backlog there is
		// no cursor to resume from, and following from `now` would miss
		// anything written while this command ran.
		since := time.Now()
		lines, err := readBacklog(ctx, r, unit, f, backlog)
		if err != nil {
			meta(err.Error())
			return
		}
		last := ""
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i].cursor != "" {
				last = lines[i].cursor
				break
			}
		}
		if !send(journalBatch{gen: gen, lines: lines, backlogDone: true}) {
			return
		}

		// Phase two.
		followJournal(ctx, r, unit, f, last, since, gen, send)
	}()
	return js
}

// backlogArgs asks for the newest n matching entries. --reverse is explicit
// because `-n` with `-g` returns newest-first on its own while `-p` returns
// oldest-first; saying which we want makes the order independent of the filter.
func backlogArgs(unit string, f logFilter, n int) []string {
	return append([]string{
		"-u", unit, "-n", strconv.Itoa(n), "--reverse",
		"--no-pager", "-o", "json", journalFields,
	}, f.args()...)
}

// followArgs tails from `after`, or from `since` when the backlog was empty and
// there is no cursor to resume from. Either way it replays the gap: the backlog
// is a separate command, and whatever the unit wrote while it ran belongs on
// screen.
//
// Deliberately no `-n 0`. It reads as "start with nothing", but journalctl
// takes it as "replay nothing", and it silently defeats both --after-cursor and
// --since — measured: `-f --after-cursor C` replays the entry after C, and
// `-f -n 0 --after-cursor C` replays nothing at all. `--since` and
// `--after-cursor` already bound the replay; the `-n 10` that bare `-f` would
// otherwise default to does not apply once either is given.
func followArgs(unit string, f logFilter, after string, since time.Time) []string {
	args := []string{"-u", unit, "-f", "--no-pager", "-o", "json", journalFields}
	if after != "" {
		args = append(args, "--after-cursor", after)
	} else {
		args = append(args, "--since", "@"+strconv.FormatInt(since.Unix(), 10))
	}
	return append(args, f.args()...)
}

// noMatches reports the one failure that is not one: journalctl exits 1 when a
// `-g` pattern matches nothing, and says nothing on stderr about it. Reported
// as an error it becomes a red line in the pane claiming the journal could not
// be read, which is both alarming and wrong — the search simply found nothing.
// A real failure, permissions being the usual one, exits with something to say.
func noMatches(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1 && len(bytes.TrimSpace(ee.Stderr)) == 0
}

// readBacklog fetches the newest `n` matching entries, oldest first.
func readBacklog(ctx context.Context, r runner, unit string, f logFilter, n int) ([]logLine, error) {
	out, err := r.command(ctx, "journalctl", backlogArgs(unit, f, n)...).Output()
	if err != nil {
		if noMatches(err) {
			return nil, nil
		}
		return nil, wrapExec(err)
	}

	var newestFirst []logLine
	for _, raw := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if l, ok := parseJournalJSON([]byte(raw)); ok {
			newestFirst = append(newestFirst, l)
		}
	}
	lines := make([]logLine, len(newestFirst))
	for i, l := range newestFirst {
		lines[len(newestFirst)-1-i] = l
	}
	return lines, nil
}

// followJournal tails the unit, picking up where the backlog left off.
func followJournal(ctx context.Context, r runner, unit string, f logFilter,
	after string, since time.Time, gen int, send func(journalBatch) bool) {

	args := followArgs(unit, f, after, since)
	fail := func(msg string) {
		send(journalBatch{gen: gen, done: true, backlogDone: true, lines: []logLine{
			{ts: time.Now(), prio: 3, msg: sanitizeText(msg), meta: true}}})
	}

	cmd := r.command(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail("cannot open journalctl stdout: " + err.Error())
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		fail("cannot run journalctl: " + err.Error())
		return
	}
	// CommandContext kills the child when ctx is cancelled, but it does not reap
	// it; only Wait does. A selection or filter change cancels this stream, so
	// always wait before returning instead of accumulating dead journalctl (or
	// ssh) children while the user navigates between units.
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Wait()
		}
	}()

	// Read on its own goroutine so the sender can be woken by a clock as well
	// as by a line. Gating the flush on the consumer instead — "send when the
	// model has caught up" — stranded the last line of any burst that arrived
	// while it had not: it sat in pending until another line turned up, which
	// on a quiet unit is however long the unit stays quiet.
	lines := make(chan logLine, 512)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			l, ok := parseJournalJSON(sc.Bytes())
			if !ok {
				continue
			}
			select {
			case lines <- l:
			case <-ctx.Done():
				return
			}
		}
	}()

	pending := make([]logLine, 0, 64)
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		if !send(journalBatch{gen: gen, lines: pending}) {
			return false
		}
		pending = make([]logLine, 0, 64)
		return true
	}

	// Coalesce bursts, but never hold a line longer than this: one message per
	// line would be one re-render per line for a unit that logs in floods.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
reading:
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				break reading
			}
			pending = append(pending, l)
			if len(pending) >= 200 {
				if !flush() {
					return
				}
			}
		case <-tick.C:
			if !flush() {
				return
			}
		case <-ctx.Done():
			return
		}
	}
	flush()
	_ = cmd.Wait()
	waited = true
	msg := sanitizeText(strings.TrimSpace(stderr.String()))
	if msg == "" {
		msg = "journal stream ended"
	}
	send(journalBatch{gen: gen, done: true, backlogDone: true, lines: []logLine{
		{ts: time.Now(), prio: 4, msg: msg, meta: true}}})
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
		return logLine{ts: time.Now(), prio: 6, msg: sanitizeMessage(s), meta: true}, true
	}
	l := logLine{prio: 6}
	// Everything here is whatever the service wrote. See sanitize.go: raw, it
	// can move the cursor and repaint the screen.
	l.msg = sanitizeMessage(jsonField(e.Message))
	l.ident = sanitizeText(jsonField(e.Ident))
	l.pid = sanitizeText(jsonField(e.PID))
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
