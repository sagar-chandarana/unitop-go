package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
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

// pad right-pads s to w terminal cells, truncating with an ellipsis when too
// long. It operates on plain strings; the TUI never pads already-styled ones.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = truncRunes(s, w)
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// padLeft is pad's right-aligned twin, used for every numeric column.
func padLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = truncRunes(s, w)
	return strings.Repeat(" ", w-ansi.StringWidth(s)) + s
}

func truncRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return ansi.Truncate(s, w, "…")
}

// wrapWords wraps on spaces where it can and falls back to a hard break for
// tokens that are longer than the pane (paths, hashes, base64 blobs).
func wrapWords(s string, w int) []string {
	if w <= 0 || ansi.StringWidth(s) <= w {
		return []string{s}
	}
	var out []string
	var line string
	lineWidth := 0
	flush := func() {
		out = append(out, line)
		line = ""
		lineWidth = 0
	}
	for _, word := range strings.Split(s, " ") {
		wordWidth := ansi.StringWidth(word)
		if wordWidth > w {
			if lineWidth > 0 {
				flush()
			}
			parts := wrapRunes(word, w)
			for len(parts) > 1 {
				out = append(out, parts[0])
				parts = parts[1:]
			}
			line = parts[0]
			lineWidth = ansi.StringWidth(line)
			continue
		}
		need := wordWidth
		if lineWidth > 0 {
			need++ // the joining space
		}
		if lineWidth+need > w {
			flush()
		}
		if lineWidth > 0 {
			line += " "
			lineWidth++
		}
		line += word
		lineWidth += wordWidth
	}
	if lineWidth > 0 || len(out) == 0 {
		out = append(out, line)
	}
	return out
}

// wrapRunes hard-wraps a line to width w terminal cells, returning at least one
// segment without splitting a grapheme cluster.
func wrapRunes(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	if ansi.StringWidth(s) <= w {
		return []string{s}
	}
	var out []string
	var b strings.Builder
	width := 0
	for g := uniseg.NewGraphemes(s); g.Next(); {
		cluster, cw := g.Str(), g.Width()
		if width > 0 && width+cw > w {
			out = append(out, b.String())
			b.Reset()
			width = 0
		}
		b.WriteString(cluster)
		width += cw
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// shortUnit drops the ".service" suffix, which is redundant here and costs
// eight columns on every row.
func shortUnit(name string) string {
	return unescapeUnit(strings.TrimSuffix(name, ".service"))
}

// tailCells keeps the last w terminal cells of s. It is for the filter editor
// on a terminal narrower than what has been typed: the end of the text is
// where the cursor is, so the end is what to show.
func tailCells(s string, w int) string {
	if n := ansi.StringWidth(s); n > w {
		return ansi.TruncateLeft(s, n-w, "")
	}
	return s
}
