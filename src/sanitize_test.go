package main

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeStripsEscapesAndControls(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain is untouched", "nginx: started", "nginx: started"},
		{"csi colour", "\x1b[31mred\x1b[0m", "red"},
		{"csi with parameters", "\x1b[1;33;44mwarn", "warn"},
		{"cursor movement", "a\x1b[2Jb\x1b[Hc", "abc"},
		{"osc title, bel-terminated", "\x1b]0;pwned\x07ok", "ok"},
		{"osc title, st-terminated", "\x1b]0;pwned\x1b\\ok", "ok"},
		{"dcs", "\x1bPq#0;2;0;0;0\x1b\\ok", "ok"},
		{"two-byte escape", "\x1b7saved", "saved"},
		{"charset selection", "\x1b(0lqk", "lqk"},
		{"lone esc at the end", "trailing\x1b", "trailing"},
		{"bare bel becomes a picture", "ring\x07ing", "ring␇ing"},
		{"del becomes a picture", "a\x7fb", "a␡b"},
		{"nul becomes a picture", "a\x00b", "a␀b"},
		{"tabs become spaces", "a\tb", "a    b"},
		{"c1 controls", "ab", "a�b"},
	}
	for _, c := range cases {
		if got := sanitizeText(c.in); got != c.want {
			t.Errorf("%s: sanitizeText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}

	// Invalid UTF-8 survives as a replacement character rather than as a byte
	// the terminal has to guess at.
	if got := sanitizeText("a\xffb"); got != "a�b" {
		t.Errorf("invalid utf-8: %q", got)
	}
}

// Carriage returns are what a serial console leaves behind. They meant "start
// this line again", so they are line breaks — not something to render.
func TestSanitizeHandlesLineBreaks(t *testing.T) {
	if got := sanitizeMessage("Loading Linux\r\nLoading initrd"); got != "Loading Linux\nLoading initrd" {
		t.Errorf("crlf: %q", got)
	}
	if got := sanitizeMessage("50%\r100%"); got != "50%\n100%" {
		t.Errorf("lone cr: %q", got)
	}
	if got := sanitizeMessage("one\ntwo"); got != "one\ntwo" {
		t.Errorf("lf: %q", got)
	}
	// The single-line fields cannot carry a break at all: one newline in a
	// description would put the rest of the table a row lower than it belongs.
	for _, in := range []string{"one\ntwo", "one\r\ntwo", "one\rtwo"} {
		if got := sanitizeText(in); strings.ContainsAny(got, "\n\r") {
			t.Errorf("sanitizeText(%q) = %q, still breaks the line", in, got)
		}
	}
}

// The whole point: a log line cannot escape its pane. A unit that writes
// escape sequences and carriage returns into its own journal used to move the
// cursor, repaint the screen and leave a background colour set for everything
// after it.
func TestLogLinesCannotEscapeThePane(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m.width, m.height, m.ready = 120, 24, true
	m.connected = true
	m.units = testUnits()
	m.rebuild()

	// Built the way a real entry is, so the test exercises the door the bytes
	// actually come through rather than sanitising them itself.
	const esc = "\\u001b" // how journalctl -o json renders the byte
	raw := `{"__REALTIME_TIMESTAMP":"1700000000000000","PRIORITY":"6",` +
		`"SYSLOG_IDENTIFIER":"` + esc + `[7mevil","_PID":"1","MESSAGE":"` +
		esc + `[44m` + esc + `[2J\rLoading Linux\rLoading Linux` +
		esc + `]0;title ` + strings.Repeat("wide ", 60) +
		esc + `[999C done"}`
	line, ok := parseJournalJSON([]byte(raw))
	if !ok {
		t.Fatal("the entry did not parse")
	}
	if strings.ContainsAny(line.msg, "\x1b\r") || strings.ContainsRune(line.ident, 0x1b) {
		t.Fatalf("the parser let the escapes through: msg=%q ident=%q", line.msg, line.ident)
	}
	m.logs = []logLine{line}
	m.logEpoch++

	for i, l := range m.formatLog(m.logs[0], m.logInnerWidth()) {
		if strings.ContainsRune(l, 0x1b) {
			// The prefix and the priority colour are ours; a raw ESC from the
			// message itself is not.
			if strings.Contains(stripANSI(l), "\x1b") {
				t.Errorf("line %d still carries an escape from the message: %q", i, l)
			}
		}
		if strings.ContainsAny(stripANSI(l), "\n\r") {
			t.Errorf("line %d still breaks the line: %q", i, l)
		}
		if w := lipglossWidth(l); w > m.logInnerWidth() {
			t.Errorf("line %d is %d wide, pane is %d: %q", i, w, m.logInnerWidth(), l)
		}
	}

	// And the screen it lands on stays rectangular.
	m.focus = focusLogs
	for i, l := range strings.Split(m.View(), "\n") {
		if w := lipglossWidth(l); w > m.width {
			t.Fatalf("screen line %d is %d wide, terminal is %d", i, w, m.width)
		}
	}
}

// journalctl sometimes prints a plain notice rather than JSON, and a malformed
// entry lands on the same path. It is shown verbatim, so it is cleaned too —
// this was the one door left open after the parsed fields were closed.
func TestNonJSONJournalOutputIsSanitized(t *testing.T) {
	l, ok := parseJournalJSON([]byte("-- Journal begins \x1b[41mhere\x07 --\r\nmore"))
	if !ok {
		t.Fatal("the notice was dropped")
	}
	if strings.ContainsRune(l.msg, 0x1b) || strings.ContainsRune(l.msg, '\r') {
		t.Errorf("the fallback path let it through: %q", l.msg)
	}
	if !strings.Contains(l.msg, "␇") {
		t.Errorf("the bell should be visible, not audible: %q", l.msg)
	}
}

// Free text from systemd goes through the same door.
func TestUnitPropertiesAreSanitized(t *testing.T) {
	units := parseShow("Id=evil.service\nDescription=\x1b[41mred\x1b[0m ground\nActiveState=active\n")
	if len(units) != 1 {
		t.Fatalf("parsed %d units", len(units))
	}
	if got := units[0].Desc; got != "red ground" {
		t.Errorf("description = %q", got)
	}
	// systemd escapes odd bytes in unit names as \xNN; decoding them is how one
	// gets back in.
	if got := unescapeUnit(`bad\x1b\x5b31mname`); strings.ContainsRune(got, 0x1b) {
		t.Errorf("unescapeUnit let an escape back in: %q", got)
	}
}
