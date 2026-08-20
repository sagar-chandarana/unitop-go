package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// hostileUnits are the names and descriptions a real fleet actually contains,
// plus the ones that break naive width arithmetic: double-width CJK, emoji,
// combining marks, and a name longer than any pane.
func hostileUnits() []Unit {
	return []Unit{
		{Name: "nginx.service", Desc: "web", Active: "active", Sub: "running", CPUPct: 12.5,
			MemCurrent: 40 << 20, Tasks: 4, MainPID: 42, Slice: "system.slice",
			ActiveSince: time.Now().Add(-time.Hour)},
		{Name: "日本語.service", Desc: "全角文字のサービスです、幅は倍あります", Active: "active",
			Sub: "running", CPUPct: 1, MemCurrent: 1 << 20, Tasks: 1, Slice: "system.slice"},
		{Name: "emoji-🔥🔥🔥.service", Desc: "🚀 launches things 🚀", Active: "failed",
			Sub: "failed", Result: "exit-code", MemCurrent: unsetU64, Tasks: unsetU64,
			NRestarts: 3, Slice: "system.slice"},
		{Name: "combining-éé.service", Desc: "é with a combining acute", Active: "active",
			Sub: "exited", MemCurrent: unsetU64, Tasks: unsetU64, Slice: "system.slice"},
		{Name: "systemd-fsck@dev-disk-by\\x2dpartlabel-disk-_dev_nvme0n1-esp.service",
			Desc: "File System Check on /dev/disk/by-partlabel/…", Active: "inactive",
			Sub: "dead", MemCurrent: unsetU64, Tasks: unsetU64, Slice: "system.slice"},
		{Name: "user@1000.service", Desc: "User Manager for UID 1000", Active: "active",
			Sub: "running", CPUPct: 300.5, MemCurrent: 5 << 30, Tasks: 1443,
			Slice: "user-1000.slice"},
	}
}

func hostileLogs() []logLine {
	now := time.Now()
	return []logLine{
		{ts: now, prio: 6, msg: "ordinary line", ident: "nginx", pid: "42"},
		{ts: now, prio: 3, msg: "日本語のログ行です。これは長い行で、折り返しが必要になります。", ident: "日本語"},
		{ts: now, prio: 4, msg: "🔥 " + strings.Repeat("emoji 🚀 ", 20), ident: "emoji-🔥"},
		{ts: now, prio: 6, msg: strings.Repeat("verylongtokenwithoutspaces", 12)},
		{ts: now, prio: 6, msg: "é́́ combining marks piled up"},
		{ts: now, prio: 7, msg: ""},
	}
}

// screenModel builds a model at a given size with the awkward fixtures loaded.
func screenModel(w, h int) *model {
	m := newModel(runner{}, "server1.local", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = w, h, true
	m.connected = true
	m.units = hostileUnits()
	m.rebuild()
	m.logs = hostileLogs()
	m.logEpoch++
	return &m
}

// checkScreen is the invariant every mode owes the terminal: exactly `height`
// lines, none wider than `width`. A line that overruns wraps, and a wrapped
// line pushes everything below it down a row — which is how a layout stops
// being a layout.
func checkScreen(t *testing.T, what string, m *model) {
	t.Helper()
	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Errorf("%s at %dx%d: %d lines, want %d", what, m.width, m.height, len(lines), m.height)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > m.width {
			t.Errorf("%s at %dx%d: line %d is %d cells wide:\n  %q",
				what, m.width, m.height, i, w, stripANSI(l))
			return // one report per mode is enough to act on
		}
	}
}

func TestEveryModeFitsEverySize(t *testing.T) {
	widths := []int{20, 30, 40, 60, 75, 76, 80, 83, 84, 85, 100, 110, 132, 200, 400}
	heights := []int{5, 8, 12, 19, 20, 21, 24, 30, 50}

	modes := []struct {
		name  string
		setup func(*model)
	}{
		{"table and log", func(m *model) {}},
		{"log focused", func(m *model) { m.focus = focusLogs }},
		{"log hidden", func(m *model) { m.showLogs = false }},
		{"full view", func(m *model) { m.activateRow() }},
		{"tree", func(m *model) { m.tree = true; m.rebuild() }},
		{"tree collapsed", func(m *model) {
			m.tree = true
			m.rebuild()
			for _, r := range m.rows {
				if r.kind == rowSlice {
					m.collapsed[r.slice] = true
				}
			}
			m.rebuild()
		}},
		{"help", func(m *model) { m.help = true }},
		{"help scrolled", func(m *model) { m.help = true; m.helpKey("end") }},
		{"menu", func(m *model) { m.openMenu("nginx.service", 4, 4) }},
		{"menu confirming", func(m *model) {
			m.openMenu("nginx.service", 4, 4)
			m.menu.cursor = 1
			m.menuKey("enter")
		}},
		{"typing a filter", func(m *model) {
			m.handleKey(keyOf("/"))
			for _, r := range "日本語🔥" {
				m.handleKey(keyOf(string(r)))
			}
		}},
		{"unit filter applied", func(m *model) { m.filter = "日本語"; m.rebuild() }},
		{"log filter applied", func(m *model) { m.logFilt = logFilter{grep: "🔥 fire", prio: 3} }},
		{"toast", func(m *model) {
			m.Update(actionResult{unit: "日本語.service", label: "stop",
				err: errNotPermitted, out: "Interactive authentication required"})
		}},
		{"poll error", func(m *model) { m.err = "ssh: connect to host 日本語 port 22: refused" }},
		{"fatal", func(m *model) { m.fatal = true; m.err = "systemd 229 is too old" }},
		{"paused", func(m *model) { m.paused = true }},
		{"no wrap", func(m *model) { m.logWrap = false }},
		{"scrolled back", func(m *model) { m.focus = focusLogs; m.logKey("F") }},
		{"empty table", func(m *model) { m.units = nil; m.rebuild() }},
	}

	for _, w := range widths {
		for _, h := range heights {
			for _, mode := range modes {
				m := screenModel(w, h)
				mode.setup(m)
				checkScreen(t, mode.name, m)
			}
		}
	}
}

