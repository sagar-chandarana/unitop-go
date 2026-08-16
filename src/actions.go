package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// action is one entry of the unit context menu. args are the systemctl
// arguments; the unit name is appended.
type action struct {
	label   string
	args    []string
	confirm bool // ask before running: this one interrupts a running service
}

var unitActions = []action{
	{label: "start", args: []string{"start"}},
	{label: "stop", args: []string{"stop"}, confirm: true},
	{label: "restart", args: []string{"restart"}, confirm: true},
	{label: "reload", args: []string{"reload"}},
	{label: "reload-or-restart", args: []string{"reload-or-restart"}, confirm: true},
	{label: "kill (SIGTERM)", args: []string{"kill", "--signal=SIGTERM"}, confirm: true},
	{label: "kill (SIGKILL)", args: []string{"kill", "--signal=SIGKILL"}, confirm: true},
	{label: "freeze", args: []string{"freeze"}, confirm: true},
	{label: "thaw", args: []string{"thaw"}},
	{label: "reset-failed", args: []string{"reset-failed"}},
}

// ctxMenu is the popup opened by a right-click (or Enter) on a unit row.
type ctxMenu struct {
	open    bool
	confirm bool
	unit    string
	x, y    int
	cursor  int
}

func (c ctxMenu) action() action { return unitActions[c.cursor] }

type actionResult struct {
	unit  string
	label string
	err   error
	out   string
}

type toastExpiredMsg struct{ seq int }

// runAction shells out to systemctl. It never uses interactive polkit: if the
// caller lacks privilege the error is reported in the toast rather than the
// TUI being taken over by an authentication prompt.
func (m *model) runAction(unit string, a action) tea.Cmd {
	r, useSudo := m.r, m.sudo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		name, args := "systemctl", append(append([]string{}, a.args...), unit)
		if useSudo {
			name, args = "sudo", append([]string{"-n", "systemctl"}, args...)
		}
		out, err := r.command(ctx, name, args...).CombinedOutput()
		return actionResult{unit: unit, label: a.label, err: err, out: strings.TrimSpace(string(out))}
	}
}

func (m *model) applyActionResult(res actionResult) tea.Cmd {
	m.toastSeq++
	if res.err != nil {
		msg := res.out
		if msg == "" {
			msg = res.err.Error()
		}
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		if strings.Contains(msg, "Interactive authentication required") && !m.sudo {
			msg += "  (try -sudo, or run as root)"
		}
		m.toast = fmt.Sprintf("%s %s failed: %s", res.label, shortUnit(res.unit), msg)
		m.toastErr = true
	} else {
		m.toast = fmt.Sprintf("%s %s ok", res.label, shortUnit(res.unit))
		m.toastErr = false
	}
	seq := m.toastSeq
	// Re-poll straight away so the table reflects the new state without
	// waiting out the refresh interval.
	var cmds []tea.Cmd
	cmds = append(cmds, tea.Tick(6*time.Second, func(time.Time) tea.Msg { return toastExpiredMsg{seq: seq} }))
	if !m.polling {
		m.polling = true
		cmds = append(cmds, m.pollCmd())
	}
	return tea.Batch(cmds...)
}

// menuKey handles keys while the context menu or its confirmation is open.
func (m *model) menuKey(k string) (bool, tea.Cmd) {
	if !m.menu.open {
		return false, nil
	}
	if m.menu.confirm {
		switch k {
		case "y", "Y", "enter":
			a := m.menu.action()
			unit := m.menu.unit
			m.menu = ctxMenu{}
			return true, m.runAction(unit, a)
		default:
			m.menu.confirm = false
			return true, nil
		}
	}
	switch k {
	case "esc", "q":
		m.menu = ctxMenu{}
	case "up":
		m.menu.cursor = (m.menu.cursor - 1 + len(unitActions)) % len(unitActions)
	case "down":
		m.menu.cursor = (m.menu.cursor + 1) % len(unitActions)
	case "home", "F":
		m.menu.cursor = 0
	case "end":
		m.menu.cursor = len(unitActions) - 1
	case "enter", " ":
		if m.menu.action().confirm {
			m.menu.confirm = true
			return true, nil
		}
		a := m.menu.action()
		unit := m.menu.unit
		m.menu = ctxMenu{}
		return true, m.runAction(unit, a)
	}
	return true, nil
}

// openMenu anchors the popup so it stays on screen near the click.
func (m *model) openMenu(unit string, x, y int) {
	if m.readOnly || unit == "" {
		if m.readOnly {
			m.toastSeq++
			m.toast = "read-only mode: unit actions are disabled"
			m.toastErr = true
		}
		return
	}
	h := len(unitActions) + 2
	w := menuWidth(unit)
	if x+w > m.width {
		x = max(0, m.width-w)
	}
	if y+h > m.height {
		y = max(0, m.height-h)
	}
	m.menu = ctxMenu{open: true, unit: unit, x: x, y: y}
}

// menuMaxWidth keeps the popup a popup. Template and device-mapped units have
// names like systemd-fsck@dev-disk-by-partlabel-… which would otherwise stretch
// the box across the whole screen; menuBox truncates the title to fit.
const menuMaxWidth = 40

func menuWidth(unit string) int {
	w := len([]rune(shortUnit(unit))) + 4
	for _, a := range unitActions {
		if n := len([]rune(a.label)) + 4; n > w {
			w = n
		}
	}
	return min(w, menuMaxWidth)
}
