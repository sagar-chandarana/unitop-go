package main

import (
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
	if m.logPaneWidth() != m.width {
		t.Errorf("log pane is %d wide, want the full %d", m.logPaneWidth(), m.width)
	}
	// The live counters must survive the loss of the table.
	for _, want := range []string{"cpu", "mem", "net", "12.5%", "40M", "pid 42", "restarts 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("full view is missing %q:\n%s", want, out)
		}
	}
	for _, line := range m.viewUnitFull(testUnits()[0], m.width) {
		if lipglossWidth(line) > m.width {
			t.Errorf("full-view header line is %d wide, screen is %d", lipglossWidth(line), m.width)
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

func TestRowAtMatchesRenderedRows(t *testing.T) {
	m := newModel(runner{}, "testhost", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 24, true
	m.units = testUnits()
	m.rebuild()

	first := m.headerLines() + 2
	for i := range m.rows {
		got, ok := m.rowAt(first + i)
		if !ok || got != i {
			t.Fatalf("rowAt(%d) = %d/%v, want %d", first+i, got, ok, i)
		}
	}
	if _, ok := m.rowAt(m.headerLines()); ok {
		t.Error("the column-title line is not a row")
	}
	if _, ok := m.rowAt(m.headerLines() + 1); ok {
		t.Error("the rule under the titles is not a row")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
