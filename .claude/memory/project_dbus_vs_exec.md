---
name: dbus-vs-exec
description: Why unitop shells out to systemctl instead of using systemd's D-Bus API — measured, not assumed
type: project
---

Shelling out to `systemctl` looks like the lazy choice, so this gets questioned.
It was measured on 2026-08-15 against this workstation: **129 service units,
systemd 257**.

## The numbers

| approach | time |
|---|---|
| D-Bus `ListUnitsByPatterns` only | **0.9 ms** |
| D-Bus `GetUnitTypeProperties`, serial | **153 ms** |
| D-Bus `GetAllProperties`, 16 workers | 158 ms |
| D-Bus `GetAllProperties`, serial | 290 ms |
| exec `systemctl list-units` | 4.7 ms |
| exec `systemctl show` (all units, one call, 33 properties) | **260 ms** |

So a whole poll is ~265 ms today and D-Bus would be ~155 ms — **1.7×, not orders
of magnitude.**

## Why the gain is small

The cost is systemd's, not the transport's. Adding properties to
`systemctl show` across all units barely moves it:

| requested | time |
|---|---|
| identity + state only | 216 ms |
| + `MemoryCurrent` | 217 ms |
| + `CPUUsageNSec` | 217 ms |
| + IP accounting | 219 ms |
| + IO accounting | 218 ms |
| all 33 unitop asks for | 230 ms |

**216 ms is the floor for touching 129 units at all**; the 30 extra properties
cost 14 ms. Fetching less does not help. Touching fewer units would.

## What is true about the D-Bus route

- `github.com/coreos/go-systemd/v22/dbus` is **pure Go** — verified building and
  running with `CGO_ENABLED=0`, so it would not cost us the static binary.
- An **unprivileged user can read** unit properties and `ListUnits` on the system
  bus; polkit only guards the methods that change things.
- `GetAllProperties` returns ~450 properties per unit (58k across the host),
  which is why it is *slower* than exec. `GetUnitTypeProperties(unit,
  "Service")` is the one to use.

## Why it was not adopted

1. **The journal would still be an exec.** `sd-journal` is a C API needing cgo,
   which would kill the static build; the pure-Go journal-file readers are
   third-party and incomplete. `journalctl -f` stays either way — and it is
   already one long-lived process, not per-poll.
2. **`-H` is the blocker.** D-Bus is a local unix socket. A remote one means ssh
   unix-socket forwarding plus matching bus auth (untested, and the polkit
   identity gets murky), or running `busctl` over ssh — which is exec again.
   The result is **two code paths**, D-Bus locally and exec remotely, doubling
   the surface of the most breakable part to save ~110 ms on a 1 s interval.
3. Actions gain nothing: `StartUnit` over D-Bus hits polkit exactly as
   `systemctl` does.

## What *would* justify it

Not speed — **`Subscribe()`**. D-Bus pushes `UnitNew`, `UnitRemoved` and
`PropertiesChanged`, so state changes arrive as events instead of being
discovered up to a poll late. That is a better model for the state columns, with
polling kept for the rate counters, and it is the one thing exec cannot do at
all. If unitop ever wants sub-second state, that is the reason to revisit —
alongside the two-code-paths cost above.

## The interval consequence

A poll costs ~265 ms on a 129-unit host, so **`-i 250ms` is already saturated**
and a 400-unit host is worse. The guard against overlap is the `m.polling` flag,
which skips a tick while one is in flight — so unitop degrades to "as fast as it
can" rather than piling up. Do not raise the lower clamp without measuring.

## PID 1 CPU follow-up (2026-08-21)

The wall-time benchmark understated the user-visible problem: on the same
systemd 257 workstation, a production-shaped query over the 103 loaded service
units consumed about **140 ms of PID 1 CPU per poll** (and 239 ms wall time),
while PID 1 used no measurable CPU over an eight-second idle sample. At the old
one-second default that is roughly 14% of one core in systemd alone.

systemd 257's `systemctl-show.c` explains why reducing `showProperties` did not
help: `show_one()` calls `bus_map_all_properties()` first, once for every unit;
only afterwards does `bus_message_print_all_properties()` apply
`arg_properties`. In other words, `--property=` is an output filter around a
D-Bus `Properties.GetAll`, not a server-side property selection. The v0.2.0
addition of eight requested properties was therefore not the 0.1.3→0.3.0 CPU
cause; old and new property lists measured the same.

The normal view hides inactive units, so it now omits those units from the
expensive detailed query too; `-a`/`a` includes them and `a` triggers a prompt
refresh. On this host that changed the steady query from 103 to 62 units and
about 106 ms of PID 1 CPU. The one-second default is intentionally retained, so
the estimated PID 1 share falls from ~14% to ~10.6% of one core. Explicit `-i`
values remain available when lower overhead matters more than one-second data.

Related: [[remote-poll]], [[go-nix-workflow]]
