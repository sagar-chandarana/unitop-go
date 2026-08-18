package main

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	// maxLogLines bounds the log buffer. Live entries trim the oldest to stay
	// under it; paging backwards stops there rather than trimming, because the
	// lines it would drop are the ones being read. 20k entries is far more
	// history than anyone scrolls by hand — past that, use journalctl.
	maxLogLines    = 20000
	journalBacklog = 500
)

type focusArea int

const (
	focusList focusArea = iota
	focusLogs
)

// Most keys belong to a pane rather than to the program, and a key that
// belongs to the other pane does nothing and is not offered in the footer.
// Pressing s while reading the log used to resort the table silently behind
// it — the screen now says what will work, and only that works.
//
// `/` is in neither set: it filters whichever pane has focus. Nor is `x` or
// `enter`, which act on the selected unit from either side.
var (
	tableKeys = map[string]bool{
		"s": true, "S": true, // sort column
		"r": true, // reverse
		"t": true, // tree
		"a": true, // include inactive
	}
	logKeys = map[string]bool{
		"f": true, // follow — which is the live end, so it is also the bottom
		"F": true, // the other end
		"e": true, // priority
		"w": true, // wrap
	}
)

// keyApplies reports whether a key does anything from where the focus is.
func (m model) keyApplies(k string) bool {
	switch {
	case tableKeys[k]:
		return !m.fullView && m.focus == focusList
	case logKeys[k]:
		return m.logPaneVisible() && m.focus == focusLogs
	case k == "tab":
		// Nothing to switch to: the full view has no table, and a hidden log
		// pane is not somewhere focus can go.
		return !m.fullView && m.logPaneVisible()
	case k == "l":
		// The full view is nothing but the log, so hiding it would leave an
		// empty screen.
		return !m.fullView
	}
	return true
}

type tickMsg time.Time

type spinnerTickMsg struct{}

type unitsMsg struct {
	units []Unit
	host  HostStats
	err   error
}

type model struct {
	r         runner
	col       *Collector
	hostLabel string
	readOnly  bool
	sudo      bool

	units    []Unit
	host     HostStats
	rows     []row
	err      string
	lastPoll time.Time
	polling  bool
	paused   bool
	interval time.Duration

	// connected goes true on the first poll that comes back without an error.
	// Until then the normal UI would be an empty table with the failure buried
	// in the footer, so the startup screen is shown instead.
	connected bool
	attempts  int
	spinner   int
	// fatal marks a failure retrying cannot fix — the target is not a machine
	// unitop can work with. Polling stops rather than repeating forever.
	fatal bool

	cursor   int
	topRow   int
	selected string

	filter      string
	filterInput bool
	// filterWas is what the editor is amending, so esc can put it back rather
	// than throw it away.
	filterWas string
	showAll   bool
	tree      bool
	collapsed map[string]bool

	sortBy  sortKey
	reverse bool

	logs      []logLine
	logGen    int
	journal   *journalStream
	logScroll int // wrapped display lines scrolled up from the bottom
	logFollow bool
	// Paging backwards through the journal: unitop starts with the last
	// journalBacklog entries and fetches earlier pages when you scroll to the
	// top, rather than pretending that is where the log begins.
	logEpoch     int        // bumped on every change to logs, to invalidate totals
	totals       *logTotals // memoised wrapped height of the buffer
	loadingOlder bool
	logAtStart   bool      // the journal has nothing older than what we hold
	logFilt      logFilter // applied by journalctl, so it searches the whole log
	filterLogs   bool      // the filter editor is aimed at the log, not the table
	logLoadErr   string    // why the last page failed, if it did
	logWrap      bool
	showLogs     bool
	fullView     bool

	menu     ctxMenu
	toast    string
	toastErr bool
	toastSeq int

	focus focusArea
	help  bool
	// helpScroll: the help is longer than a small terminal is tall, so it
	// scrolls rather than losing its last group off the bottom.
	helpScroll int
	width      int
	height     int
	ready      bool
}

