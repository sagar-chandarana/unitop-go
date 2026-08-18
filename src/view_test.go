package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func TestSortUnits(t *testing.T) {
	us := []Unit{
		{Name: "b.service", CPUPct: 1, MemCurrent: 300, NRestarts: 0, Active: "active", Sub: "running"},
		{Name: "a.service", CPUPct: 50, MemCurrent: 100, NRestarts: 3, Active: "active", Sub: "running"},
		{Name: "c.service", CPUPct: 10, MemCurrent: unsetU64, NRestarts: unsetU64, Active: "failed", Sub: "failed"},
	}

	sortUnits(us, sortCPU, false)
	if us[0].Name != "a.service" || us[2].Name != "b.service" {
		t.Errorf("cpu desc: %v", names(us))
	}
	sortUnits(us, sortCPU, true)
	if us[0].Name != "b.service" {
		t.Errorf("cpu reversed: %v", names(us))
	}
	sortUnits(us, sortMem, false)
	if us[0].Name != "b.service" || us[2].Name != "c.service" {
		t.Errorf("unset memory must sort as zero: %v", names(us))
	}
	sortUnits(us, sortRestarts, false)
	if us[0].Name != "a.service" {
		t.Errorf("restarts desc: %v", names(us))
	}
	sortUnits(us, sortState, false)
	if us[0].Name != "c.service" {
		t.Errorf("failed units must come first: %v", names(us))
	}
	sortUnits(us, sortName, false)
	if names(us) != "a.service b.service c.service" {
		t.Errorf("name asc: %v", names(us))
	}
}

func TestSortNetInAndOutAreSeparateColumns(t *testing.T) {
	us := []Unit{
		{Name: "a.service", NetInRate: 100, NetOutRate: 1},
		{Name: "b.service", NetInRate: 1, NetOutRate: 100},
	}
	sortUnits(us, sortNetIn, false)
	if us[0].Name != "a.service" {
		t.Errorf("net-in: %v", names(us))
	}
	sortUnits(us, sortNetOut, false)
	if us[0].Name != "b.service" {
		t.Errorf("net-out: %v", names(us))
	}
}

func TestParseSortKeyShorthands(t *testing.T) {
	for in, want := range map[string]sortKey{
		"net": sortNetIn, "net-out": sortNetOut, "io": sortIORead,
		"io-write": sortIOWrite, "cpu": sortCPU, "name": sortName,
	} {
		got, ok := parseSortKey(in)
		if !ok || got != want {
			t.Errorf("parseSortKey(%q) = %v/%v", in, got, ok)
		}
	}
	if _, ok := parseSortKey("nonsense"); ok {
		t.Error("unknown key should not parse")
	}
}

func names(us []Unit) string {
	var n []string
	for _, u := range us {
		n = append(n, u.Name)
	}
	return strings.Join(n, " ")
}

func rowNames(rows []row) string {
	var n []string
	for _, r := range rows {
		if r.kind == rowSlice {
			n = append(n, strings.Repeat(" ", r.depth)+"["+sliceLabel(r.slice)+"]")
		} else {
			n = append(n, strings.Repeat(" ", r.depth)+shortUnit(r.unit.Name))
		}
	}
	return strings.Join(n, " ")
}

func TestSortIsStableOnTies(t *testing.T) {
	us := []Unit{
		{Name: "z.service", CPUPct: 0},
		{Name: "m.service", CPUPct: 0},
		{Name: "a.service", CPUPct: 0},
	}
	sortUnits(us, sortCPU, false)
	if names(us) != "a.service m.service z.service" {
		t.Errorf("ties must fall back to name so rows do not jitter: %v", names(us))
	}
}

func TestLayoutFitsWidth(t *testing.T) {
	m := model{}
	for _, w := range []int{20, 40, 60, 80, 120, 200} {
		cols := m.layout(w)
		total := -1
		for _, c := range cols {
			total += c.width + 1
		}
		if total > w {
			t.Errorf("width %d: layout is %d wide", w, total)
		}
		if cols[0].title != "UNIT" {
			t.Errorf("width %d: UNIT column was dropped", w)
		}
		if w >= 100 && total != w {
			t.Errorf("width %d: slack %d not given to UNIT", w, w-total)
		}
	}
}

