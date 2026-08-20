package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func viewportModel(t *testing.T, w, h int) *model {
	t.Helper()
	m := actionModel(t)
	m.width, m.height = w, h
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// Every action becomes VISIBLY selected before Enter can execute it, at
// every supported height: the viewport follows the cursor, and the row the
// keyboard is on is always on screen.
func TestEveryActionVisibleBeforeExecuteAtAllHeights(t *testing.T) {
	for h := 10; h <= 17; h++ {
		m := viewportModel(t, 100, h)
		m.handleKey(keyOf("x"))
		if !m.menu.open {
			t.Fatalf("h=%d: menu did not open", h)
		}
		for i := 0; i < len(unitActions); i++ {
			label := unitActions[m.menu.cursor].label
			if row := selectedMenuRow(t, m); !strings.Contains(row, "│ "+label) {
				t.Fatalf("h=%d: selected action %q is not on its drawn row: %q", h, label, row)
			}
			x, y, _, first, visible := m.menuGeometry()
			if m.menu.cursor < first || m.menu.cursor >= first+visible {
				t.Fatalf("h=%d: cursor %d outside viewport [%d,%d)", h, m.menu.cursor, first, first+visible)
			}
			top, bottom := m.headerLines()+1, m.headerLines()+1+m.paneInner()
			if y < top || y+visible+2 > bottom || x < 1 {
				t.Fatalf("h=%d: popup outside the pane: x=%d y=%d rows=%d", h, x, y, visible+2)
			}
			m.handleKey(keyOf("down"))
		}
	}
}

// Wrap, home and end all land the cursor somewhere visible.
func TestMenuWrapHomeEndStayVisible(t *testing.T) {
	m := viewportModel(t, 100, 10) // five pane rows: the tightest supported case
	m.handleKey(keyOf("x"))

	for _, k := range []string{"up", "home", "end"} { // wrap, then both ends
		m.handleKey(keyOf(k))
		label := unitActions[m.menu.cursor].label
		if row := selectedMenuRow(t, m); !strings.Contains(row, "│ "+label) {
			t.Errorf("%s selection %q not on its drawn row: %q", k, label, row)
		}
	}
}

// The mouse maps through the viewport offset: a click on the k-th drawn row
// answers the k-th VISIBLE action, and a click below the box answers none.
func TestMouseMapsThroughTheViewport(t *testing.T) {
	m := viewportModel(t, 100, 10)
	m.handleKey(keyOf("x"))
	m.handleKey(keyOf("end")) // slide the viewport to the bottom of the list
	x, y, _, first, visible := m.menuGeometry()
	if first == 0 {
		t.Fatal("the fixture did not scroll the viewport")
	}
	for k := 0; k < visible; k++ {
		if got := m.menuItemAt(x+1, y+1+k); got != first+k {
			t.Errorf("click row %d = action %d, want %d", k, got, first+k)
		}
	}
	if got := m.menuItemAt(x+1, y+1+visible); got != -1 {
		t.Errorf("a click on the border hit action %d", got)
	}
	if got := m.menuItemAt(x+1, y+visible+2); got != -1 {
		t.Errorf("a click below the box hit action %d", got)
	}
}

// Resizes in every direction keep the popup and its selection on screen:
// the geometry is computed fresh, so nothing stored can go stale.
func TestMenuSurvivesResizeInEveryDirection(t *testing.T) {
	m := viewportModel(t, 140, 30)
	m.handleKey(keyOf("x"))
	m.handleKey(keyOf("end"))
	for _, size := range [][2]int{{100, 12}, {40, 10}, {200, 40}, {84, 11}, {40, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if !m.menu.open {
			t.Fatalf("resize to %v closed the menu", size)
		}
		label := unitActions[m.menu.cursor].label
		if row := selectedMenuRow(t, m); !strings.Contains(row, "│ "+label) {
			t.Errorf("after resize to %v the selection %q is not on its drawn row: %q", size, label, row)
		}
		x, y, w, _, visible := m.menuGeometry()
		top, bottom := m.headerLines()+1, m.headerLines()+1+m.paneInner()
		if x < 1 || x+w > m.width-1 || y < top || y+visible+2 > bottom {
			t.Errorf("after resize to %v the popup escapes: x=%d y=%d w=%d rows=%d", size, x, y, w, visible+2)
		}
	}
}

// The confirmation's dynamic centring is CORRECT today; this pins that the
// viewport refactor keeps it that way: open a destructive action's
// confirmation, resize every direction, and the four-row box stays centred,
// fully on screen, with the same action still pending.
func TestConfirmationSurvivesResizeCentred(t *testing.T) {
	m := viewportModel(t, 140, 30)
	m.handleKey(keyOf("x"))
	m.handleKey(keyOf("down")) // "stop", which asks first
	pending := m.menu.cursor
	m.handleKey(keyOf("\r"))
	if !m.menu.confirm {
		t.Fatal("the destructive action did not ask")
	}
	for _, size := range [][2]int{{100, 12}, {40, 10}, {200, 40}, {84, 11}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if !m.menu.confirm || m.menu.cursor != pending {
			t.Fatalf("resize to %v disturbed the pending confirmation", size)
		}
		// Expected geometry, computed exactly as overlayMenu computes it.
		box := m.confirmBox()
		bw := lipglossWidth(box[0])
		wantX := max(0, (m.width-bw)/2)
		wantY := max(0, m.height/2-len(box)/2)

		rows := strings.Split(stripANSI(m.View()), "\n")
		if wantY+3 >= len(rows) {
			t.Fatalf("resize to %v: the four-row box does not fit rows 0..%d", size, len(rows)-1)
		}
		// Top and bottom frame rows start at the expected column, cell-aware.
		for _, ri := range []struct {
			idx    int
			corner string
		}{{wantY, "╭"}, {wantY + 3, "╰"}} {
			line := rows[ri.idx]
			cut := strings.Index(line, ri.corner)
			if cut < 0 {
				t.Fatalf("resize to %v: row %d lacks its %s frame: %q", size, ri.idx, ri.corner, line)
			}
			if got := lipglossWidth(line[:cut]); got != wantX {
				t.Errorf("resize to %v: frame begins at cell %d, want %d", size, got, wantX)
			}
		}
		// The body row names the pending action; the hint row is beneath it.
		if body := rows[wantY+1]; !strings.Contains(body, unitActions[pending].label+" ") ||
			!strings.Contains(body, "?") {
			t.Errorf("resize to %v: the body row lost the pending action: %q", size, body)
		}
		if hint := rows[wantY+2]; !strings.Contains(hint, "y = yes") {
			t.Errorf("resize to %v: the hint row is missing: %q", size, hint)
		}
	}
}

// A hostile stored anchor — the popup opened by a click at the very
// bottom-right — then a hard shrink: the previously confirmed staleness
// mechanism. The dynamic geometry must recompute, not remember.
func TestHostileAnchorSurvivesShrink(t *testing.T) {
	m := viewportModel(t, 200, 40)
	m.openMenu("nginx.service", m.width-2, m.height-2)
	if !m.menu.open {
		t.Fatal("menu did not open")
	}
	m.handleKey(keyOf("end"))
	for _, size := range [][2]int{{100, 12}, {40, 10}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		x, y, w, first, visible := m.menuGeometry()
		top, bottom := m.headerLines()+1, m.headerLines()+1+m.paneInner()
		if x < 1 || x+w > m.width-1 || y < top || y+visible+2 > bottom {
			t.Errorf("shrink to %v left the hostile anchor astray: x=%d y=%d w=%d rows=%d", size, x, y, w, visible+2)
		}
		if m.menu.cursor < first || m.menu.cursor >= first+visible {
			t.Errorf("shrink to %v lost the selection from the viewport", size)
		}
		if got := selectedMenuRow(t, m); !strings.Contains(got, "│ "+unitActions[m.menu.cursor].label) {
			t.Errorf("shrink to %v: the drawn selected row is %q", size, got)
		}
	}
}

// selectedMenuRow returns the drawn frame line the menu cursor sits on —
// the exact row, so "start" can never pass by matching inside "restart".
func selectedMenuRow(t *testing.T, m *model) string {
	t.Helper()
	_, y, _, first, visible := m.menuGeometry()
	rows := strings.Split(stripANSI(m.View()), "\n")
	idx := y + 1 + (m.menu.cursor - first)
	if idx < 0 || idx >= len(rows) {
		t.Fatalf("selected row index %d outside the %d-row frame", idx, len(rows))
	}
	if m.menu.cursor < first || m.menu.cursor >= first+visible {
		t.Fatalf("cursor %d outside viewport [%d,%d)", m.menu.cursor, first, first+visible)
	}
	return rows[idx]
}