func newModel(r runner, hostLabel string, interval time.Duration, sortBy sortKey, reverse, showAll, tree bool, filter string) model {
	return model{
		r:         r,
		col:       NewCollector(r),
		hostLabel: hostLabel,
		interval:  interval,
		sortBy:    sortBy,
		reverse:   reverse,
		showAll:   showAll,
		tree:      tree,
		filter:    filter,
		collapsed: map[string]bool{},
		totals:    &logTotals{epoch: -1},
		logFollow: true,
		logWrap:   true,
		showLogs:  true,
		width:     100,
		height:    30,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pollCmd(), tickCmd(m.interval), spinnerTickCmd())
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// spinnerTickCmd animates the startup screen. It runs faster than the poll
// interval so the spinner still moves while a slow ssh connection is opening,
// and stops re-arming once the first poll lands.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

func (m model) pollCmd() tea.Cmd {
	col := m.col
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		us, host, err := col.Poll(ctx)
		return unitsMsg{units: us, host: host, err: err}
	}
}

func waitJournal(js *journalStream) tea.Cmd {
	if js == nil {
		return nil
	}
	ch, gen := js.ch, js.gen
	return func() tea.Msg {
		b, ok := <-ch
		if !ok {
			return journalBatch{gen: gen, done: true}
		}
		return b
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.clampCursor()
		return m, nil

	case tickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, tickCmd(m.interval))
		if !m.paused && !m.polling && !m.fatal {
			m.polling = true
			cmds = append(cmds, m.pollCmd())
		}
		return m, tea.Batch(cmds...)

	case spinnerTickMsg:
		// Animate while something is pending: the first connection, or a page
		// of earlier log entries. Otherwise stop re-arming.
		if m.connected && !m.loadingOlder && !m.logStarting() {
			return m, nil
		}
		m.spinner++
		return m, spinnerTickCmd()

	case unitsMsg:
		m.polling = false
		m.lastPoll = time.Now()
		if msg.err != nil {
			// Most of this is the far end's stderr, which is no more trusted
			// than the journal is. See sanitize.go.
			m.err = sanitizeText(msg.err.Error())
			m.attempts++
			var unsupported *UnsupportedError
			m.fatal = errors.As(msg.err, &unsupported)
		} else {
			m.err = ""
			m.connected = true
			// A poll that worked is the evidence that whatever we called fatal
			// is not. Leaving the flag set froze polling for good: the tick
			// suppresses itself while fatal, so the display stopped updating
			// and only manual refreshes moved it, forever.
			m.fatal = false
			m.units = msg.units
			m.host = msg.host
		}
		m.rebuild()
		return m, m.syncJournal()

	case actionResult:
		return m, m.applyActionResult(msg)

	case toastExpiredMsg:
		if msg.seq == m.toastSeq {
			m.toast, m.toastErr = "", false
		}
		return m, nil

	case journalBatch:
		if msg.gen != m.logGen {
			return m, nil // belongs to a unit we have already navigated away from
		}
		if len(msg.lines) > 0 {
			m.logs = append(m.logs, msg.lines...)
			if len(m.logs) > maxLogLines {
				m.logs = append([]logLine(nil), m.logs[len(m.logs)-maxLogLines:]...)
			}
			m.logEpoch++
			if m.logFollow {
				m.logScroll = 0
			} else {
				// Keep the reader anchored where they scrolled to, but never
				// above the buffer: a position with nothing behind it renders
				// as an empty pane.
				m.logScroll += m.countDisplayLines(msg.lines)
				m.clampLogScroll()
			}
		}
		if msg.done {
			return m, nil
		}
		return m, waitJournal(m.journal)

	case olderBatch:
		if msg.gen != m.logGen {
			return m, nil // for a unit we have navigated away from
		}
		m.loadingOlder = false
		m.logLoadErr = msg.err
		m.logAtStart = msg.atEnd
		if len(msg.lines) > 0 {
			// Prepending does not move the view: logScroll counts from the
			// bottom, and the bottom has not moved.
			m.logs = append(msg.lines, m.logs...)
			m.logEpoch++
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Before the first successful poll there is nothing to navigate, sort or
	// act on, so only quitting and retrying mean anything.
	if !m.connected {
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "R", "enter":
			// An explicit retry clears a fatal verdict: the user may have just
			// upgraded systemd on the other end.
			if !m.polling {
				m.polling, m.fatal = true, false
				return m, m.pollCmd()
			}
		}
		return m, nil
	}

	if handled, cmd := m.menuKey(msg.String()); handled {
		return m, cmd
	}

	if m.filterInput {
		// The same editor drives both filters; filterLogs says which one is
		// being typed into.
		text := &m.filter
		if m.filterLogs {
			text = &m.logFilt.grep
		}
		switch msg.Type {
		case tea.KeyEnter:
			m.filterInput = false
		case tea.KeyEsc:
			// Cancel, not clear. Amending an applied filter and thinking
			// better of it used to throw the filter away rather than put it
			// back. Escaping again clears it, which is the next thing on the
			// stack.
			m.filterInput = false
			*text = m.filterWas
		case tea.KeyBackspace:
			if r := []rune(*text); len(r) > 0 {
				*text = string(r[:len(r)-1])
			}
		case tea.KeyCtrlU:
			*text = ""
		case tea.KeyRunes, tea.KeySpace:
			*text += string(msg.Runes)
			if msg.Type == tea.KeySpace {
				*text += " "
			}
		case tea.KeyCtrlC:
			return m, tea.Quit
		default:
			return m, nil
		}
		if m.filterLogs {
			// Re-running journalctl per keystroke would spawn a process per
			// character; wait for Enter or Esc to settle.
			if !m.filterInput {
				return m, m.syncJournal()
			}
			return m, nil
		}
		m.rebuild()
		return m, m.syncJournal()
	}

	// Checked after the filter editor, which takes every rune it is given: a
	// pane's letters are commands only when nothing is being typed.
	if !m.keyApplies(msg.String()) {
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.journal.stop()
		return m, tea.Quit
	case "?":
		m.help = !m.help
		m.helpScroll = 0 // it always opens at the top
		return m, nil
	case "esc":
		return m, m.escape()
	case "tab":
		if m.logPaneVisible() {
			if m.focus == focusList {
				m.focus = focusLogs
			} else {
				m.focus = focusList
			}
		}
		return m, nil
	case "/":
		// Filter whatever has focus: the log when reading it, else the table.
		m.filterLogs = m.logHasFocus()
		m.filterWas = m.filter
		if m.filterLogs {
			m.filterWas = m.logFilt.grep
		}
		m.filterInput = true
		m.help = false
		return m, nil
	case "e":
		m.logFilt.prio = nextPriority(m.logFilt.prio)
		return m, m.syncJournal()
	case "s":
		m.sortBy = m.nextVisibleSort(1)
		m.rebuild()
		return m, nil
	case "S":
		m.sortBy = m.nextVisibleSort(-1)
		m.rebuild()
		return m, nil
	case "r":
		m.reverse = !m.reverse
		m.rebuild()
		return m, nil
	case "t":
		m.tree = !m.tree
		m.rebuild()
		return m, m.syncJournal()
	case "a":
		m.showAll = !m.showAll
		m.rebuild()
		return m, m.syncJournal()
	case "f":
		m.logFollow = !m.logFollow
		if m.logFollow {
			m.logScroll = 0
		}
		return m, nil
	case "l":
		m.showLogs = !m.showLogs
		if !m.logPaneVisible() {
			m.focus = focusList
		}
		return m, m.syncJournal()
	case "w":
		m.logWrap = !m.logWrap
		m.logScroll = 0
		return m, nil
	case "p":
		m.paused = !m.paused
		return m, nil
	case "R":
		// One poll now, out of band from the timer. It ignores `paused`, which
		// is the point: p freezes the table and R steps it one frame. Asking
		// explicitly also clears a fatal verdict, as it does on the startup
		// screen — the host may have been upgraded since.
		if !m.polling {
			m.polling, m.fatal = true, false
			return m, m.pollCmd()
		}
		return m, nil
	case "+":
		m.interval = clampInterval(m.interval - stepFor(m.interval, -1))
		return m, nil
	case "-":
		m.interval = clampInterval(m.interval + stepFor(m.interval, 1))
		return m, nil
	case "enter":
		return m, m.activateRow()
	case "x":
		if r, ok := m.selectedRow(); ok && r.kind == rowUnit {
			x, y := m.menuAnchor()
			m.openMenu(r.unit.Name, x, y)
		}
		return m, nil
	}

	// The help covers the panes, so the motion keys move it rather than
	// whatever is underneath — scrolling a table you cannot see is no use.
	if m.help {
		m.helpKey(msg.String())
		return m, nil
	}
	if m.focus == focusLogs {
		return m, m.logKey(msg.String())
	}
	return m, m.listKey(msg.String())
}