func TestLayoutDropsLowestPriorityFirst(t *testing.T) {
	m := model{}
	var titles []string
	for _, c := range m.layout(48) {
		titles = append(titles, c.title)
	}
	joined := strings.Join(titles, ",")
	for _, want := range []string{"UNIT", "STATE", "CPU%"} {
		if !strings.Contains(joined, want) {
			t.Errorf("narrow layout dropped %s: %s", want, joined)
		}
	}
	if strings.Contains(joined, "IO↑") {
		t.Errorf("narrow layout kept IO↑: %s", joined)
	}
}

// The s key must walk the columns that are actually on screen, in the order
// they are drawn.
func TestNextVisibleSortWalksVisibleColumns(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortName, false, false, false, "")
	m.width, m.height = 200, 40
	m.connected = true
	m.showLogs = false

	cols := m.layout(m.tableWidth())
	seen := map[sortKey]bool{}
	k := m.sortBy
	for range cols {
		k = func() sortKey { m.sortBy = k; return m.nextVisibleSort(1) }()
		if seen[k] {
			t.Fatalf("s revisited %v before covering every column", k)
		}
		seen[k] = true
	}
	if len(seen) != len(cols) {
		t.Errorf("s covered %d of %d visible columns", len(seen), len(cols))
	}

	// Narrow enough that IO is dropped: s must never land on it.
	m.width = 60
	m.sortBy = sortIOWrite
	if got := m.nextVisibleSort(1); got == sortIOWrite {
		t.Errorf("s stuck on a hidden column: %v", got)
	}
}

func TestColumnAtMapsClickToSortKey(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height = 200, 40
	m.connected = true
	m.showLogs = false

	cols := m.layout(m.tableWidth())
	at := 0
	for _, c := range cols {
		got, ok := m.columnAt(at + c.width/2)
		if !ok || got != c.key {
			t.Errorf("click in %s gave %v/%v", c.title, got, ok)
		}
		at += c.width + 1
	}
	if _, ok := m.columnAt(m.tableWidth() + 50); ok {
		t.Error("click past the last column should not sort")
	}
}

func TestRebuildFilters(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortName, false, false, false, "")
	m.units = []Unit{
		{Name: "nginx.service", Desc: "web", Active: "active", Sub: "running", Slice: "system.slice"},
		{Name: "old.service", Desc: "gone", Active: "inactive", Sub: "dead", Slice: "system.slice"},
		{Name: "bad.service", Desc: "broken", Active: "failed", Sub: "failed", Slice: "system.slice"},
	}

	m.rebuild()
	if len(m.rows) != 2 {
		t.Errorf("inactive units should be hidden by default: %v", rowNames(m.rows))
	}
	m.showAll = true
	m.rebuild()
	if len(m.rows) != 3 {
		t.Errorf("showAll should reveal inactive units: %v", rowNames(m.rows))
	}
	m.showAll, m.filter = false, "WEB"
	m.rebuild()
	if len(m.rows) != 1 || m.rows[0].unit.Name != "nginx.service" {
		t.Errorf("filter must be case-insensitive and match the description: %v", rowNames(m.rows))
	}
}

