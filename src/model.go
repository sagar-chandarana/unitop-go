package main

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	maxLogLines    = 4000
	journalBacklog = 500
)

type focusArea int

const (
	focusList focusArea = iota
	focusLogs
)

// tableOnlyKeys sort, filter, group or move focus between panes — all of which
// act on the unit table. In the full view there is no table to act on.
var tableOnlyKeys = map[string]bool{
	"s": true, "S": true, // sort column
	"r":   true, // reverse
	"t":   true, // tree
	"a":   true, // include inactive
	"/":   true, // filter
	"tab": true,
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
	showAll     bool
	tree        bool
	collapsed   map[string]bool

	sortBy  sortKey
	reverse bool

	logs      []logLine
	logGen    int
	journal   *journalStream
	logScroll int // wrapped display lines scrolled up from the bottom
	logFollow bool
	logWrap   bool
	showLogs  bool
	fullView  bool

	menu     ctxMenu
	toast    string
	toastErr bool
	toastSeq int

	focus  focusArea
	help   bool
	width  int
	height int
	ready  bool
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
		if m.connected {
			return m, nil // stop animating; nothing re-arms it
		}
		m.spinner++
		return m, spinnerTickCmd()

	case unitsMsg:
		m.polling = false
		m.lastPoll = time.Now()
		if msg.err != nil {
			m.err = msg.err.Error()
			m.attempts++
			var unsupported *UnsupportedError
			m.fatal = errors.As(msg.err, &unsupported)
		} else {
			m.err = ""
			m.connected = true
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
			if m.logFollow {
				m.logScroll = 0
			} else {
				// Keep the reader anchored where they scrolled to.
				m.logScroll += m.countDisplayLines(msg.lines)
			}
		}
		if msg.done {
			return m, nil
		}
		return m, waitJournal(m.journal)

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
		case "R", "r", "enter":
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

	// The full view has no table, so the keys that act on one do nothing there.
	// They are not offered in the footer either.
	if m.fullView && tableOnlyKeys[msg.String()] {
		return m, nil
	}

	if m.filterInput {
		switch msg.Type {
		case tea.KeyEnter:
			m.filterInput = false
		case tea.KeyEsc:
			m.filterInput = false
			m.filter = ""
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeyCtrlU:
			m.filter = ""
		case tea.KeyRunes, tea.KeySpace:
			m.filter += string(msg.Runes)
			if msg.Type == tea.KeySpace {
				m.filter += " "
			}
		case tea.KeyCtrlC:
			return m, tea.Quit
		default:
			return m, nil
		}
		m.rebuild()
		return m, m.syncJournal()
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.journal.stop()
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "esc":
		if m.help {
			m.help = false
		} else if m.filter != "" {
			m.filter = ""
			m.rebuild()
			return m, m.syncJournal()
		} else if m.fullView {
			m.fullView = false
			m.focus = focusList
			return m, m.syncJournal()
		} else if m.focus == focusLogs {
			m.focus = focusList
		}
		return m, nil
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
		m.filterInput = true
		m.help = false
		return m, nil
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
		// The full view is nothing but the log, so hiding it would leave an
		// empty screen. Ignore the key rather than acting on it.
		if m.fullView {
			return m, nil
		}
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
		if !m.polling {
			m.polling = true
			return m, m.pollCmd()
		}
		return m, nil
	case "+", "=":
		m.interval = clampInterval(m.interval - stepFor(m.interval, -1))
		return m, nil
	case "-", "_":
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

	if m.focus == focusLogs {
		return m, m.logKey(msg.String())
	}
	return m, m.listKey(msg.String())
}

// menuAnchor is where a keyboard-opened popup goes. In the table it points at
// the selected row. The full view has no rows, so anchoring to the cursor put
// it at an arbitrary height over the log; there it sits just under the unit
// summary instead, in the same place every time.
func (m model) menuAnchor() (int, int) {
	if m.fullView {
		return 4, m.headerLines() + m.detailHeight()
	}
	return 4, min(m.cursor-m.topRow+m.headerLines()+2, m.height-4)
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

func (m *model) listKey(k string) tea.Cmd {
	page := max(1, m.listRows()-1)
	switch k {
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup", "ctrl+b":
		m.cursor -= page
	case "pgdown", "ctrl+f":
		m.cursor += page
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.rows) - 1
	case "left", "h":
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
func (m *model) scrollLog(delta int) {
	m.logScroll += delta
	m.clampLogScroll()
	m.logFollow = m.logScroll == 0
}

const scrollToEnd = 1 << 30 // clamped to the real limit by clampLogScroll

func (m *model) logKey(k string) tea.Cmd {
	page := max(1, m.logHeight()-1)
	switch k {
	case "up", "k":
		m.scrollLog(1)
	case "down", "j":
		m.scrollLog(-1)
	case "pgup", "ctrl+b":
		m.scrollLog(page)
	case "pgdown", "ctrl+f":
		m.scrollLog(-page)
	case "end", "G":
		m.scrollLog(-scrollToEnd)
	case "home", "g":
		m.scrollLog(scrollToEnd)
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

	overLogs := m.logPaneVisible() && (m.fullView || msg.X >= m.tableWidth())
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if overLogs {
			m.scrollLog(3)
			return m, nil
		}
		m.cursor -= 3
		return m, m.afterCursorMove()
	case tea.MouseButtonWheelDown:
		if overLogs {
			m.scrollLog(-3)
			return m, nil
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
		if msg.Y == m.headerLines() {
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
			if r, ok := m.selectedRow(); ok && r.kind == rowSlice && msg.X <= r.depth*2+1 {
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
	first := m.headerLines() + 2 // host block, then the column titles and their rule
	idx := m.topRow + y - first
	if y < first || idx < 0 || idx >= len(m.rows) {
		return 0, false
	}
	return idx, true
}

// columnAt maps a screen column onto the sort key of the column under it.
func (m model) columnAt(x int) (sortKey, bool) {
	cols := m.layout(m.tableWidth())
	at := 0
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
	total := m.countDisplayLines(m.logs)
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
	if m.journal != nil && m.journal.unit == want {
		return nil
	}
	m.journal.stop()
	m.journal = nil
	m.logs = nil
	m.logScroll = 0
	m.logGen++
	if want == "" {
		return nil
	}
	m.journal = startJournal(context.Background(), m.r, want, journalBacklog, m.logGen)
	return waitJournal(m.journal)
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