// helpKey scrolls the help, using the same motion keys as everything else.
func (m *model) helpKey(k string) {
	page := max(1, m.contentHeight()-1)
	switch k {
	case "up":
		m.helpScroll--
	case "down":
		m.helpScroll++
	case "pgup":
		m.helpScroll -= page
	case "pgdown":
		m.helpScroll += page
	case "home":
		m.helpScroll = 0
	case "end":
		m.helpScroll = m.helpScrollMax()
	default:
		return
	}
	m.helpScroll = min(max(m.helpScroll, 0), m.helpScrollMax())
}

// logHasFocus is the rule that decides which pane a key belonging to neither
// acts on: `/` filters that pane, esc clears that pane's filter. The full view
// is the log and nothing else, so it counts.
func (m model) logHasFocus() bool {
	return m.logPaneVisible() && (m.fullView || m.focus == focusLogs)
}

// escape pops exactly one thing, innermost first. The two modal layers — the
// filter editor and the action menu — are handled before this, so what is left
// is: the help overlay, then whatever is narrowing the pane you are looking at,
// then the full view, then focus.
//
// It clears the *focused* pane's filter, not always the table's. Clearing the
// table's from inside the full view threw away something not on screen and left
// you still in the full view; and an applied log search could not be cleared at
// all, because the cascade only ever looked at the unit filter.
func (m *model) escape() tea.Cmd {
	switch {
	case m.help:
		m.help = false
	case m.logHasFocus() && !m.logFilt.empty():
		// The pane's title advertises the search and the level as one thing,
		// so esc undoes it as one thing; `e` puts a level back in one press.
		m.logFilt = logFilter{}
		return m.syncJournal()
	case !m.logHasFocus() && m.filter != "":
		m.filter = ""
		m.rebuild()
		return m.syncJournal()
	case m.fullView:
		m.fullView = false
		m.focus = focusList
		return m.syncJournal()
	case m.focus == focusLogs:
		m.focus = focusList
	}
	return nil
}

