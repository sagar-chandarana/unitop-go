---
name: remote-poll
description: The -H remote poll is one shell one-liner over a multiplexed ssh connection; a '#' in it once silently emptied the unit list
type: project
---

With `-H user@host`, a poll must stay at **two ssh round trips**:

1. `sh -c "grep -H '' /proc/… ; echo '<marker>'; systemctl list-units …"` — the
   `/proc` dump and the unit list in one command. `grep -H ''` prefixes each
   line with its filename so one stream carries five files; only the *first*
   colon on a line is the separator, since `/proc/net/dev` and `/proc/meminfo`
   contain colons of their own.
2. `systemctl show --property=… <every unit name>` — one batch (limit 400,
   which no realistic host reaches).

**The bug that shipped once:** the marker was `###unitop###`. The script is a
single line, so `#` started a shell comment and everything after it —
`systemctl list-units` — never ran. No error, no stderr; the UI just showed
"0 units" and an empty table. `remote_test.go` now runs the real generated
script through a real `sh` and asserts both halves survive.

**Why two round trips matter:** before multiplexing, each poll paid four full
ssh handshakes (~1s each on a distant host), which exceeded the poll interval.
Rates never populated because consecutive samples were too far apart, and the
CPU/NET columns sat on `-` indefinitely — it looked like a parsing bug, not a
latency one. unitop now sets up its own `ControlMaster` socket in `$TMPDIR`
(`ControlPersist=30s`, torn down on exit) so all commands and the journal tail
share one connection.

**How to apply:** never add a third command per poll — fold it into the shell
line. Never put a `#` in anything interpolated into that line. Keep
`BatchMode=yes` so unitop can never sit waiting for a password behind the
alt-screen.

Related: [[verification-hosts]], [[go-nix-workflow]]
