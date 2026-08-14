---
name: key-bindings
description: unitop's key assignments were arrived at by iteration — enter=full view, x=actions, f=follow, l inert in full view
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

**How to apply:** treat the table above as the intended design, not as
incidental. If a new key is needed, check it does not collide, and update both
the footer (`viewFooter`) and the help overlay (`viewHelp`) — they are written
out separately and drift easily.

Related: [[no-charts]]