// menuAnchor is where a keyboard-opened popup goes. In the table it points at
// the selected row. The full view has no rows, so anchoring to the cursor put
// it at an arbitrary height over the log; there it sits just under the unit
// summary instead, in the same place every time.
func (m model) menuAnchor() (int, int) {
	if m.fullView {
		return 4, m.headerLines() + m.detailHeight() + 1
	}
	return 4, min(m.cursor-m.topRow+m.headerLines()+3, m.height-4)
}

// activateRow is Enter: on a slice it expands or collapses; on a unit it opens
// the full view, where the unit's log gets the whole width.
func (m *model) activateRow() tea.Cmd {
	r, ok := m.selectedRow()
	if !ok {
		return nil
	}
	if r.kind == rowSlice {
		m.collapsed[r.slice] = !m.collapsed[r.slice]
		m.rebuild()
		return nil
	}
	m.fullView = !m.fullView
	if m.fullView {
		m.showLogs = true
		m.focus = focusLogs
	} else {
		m.focus = focusList
	}
	return m.syncJournal()
}

// listKey moves the table cursor: arrows by a line, PgUp/PgDn by a page, Home
// and End to the ends. One key per motion, and all of them named keys — the
// letters belong to the log, where F and f are its two ends. The vim aliases
// (hjkl, g/G) and the readline ones (ctrl+b/ctrl+f) each gave a second and
// third way to say what the arrows already say, and every letter they held is
// a letter unavailable for a command.
func (m *model) listKey(k string) tea.Cmd {
	page := max(1, m.listRows()-1)
	switch k {
	case "up":
		m.cursor--
	case "down":
		m.cursor++
	case "pgup":
		m.cursor -= page
	case "pgdown":
		m.cursor += page
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = len(m.rows) - 1
	case "left":
		return m.collapseOrParent()
	case "right":
		if r, ok := m.selectedRow(); ok && r.kind == rowSlice && !r.expanded {
			m.collapsed[r.slice] = false
			m.rebuild()
		}
		return nil
	default:
		return nil
	}
	return m.afterCursorMove()
}

