package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func leakModel(t *testing.T) *model {
	t.Helper()
	// Under `go test` there is no terminal, so lipgloss emits no escapes at all
	// and these checks would pass vacuously.
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 140, 24, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	// Priority 3 is err, which formatLog renders bold — the attribute that was
	// bleeding into the table row underneath.
	for i := 0; i < 40; i++ {
		m.logs = append(m.logs, logLine{ts: time.Now(), prio: 3,
			msg: "pve-firewall.service - Proxmox VE firewall failed to start, and this is long enough to fill the pane"})
	}
	return &m
}

// A line must not inherit anything from the line before it. The trailing reset
// does not always survive the renderer, so each styled line starts with one.
func TestEveryStyledLineStartsClean(t *testing.T) {
	m := leakModel(t)
	out := m.View()
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("no escapes rendered; the check would be vacuous")
	}
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\x1b[") && !strings.HasPrefix(line, "\x1b[0m") {
			t.Errorf("line %d can inherit attributes from the line above:\n  %q", i, line)
		}
	}
}

// And each line should still close what it opens.
func TestNoAttributeLeaksAcrossLines(t *testing.T) {
	m := leakModel(t)
	for i, line := range strings.Split(m.View(), "\n") {
		if open := openAttrs(line); open != "" {
			t.Errorf("line %d ends with %s still set:\n  %q", i, open, line)
		}
	}
}

// openAttrs replays a line's SGR codes and reports anything still in effect at
// the end of it.
func openAttrs(line string) string {
	bold, faint, fg, bg := false, false, false, false
	for _, seq := range sgrCodes(line) {
		for _, p := range strings.Split(seq, ";") {
			switch p {
			case "", "0":
				bold, faint, fg, bg = false, false, false, false
			case "1":
				bold = true
			case "2":
				faint = true
			case "22":
				bold, faint = false, false
			case "39":
				fg = false
			case "49":
				bg = false
			default:
				switch {
				case p >= "30" && p <= "37", p >= "90" && p <= "97":
					fg = true
				case p >= "40" && p <= "47", p >= "100" && p <= "107":
					bg = true
				}
			}
		}
	}
	var open []string
	for _, c := range []struct {
		on bool
		s  string
	}{{bold, "bold"}, {faint, "faint"}, {fg, "a foreground"}, {bg, "a background"}} {
		if c.on {
			open = append(open, c.s)
		}
	}
	return strings.Join(open, " and ")
}

func sgrCodes(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			return out
		}
		s = s[i+2:]
		j := strings.IndexByte(s, 'm')
		if j < 0 {
			return out
		}
		out = append(out, s[:j])
		s = s[j+1:]
	}
}
