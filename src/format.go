package main

import (
	"fmt"
	"strings"
	"time"
)

// unsetU64 is what systemd reports for a counter it is not tracking.
const unsetU64 = ^uint64(0)

func humanBytes(b uint64) string {
	if b == unsetU64 {
		return "-"
	}
	const unit = 1024.0
	v := float64(b)
	if v < unit {
		return fmt.Sprintf("%dB", b)
	}
	units := []string{"K", "M", "G", "T", "P"}
	for _, u := range units {
		v /= unit
		if v < unit {
			if v < 10 {
				return fmt.Sprintf("%.1f%s", v, u)
			}
			return fmt.Sprintf("%.0f%s", v, u)
		}
	}
	return fmt.Sprintf("%.0fP", v)
}

// humanRate is the table form: an idle counter shows as a quiet dot so the eye
// skips it.
func humanRate(bps float64) string {
	if bps < 0 {
		return "-"
	}
	if bps < 1 {
		return "."
	}
	return humanBytes(uint64(bps)) + "/s"
}

// humanRateFull is the label form, where a bare dot would be cryptic.
func humanRateFull(bps float64) string {
	if bps < 0 {
		return "-"
	}
	if bps < 1 {
		return "0B/s"
	}
	return humanBytes(uint64(bps)) + "/s"
}

func humanCount(n uint64) string {
	if n == unsetU64 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

func humanDur(d time.Duration) string {
	if d < 0 {
		return "-"
	}
	s := int64(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 86400:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	default:
		return fmt.Sprintf("%dd%02dh", s/86400, (s%86400)/3600)
	}
}

// pad right-pads s to w columns, truncating with an ellipsis when too long.
// Operates on runes; the TUI never pads already-styled strings.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) == w {
		return s
	}
	if len(r) > w {
		if w == 1 {
			return "…"
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

// padLeft is pad's right-aligned twin, used for every numeric column.
func padLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) == w {
		return s
	}
	if len(r) > w {
		if w == 1 {
			return "…"
		}
		return string(r[:w-1]) + "…"
	}
	return strings.Repeat(" ", w-len(r)) + s
}

func truncRunes(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// wrapWords wraps on spaces where it can and falls back to a hard break for
// tokens that are longer than the pane (paths, hashes, base64 blobs).
func wrapWords(s string, w int) []string {
	if w <= 0 || len([]rune(s)) <= w {
		return []string{s}
	}
	var out []string
	var line []rune
	flush := func() {
		out = append(out, string(line))
		line = line[:0]
	}
	for _, word := range strings.Split(s, " ") {
		wr := []rune(word)
		if len(wr) > w {
			if len(line) > 0 {
				flush()
			}
			for len(wr) > w {
				out = append(out, string(wr[:w]))
				wr = wr[w:]
			}
			line = append(line, wr...)
			continue
		}
		need := len(wr)
		if len(line) > 0 {
			need++ // the joining space
		}
		if len(line)+need > w {
			flush()
		}
		if len(line) > 0 {
			line = append(line, ' ')
		}
		line = append(line, wr...)
	}
	if len(line) > 0 || len(out) == 0 {
		out = append(out, string(line))
	}
	return out
}

// wrapRunes hard-wraps a line to width w, returning at least one segment.
func wrapRunes(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	r := []rune(s)
	if len(r) <= w {
		return []string{s}
	}
	var out []string
	for len(r) > w {
		out = append(out, string(r[:w]))
		r = r[w:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// shortUnit drops the ".service" suffix, which is redundant here and costs
// eight columns on every row.
func shortUnit(name string) string {
	return unescapeUnit(strings.TrimSuffix(name, ".service"))
}
