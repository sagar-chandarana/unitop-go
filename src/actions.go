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

		name, args := actionCommand(a, unit, useSudo)
		out, err := r.command(ctx, name, args...).CombinedOutput()
		// systemctl's output ends up in the toast; it is the far end's, so it
		// goes through sanitize like everything else from there.
		return actionResult{unit: unit, label: a.label, err: err,
			out: sanitizeText(strings.TrimSpace(string(out)))}
	}
}

// actionCommand builds the argv for one context-menu action. The
// --no-ask-password sits immediately after systemctl and backs the promise
// above: without it systemctl can summon a polkit password agent on the
// terminal and take the TUI over. sudo -n stays for the same reason — the
// sudo path must fail rather than prompt, too. The runner receives this
// argv unchanged whether it executes locally or over ssh.
func actionCommand(a action, unit string, useSudo bool) (string, []string) {
	args := append(append([]string{"--no-ask-password"}, a.args...), unit)
	if useSudo {
		return "sudo", append([]string{"-n", "systemctl"}, args...)
	}
	return "systemctl", args
}

func (m *model) applyActionResult(res actionResult) tea.Cmd {
	m.toastSeq++
	if res.err != nil {
		msg := res.out
		if msg == "" {
			msg = sanitizeText(res.err.Error())
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
	case "home":
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
	// Keep the popup inside the pane's box. Overrunning it breaks the outline
	// and lands on the footer, which reads as a rendering fault rather than as
	// a popup.
	h := len(unitActions) + 2
	w := min(menuWidth(unit), max(8, m.width-2))
	if x+w > m.width-1 {
		x = max(1, m.width-1-w)
	}
	top, bottom := m.headerLines()+1, m.headerLines()+1+m.paneInner()
	if y+h > bottom {
		y = max(top, bottom-h)
	}
	m.menu = ctxMenu{open: true, unit: unit, x: x, y: y}
}

// menuBoxWidth is how wide the popup is actually drawn — the one answer the
// box, the anchor and the hit-test all use, so a click lands where the frame
// is. Capping it in only one of the three put the edge somewhere else.
func (m model) menuBoxWidth() int {
	return min(menuWidth(m.menu.unit), max(8, m.width-2))
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
