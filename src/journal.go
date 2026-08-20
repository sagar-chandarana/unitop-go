package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rivo/uniseg"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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
func fetchOlder(js *journalStream, r runner, unit, cursor string, f logFilter, n, gen int) tea.Cmd {
	return func() tea.Msg {
		if js == nil || !js.beginPage() {
			// Teardown has begun: launch nothing. The empty batch clears the
			// loading flag if this message is ever delivered at all.
			return olderBatch{gen: gen}
		}
		defer js.pages.Done()
		ctx, cancel := context.WithTimeout(js.ctx, backlogTimeout)
		defer cancel()

		args := append([]string{
			"-u", unit, "--cursor", cursor, "--reverse",
			"-n", strconv.Itoa(n + 1), "--no-pager", "-o", "json", journalFields,
		}, f.args()...)
		res := runFinite(ctx, r, args, n+1)
		if res.err != nil {
			return olderBatch{gen: gen, err: res.errText()}
		}
		if res.records == 0 {
			// Nothing at all: a -g that matched nothing older, or the very
			// start of the journal. Not a failure to read it — and captured
			// warnings still belong on screen.
			return olderBatch{gen: gen, atEnd: true, lines: warningLines(res.warnings)}
		}
		// The anchor is dropped by POSITION: --cursor --reverse returns it
		// first, whatever it looks like. An entry too large to hold comes
		// back as a cursorless placeholder, and matching on cursor equality
		// would mistake it for data — and keep fetching the same page.
		kept := res.newestFirst
		if len(kept) > 0 {
			kept = kept[1:]
		}
		if len(kept) == 0 {
			if res.truncated {
				// Terminal, not retryable: atEnd stops the next top-scroll
				// relaunching this identical page forever.
				return olderBatch{gen: gen, atEnd: true, lines: warningLines(res.warnings),
					err: "the next page is too large to hold: even its newest entry passed the 16 MiB budget"}
			}
			return olderBatch{gen: gen, atEnd: true, lines: warningLines(res.warnings)}
		}
		// Paging anchors on the oldest retained cursor; a page of nothing
		// but placeholders has none, and refetching it would never advance.
		usable := false
		for _, l := range kept {
			if l.cursor != "" {
				usable = true
				break
			}
		}
		if !usable {
			return olderBatch{gen: gen, atEnd: true, lines: warningLines(res.warnings),
				err: "cannot page further back: every entry here is too large to anchor the next page on"}
		}
		lines := chronological(kept)
		// Complete only if the budget held AND the journal ran dry: fewer
		// records after the anchor than asked for.
		atEnd := !res.truncated && res.records-1 < n
		if res.truncated {
			lines = append([]logLine{truncationNotice(lines[0].ts, "page")}, lines...)
		}
		lines = append(lines, warningLines(res.warnings)...)
		return olderBatch{gen: gen, lines: lines, atEnd: atEnd}
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
	// ctx is cancelled when this stream is torn down. Backwards page fetches
	// hang off it so they die with the stream that asked for them.
	ctx    context.Context
	cancel context.CancelFunc
	// done is closed when the stream goroutine has fully wound down: children
	// reaped, batch channel closed. stop() only asks for teardown — exec kills
	// and reaps on other goroutines — so exits use stopAndWait instead.
	done chan struct{}
	// Backwards page fetches run as their own tea.Cmds with their own
	// children, merely parented to ctx; the stream tracks them so teardown
	// can wait for those too. stopping is flipped under mu before pages is
	// waited on, so no page can register once the wait has begun.
	mu       sync.Mutex
	stopping bool
	pages    sync.WaitGroup
}

func (j *journalStream) stop() {
	if j == nil || j.cancel == nil {
		return
	}
	j.cancel()
}

// beginPage registers a page fetch with the stream that owns it. It refuses
// once teardown has begun, so a Cmd scheduled after the quit can never
// launch a child nobody will wait for.
func (j *journalStream) beginPage() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stopping {
		return false
	}
	j.pages.Add(1)
	return true
}

