package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A bracketed paste is one KeyRunes event carrying whatever was on the
// clipboard: newlines, C0 controls, whole escape sequences. Appended raw it
// repainted the screen and broke every width calculation; sanitized at
// ingress, the editor holds exactly what sanitize.go promises — sequences
// dropped whole, controls as their pictures, newlines flattened, tabs four
// spaces — and ordinary Unicode unharmed.
func TestPasteIsSanitizedInBothEditors(t *testing.T) {
	hostile := tea.KeyMsg{Type: tea.KeyRunes, Paste: true,
		Runes: []rune("one\ntwo\rthree\x1b]0;EVILTITLE\x07four\x1b[31mfive\x07\tsix")}
	unicode := tea.KeyMsg{Type: tea.KeyRunes, Paste: true,
		Runes: []rune(" 東京 héllo")}

	editors := []struct {
		name string
		logs bool
		read func(m *model) string
	}{
		{"unit filter", false, func(m *model) string { return m.filter }},
		{"journal grep", true, func(m *model) string { return m.logFilt.grep }},
	}
	for _, ed := range editors {
		mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
		m := &mm
		m.width, m.height, m.ready, m.connected = 140, 30, true, true
		m.units = testUnits()
		m.rebuild()
		if ed.logs {
			m.focus = focusLogs
		}
		m.handleKey(keyOf("/"))
		m.handleKey(hostile)
		m.handleKey(unicode)

		want := "one two threefourfive␇    six 東京 héllo"
		if got := ed.read(m); got != want {
			t.Errorf("%s = %q, want %q", ed.name, got, want)
		}
		if strings.Contains(ed.read(m), "\x1b") {
			t.Errorf("%s kept a raw escape", ed.name)
		}

		// The frame stays a frame: exactly m.height lines, and the pasted
		// escape sequence appears nowhere in it.
		out := m.View()
		if lines := strings.Split(out, "\n"); len(lines) != m.height {
			t.Errorf("%s: view is %d lines of %d", ed.name, len(lines), m.height)
		}
		if strings.Contains(out, "EVILTITLE") {
			t.Errorf("%s: the pasted OSC payload reached the frame", ed.name)
		}
	}
}

// The -f flag is the same ingress with a different door — and unlike a real
// bracketed paste, whose decoder drops malformed UTF-8 before we see it, a
// flag can carry invalid bytes.
func TestInitialFilterFlagIsSanitized(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false,
		"a\x1b[2Jb\xffc\nmulti")
	if want := "ab�c multi"; m.filter != want {
		t.Errorf("filter = %q, want %q", m.filter, want)
	}
}

// -H (or the local hostname) is rendered on the startup screen, in the
// header, and inside troubleshooting advice. The raw value stays with the
// ssh transport; every screen renders the sanitized label only.
func TestHostLabelNeverReachesTheScreenRaw(t *testing.T) {
	host := "root@evil\x1b[2J.example\x07"
	mm := newModel(newRunner(host), host, time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready = 100, 30, true

	clean := "root@evil.example␇"
	if m.hostLabel != clean {
		t.Fatalf("hostLabel = %q, want %q", m.hostLabel, clean)
	}
	if m.r.host != host {
		t.Fatalf("the transport must keep the raw value exactly: %q", m.r.host)
	}

	// Startup, connecting.
	if out := m.View(); !strings.Contains(out, "connecting to "+clean) || strings.Contains(out, "\x1b[2J") {
		t.Errorf("startup renders the raw host:\n%s", out)
	}

	// Startup, failed — the advice embeds the target in commands to try.
	m.err = "permission denied (publickey)"
	out := m.View()
	if !strings.Contains(out, clean) || strings.Contains(out, "\x1b[2J") {
		t.Errorf("failure screen renders the raw host:\n%s", out)
	}
	advice := strings.Join(troubleshoot(m.err, m.sshTarget()), " ")
	if !strings.Contains(advice, "ssh-copy-id "+clean) || strings.Contains(advice, "\x1b") {
		t.Errorf("advice does not use the sanitized target: %q", advice)
	}

	// Connected header.
	m.err = ""
	m.connected = true
	m.units = testUnits()
	m.rebuild()
	if out := m.View(); !strings.Contains(out, clean) || strings.Contains(out, "\x1b[2J") {
		t.Errorf("header renders the raw host:\n%s", out)
	}

	// A hostname-like local value takes the same door.
	local := newModel(runner{}, "bad\x1b[31mhost", time.Second, sortCPU, false, false, false, "")
	if local.hostLabel != "badhost" {
		t.Errorf("local label = %q, want %q", local.hostLabel, "badhost")
	}
}

// A raw -H that is nothing but a dropped escape sequence sanitizes to an
// empty label. The screens still need a name for the far end, and
// troubleshoot keys remote-ness on the transport, not the label.
func TestAllEscapeHostLabelFallsBackToRemote(t *testing.T) {
	raw := "\x1b[2J"
	m := newModel(newRunner(raw), raw, time.Second, sortCPU, false, false, false, "")
	if m.hostLabel != "remote" {
		t.Errorf("hostLabel = %q, want %q", m.hostLabel, "remote")
	}
	if m.sshTarget() != "remote" {
		t.Errorf("sshTarget = %q, want %q", m.sshTarget(), "remote")
	}
	if m.r.host != raw {
		t.Errorf("the transport must keep the raw value: %q", m.r.host)
	}
	advice := strings.Join(troubleshoot("permission denied (publickey)", m.sshTarget()), " ")
	if !strings.Contains(advice, "ssh-copy-id remote") {
		t.Errorf("advice misclassified the attempt as local: %q", advice)
	}
}
