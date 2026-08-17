package main

import (
	"strings"
	"unicode/utf8"
)

// Nothing that comes off another machine is safe to put on a terminal as it
// stands. A journal message is arbitrary bytes: a unit whose output goes to a
// serial console leaves carriage returns in it, a boot log arrives with
// embedded newlines, and any service at all can write escape sequences into
// its own log. Rendered raw, those move the cursor, repaint the screen, leave
// a colour set for everything drawn afterwards, and make every width
// calculation wrong — one console log tore the pane's box apart and painted
// the rest of the screen with its own background.
//
// So every string from outside — journal fields, systemd property values —
// passes through here first, before anything measures or renders it.
//
//   - escape sequences are dropped whole, not just the ESC
//   - other C0 controls and DEL become their Unicode pictures (␀ ␇ ␡), so the
//     line says what was there rather than quietly losing it
//   - carriage returns are line breaks, which is what they meant
//   - tabs become four spaces, as the log pane already assumed
//   - invalid UTF-8 and the C1 range become U+FFFD
//
// sanitizeText additionally flattens newlines: it is for the fields that get
// one line each — a description, a status, a command.
func sanitizeText(s string) string { return sanitize(s, false) }

// sanitizeMessage keeps newlines, so a multi-line log entry stays multi-line.
func sanitizeMessage(s string) string { return sanitize(s, true) }

func sanitize(s string, keepNewlines bool) string {
	if isPlain(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
			i++
		case r == 0x1b:
			i += escapeLen(s[i:])
		case r == '\t':
			b.WriteString("    ")
			i += size
		case r == '\r':
			// A lone CR meant "start this line again"; CRLF is just a line
			// ending. Either way the next thing belongs on its own line.
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
				continue
			}
			b.WriteString(newlineOr(keepNewlines))
			i += size
		case r == '\n':
			b.WriteString(newlineOr(keepNewlines))
			i += size
		case r < 0x20:
			b.WriteRune(0x2400 + r) // ␀ … ␟
			i += size
		case r == 0x7f:
			b.WriteRune(0x2421) // ␡
			i += size
		case r >= 0x80 && r <= 0x9f:
			b.WriteRune(utf8.RuneError)
			i += size
		default:
			b.WriteRune(r)
			i += size
		}
	}
	return b.String()
}

func newlineOr(keep bool) string {
	if keep {
		return "\n"
	}
	return " "
}

// isPlain is the common case — printable ASCII and ordinary UTF-8 — and says
// so without allocating.
func isPlain(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c >= 0x80 {
			return false
		}
	}
	return true
}

// escapeLen measures the escape sequence starting at s[0] == ESC, so the whole
// thing can be dropped. Leaving the payload behind would be worse than leaving
// the ESC: "\e[31m" would render as "[31m" in the middle of a log line.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[': // CSI: parameters, then intermediates, then one final byte
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) {
			i++ // the final byte
		}
		return i
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: run to a terminator
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 { // BEL
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' { // ST
				return i + 2
			}
		}
		return len(s)
	}
	// Everything else is ESC, any number of intermediates, and one final byte.
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) {
		i++
	}
	return i
}