// The startup screen owns the whole terminal before the first poll lands, and
// has its own rendering path.
func TestStartupScreenFitsEverySize(t *testing.T) {
	for _, w := range []int{20, 40, 60, 80, 132, 400} {
		for _, h := range []int{5, 8, 12, 24, 50} {
			for _, name := range []string{"connecting", "failed", "fatal"} {
				m := screenModel(w, h)
				m.connected = false
				switch name {
				case "failed":
					m.err = "ssh: connect to host verylonghostname.example.invalid port 22: " +
						"Connection timed out after a while"
					m.attempts = 3
				case "fatal":
					m.err = "systemd 229 on host is older than 251"
					m.fatal = true
				}
				checkScreen(t, "startup/"+name, m)
			}
		}
	}
}

var errNotPermitted = tea.ErrProgramKilled // any non-nil error; the text is what matters

// The backstop in View() guarantees nothing wraps, but reaching it means a line
// was composed too long and got cut. These are the places where being cut looks
// like a fault rather than a trim, so they have to fit on their own.
func TestComposersFitWithoutTheBackstop(t *testing.T) {
	// From minWidth up: below it the too-small notice owns the screen and the
	// keys that would open these are inert.
	for _, w := range []int{minWidth, 45, 60, 75, 80, 100, 200} {
		// The filter editor: what you are typing must stay visible, and the
		// explanation of it must give way rather than push the line over.
		m := screenModel(w, 24)
		m.handleKey(keyOf("/"))
		for _, r := range "日本語の検索文字列" {
			m.handleKey(keyOf(string(r)))
		}
		foot := m.viewFooter()
		if got := ansi.StringWidth(foot); got > w {
			t.Errorf("filter prompt at %d: %d cells: %q", w, got, stripANSI(foot))
		}
		if !strings.Contains(stripANSI(foot), "語") {
			t.Errorf("filter prompt at %d dropped what was typed: %q", w, stripANSI(foot))
		}

		// The popups are frames; a frame missing its right edge reads as broken.
		m = screenModel(w, 24)
		m.openMenu("systemd-fsck@dev-disk-by-partlabel-verylongthing.service", 4, 4)
		for i, l := range m.menuBox() {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("menu line %d at %d: %d cells: %q", i, w, got, stripANSI(l))
				break
			}
		}
		m.menu.cursor = 1
		m.menuKey("enter")
		for i, l := range m.confirmBox() {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("confirm line %d at %d: %d cells: %q", i, w, got, stripANSI(l))
				break
			}
		}
	}
}

// The too-small notice says q quits, so q has to quit — whatever was open when
// the window shrank. With the filter editor up q was a character to type, and
// with the menu up it closed the menu; neither is on screen to be closed.
func TestTooSmallScreenAlwaysQuits(t *testing.T) {
	for _, open := range []struct {
		name  string
		setup func(*model)
	}{
		{"nothing open", func(m *model) {}},
		{"filter editor open", func(m *model) { m.handleKey(keyOf("/")) }},
		{"log search open", func(m *model) { m.focus = focusLogs; m.handleKey(keyOf("/")) }},
		{"menu open", func(m *model) { m.openMenu("nginx.service", 4, 4) }},
		{"menu confirming", func(m *model) {
			m.openMenu("nginx.service", 4, 4)
			m.menu.cursor = 1
			m.menuKey("enter")
		}},
		{"help open", func(m *model) { m.help = true }},
		{"full view", func(m *model) { m.activateRow() }},
		{"not yet connected", func(m *model) { m.connected = false }},
	} {
		t.Run(open.name, func(t *testing.T) {
			// Open it at a usable size, then shrink the window under it.
			m := screenModel(100, 30)
			open.setup(m)
			m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})

			if !strings.Contains(stripANSI(m.View()), "too small") {
				t.Fatal("not showing the too-small notice")
			}
			before := m.filter + "|" + m.logFilt.grep
			_, cmd := m.handleKey(keyOf("q"))
			if cmd == nil {
				t.Fatal("q did not quit")
			}
			if got, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("q produced %T, not a quit", got)
			}
			if after := m.filter + "|" + m.logFilt.grep; after != before {
				t.Errorf("q was typed into a filter instead: %q -> %q", before, after)
			}
		})
	}
}
