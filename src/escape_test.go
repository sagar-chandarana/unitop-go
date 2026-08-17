package main

import (
	"testing"
	"time"
)

func escModel(t *testing.T) *model {
	t.Helper()
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	return &m
}

// esc pops exactly one thing, innermost first, and never reaches past it. The
// order is: the filter editor, the action menu, the help overlay, the focused
// pane's filter, the full view, focus.
func TestEscapePopsOneThingAtATime(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*model)
		want  func(*testing.T, *model)
	}{
		{"the action menu closes", func(m *model) {
			m.openMenu("nginx.service", 4, 4)
		}, func(t *testing.T, m *model) {
			if m.menu.open {
				t.Error("the menu is still open")
			}
		}},
		{"a confirmation returns to the menu, not out of it", func(m *model) {
			m.openMenu("nginx.service", 4, 4)
			m.menu.cursor = 1 // stop, which confirms
			m.menuKey("enter")
		}, func(t *testing.T, m *model) {
			if !m.menu.open || m.menu.confirm {
				t.Errorf("open=%v confirm=%v, want the menu back without its prompt",
					m.menu.open, m.menu.confirm)
			}
		}},
		{"the help overlay closes", func(m *model) {
			m.handleKey(keyOf("?"))
		}, func(t *testing.T, m *model) {
			if m.help {
				t.Error("help is still open")
			}
		}},
		{"the help overlay closes without touching a filter", func(m *model) {
			m.filter = "ngin"
			m.rebuild()
			m.handleKey(keyOf("?"))
		}, func(t *testing.T, m *model) {
			if m.help || m.filter != "ngin" {
				t.Errorf("help=%v filter=%q", m.help, m.filter)
			}
		}},
		{"the table's filter clears when the table has focus", func(m *model) {
			m.filter = "ngin"
			m.rebuild()
		}, func(t *testing.T, m *model) {
			if m.filter != "" {
				t.Errorf("filter = %q", m.filter)
			}
		}},
		{"the log's filter clears when the log has focus", func(m *model) {
			m.focus = focusLogs
			m.logFilt = logFilter{grep: "boom", prio: 3}
		}, func(t *testing.T, m *model) {
			if !m.logFilt.empty() {
				t.Errorf("logFilt = %+v; the title advertises search and level as one thing", m.logFilt)
			}
		}},
		{"the log's filter survives esc in the table", func(m *model) {
			m.logFilt = logFilter{grep: "boom", prio: 3}
		}, func(t *testing.T, m *model) {
			if m.logFilt.grep != "boom" {
				t.Error("esc in the table cleared the log's filter")
			}
		}},
		{"the full view closes", func(m *model) {
			m.activateRow()
		}, func(t *testing.T, m *model) {
			if m.fullView {
				t.Error("still in the full view")
			}
		}},
		{"in the full view the log's filter goes first", func(m *model) {
			m.logFilt.grep = "boom"
			m.activateRow()
		}, func(t *testing.T, m *model) {
			if !m.fullView {
				t.Error("left the full view before clearing the search")
			}
			if m.logFilt.grep != "" {
				t.Error("the search was not cleared")
			}
		}},
		{"in the full view the table's filter is left alone", func(m *model) {
			m.filter = "ngin"
			m.rebuild()
			m.activateRow()
		}, func(t *testing.T, m *model) {
			// It is not on screen; throwing it away here was invisible.
			if m.filter != "ngin" {
				t.Errorf("the full view cleared the table's filter: %q", m.filter)
			}
			if m.fullView {
				t.Error("did not leave the full view")
			}
		}},
		{"focus returns to the table", func(m *model) {
			m.focus = focusLogs
		}, func(t *testing.T, m *model) {
			if m.focus != focusList {
				t.Error("focus did not return to the table")
			}
		}},
		{"nothing pending does nothing", func(m *model) {}, func(t *testing.T, m *model) {
			if m.help || m.fullView || m.focus != focusList || m.filter != "" {
				t.Error("esc changed something with nothing to pop")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := escModel(t)
			c.setup(m)
			m.handleKey(escKey())
			c.want(t, m)
		})
	}
}

// esc in the editor cancels: it puts back what was being amended. Throwing the
// filter away instead meant thinking better of an edit cost you the filter.
func TestEscapeInTheEditorCancels(t *testing.T) {
	m := escModel(t)
	m.filter = "ngin"
	m.rebuild()
	m.handleKey(keyOf("/"))
	m.handleKey(keyOf("x"))
	if m.filter != "nginx" {
		t.Fatalf("the edit did not take: %q", m.filter)
	}
	m.handleKey(escKey())
	if m.filter != "ngin" {
		t.Errorf("esc did not restore the filter being amended: %q", m.filter)
	}
	if m.filterInput {
		t.Error("the editor is still open")
	}

	// The same for the log, and a second esc then clears it — the next thing
	// on the stack.
	m.focus = focusLogs
	m.logFilt.grep = "boom"
	m.handleKey(keyOf("/"))
	m.handleKey(keyOf("x"))
	m.handleKey(escKey())
	if m.logFilt.grep != "boom" {
		t.Errorf("esc did not restore the log search: %q", m.logFilt.grep)
	}
	m.handleKey(escKey())
	if m.logFilt.grep != "" {
		t.Errorf("a second esc did not clear it: %q", m.logFilt.grep)
	}

	// Opening the editor on nothing and escaping leaves nothing behind.
	fresh := escModel(t)
	fresh.handleKey(keyOf("/"))
	fresh.handleKey(keyOf("z"))
	fresh.handleKey(escKey())
	if fresh.filter != "" {
		t.Errorf("a cancelled new filter left %q behind", fresh.filter)
	}
}
