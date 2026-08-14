package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func actionModel(t *testing.T) *model {
	t.Helper()
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = testUnits()
	m.rebuild()
	return &m
}

// x opens the action menu, and only on a unit — a slice has nothing to start
// or stop.
func TestMenuOpensOnUnitsOnly(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, true, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = treeUnits()
	m.rebuild()

	m.cursor = 0 // root slice
	m.afterCursorMove()
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.menu.open {
		t.Error("x on a slice must not open the action menu")
	}

	for i, r := range m.rows {
		if r.kind == rowUnit {
			m.cursor = i
			break
		}
	}
	m.afterCursorMove()
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !m.menu.open {
		t.Fatal("x on a unit should open the action menu")
	}
	if m.menu.unit == "" || !strings.HasSuffix(m.menu.unit, ".service") {
		t.Errorf("menu opened on %q", m.menu.unit)
	}
	if m.fullView {
		t.Error("x should not also open the full view")
	}
}

func TestMenuConfirmsDestructiveActions(t *testing.T) {
	m := actionModel(t)
	m.openMenu("nginx.service", 0, 0)

	// Walk to "stop", which is flagged as needing confirmation.
	for i, a := range unitActions {
		if a.label == "stop" {
			m.menu.cursor = i
		}
	}
	handled, cmd := m.menuKey("enter")
	if !handled {
		t.Fatal("menu did not handle enter")
	}
	if cmd != nil {
		t.Error("stop ran without confirmation")
	}
	if !m.menu.confirm {
		t.Fatal("stop did not raise a confirmation")
	}
	// Anything other than y cancels, leaving the menu open.
	if _, cmd := m.menuKey("n"); cmd != nil {
		t.Error("cancelling still ran the action")
	}
	if m.menu.confirm {
		t.Error("cancel did not dismiss the confirmation")
	}
	if _, cmd := m.menuKey("enter"); cmd != nil || !m.menu.confirm {
		t.Fatal("re-arming the confirmation failed")
	}
	if _, cmd := m.menuKey("y"); cmd == nil {
		t.Error("y did not run the action")
	}
	if m.menu.open {
		t.Error("menu should close once the action runs")
	}
}

func TestMenuRunsSafeActionsDirectly(t *testing.T) {
	m := actionModel(t)
	m.openMenu("nginx.service", 0, 0)
	for i, a := range unitActions {
		if a.label == "start" {
			m.menu.cursor = i
		}
	}
	_, cmd := m.menuKey("enter")
	if cmd == nil {
		t.Error("start should run without a confirmation step")
	}
	if m.menu.confirm {
		t.Error("start should not raise a confirmation")
	}
}

func TestReadOnlyBlocksTheMenu(t *testing.T) {
	m := actionModel(t)
	m.readOnly = true
	m.openMenu("nginx.service", 0, 0)
	if m.menu.open {
		t.Error("read-only mode still opened the action menu")
	}
	if !strings.Contains(m.toast, "read-only") {
		t.Errorf("read-only refusal not explained to the user: %q", m.toast)
	}
}

func TestMenuStaysOnScreen(t *testing.T) {
	m := actionModel(t)
	m.openMenu("nginx.service", m.width-2, m.height-2)
	w := menuWidth("nginx.service")
	if m.menu.x+w > m.width {
		t.Errorf("menu runs off the right edge: x=%d w=%d width=%d", m.menu.x, w, m.width)
	}
	if m.menu.y+len(unitActions)+2 > m.height {
		t.Errorf("menu runs off the bottom: y=%d height=%d", m.menu.y, m.height)
	}
}

// Template and device-mapped units have very long names; the popup must stay a
// popup rather than stretching across the screen.
func TestMenuWidthIsCapped(t *testing.T) {
	long := "systemd-fsck@dev-disk-by-partlabel-disk-_dev_nvme0n1-esp.service"
	if w := menuWidth(long); w > menuMaxWidth {
		t.Errorf("menuWidth(long) = %d, want <= %d", w, menuMaxWidth)
	}
	// A short name still gets a box wide enough for the longest action label.
	if w := menuWidth("a.service"); w < len("reload-or-restart")+4 {
		t.Errorf("menuWidth(short) = %d, too narrow for the action labels", w)
	}

	m := actionModel(t)
	m.openMenu(long, 0, 0)
	for i, line := range m.menuBox() {
		if got := lipglossWidth(line); got != menuWidth(long) {
			t.Errorf("menu line %d is %d wide, want %d", i, got, menuWidth(long))
		}
	}
}

func TestMenuItemAt(t *testing.T) {
	m := actionModel(t)
	m.openMenu("nginx.service", 10, 4)
	if got := m.menuItemAt(12, 5); got != 0 {
		t.Errorf("first item = %d", got)
	}
	if got := m.menuItemAt(12, 4); got != -1 {
		t.Errorf("the title row is not an item, got %d", got)
	}
	if got := m.menuItemAt(2, 5); got != -1 {
		t.Errorf("a click left of the box is not an item, got %d", got)
	}
	if got := m.menuItemAt(12, 5+len(unitActions)); got != -1 {
		t.Errorf("the bottom border is not an item, got %d", got)
	}
}

