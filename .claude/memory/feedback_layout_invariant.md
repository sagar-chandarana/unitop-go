---
name: layout-invariant
description: Rendering bugs hide in sizes nobody tests; render every mode at every awkward size and assert the width, then disable the backstop
type: feedback
---

**One line wider than the terminal spoils the whole screen**, not just itself:
it wraps, and the wrap pushes every line below it down a row. So the invariant
worth testing is not "does it look right at 120×40" but "is any line, in any
mode, at any size, wider than the screen".

Asserting exactly that found **six** bugs in one pass — the filter prompt (at
*any* width narrower than the prompt, so 60 or 75 columns, not just absurd
ones), the help in one-column mode, its "more below" marker, the action menu,
its confirmation box, and the startup screen's error and key hints. Every one
of them had been on screen for weeks.

**The recipe** (`layout_test.go`): a matrix of widths × heights × modes —
including help scrolled, menu confirming, filter being typed, toast, poll
error, tree collapsed, full view — with fixtures containing double-width CJK,
emoji, combining marks and a name longer than any pane. Assert `len(lines) ==
height` and `ansi.StringWidth(line) <= width`.

**Then disable the backstop and run it again.** `View()` truncates every line as
a last resort. That guarantees the invariant but hides which composer is
sloppy, so a second test (`TestComposersFitWithoutTheBackstop`) checks they fit
unaided. Without that check the backstop quietly becomes the mechanism, and
things get cut that should have degraded gracefully instead.

**The other half of the answer is to refuse.** Below `minWidth`×`minHeight` (40×10)
there is no layout to be had, so unitop says so — which also means the layout
code does not have to defend sizes nobody runs. Adding that floor needs a key
gate too: the notice says `q` quits, so `q` must quit even with the filter
editor or the menu open underneath, where it would otherwise be a character to
type or close an invisible menu.

**How to apply:** any new pane, popup or status line joins the matrix. Measure
in cells (`ansi.StringWidth`), never runes — `len([]rune(s))` is wrong for CJK,
emoji and combining marks alike.

Related: [[verify-tui-with-pty]], [[key-bindings]]
