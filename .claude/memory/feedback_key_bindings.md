---
name: key-bindings
description: unitop's key assignments were arrived at by iteration — enter=full view, x=actions, F/f the log's ends, esc pops one thing, r/R deliberately left alone
type: feedback
---

The current bindings replaced earlier arrangements that were explicitly
rejected. Check here before rebinding anything.

| key | now | was |
|---|---|---|
| `enter` | full view on a unit (expand/collapse on a slice) | opened the action menu |
| `x` | action menu | — |
| `f` | follow the log (auto-scroll) | failed-only filter |
| `L` | *removed* | full-width log pane |
| `l` | toggle log pane, **inert in the full view** | always toggled |

The failed-only filter was dropped entirely rather than moved: `/` plus sorting
by state already covers it.

`l` does nothing in the full view because the full view *is* the log — hiding it
would leave an empty screen. The footer also drops the `l log` hint there, so
the hints never advertise a key that does nothing.

**Why:** the user drove each of these directly. `enter` for "look closer" and
`x` for "do something" separates inspection from mutation, which matters when
the actions include stop and kill.

## The second pass, after a consistency audit

The user asked what else was inconsistent; these came out of that and are just
as deliberate.

| decision | why |
|---|---|
| **one key per motion**, and motion is named keys | every letter an alias holds is a letter unavailable for a command. `j k h g G ctrl+b ctrl+f = _` all removed; `ctrl+f` was fighting the `f` that follows the log |
| `F`/`f` = top/bottom **of the log only** | the log is the one pane with two ends worth naming — `f` resumes following, and the live end *is* the bottom. Both are in `logKeys` and inert everywhere else; the table's ends are `home`/`end` |
| `esc` pops **one** thing, innermost first | editor → menu → help → the *focused pane's* filter → full view → focus. It clears the filter of the pane you can see, and in the editor it cancels rather than clears |
| `r`/`R` left knowingly inconsistent | `r` = reverse, `R` = refresh are unrelated commands sharing a letter, unlike `s`/`S`. **Rebinding to `r` = refresh, `i` = invert was tried and reverted**: the flags `-r` and `-i` already mean those letters, and disagreeing with your own flags is worse. Do not try it again |

**How to apply:** treat both tables as the intended design, not as incidental.
If a new key is needed, check it does not collide, and update `viewFooter`,
`viewHelp` **and** the readme — they are written out separately and drift
easily. `keyApplies()` is the single answer both the handler and the footer
consult; if they ever disagree, the screen is lying about what the next
keystroke does.

Related: [[no-charts]], [[layout-invariant]]
