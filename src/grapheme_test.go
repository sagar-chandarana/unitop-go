package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// sliceANSI cuts on grapheme clusters, never through them: a combining mark
// stays on its base, a ZWJ family emoji travels whole, and a cut landing
// inside a double-width cluster skips it and pads, so the tail resumes at
// exactly the cell it covered.
func TestSliceANSIRespectsGraphemes(t *testing.T) {
	// A wide CJK glyph straddling the cut: skipped whole, one pad space.
	if got := sliceANSI("ab日本cd", 3); got != " 本cd" {
		t.Errorf("CJK straddle: %q, want %q", got, " 本cd")
	}
	// Aligned cut just after the wide glyph: no pad, nothing torn.
	if got := sliceANSI("ab日本cd", 4); got != "本cd" {
		t.Errorf("aligned CJK cut: %q, want %q", got, "本cd")
	}
	// A combining accent stays with its base on either side of the cut.
	if got := sliceANSI("éx", 1); got != "x" {
		t.Errorf("combining mark torn: %q", got)
	}
	if got := sliceANSI("éx", 0); got != "éx" {
		t.Errorf("combining mark lost from the left edge: %q", got)
	}
	// ZWJ family and a flag: a cut inside emits padding, never fragments.
	for _, s := range []string{"\U0001F468‍\U0001F469‍\U0001F467‍\U0001F466x", "\U0001F1E9\U0001F1EAx", "\U0001F44D\U0001F3FDx"} {
		got := sliceANSI(s, 1)
		if strings.ContainsRune(got, '‍') || strings.ContainsAny(got, "\U0001F468\U0001F1E9\U0001F3FD") {
			t.Errorf("cluster fragment leaked through the cut: %q", got)
		}
		if got != " x" {
			t.Errorf("straddled cluster not padded: %q, want %q", got, " x")
		}
	}
	// The style active at the boundary survives onto the tail.
	styled := "\x1b[31mAB日CD\x1b[0m"
	got := sliceANSI(styled, 3)
	if !strings.HasPrefix(got, "\x1b[31m") {
		t.Errorf("boundary style lost: %q", got)
	}
	if plain := stripSGR(got); plain != " CD" {
		t.Errorf("styled straddle text: %q, want %q", plain, " CD")
	}
}

// The overlay keeps every frame line at exactly the terminal width with CJK
// text underneath the popup — the whole reason the cut must count cells.
func TestOverlayStaysAlignedOverWideText(t *testing.T) {
	m := actionModel(t)
	m.units = []Unit{{Name: "日本語テスト.service", Desc: "国際化 ünïcode 🚀 desc", Active: "active",
		Sub: "running", Slice: "system.slice", ActiveSince: time.Now()}}
	m.rebuild()
	m.handleKey(keyOf("x"))
	if !m.menu.open {
		t.Fatal("menu did not open")
	}
	// The footer is unpadded by design; the invariants that matter are that
	// NOTHING exceeds the width, and every row the popup overlays is exactly
	// full — a torn cut shows up as one covered row running long or short.
	_, y, _, _, visible := m.menuGeometry()
	for i, line := range strings.Split(m.View(), "\n") {
		got := lipglossWidth(line)
		if got > m.width {
			t.Errorf("line %d is %d cells of %d — overflow", i, got, m.width)
		}
		if i >= y && i < y+visible+2 && got != m.width {
			t.Errorf("overlaid line %d is %d cells of %d — the overlay tore the frame", i, got, m.width)
		}
	}
}

// tailCells keeps whole clusters and never exceeds its budget: the caret of
// a full editor depends on that one spare cell.
func TestTailCellsKeepsWholeClusters(t *testing.T) {
	cases := []struct {
		s string
		w int
	}{
		{"日本語テスト", 5},
		{"abc日本語", 4},
		{"👨‍👩‍👧‍👦👨‍👩‍👧‍👦abc", 4},
		{"👍🏽👍🏽👍🏽ab", 3},
		{"ééé", 2},
		{"plain ascii tail", 7},
	}
	for _, c := range cases {
		got := tailCells(c.s, c.w)
		if gw := ansi.StringWidth(got); gw > c.w {
			t.Errorf("tailCells(%q, %d) is %d cells wide", c.s, c.w, gw)
		}
		if !strings.HasSuffix(c.s, got) {
			t.Errorf("tailCells(%q, %d) = %q is not a suffix", c.s, c.w, got)
		}
	}
	// The caret survives: a full editor line plus caret fits the width.
	text := tailCells("日本語のフィルタ入力テスト", 9)
	if ansi.StringWidth(text)+1 > 10 {
		t.Errorf("editor text %q leaves no room for the caret", text)
	}
}

// menuWidth counts cells: a CJK unit title gets a box wide enough for its
// glyphs, capped as ever, and every drawn line agrees on the width.
func TestMenuWidthCountsCells(t *testing.T) {
	cjk := "日本語サービス.service"
	if w := menuWidth(cjk); w < ansi.StringWidth("日本語サービス")+4 {
		t.Errorf("menuWidth(%q) = %d counts runes, not cells", cjk, w)
	}
	if w := menuWidth(strings.Repeat("日", 40) + ".service"); w != menuMaxWidth {
		t.Errorf("a wide title must land exactly on the %d-cell cap, got %d", menuMaxWidth, w)
	}
	m := actionModel(t)
	m.openMenu(cjk, 4, 5)
	want := m.menuBoxWidth()
	for i, line := range m.menuBox() {
		if got := lipglossWidth(line); got != want {
			t.Errorf("menu line %d is %d wide, want %d", i, got, want)
		}
	}
}

// The original failure, end to end: a supported 40-column terminal, the
// filter full of CJK — the caret stays on screen and no rendered row
// exceeds the width. The helper-level budget check cannot prove the
// footer/backstop integration; this does.
func TestNarrowEditorKeepsTheCaret(t *testing.T) {
	mm := newModel(runner{}, "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 40, 12, true, true
	m.units = testUnits()
	m.rebuild()
	m.handleKey(keyOf("/"))
	for _, r := range strings.Repeat("日", 20) {
		m.handleKey(keyOf(string(r)))
	}
	out := stripANSI(m.View())
	if !strings.Contains(out, "▏") {
		t.Error("the caret fell off the 40-column editor")
	}
	for i, line := range strings.Split(out, "\n") {
		if got := lipglossWidth(line); got > 40 {
			t.Errorf("row %d is %d cells of 40", i, got)
		}
	}
}

// The cut landing inside the LINE'S FINAL wide cluster: the overshoot pad is
// the only tail output, and it is load-bearing — an overlay composed over
// such a line must come out exactly the original width.
func TestTerminalStraddlePadSurvives(t *testing.T) {
	if got := sliceANSI("ab日", 3); got != " " {
		t.Errorf("terminal straddle pad lost: %q, want single space", got)
	}
	styled := "\x1b[7mab日\x1b[0m"
	if got := stripSGR(sliceANSI(styled, 3)); got != " " {
		t.Errorf("styled terminal straddle pad lost: %q", got)
	}
	// One overlay row, composed the way overlayMenu composes it: the box's
	// right edge lands inside the row's trailing wide glyph.
	line, box := "abc日", "[]"
	x, boxW := 2, 2
	prefix := truncANSI(line, x)
	prefix += strings.Repeat(" ", max(0, x-lipglossWidth(prefix)))
	row := prefix + box + sliceANSI(line, x+boxW)
	if got := lipglossWidth(row); got != lipglossWidth(line) {
		t.Errorf("overlay row is %d cells, want %d: %q", got, lipglossWidth(line), row)
	}
}