// collapseOrParent mirrors what file trees do: collapse the slice you are on,
// or jump to the parent slice if you are already inside one.
func (m *model) collapseOrParent() tea.Cmd {
	r, ok := m.selectedRow()
	if !ok || !m.tree {
		return nil
	}
	if r.kind == rowSlice && r.expanded {
		m.collapsed[r.slice] = true
		m.rebuild()
		return nil
	}
	for i := m.cursor - 1; i >= 0; i-- {
		if m.rows[i].kind == rowSlice && m.rows[i].depth < r.depth {
			m.cursor = i
			return m.afterCursorMove()
		}
	}
	return nil
}

// scrollLog moves the log view and keeps follow in step with it: resting at the
// live end *is* following, and anywhere else is not. Without this, scrolling
// back down to the bottom left follow off and the log sat still.
//
// Reaching the top asks the journal for the page before what we hold, so the
// buffer is a window onto the log rather than its beginning.
func (m *model) scrollLog(delta int) tea.Cmd {
	m.logScroll += delta
	m.clampLogScroll()
	m.logFollow = m.logScroll == 0
	if delta > 0 && m.atTopOfLog() {
		return m.loadOlder()
	}
	return nil
}

func (m model) atTopOfLog() bool {
	return m.logScroll >= m.logDisplayTotal()-m.logHeight()
}

// logBufferFull reports that no more history will be paged in.
func (m model) logBufferFull() bool { return len(m.logs) >= maxLogLines }

// loadOlder fetches the page before the oldest line held, once at a time.
func (m *model) loadOlder() tea.Cmd {
	if m.loadingOlder || m.logAtStart || len(m.logs) == 0 || m.journal == nil {
		return nil
	}
	// Refuse rather than grow without bound. Trimming instead would throw away
	// the very lines the reader scrolled back to see.
	if m.logBufferFull() {
		return nil
	}
	oldest := ""
	for _, l := range m.logs {
		if l.cursor != "" {
			oldest = l.cursor
			break
		}
	}
	if oldest == "" {
		return nil // nothing but meta lines; there is no position to page from
	}
	m.loadingOlder = true
	m.logLoadErr = ""
	// Restart the spinner: it stops re-arming once connected.
	return tea.Batch(
		fetchOlder(context.Background(), m.r, m.journal.unit, oldest, m.logFilt, journalBacklog, m.logGen),
		spinnerTickCmd(),
	)
}

const scrollToEnd = 1 << 30 // clamped to the real limit by clampLogScroll

// logKey scrolls the log. F and f are its two ends — F to the top, f to the
// live end, which is what following means. They are the only letters bound to
// motion anywhere, and they are bound here because this is the only pane with
// two ends worth naming: the table's are just Home and End.
func (m *model) logKey(k string) tea.Cmd {
	page := max(1, m.logHeight()-1)
	switch k {
	case "up":
		return m.scrollLog(1)
	case "down":
		return m.scrollLog(-1)
	case "pgup":
		return m.scrollLog(page)
	case "pgdown":
		return m.scrollLog(-page)
	case "end":
		return m.scrollLog(-scrollToEnd)
	case "home", "F":
		return m.scrollLog(scrollToEnd)
	}
	return nil
}

