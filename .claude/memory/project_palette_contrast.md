---
name: project_palette_contrast
description: Measured contrast of every ANSI index on the two themes unitop is checked against, and why structure stopped using hue
metadata:
  type: project
---

Contrast of each palette index against its own theme's background, measured
with the WCAG formula (`docs/helpers/palette.awk` carries the same two
palettes). duskfox is the dark one, a corrected Catppuccin Latte the light one
— both are what the user actually runs.

|            | duskfox | latte |
|---|---|---|
| red (1)    | 5.38 | 4.51 |
| green (2)  | 7.67 | 2.16 |
| yellow (3) | 9.55 | 1.73 |
| blue (4)   | 5.26 | 3.52 |
| magenta(5) | 7.47 | 1.65 |
| cyan (6)   | 9.19 | 2.36 |
| grey (8)   | 1.71 | 4.37 |
| foreground | 11.86 | 7.06 |

**Only red and blue clear 3:1 on both.** Latte's palette is a dark-theme colour
set on a light ground, so its green, yellow, magenta and cyan wash out; duskfox
annotates colour 8 "backgrounds only" and gives it 1.71:1, so it cannot be text
there. This is not a bug in either theme — an ANSI index is a name, not a
promise about contrast, which is exactly why unitop names indices in the first
place.

The consequence, now enforced in `theme.go`: **colour never carries anything
you have to read.** Headings, the host name, column titles, labels,
timestamps, idle values, unit and slice names and log lines are the default
foreground — bold for emphasis, faint for below notice (SGR 2). A terminal
that ignores SGR 2 flattens the hierarchy but keeps every word legible, the
right way round to fail; colour 8 fails closed.

The host name is cyan and bold — the one heading with a hue, because it answers
which machine you are looking at, and it is short and always in the same corner.

Colour is kept everywhere it costs nothing: magenta for the focused frame,
the menu box, the spinners and the editor caret, colour 8 for the rules and
the unfocused frame — decoration that may vanish on a theme without loss,
since the focused frame is a heavier glyph too. The filter is yellow bold by
explicit choice even though it is words; it was made colourless once and put
back. The sorted column title is red for high-to-low and green for
low-to-high (`sortStyle()`), the one place those two mean a direction rather
than health — the arrow says it as well, so the colour is never load-bearing.

`heat()` and `stateStyle()` return a `lipgloss.Style` rather than a colour for
this reason: their quietest step has no colour of its own.

To check a palette change, render both: `nix run .#screenshots` (duskfox) and
`THEME=latte nix run .#screenshots`. A colour usually fails on one side only.
See [[feedback_verify_tui_with_pty]] for the capture pitfalls.
