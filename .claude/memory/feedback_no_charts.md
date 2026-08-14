---
name: no-charts
description: Charts/sparklines were built for unitop and then removed on request — do not reintroduce them
type: feedback
---

unitop deliberately has **no charts**. Sparklines (CPU/mem/net history with
peak labels, a `series` ring buffer, per-unit history in the model) were fully
built, reviewed on screen, and then removed at the user's request: "remove
charts and simplify".

The full view shows the unit's **live counters** — cpu, mem, peak, net, io — as
text, not history. The host header is compact text too; an early draft with
gauges and sparklines was rejected with "no charts" and "make host stats
smaller".

**Why:** the tool is for answering "what is happening right now and what does
its log say", not for trend analysis. History widgets cost vertical space that
the log wants, and they were judged not to earn it.

**How to apply:** don't add sparklines, gauges, bar meters or history buffers
without being asked for them explicitly. If more signal is needed in a view,
prefer another live number or a colour, and keep the line count the same. The
`series` type and `chart.go` were deleted — resurrecting them from git history
is the wrong instinct.

Related: [[key-bindings]], [[verify-tui-with-pty]]