func TestRebuildKeepsSelection(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.units = []Unit{
		{Name: "a.service", Active: "active", CPUPct: 1, Slice: "system.slice"},
		{Name: "b.service", Active: "active", CPUPct: 2, Slice: "system.slice"},
	}
	m.rebuild()
	m.cursor = 1
	m.selected = m.rows[1].key() // a.service, currently last

	// b's CPU collapses, so the order flips.
	m.units[0].CPUPct = 99
	m.rebuild()
	if m.rows[m.cursor].unit.Name != "a.service" {
		t.Errorf("cursor should follow the selected unit, landed on %s", m.rows[m.cursor].unit.Name)
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := humanBytes(unsetU64); got != "-" {
		t.Errorf("unset bytes = %q", got)
	}
	if got := humanBytes(5308416); got != "5.1M" {
		t.Errorf("humanBytes(5308416) = %q, want 5.1M", got)
	}
	if got := humanDur(3*time.Hour + 12*time.Minute); got != "3h12m" {
		t.Errorf("humanDur = %q", got)
	}
	if got := pad("verylongname", 6); got != "veryl…" {
		t.Errorf("pad truncation = %q", got)
	}
	if got := padLeft("12", 5); got != "   12" {
		t.Errorf("padLeft = %q", got)
	}
	if got := shortUnit("nginx.service"); got != "nginx" {
		t.Errorf("shortUnit = %q", got)
	}
	// systemd escapes anything unusual in a unit name; show it as written.
	if got := shortUnit(`my\x2dapp@dev.service`); got != "my-app@dev" {
		t.Errorf("escaped unit name = %q", got)
	}
	if got := sliceLabel(`system-my\x2dapp.slice`); got != "system-my-app" {
		t.Errorf("escaped slice label = %q", got)
	}
	if got := sliceLabel("-.slice"); got != "/" {
		t.Errorf("root slice label = %q", got)
	}
	if got := unescapeUnit(`a\xZZb`); got != `a\xZZb` {
		t.Errorf("a malformed escape should pass through: %q", got)
	}
	if got := wrapRunes("abcdefg", 3); strings.Join(got, "|") != "abc|def|g" {
		t.Errorf("wrapRunes = %v", got)
	}
}

func TestWrapWords(t *testing.T) {
	if got := wrapWords("the quick brown fox", 10); strings.Join(got, "|") != "the quick|brown fox" {
		t.Errorf("word wrap = %v", got)
	}
	got := wrapWords("ab /nix/store/aaaaaaaaaaaaaaaaaaaa/x", 8)
	for _, seg := range got {
		if len([]rune(seg)) > 8 {
			t.Errorf("segment %q exceeds width: %v", seg, got)
		}
	}
	if got := wrapWords("short", 20); len(got) != 1 || got[0] != "short" {
		t.Errorf("short line should pass through: %v", got)
	}
	if got := wrapWords("", 10); len(got) != 1 {
		t.Errorf("empty line should yield one segment: %v", got)
	}
}

func TestTruncANSIKeepsEscapes(t *testing.T) {
	out := truncANSI(stBad.Render("abcdefghij"), 4)
	if strings.Count(out, "abcd") != 1 {
		t.Errorf("visible text not truncated to 4: %q", out)
	}
	if strings.Contains(out, "efgh") {
		t.Errorf("truncation left extra text: %q", out)
	}
}

func TestHumanRateFullSpellsOutZero(t *testing.T) {
	if got := humanRate(0); got != "." {
		t.Errorf("table form of an idle rate = %q", got)
	}
	if got := humanRateFull(0); got != "0B/s" {
		t.Errorf("label form of an idle rate = %q", got)
	}
	if got := humanRateFull(2048); got != "2.0K/s" {
		t.Errorf("humanRateFull(2048) = %q", got)
	}
}

func TestViewRendersWithoutData(t *testing.T) {
	m := newModel(runner{}, "testhost", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 120, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.rebuild()
	out := m.View()
	if !strings.Contains(out, "testhost") {
		t.Errorf("host block missing: %q", firstLine(out))
	}
	if n := strings.Count(out, "\n"); n != 29 {
		t.Errorf("View should fill exactly 30 lines, got %d", n+1)
	}
}

func testUnits() []Unit {
	return []Unit{
		{Name: "nginx.service", Desc: "web", Active: "active", Sub: "running", CPUPct: 12.5,
			MemCurrent: 40 << 20, IPAccount: true, NetInRate: 2048, HasRates: true, NRestarts: 1,
			ActiveSince: time.Now().Add(-time.Hour), Tasks: 4, MainPID: 42, Slice: "system.slice"},
		{Name: "bad.service", Desc: "broken", Active: "failed", Sub: "failed", Result: "exit-code",
			MemCurrent: unsetU64, NRestarts: 7, Tasks: unsetU64, Slice: "system.slice"},
		{Name: "shell.service", Desc: "user shell", Active: "active", Sub: "running", CPUPct: 3,
			MemCurrent: 8 << 20, Tasks: 2, Slice: "user-1000.slice"},
	}
}

func TestViewRendersRows(t *testing.T) {
	m := newModel(runner{}, "testhost", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 24, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = testUnits()
	m.host = HostStats{OK: true, NCPU: 8, MemTotal: 16 << 30, MemUsed: 4 << 30, CPUPct: 12}
	m.rebuild()
	out := m.View()
	for _, want := range []string{"nginx", "bad", "12.5", "40M", "failed", "1 failed", "8 cpu"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered view is missing %q", want)
		}
	}
	if n := strings.Count(out, "\n"); n != 23 {
		t.Errorf("View should fill 24 lines, got %d", n+1)
	}
}

// Enter opens the full view on a unit and gives the log the whole width; Esc
// brings the table back.
func TestEnterTogglesFullView(t *testing.T) {
	m := newModel(runner{}, "testhost", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = testUnits()
	m.rebuild()

	if strings.Contains(m.View(), "UNIT") == false {
		t.Fatal("the table should be visible to begin with")
	}
	m.activateRow()
	if !m.fullView {
		t.Fatal("enter did not open the full view")
	}
	if m.focus != focusLogs {
		t.Error("full view should focus the log")
	}
	out := m.View()
	if strings.Contains(out, "UNIT ") {
		t.Error("full view should hide the table")
	}
	// The whole screen bar the pane's own box.
	if m.logPaneWidth() != m.width-4 {
		t.Errorf("log pane is %d wide, want %d", m.logPaneWidth(), m.width-4)
	}
	// The live counters must survive the loss of the table.
	for _, want := range []string{"cpu", "mem", "net", "12.5%", "40M", "pid 42", "restarts 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("full view is missing %q:\n%s", want, out)
		}
	}
	for _, line := range m.unitDetail(testUnits()[0], m.width) {
		if lipglossWidth(line) > m.width {
			t.Errorf("detail line is %d wide, screen is %d", lipglossWidth(line), m.width)
		}
	}
	if n := strings.Count(out, "\n"); n != 29 {
		t.Errorf("full view should fill 30 lines, got %d", n+1)
	}

	m.activateRow()
	if m.fullView {
		t.Error("enter did not close the full view")
	}
	m.activateRow()
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.fullView {
		t.Error("esc did not leave the full view")
	}
	if m.focus != focusList {
		t.Error("leaving the full view should return focus to the list")
	}
}

// The full view is nothing but the log, so l must not be able to hide it.
func TestLogToggleIsInertInFullView(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = testUnits()
	m.rebuild()
	m.activateRow()
	if !m.fullView {
		t.Fatal("enter did not open the full view")
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if !m.showLogs || !m.logPaneVisible() {
		t.Error("l hid the log while in the full view")
	}
	if m.focus != focusLogs {
		t.Error("l changed focus in the full view")
	}
	if strings.Contains(m.viewFooter(), "l log") {
		t.Error("the footer still offers l in the full view")
	}

	// Outside the full view it works as before.
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if m.showLogs {
		t.Error("l did not hide the log pane outside the full view")
	}
	if !strings.Contains(m.viewFooter(), "l log") {
		t.Error("the footer should offer l outside the full view")
	}
}

// Enter on a slice is expand/collapse, not the full view.
func TestEnterOnSliceDoesNotOpenFullView(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, true, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = treeUnits()
	m.rebuild()
	m.cursor = 0
	m.afterCursorMove()
	m.activateRow()
	if m.fullView {
		t.Error("enter on a slice opened the full view")
	}
	if !m.collapsed["-.slice"] {
		t.Error("enter on a slice did not collapse it")
	}
}

var allFooterHints = []string{
	"↑↓ move", "F/f top/bottom", "enter full view", "enter/esc back", "x actions", "tab focus",
	"s sort", "r rev", "t tree", "/ filter units", "/ search log", "a all",
	"f follow", "f follow off",
	"e level", "w wrap", "l log", "? help", "q quit",
}

// The footer drops whole hints when the terminal is narrow; a hint cut in half
// looks like a rendering fault.
func TestFooterDropsWholeHints(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 140, 200} {
		m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m.width, m.height, m.ready = w, 30, true
		m.connected = true
		m.units = testUnits()
		m.rebuild()

		foot := m.viewFooter()
		if got := lipglossWidth(foot); got > w {
			t.Errorf("width %d: footer is %d wide", w, got)
		}
		// Whatever survived must be complete hints, not a truncated tail.
		plain := stripANSI(foot)
		if strings.HasSuffix(plain, " ") || strings.HasSuffix(plain, "·") {
			t.Errorf("width %d: footer ends mid-separator: %q", w, plain)
		}
		// Every hint present must be one of the complete ones.
		for _, hint := range strings.Split(plain, " · ") {
			if hint == "" {
				continue
			}
			if !slices.Contains(allFooterHints, hint) {
				t.Errorf("width %d: %q is not a complete hint (footer %q)", w, hint, plain)
			}
		}
		// Quit is reserved, so it survives at every width — it is the one hint
		// you cannot afford to lose.
		if !strings.Contains(plain, "q quit") {
			t.Errorf("width %d: the footer dropped how to quit: %q", w, plain)
		}
		if w == 40 && strings.Contains(plain, "t tree") {
			t.Errorf("width 40: footer should have dropped later hints: %q", plain)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestRowAtMatchesRenderedRows(t *testing.T) {
	m := newModel(runner{}, "testhost", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 24, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = testUnits()
	m.rebuild()

	first := m.headerLines() + 3
	for i := range m.rows {
		got, ok := m.rowAt(first + i)
		if !ok || got != i {
			t.Fatalf("rowAt(%d) = %d/%v, want %d", first+i, got, ok, i)
		}
	}
	for off, what := range map[int]string{
		0: "the pane's top border",
		1: "the column-title line",
		2: "the rule under the titles",
	} {
		if _, ok := m.rowAt(m.headerLines() + off); ok {
			t.Errorf("%s is not a row", what)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Sorting, filtering, grouping and pane focus all act on the table. With no
// table on screen they must do nothing, and must not be advertised.
func TestFullViewIgnoresTableKeys(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	m.activateRow()
	if !m.fullView {
		t.Fatal("enter did not open the full view")
	}

	before := struct {
		sortBy         sortKey
		reverse, tree  bool
		showAll, input bool
		focus          focusArea
	}{m.sortBy, m.reverse, m.tree, m.showAll, m.filterInput, m.focus}

	// "/" is deliberately absent: in the full view it filters the log, which is
	// checked separately below.
	for _, k := range []string{"s", "S", "r", "t", "a", "tab"} {
		m.handleKey(keyOf(k))
		if m.sortBy != before.sortBy || m.reverse != before.reverse || m.tree != before.tree ||
			m.showAll != before.showAll || m.filterInput != before.input || m.focus != before.focus {
			t.Errorf("%q changed state in the full view", k)
		}
		if !m.fullView {
			t.Fatalf("%q dropped out of the full view", k)
		}
	}

	foot := stripANSI(m.viewFooter())
	for _, gone := range []string{"s sort", "r rev", "t tree", "/ filter", "a all", "tab focus", "l log"} {
		if strings.Contains(foot, gone) {
			t.Errorf("footer still offers %q in the full view: %s", gone, foot)
		}
	}
	for _, kept := range []string{"enter/esc back", "x actions", "f follow", "/ search", "q quit"} {
		if !strings.Contains(foot, kept) {
			t.Errorf("footer dropped %q, which still works: %s", kept, foot)
		}
	}

	// "/" does work in the full view: it searches the log rather than the table.
	m.handleKey(keyOf("/"))
	if !m.filterInput || !m.filterLogs {
		t.Errorf("/ in the full view should open the log search: input=%v logs=%v",
			m.filterInput, m.filterLogs)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter != "" {
		t.Error("the log search must not have touched the unit filter")
	}

	// The same keys keep working once the table is back.
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.handleKey(keyOf("t"))
	if !m.tree {
		t.Error("t stopped working after leaving the full view")
	}
	if !strings.Contains(stripANSI(m.viewFooter()), "s sort") {
		t.Error("the table footer lost its sort hint")
	}
}

func keyOf(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "\r":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Scrolling back down to the newest line means "follow again" — by keyboard as
// well as by wheel. Leaving follow off there froze the log at the bottom.
func TestScrollingToTheBottomResumesFollowing(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 120, 24, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	m.focus = focusLogs
	for i := 0; i < 200; i++ {
		m.logs = append(m.logs, logLine{ts: time.Now(), prio: 6, msg: "line"})
	}

	if !m.logFollow {
		t.Fatal("should start out following")
	}
	m.logKey("up")
	if m.logFollow || m.logScroll == 0 {
		t.Fatalf("scrolling up should stop following: follow=%v scroll=%d", m.logFollow, m.logScroll)
	}
	m.logKey("pgup")
	if m.logFollow {
		t.Error("paging up should stop following")
	}

	// Walk back down one line at a time; arriving at the end resumes follow.
	for i := 0; i < 500 && m.logScroll > 0; i++ {
		m.logKey("down")
	}
	if m.logScroll != 0 {
		t.Fatalf("never reached the bottom: scroll=%d", m.logScroll)
	}
	if !m.logFollow {
		t.Error("reaching the bottom with 'down' did not resume following")
	}

	// The wheel behaves identically.
	m.logKey("pgup")
	if m.logFollow {
		t.Fatal("paging up should stop following")
	}
	for i := 0; i < 500 && m.logScroll > 0; i++ {
		m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, X: m.tableWidth() + 4})
	}
	if !m.logFollow {
		t.Error("wheeling to the bottom did not resume following")
	}

	// F jumps to the top and stops following; end returns to the live end.
	m.logKey("F")
	if m.logFollow || m.logScroll == 0 {
		t.Errorf("F should go to the top and stop following: follow=%v scroll=%d", m.logFollow, m.logScroll)
	}
	m.logKey("end")
	if !m.logFollow || m.logScroll != 0 {
		t.Errorf("end should return to the live end: follow=%v scroll=%d", m.logFollow, m.logScroll)
	}

	// A log shorter than the pane has nowhere to scroll, so follow stays on.
	m.logs = m.logs[:2]
	m.logKey("up")
	if !m.logFollow {
		t.Error("a log that fits on screen should keep following")
	}
}

func detailUnit() Unit {
	return Unit{
		Name: "caddy.service", Desc: "Caddy web server", Slice: "system.slice",
		Active: "active", Sub: "running", Type: "notify", FileState: "enabled",
		RestartPol: "on-failure", User: "caddy", TriggeredBy: "caddy.socket",
		StatusText: "serving 4 sites", ExecStart: "caddy run --config /etc/caddy/conf",
		Fragment: "/etc/systemd/system/caddy.service",
		MainPID:  1314, NRestarts: 2, Tasks: 22, MemCurrent: 76 << 20, MemMax: 512 << 20,
		CPUPct: 18.9, HasRates: true, ActiveSince: time.Now().Add(-2 * time.Hour),
	}
}

// The detail block should answer the questions you open a service to ask.
func TestUnitDetailShowsConfiguration(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 40, true
	m.connected = true
	m.fullView = true

	got := strings.Join(m.unitDetail(detailUnit(), 140), "\n")
	for _, want := range []string{
		"caddy", "running", "Caddy web server", // identity
		"pid 1314", "restarts 2", "tasks 22", // lifecycle
		"18.9%", "76M", // live
		"type notify", "enabled", "restart on-failure", "user caddy", // configuration
		"triggered by caddy.socket",
		"serving 4 sites",                   // what it says about itself
		"caddy run --config",                // what it runs
		"/etc/systemd/system/caddy.service", // where it is defined
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail is missing %q:\n%s", want, got)
		}
	}

	// A default Restart=no is not worth a line, nor is the default slice.
	plain := detailUnit()
	plain.RestartPol, plain.Slice, plain.User = "no", "system.slice", ""
	quiet := strings.Join(m.unitDetail(plain, 140), "\n")
	for _, noise := range []string{"restart no", "slice system", "user "} {
		if strings.Contains(quiet, noise) {
			t.Errorf("detail should not spell out the default %q:\n%s", noise, quiet)
		}
	}
}

// Every line must fit its pane, and the pane must get exactly the number of
// lines the geometry promised.
func TestUnitDetailFitsItsPane(t *testing.T) {
	for _, tc := range []struct{ w, h int }{{140, 40}, {100, 24}, {90, 18}, {84, 12}} {
		for _, full := range []bool{false, true} {
			m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
			m.width, m.height, m.ready = tc.w, tc.h, true
			m.connected = true
			m.fullView = full
			m.units = []Unit{detailUnit()}
			m.rebuild()

			w := m.logPaneWidth()
			for i, line := range m.unitDetail(detailUnit(), w) {
				if lipglossWidth(line) > w {
					t.Errorf("%dx%d full=%v: detail line %d is %d wide, pane is %d",
						tc.w, tc.h, full, i, lipglossWidth(line), w)
				}
			}

			pane := m.viewLogPane(w, m.contentHeight())
			if len(pane) < m.detailHeight() {
				t.Errorf("%dx%d full=%v: pane has %d lines, detail alone needs %d",
					tc.w, tc.h, full, len(pane), m.detailHeight())
			}
			// The rule sits immediately under the detail block.
			if rule := pane[m.detailHeight()-1]; !strings.Contains(stripANSI(rule), "─") {
				t.Errorf("%dx%d full=%v: no rule under the detail block, got %q",
					tc.w, tc.h, full, stripANSI(rule))
			}
			if n := strings.Count(m.View(), "\n"); n != tc.h-1 {
				t.Errorf("%dx%d full=%v: frame is %d lines", tc.w, tc.h, full, n+1)
			}
		}
	}
}

// A narrow pane cannot fit the lifecycle facts beside the name; they must not
// simply disappear.
func TestNarrowPaneKeepsTheLifecycleLine(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 130, 32, true
	m.connected = true

	narrow := strings.Join(m.unitDetail(detailUnit(), 55), "\n")
	for _, want := range []string{"pid 1314", "up 2h00m", "tasks 22"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow pane lost %q:\n%s", want, narrow)
		}
	}
	wide := strings.Join(m.unitDetail(detailUnit(), 130), "\n")
	if !strings.Contains(wide, "pid 1314") {
		t.Errorf("wide pane lost the lifecycle facts:\n%s", wide)
	}
}

// TasksMax defaults to tens of thousands, which says nothing.
func TestTasksLimitOnlyShownWhenItMeansSomething(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")

	def := detailUnit()
	def.Tasks, def.TasksLimit = 22, 35789 // the systemd default
	if got := strings.Join(m.unitStats(def), " "); strings.Contains(got, "35789") {
		t.Errorf("the default TasksMax should stay quiet: %s", got)
	}

	tight := detailUnit()
	tight.Tasks, tight.TasksLimit = 22, 64 // deliberately lowered
	if got := strings.Join(m.unitStats(tight), " "); !strings.Contains(got, "22/64") {
		t.Errorf("a configured TasksMax should be shown: %s", got)
	}

	near := detailUnit()
	near.Tasks, near.TasksLimit = 34000, 35789 // about to hit it
	if got := strings.Join(m.unitStats(near), " "); !strings.Contains(got, "/35789") {
		t.Errorf("a limit being approached should be shown: %s", got)
	}

	// MemoryMax=infinity parses as unset and must not print.
	inf := detailUnit()
	inf.MemMax = unsetU64
	if got := m.unitLive(inf); strings.Contains(got, "/") && strings.Contains(got, "mem") {
		if strings.Contains(strings.SplitN(got, "·", 3)[1], "/") {
			t.Errorf("an unset MemoryMax should not print a fraction: %s", got)
		}
	}
}

func escKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEsc} }

// The help must fit the screen it is drawn on. In two columns a row that runs
// past its half wraps, and a wrapped line pushes everything below it down —
// which is how the last group ends up off the bottom.
func TestHelpFitsTheScreen(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}, {140, 30}, {150, 34}, {200, 50}} {
		m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m.width, m.height, m.ready = size[0], size[1], true
		m.connected = true
		m.units = testUnits()
		m.rebuild()
		m.help = true

		// Every line, not only the ones currently in the window.
		for i, l := range m.helpLines() {
			if w := lipglossWidth(l); w > m.width {
				t.Errorf("%dx%d: help line %d is %d wide: %q",
					size[0], size[1], i, w, stripANSI(l))
			}
		}

		lines := m.viewHelp()
		if len(lines) != m.contentHeight() {
			t.Errorf("%dx%d: help is %d lines, content area is %d",
				size[0], size[1], len(lines), m.contentHeight())
		}
		for i, l := range lines {
			if w := lipglossWidth(l); w > m.width {
				t.Errorf("%dx%d: help line %d is %d wide: %q",
					size[0], size[1], i, w, stripANSI(l))
			}
		}
		// Quit is the last row of the last group; losing it is losing the way
		// out. If it does not fit, the help must say there is more and scrolling
		// must reach it.
		shown := stripANSI(strings.Join(lines, "\n"))
		if !strings.Contains(shown, "quit") {
			if !strings.Contains(shown, "more below") {
				t.Errorf("%dx%d: the last group is off screen and unannounced", size[0], size[1])
			}
			m.helpKey("end")
			if !strings.Contains(stripANSI(strings.Join(m.viewHelp(), "\n")), "quit") {
				t.Errorf("%dx%d: scrolling to the end does not reach the last group", size[0], size[1])
			}
		}
	}
}