// handleMouse gives the wheel to whichever pane the pointer is over, makes a
// click on a row select it, a click on a column header sort by it, and a
// right-click on a unit open the action menu.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.menu.open {
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if i := m.menuItemAt(msg.X, msg.Y); i >= 0 && !m.menu.confirm {
				m.menu.cursor = i
				_, cmd := m.menuKey("enter")
				return m, cmd
			}
			if !m.menu.confirm {
				m.menu = ctxMenu{}
			}
		}
		return m, nil
	}

	// The table's box takes four columns of its own — two borders and the air
	// inside them — so the log pane begins four past the table's content.
	overLogs := m.logPaneVisible() && (m.fullView || msg.X >= m.tableWidth()+4)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if overLogs {
			return m, m.scrollLog(3)
		}
		m.cursor -= 3
		return m, m.afterCursorMove()
	case tea.MouseButtonWheelDown:
		if overLogs {
			return m, m.scrollLog(-3)
		}
		m.cursor += 3
		return m, m.afterCursorMove()

	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if overLogs {
			m.focus = focusLogs
			return m, nil
		}
		m.focus = focusList
		if msg.Y == m.headerLines()+1 { // host block, then the pane's top border
			if k, ok := m.columnAt(msg.X); ok {
				if k == m.sortBy {
					m.reverse = !m.reverse
				} else {
					m.sortBy, m.reverse = k, false
				}
				m.rebuild()
			}
			return m, nil
		}
		if row, ok := m.rowAt(msg.Y); ok {
			m.cursor = row
			cmd := m.afterCursorMove()
			// A click on the twisty of a slice row toggles it, like a file tree.
			if r, ok := m.selectedRow(); ok && r.kind == rowSlice && msg.X-2 <= r.depth*2+1 {
				m.collapsed[r.slice] = !m.collapsed[r.slice]
				m.rebuild()
			}
			return m, cmd
		}

	case tea.MouseButtonRight:
		if msg.Action != tea.MouseActionPress || overLogs {
			return m, nil
		}
		if row, ok := m.rowAt(msg.Y); ok {
			m.cursor = row
			cmd := m.afterCursorMove()
			if r, ok := m.selectedRow(); ok && r.kind == rowUnit {
				m.openMenu(r.unit.Name, msg.X, msg.Y)
			}
			return m, cmd
		}
	}
	return m, nil
}

// rowAt maps a screen line onto an index in m.rows.
func (m model) rowAt(y int) (int, bool) {
	// The host block, the pane's top border, the column titles and their rule.
	first := m.headerLines() + 3
	idx := m.topRow + y - first
	if y < first || idx < 0 || idx >= len(m.rows) {
		return 0, false
	}
	return idx, true
}

// columnAt maps a screen column onto the sort key of the column under it. The
// table's content starts two columns in, past its border and the air inside it.
func (m model) columnAt(x int) (sortKey, bool) {
	cols := m.layout(m.tableWidth())
	x, at := x-2, 0
	for _, c := range cols {
		if x >= at && x < at+c.width {
			return c.key, true
		}
		at += c.width + 1
	}
	return sortName, false
}

// nextVisibleSort walks the columns actually on screen, in display order.
func (m model) nextVisibleSort(dir int) sortKey {
	cols := m.layout(m.tableWidth())
	if len(cols) == 0 {
		return m.sortBy
	}
	at := -1
	for i, c := range cols {
		if c.key == m.sortBy {
			at = i
			break
		}
	}
	if at < 0 {
		if dir > 0 {
			return cols[0].key
		}
		return cols[len(cols)-1].key
	}
	return cols[(at+dir+len(cols))%len(cols)].key
}