// stopAndWait tears the stream down and returns only once its children are
// reaped and its channel closed — the follow AND any page fetch in flight.
// Exits and stream replacement need this variant: cancel alone only asks,
// and main can reach os.Exit before the other goroutines act on it.
func (j *journalStream) stopAndWait() {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.stopping = true
	j.mu.Unlock()
	j.stop()
	if j.done != nil {
		<-j.done
	}
	j.pages.Wait()
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
	js := &journalStream{gen: gen, unit: unit, filter: f, ch: ch, ctx: ctx, cancel: cancel,
		done: make(chan struct{})}

	go func() {
		defer close(js.done) // deferred first, so it runs last — after close(ch)
		defer close(ch)
		send := func(b journalBatch) bool {
			// The pre-check makes suppression firm for any send STARTED after
			// cancellation. One already inside the select when cancel lands
			// may still deliver — Go picks freely among ready cases — which
			// is why stale batches are generation-checked by the consumer.
			if ctx.Err() != nil {
				return false
			}
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

		// Phase one. Note the boundary first: with nothing in the backlog
		// there is no cursor to resume from, and the follow starts --since
		// here. On a remote, the boundary must be the REMOTE's now — the
		// client's clock re-created the skew bug, excluding every new entry
		// until a behind remote caught up. Floor-second epoch: harmless
		// replay beats loss. A probe that cannot run is a visible, retryable
		// stream failure, and the retirement path recovers it like any other.
		since := time.Now()
		// Phase one is bounded: a remote that accepts the session but never
		// answers must not pin the pane on the spinner forever. Only the
		// follow tail (phase two) is meant to be unbounded — the probe and
		// the backlog are a bootstrap. A timeout turns a silent remote into
		// a terminal, retryable stream death, which the dead-stream recovery
		// handles like any other. fetchOlder bounds its page the same way.
		phaseCtx, phaseCancel := context.WithTimeout(ctx, backlogTimeout)
		defer phaseCancel()
		if r.host != "" {
			out, _, err := boundedRun(r.command(phaseCtx, "date", "+%s"))
			if err != nil {
				meta("remote clock probe: " + err.Error())
				return
			}
			remoteNow, perr := parseEpochLine(string(out))
			if perr != nil {
				meta("remote clock probe: " + perr.Error())
				return
			}
			since = remoteNow
		}
		lines, err := readBacklog(phaseCtx, r, unit, f, backlog)
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

// ---------- bounded finite reads ----------

// maxPageBytes bounds what one finite read RETAINS. The per-entry cap alone
// still allowed count×4MiB in flight: a backlog of hundreds of near-limit
// entries was parsed whole before the first line reached the pane.
const maxPageBytes = 16 << 20

// The stderr transcript'"'"'s lifetime caps — bytes and lines both, because ten
// thousand short lines are as useless on screen as one enormous one.
const (
	maxStderrBytes = 64 << 10
	maxStderrLines = 128
)

// stderrPump drains a command'"'"'s stderr for as long as the process lives,
// keeping a bounded transcript. Past its caps it reads on and discards — the
// child must never block on a full pipe — and notify is poked without ever
// blocking, so a live consumer can surface warnings while the process still
// runs. take hands over what has accumulated.
type stderrPump struct {
	notify chan struct{}
	done   chan struct{}

	mu         sync.Mutex
	kept       []string
	bytes      int  // lifetime SANITIZED bytes, not per-take: controls expand
	count      int  // lifetime line count; take() clears kept
	text       bool // nonblank stderr was seen — retained, clipped, or not
	suppressed bool // a cap was hit or a line was clipped; one marker says so
	marked     bool // ...and has been handed out
}

// readShortLine reads one line keeping at most 4 KiB of it: stderr is
// diagnostics, not payload, and a pathological line must not cost the 4 MiB
// a journal record is allowed.
// clipped says the keep ran out — the caller owes the reader the one
// truncation marker for it — and nonblank reports whether ANY byte of the
// line, kept or discarded, was more than whitespace: the no-matches
// classifier needs the truth about the whole line, not about the prefix
// that happened to fit.
func readShortLine(br *bufio.Reader) (line []byte, clipped, nonblank bool, err error) {
	const keep = 4 << 10
	for {
		chunk, more, rerr := br.ReadLine()
		// The blank test runs over kept and discarded bytes alike, per
		// ReadLine chunk: bytes.TrimSpace knows Unicode whitespace within a
		// chunk, so CR-only or NBSP diagnostics stay blank — with one
		// accepted gap: a multibyte space split across the 8 KiB read
		// boundary reads as text. Erring toward "journalctl spoke" only
		// costs a real error message instead of a silent no-match.
		if !nonblank && len(bytes.TrimSpace(chunk)) > 0 {
			nonblank = true
		}
		if len(chunk) > 0 {
			if len(line) < keep {
				if len(line)+len(chunk) > keep {
					chunk = chunk[:keep-len(line)]
					clipped = true
				}
				line = append(line, chunk...)
			} else {
				clipped = true
			}
		}
		if more && rerr == nil {
			continue
		}
		return line, clipped, nonblank, rerr
	}
}

func pumpStderr(rc io.Reader) *stderrPump {
	p := &stderrPump{notify: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		br := bufio.NewReaderSize(rc, 8*1024)
		for {
			line, clipped, nonblank, err := readShortLine(br)
			if s := strings.TrimSpace(string(line)); s != "" || nonblank {
				clean := sanitizeText(s)
				p.mu.Lock()
				if nonblank {
					p.text = true
				}
				news := false
				if p.suppressed || clipped || p.count >= maxStderrLines || p.bytes+len(clean) > maxStderrBytes {
					news = !p.suppressed // the marker itself, exactly once
					p.suppressed = true
					if clipped && clean != "" && p.count < maxStderrLines && p.bytes+len(clean) <= maxStderrBytes {
						// The clipped prefix is still worth showing, once.
						p.kept = append(p.kept, clean)
						p.bytes += len(clean)
						p.count++
						news = true
					}
				} else if clean != "" {
					p.kept = append(p.kept, clean)
					p.bytes += len(clean)
					p.count++
					news = true
				}
				p.mu.Unlock()
				if news {
					// Only when take() gained something: a flood past the caps
					// must not keep the consumer's select loop hot for lines
					// that will never surface.
					select {
					case p.notify <- struct{}{}:
					default:
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

// sawText reports whether stderr ever carried more than whitespace —
// retained, clipped, or discarded. The no-matches classifier keys on this,
// never on the warnings slice, which stdout notices also feed.
func (p *stderrPump) sawText() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.text
}

// take returns the warnings accumulated since the last call, plus the
// one-time suppression marker once the caps have been passed.
func (p *stderrPump) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.kept
	p.kept = nil
	if p.suppressed && !p.marked {
		p.marked = true
		out = append(out, "further journalctl diagnostics suppressed")
	}
	return out
}

// finiteRead is what one bounded, streamed journalctl run produced.
type finiteRead struct {
	newestFirst []logLine // the contiguous newest records that fit
	records     int       // records in the output, retained or drained
	truncated   bool      // output continued past the page budget
	warnings    []string  // bounded, sanitized stderr and stray notices
	err         error     // the command failed; warnings still stand
}

// errText is the display form of a failure: the exit status plus the first
// captured diagnostic — what ExitError.Stderr used to carry before the pipes
// became ours.
func (fr finiteRead) errText() string {
	msg := fr.err.Error()
	if len(fr.warnings) > 0 {
		msg += ": " + fr.warnings[0]
	}
	return sanitizeText(msg)
}

// runFinite executes one finite journalctl and streams its newest-first
// output: records are retained until `keep` are held or the page budget is
// spent, and everything older is read on and DISCARDED through EOF — peak
// memory is the budget plus one bounded entry, not count×cap, and the child
// is never left blocked on a full pipe. Stderr is pumped concurrently under
// its own caps. The command is reaped before returning.
//
// journalctl'"'"'s one non-failure failure is classified here, from the captured
// result: exit status 1 with nothing on stderr and no output records is a -g
// that matched nothing. It used to be read out of ExitError.Stderr, which a
// custom pipe leaves permanently empty.
func runFinite(ctx context.Context, r runner, args []string, keep int) finiteRead {
	var res finiteRead
	cmd := r.command(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.err = err
		return res
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		res.err = err
		return res
	}
	if err := cmd.Start(); err != nil {
		res.err = err
		return res
	}
	pump := pumpStderr(stderr)

	keptBytes, noticeBytes := 0, 0
	sawNotice := false
	var readErr error
	br := bufio.NewReaderSize(stdout, 64*1024)
	for {
		raw, err := readEntryLine(br)
		if len(raw) > 0 {
			if l, ok := parseJournalJSON(raw); ok {
				switch {
				case l.meta:
					// A stray plain-text notice, not a record. It rides with the
					// warnings — under the same lifetime budget, or a flood of
					// near-cap notices rebuilds the memory problem sideways —
					// and it counts as output: exit 1 with ANY nonblank stdout
					// is a real failure, not a -g that matched nothing.
					sawNotice = true
					if len(res.warnings) < maxStderrLines && noticeBytes+len(l.msg) <= maxStderrBytes {
						res.warnings = append(res.warnings, l.msg)
						noticeBytes += len(l.msg)
					}
				case res.truncated, len(res.newestFirst) >= keep,
					keptBytes+len(raw) > maxPageBytes:
					// Latched: once anything is discarded, everything older is
					// too, or the page would stop being the contiguous newest.
					res.records++
					res.truncated = true
				default:
					res.records++
					res.newestFirst = append(res.newestFirst, l)
					keptBytes += len(raw)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	<-pump.done
	werr := cmd.Wait()
	if werr == nil && readErr != nil {
		// The exit status says fine, but the output was not fully readable.
		werr = readErr
	}
	res.warnings = append(pump.take(), res.warnings...)
	if werr != nil {
		// exit 1 counts as "no matches" only when journalctl said NOTHING:
		// no records, no stdout notice, and no nonblank stderr anywhere in
		// the drained stream — clipped and discarded bytes included.
		var ee *exec.ExitError
		noMatches := errors.As(werr, &ee) && ee.ExitCode() == 1 &&
			!pump.sawText() && res.records == 0 && !sawNotice
		if !noMatches {
			res.err = werr
		}
	}
	return res
}

// chronological flips a newest-first page into prepend/display order.
func chronological(newestFirst []logLine) []logLine {
	lines := make([]logLine, len(newestFirst))
	for i, l := range newestFirst {
		lines[len(newestFirst)-1-i] = l
	}
	return lines
}

// truncationNotice is the honest boundary of a page that hit its budget: the
// lines above it were not fetched, and it carries no cursor, so paging picks
// its anchor from the oldest RETAINED record below.
func truncationNotice(oldest time.Time, what string) logLine {
	if oldest.IsZero() {
		oldest = time.Now()
	}
	return logLine{ts: oldest, prio: 4, meta: true,
		msg: "older entries not held: this " + what + " hit its 16 MiB budget; scroll on to fetch them"}
}

// warningLines turns captured diagnostics into meta lines for the pane.
func warningLines(ws []string) []logLine {
	out := make([]logLine, 0, len(ws))
	for _, w := range ws {
		out = append(out, logLine{ts: time.Now(), prio: 4, msg: w, meta: true})
	}
	return out
}

// readBacklog fetches the newest `n` matching entries, oldest first, under
// the page budget; a truncated read is a partial success with the boundary
// said out loud, and stderr warnings from a SUCCESSFUL read surface as meta
// lines instead of vanishing with the exit status.
func readBacklog(ctx context.Context, r runner, unit string, f logFilter, n int) ([]logLine, error) {
	res := runFinite(ctx, r, backlogArgs(unit, f, n), n)
	if res.err != nil {
		return nil, errors.New(res.errText())
	}
	lines := chronological(res.newestFirst)
	if res.truncated {
		var oldest time.Time
		if len(lines) > 0 {
			oldest = lines[0].ts
		}
		lines = append([]logLine{truncationNotice(oldest, "backlog")}, lines...)
	}
	return append(lines, warningLines(res.warnings)...), nil
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
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fail("cannot open journalctl stderr: " + err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		fail("cannot run journalctl: " + err.Error())
		return
	}
	pump := pumpStderr(stderrPipe)

	// Read on its own goroutine so the sender can be woken by a clock as well
	// as by a line. Gating the flush on the consumer instead — "send when the
	// model has caught up" — stranded the last line of any burst that arrived
	// while it had not: it sat in pending until another line turned up, which
	// on a quiet unit is however long the unit stays quiet.
	lines := make(chan logLine, 512)
	go func() {
		defer close(lines)
		br := bufio.NewReaderSize(stdout, 64*1024)
		for {
			raw, err := readEntryLine(br)
			if len(raw) > 0 {
				if l, ok := parseJournalJSON(raw); ok {
					select {
					case lines <- l:
					case <-ctx.Done():
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// reap joins both pipe readers and waits the child, once. Every way out
	// of this function runs it: CommandContext kills on cancellation, but
	// only Wait reaps, and the pipes must reach EOF before Wait is safe. On
	// cancellation nothing more is sent to the UI, but the draining and the
	// reaping still happen in full.
	reaped := false
	var exitErr error
	reap := func() {
		if reaped {
			return
		}
		reaped = true
		for range lines {
		}
		<-pump.done
		exitErr = cmd.Wait()
	}
	defer reap()

	// warn surfaces captured diagnostics while the process is still running:
	// a permissions warning printed once by a live -f used to sit invisible
	// in a buffer until the stream died, which on a healthy unit is never.
	warn := func() bool {
		if ctx.Err() != nil {
			return false // cancelled: a ready buffered case must not enqueue UI work
		}
		ws := pump.take()
		if len(ws) == 0 {
			return true
		}
		return send(journalBatch{gen: gen, lines: warningLines(ws)})
	}

	pending := make([]logLine, 0, 64)
	flush := func() bool {
		if ctx.Err() != nil {
			return false // as above: cancellation outranks a ready buffer
		}
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
		case <-pump.notify:
			if !warn() {
				return
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

	// stdout has closed, but the process may still be alive and still
	// warning on stderr; keep surfacing until the pump finishes too.
stderrTail:
	for {
		select {
		case <-pump.notify:
			if !warn() {
				return
			}
		case <-pump.done:
			break stderrTail
		case <-ctx.Done():
			return
		}
	}
	reap()
	if ctx.Err() != nil {
		return // cancelled while the pump wound down; send nothing more
	}
	final := pump.take()
	if len(final) == 0 {
		msg := "journal stream ended"
		if exitErr != nil {
			// A nonzero exit with nothing on stderr would otherwise vanish
			// into the generic sign-off.
			msg += ": " + exitErr.Error()
		}
		final = []string{sanitizeText(msg)}
	}
	send(journalBatch{gen: gen, done: true, backlogDone: true,
		lines: warningLines(final)})
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
		return logLine{ts: time.Now(), prio: 6, msg: capMessage(sanitizeMessage(s)), meta: true}, true
	}
	l := logLine{prio: 6}
	// Everything here is whatever the service wrote. See sanitize.go: raw, it
	// can move the cursor and repaint the screen.
	// Every retained field is bounded here, at the parse boundary, so a
	// hostile host cannot smuggle a 4 MiB SYSLOG_IDENTIFIER, _PID or
	// __CURSOR past the per-entry cap and into the channel, the batch and
	// the buffer. A cursor over the bound is dropped to empty — the entry
	// simply cannot anchor a backward page, which the paging code already
	// handles for cursorless entries — rather than being retained AND fed
	// to journalctl as a giant --cursor argument.
	l.msg = capMessage(sanitizeMessage(jsonField(e.Message)))
	l.ident = capField(sanitizeText(jsonField(e.Ident)), maxFieldBytes)
	l.pid = capField(sanitizeText(jsonField(e.PID)), maxFieldBytes)
	if c := jsonField(e.Cursor); len(c) <= maxCursorBytes {
		l.cursor = c
	}
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

// maxEntryBytes is the largest single journal entry we will hold. Past it the
// entry is dropped and replaced with a note, rather than the whole tail dying.
const maxEntryBytes = 4 << 20

// readEntryLine reads one newline-terminated entry, however long it is.
//
// bufio.Scanner was the obvious thing and it was wrong: a line past its buffer
// makes Scan return false with ErrTooLong, and since nothing checked Err() the
// tail simply ended — one oversized entry and the pane stopped updating for as
// long as you left it there, with "journal stream ended" the only clue. A
// Reader cannot be stopped that way: an entry over the cap is discarded to the
// end of its line and reading continues with the next one.
func readEntryLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	over := false
	for {
		chunk, more, err := br.ReadLine()
		if len(chunk) > 0 && !over {
			if len(buf)+len(chunk) > maxEntryBytes {
				over, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if more && err == nil {
			continue // the line goes on past the buffer
		}
		if over {
			return []byte(`{"MESSAGE":"⟨unitop⟩ dropped a journal entry larger than 4 MiB"}`), err
		}
		return buf, err
	}
}

// maxLineBytes is the hard cap on one entry's DISPLAY message, MARKER
// INCLUDED: a screen shows only a few lines of any one entry, so the 4 MiB
// an entry is allowed on the wire (maxEntryBytes) is never readable —
// retaining thousands of them, or re-wrapping one every frame, was the
// memory and CPU blow-up. maxFieldBytes/maxCursorBytes bound the other
// attacker-controlled fields. Vars so a test can shrink them without GiB
// fixtures.
var (
	maxLineBytes   = 8 << 10
	maxFieldBytes  = 256
	maxCursorBytes = 512
)

// elisionReserve is the space capMessage holds back for its marker so the
// returned string never exceeds maxLineBytes. The marker is
// " ⟨unitop: <n> bytes elided⟩" — under 40 bytes even for a 4 MiB count.
const elisionReserve = 48

// capMessage truncates a sanitized message so the result — truncated body
// plus the elision marker — is at most maxLineBytes, cutting on a grapheme
// boundary so a cluster is never split.
func capMessage(msg string) string {
	if len(msg) <= maxLineBytes {
		return msg
	}
	budget, marker := maxLineBytes-elisionReserve, true
	if budget < 0 {
		// The cap is smaller than the marker itself: hard-truncate with no
		// marker so the hard limit still holds.
		budget, marker = maxLineBytes, false
	}
	off, state := 0, -1
	rest := msg
	for len(rest) > 0 {
		cl, r, _, st := uniseg.FirstGraphemeClusterInString(rest, state)
		if off+len(cl) > budget {
			break
		}
		off += len(cl)
		rest, state = r, st
	}
	if !marker {
		return msg[:off]
	}
	return msg[:off] + fmt.Sprintf(" ⟨unitop: %d bytes elided⟩", len(msg)-off)
}

// capField truncates an identifier-like field to at most n bytes on a UTF-8
// boundary, silently — ident and pid are short by nature; an oversized one
// is a hostile host, not something to annotate.
func capField(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xc0 == 0x80 { // back off a UTF-8 continuation byte
		cut--
	}
	return s[:cut]
}

// backlogTimeout bounds journal phase one — the remote clock probe and the
// backlog read — and one backward page. A remote that connects but never
// answers must not pin the pane on the spinner forever; the follow tail is
// the only phase that stays unbounded. A var so a test can shrink it.
var backlogTimeout = 30 * time.Second
