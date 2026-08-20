package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func tooSmallModel(t *testing.T) model {
	t.Helper()
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	m.cursor = firstUnitRow(t, &m)
	return m
}

// Below the minimum size the only thing on screen is the too-small notice, and
// handleKey already swallows every key but q/esc. The mouse must be just as
// modal — a click or the wheel would otherwise hit-test the hidden table, menu
// and log. Cover width-only and height-only too-small cases too, so the guard
// cannot cover only one dimension.
func TestTooSmallNoticeSwallowsTheMouse(t *testing.T) {
	for _, sz := range []struct {
		name string
		w, h int
	}{
		{"both dimensions", 30, 8},
		{"width only", 30, 30},
		{"height only", 140, 8},
	} {
		t.Run(sz.name, func(t *testing.T) {
			m := tooSmallModel(t)
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			if !(m.width < minWidth || m.height < minHeight) {
				t.Fatalf("%dx%d is not below the minimum", sz.w, sz.h)
			}
			cursor0 := m.cursor

			press := func(b tea.MouseButton, x, y int) tea.Cmd {
				_, cmd := m.handleMouse(tea.MouseMsg{Button: b, Action: tea.MouseActionPress, X: x, Y: y})
				return cmd
			}
			// Coordinates that mapped to content at the supported size.
			for _, ev := range []struct {
				name string
				b    tea.MouseButton
			}{
				{"right-click", tea.MouseButtonRight},
				{"left-click", tea.MouseButtonLeft},
				{"wheel", tea.MouseButtonWheelDown},
			} {
				if cmd := press(ev.b, 2, 4); cmd != nil {
					t.Errorf("%s under the too-small notice returned a command", ev.name)
				}
				if m.menu.open {
					t.Errorf("%s opened a menu behind the too-small notice", ev.name)
				}
				if m.cursor != cursor0 {
					t.Errorf("%s moved the selection behind the too-small notice", ev.name)
				}
			}
		})
	}
}

// A menu open at a supported size must not be actable by the mouse once the
// terminal shrinks below the minimum: the notice owns input, so a click where a
// menu row was cannot run an action or move the menu.
func TestTooSmallNoticeSwallowsClicksIntoAnOpenMenu(t *testing.T) {
	m := tooSmallModel(t)

	// Open the action menu on the selected unit while the screen is large.
	rowY := m.headerLines() + 3 + m.cursor
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonRight, Action: tea.MouseActionPress, X: 2, Y: rowY})
	if !m.menu.open {
		t.Fatal("the menu did not open at a supported size")
	}
	cursor0, confirm0 := m.menu.cursor, m.menu.confirm

	// Shrink below the minimum, then click where a menu row would have been.
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	_, cmd := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 4, Y: 4})
	if cmd != nil {
		t.Error("a click behind the too-small notice ran a menu action")
	}
	if m.menu.cursor != cursor0 || m.menu.confirm != confirm0 {
		t.Error("a click behind the too-small notice moved the menu")
	}
}