func (m model) menuItemAt(x, y int) int {
	if !m.menu.open {
		return -1
	}
	w := menuWidth(m.menu.unit)
	i := y - m.menu.y - 1
	if x < m.menu.x || x >= m.menu.x+w || i < 0 || i >= len(unitActions) {
		return -1
	}
	return i
}

func (m *model) afterCursorMove() tea.Cmd {
	m.clampCursor()
	if r, ok := m.selectedRow(); ok {
		m.selected = r.key()
	}
	return m.syncJournal()
}

func stepFor(d time.Duration, dir int) time.Duration {
	if dir < 0 && d <= time.Second {
		return 250 * time.Millisecond
	}
	if dir > 0 && d < time.Second {
		return 250 * time.Millisecond
	}
	return time.Second
}

func clampInterval(d time.Duration) time.Duration {
	if d < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

// rebuild applies the filter, sort and tree grouping, then re-anchors the
// cursor on the row that was selected before so a reorder never yanks the
// selection away.
func (m *model) rebuild() {
	f := strings.ToLower(strings.TrimSpace(m.filter))
	kept := make([]Unit, 0, len(m.units))
	for _, u := range m.units {
		if !m.showAll && u.Active == "inactive" && !u.Failed() {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.Desc), f) {
			continue
		}
		kept = append(kept, u)
	}
	m.rows = buildRows(kept, m.sortBy, m.reverse, m.tree, m.collapsed)

	if m.selected != "" {
		for i, r := range m.rows {
			if r.key() == m.selected {
				m.cursor = i
				m.clampCursor()
				return
			}
		}
	}
	m.clampCursor()
	if r, ok := m.selectedRow(); ok {
		m.selected = r.key()
	} else {
		m.selected = ""
	}
}

func (m *model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	h := m.listRows()
	if m.cursor < m.topRow {
		m.topRow = m.cursor
	}
	if m.cursor >= m.topRow+h {
		m.topRow = m.cursor - h + 1
	}
	if m.topRow > len(m.rows)-h {
		m.topRow = len(m.rows) - h
	}
	if m.topRow < 0 {
		m.topRow = 0
	}
}

func (m *model) clampLogScroll() {
	total := m.logDisplayTotal()
	maxScroll := max(0, total-m.logHeight())
	if m.logScroll > maxScroll {
		m.logScroll = maxScroll
	}
	if m.logScroll < 0 {
		m.logScroll = 0
	}
}

// syncJournal makes the log stream follow the selection, restarting journalctl
// only when the selected unit actually changed. Slice rows have no journal.
func (m *model) syncJournal() tea.Cmd {
	want := ""
	if m.logPaneVisible() {
		if r, ok := m.selectedRow(); ok && r.kind == rowUnit {
			want = r.unit.Name
		}
	}
	// A filter change reruns journalctl, because the filtering happens there.
	if m.journal != nil && m.journal.unit == want && m.journal.filter == m.logFilt {
		return nil
	}
	m.journal.stop()
	m.journal = nil
	m.logs = nil
	m.logEpoch++
	// A new unit's log opens where a log opens: at the live end. Carrying the
	// old one's position over left the view above an empty buffer, and every
	// batch that arrived pushed it further up rather than filling it in.
	m.logScroll, m.logFollow = 0, true
	m.loadingOlder, m.logAtStart, m.logLoadErr = false, false, ""
	m.logGen++
	if want == "" {
		return nil
	}
	m.journal = startJournal(context.Background(), m.r, want, m.logFilt, journalBacklog, m.logGen)
	// Restart the spinner: it stops re-arming once connected, and the empty
	// pane needs it while the first entries are on their way.
	return tea.Batch(waitJournal(m.journal), spinnerTickCmd())
}

func (m model) selectedRow() (row, bool) {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor], true
	}
	return row{}, false
}

// selectedUnit is the selected service, if the cursor is on one.
func (m model) selectedUnit() (Unit, bool) {
	if r, ok := m.selectedRow(); ok && r.kind == rowUnit {
		return r.unit, true
	}
	return Unit{}, false
}