// A popup covers its own width and nothing more — the table must still be
// readable on both sides of it.
func TestMenuOverlayKeepsTheRestOfTheRow(t *testing.T) {
	m := actionModel(t)
	m.width, m.height = 140, 30
	m.rebuild()

	before := strings.Split(m.View(), "\n")
	m.openMenu("nginx.service", 20, 6)
	after := strings.Split(m.View(), "\n")

	w := menuWidth("nginx.service")
	covered := 0
	for i := range after {
		if lipglossWidth(after[i]) > m.width {
			t.Fatalf("line %d grew to %d columns", i, lipglossWidth(after[i]))
		}
		if i < m.menu.y || i >= m.menu.y+len(m.menuBox()) {
			if after[i] != before[i] {
				t.Errorf("line %d changed but the popup does not reach it", i)
			}
			continue
		}
		covered++
		// What lay left of and right of the popup must survive it.
		left, right := stripANSI(sliceANSI(before[i], 0)), stripANSI(sliceANSI(before[i], 20+w))
		gotLeft := stripANSI(sliceANSI(after[i], 0))
		gotRight := stripANSI(sliceANSI(after[i], 20+w))
		if len(strings.TrimSpace(left)) > 0 && !strings.HasPrefix(gotLeft, left[:20]) {
			t.Errorf("line %d lost the text left of the popup", i)
		}
		if s := strings.TrimSpace(right); s != "" && strings.TrimSpace(gotRight) != s {
			t.Errorf("line %d lost the text right of the popup:\n  want %q\n  got  %q", i, s, gotRight)
		}
	}
	if covered == 0 {
		t.Fatal("the popup covered no rows")
	}
}

func TestSliceANSI(t *testing.T) {
	plain := "abcdefghij"
	if got := stripANSI(sliceANSI(plain, 4)); got != "efghij" {
		t.Errorf("sliceANSI(plain,4) = %q", got)
	}
	if got := sliceANSI(plain, 99); got != "" {
		t.Errorf("slicing past the end should give nothing, got %q", got)
	}
	// Styling in effect at the cut must be carried over. Written out by hand:
	// lipgloss renders plain when there is no terminal, as under `go test`.
	styled := "\x1b[31mabc\x1b[1mdef\x1b[0m"
	got := sliceANSI(styled, 3)
	if stripANSI(got) != "def" {
		t.Errorf("visible tail = %q", stripANSI(got))
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("tail lost the colour that was in effect: %q", got)
	}
	if !strings.Contains(got, "\x1b[1m") {
		t.Errorf("tail lost a style set at the cut: %q", got)
	}
	// Trailing padding is not worth carrying.
	if got := sliceANSI("abc      ", 3); got != "" {
		t.Errorf("whitespace tail should be dropped, got %q", got)
	}
}

func TestMenuOverlayKeepsFrameSize(t *testing.T) {
	m := actionModel(t)
	m.openMenu("nginx.service", 6, 5)
	out := m.View()
	if n := strings.Count(out, "\n"); n != m.height-1 {
		t.Errorf("overlay changed the frame height: %d lines", n+1)
	}
	if !strings.Contains(out, "kill (SIGKILL)") {
		t.Error("menu not drawn")
	}
	m.menu.confirm = true
	m.menu.cursor = 1 // stop
	out = m.View()
	if !strings.Contains(out, "stop nginx?") {
		t.Errorf("confirmation not drawn:\n%s", out)
	}
}

func TestActionResultToast(t *testing.T) {
	m := actionModel(t)
	m.applyActionResult(actionResult{unit: "nginx.service", label: "restart"})
	if m.toastErr || !strings.Contains(m.toast, "restart nginx ok") {
		t.Errorf("success toast = %q", m.toast)
	}
	m.applyActionResult(actionResult{
		unit: "nginx.service", label: "stop", err: errors.New("exit 1"),
		out: "Interactive authentication required.",
	})
	if !m.toastErr {
		t.Error("failure was not flagged")
	}
	if !strings.Contains(m.toast, "-sudo") {
		t.Errorf("polkit failure should point at -sudo: %q", m.toast)
	}
}

func TestActionArgsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range unitActions {
		if len(a.args) == 0 {
			t.Errorf("%s has no systemctl verb", a.label)
		}
		if seen[a.label] {
			t.Errorf("duplicate action %q", a.label)
		}
		seen[a.label] = true
	}
	for _, want := range []string{"start", "stop", "restart", "kill (SIGTERM)", "kill (SIGKILL)", "reset-failed"} {
		if !seen[want] {
			t.Errorf("missing action %q", want)
		}
	}
}

// With no table on screen there is no row to point at, so the popup must land
// somewhere predictable rather than wherever the hidden cursor happens to be.
func TestFullViewMenuAnchorIsStable(t *testing.T) {
	m := actionModel(t)
	m.units = append(m.units, Unit{Name: "extra.service", Active: "active", Sub: "running",
		Slice: "system.slice", MemCurrent: 1 << 20, Tasks: 1, NRestarts: 0})
	m.rebuild()
	m.activateRow()
	if !m.fullView {
		t.Fatal("not in the full view")
	}

	seen := map[int]bool{}
	for _, cursor := range []int{0, 1, 2} {
		if cursor >= len(m.rows) {
			continue
		}
		m.cursor = cursor
		x, y := m.menuAnchor()
		seen[y] = true
		if x != 4 {
			t.Errorf("cursor %d: x = %d", cursor, x)
		}
		// It must clear the host block and the unit summary above the log.
		if y < m.headerLines() {
			t.Errorf("cursor %d: popup at y=%d overlaps the host block", cursor, y)
		}
	}
	if len(seen) != 1 {
		t.Errorf("the popup moved with the hidden cursor: rows %v", seen)
	}

	// In the table it still points at the selected row.
	m.fullView = false
	m.cursor, m.topRow = 0, 0
	_, y0 := m.menuAnchor()
	m.cursor = 2
	_, y2 := m.menuAnchor()
	if y2 <= y0 {
		t.Errorf("in the table the popup should follow the row: %d then %d", y0, y2)
	}
}
