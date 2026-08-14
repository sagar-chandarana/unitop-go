# unitop

A terminal UI for watching systemd services: per-unit CPU, memory, network and
disk I/O rates, failure state and restart counts, a live journal tail for the
selected unit, an optional slice tree, and start/stop/kill from a context menu.

```
server1 · 8 cpu · up 14d21h · load 1.20 0.95 0.80              sort cpu↓ · tree · 1s
cpu 11% · mem 12G/29G · swap 3.8G · net ↓42K/s ↑134K/s              61/105 units · 1 failed
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
UNIT              STATE      CPU%↓     MEM      NET↓      NET↑  RST      UP │ caddy  active/running
──────────────────────────────────────────────────────────────────────────── │ Caddy web server
▾ system          1 fail      3.2    277M     1.4K/s    1.1K/s    0  14d02h │ pid 1314 · restarts 0 · up 3h46m
  caddy           running    18.9     73M      14K/s     16K/s    0   3h46m │ ─────────────────────────────────
  postgres     running    20.0    101M          ·         ·    0  14d21h │ 13:19:39 caddy[1314]: served /
  deploy-config    failed         -       -         -         -    0       - │ 13:19:40 caddy[1314]: served /ok
▾ user-1000       2 unit     80.6     6.3G    1.1K/s     424B/s    0 14d18h │
```

`enter` on a unit opens the **full view**, which drops the table and gives that
unit's log the whole width. `esc` (or `enter` again) goes back.

```
caddy  active/running                       pid 1314 · restarts 0 · up 3h46m · tasks 22
Caddy web server
cpu 18.9% · mem 73M · peak 91M · net ↓14K/s ↑16K/s · io ↓1.2M/s ↑840K/s
──────────────────────────────────────────────────────────────────────────────────────
13:19:39 caddy[1314]: served /
13:19:40 caddy[1314]: served /health
```

The live counters keep updating there — the full view trades the table for log
width, not for the numbers.

## Build & run

```sh
nix run .                          # local machine
nix run . -- -H root@server1   # a remote host over ssh
nix build .                        # ./result/bin/unitop
```

The binary is **statically linked** (`CGO_ENABLED=0`, stripped, ~4 MB), so
`scp result/bin/unitop host:/usr/local/bin/` works regardless of the target's
libc. In the dev shell: `nix develop`, then `cd src && go run .`

## Flags

| flag | meaning |
| --- | --- |
| `-H user@host` | run `systemctl`/`journalctl` on a remote host over ssh (BatchMode, key auth only) |
| `-i 2s` | refresh interval (default `1s`, clamped to 250ms–30s) |
| `-s cpu` | initial sort: `name state cpu mem net net-out io io-write restarts tasks uptime` |
| `-r` | reverse the initial sort |
| `-t` | start in tree view, grouped by slice |
| `-a` | include inactive/dead units |
| `-f nginx` | initial filter |
| `-no-logs` | start with the log pane hidden |
| `-sudo` | run unit actions through `sudo -n systemctl` |
| `-read-only` | remove the action menu entirely |

## Keys

| key | action |
| --- | --- |
| `↑`/`k` `↓`/`j`, `pgup`/`pgdn`, `g`/`G` | move (scrolls the log pane when it has focus) |
| `←`/`h` `→` | collapse / expand a slice in tree mode |
| `tab` | switch focus between the unit list and the log pane |
| `enter` | on a unit: open the full view (`esc` returns). On a slice: expand/collapse |
| `x` | start / stop / restart / kill the selected unit |
| `s` / `S` | sort by the next / previous **visible** column, in the order they are drawn |
| `r` | reverse the sort |
| `t` | tree view, grouped by slice |
| `/` | filter on unit name or description (`esc` clears) |
| `a` | include inactive/dead units |
| `f` | follow the log — auto-scroll to the newest line. Scrolling up turns it off |
| `l` | toggle the log pane |
| `w` | toggle log wrapping |
| `p` | pause polling |
| `R` | refresh now |
| `+` / `-` | faster / slower refresh |
| `?` | help |
| `q` | quit |

**Mouse**: the wheel scrolls whichever pane it is over; a left click selects a
row, and on a **column header** sorts by that column (click again to reverse);
a click on a slice's `▾` toggles it; a **right click on a unit** opens the
action menu.

## Unit actions

Right-click a unit (or press `x`) for start, stop, restart, reload,
reload-or-restart, kill with SIGTERM or SIGKILL, freeze, thaw and reset-failed.
Anything that interrupts a running service asks for confirmation first, and
those entries are drawn in orange in the menu.

Actions shell out to `systemctl` directly, so they need privilege: run unitop
as root, or pass `-sudo` to route them through `sudo -n systemctl`. Interactive
polkit is never used — it would take over the terminal — so an unprivileged run
reports `Interactive authentication required` in the status line and suggests
`-sudo`. `-read-only` removes the menu altogether.

## Tree view

`t` groups units by their systemd slice, following the nesting implied by slice
names (`user-1000.slice` sits under `user.slice` under `-.slice`). Each slice
row totals every unit beneath it — CPU, memory, network, I/O, tasks and
restarts — and turns red with a `N fail` state when any descendant has failed.
Slices sort against each other by whichever column you are sorted on, using
those totals.

## What the columns mean

`CPU%` is the delta of the unit's `CPUUsageNSec` over wall-clock time between
two polls, summed across cores — so 200% means two cores' worth. `MEM` is the
cgroup's current total, not a delta. `NET` and `IO` are byte rates derived the
same way from `IPIngressBytes`/`IPEgressBytes` and `IOReadBytes`/`IOWriteBytes`.

A counter that goes backwards (which is what a restart looks like) reads as
zero for that interval rather than as a spike.

`-` means systemd is not tracking that counter for the unit; `·` means it is
tracked but there is not yet a second sample, or the rate is zero.

## Requirements

- **Network columns** need IP accounting, which is off by default for most
  units. Enable it per unit with `IPAccounting=yes`, or fleet-wide with
  `DefaultIPAccounting=yes` in `/etc/systemd/system.conf`. On NixOS:

  ```nix
  systemd.extraConfig = "DefaultIPAccounting=yes";
  ```

  Without it the `NET` columns show `-` and the detail pane notes
  `ip-accounting off`.

- **Logs** need membership of the `systemd-journal` group, or root. Without it
  the log pane shows journalctl's own complaint rather than staying blank.

- **`-H`** needs key-based ssh: `BatchMode=yes` is set, so unitop never prompts
  for a password. unitop sets up its own `ControlMaster` socket in `$TMPDIR`, so
  every poll and the journal tail share one connection; it is torn down on exit
  (and `ControlPersist=30s` cleans up if unitop is killed).

## Implementation notes

A poll is two commands: one that enumerates the services (and, remotely, dumps
`/proc/{stat,meminfo,loadavg,uptime,net/dev}` in the same shell), and one
batched `systemctl show --timestamp=unix` that fetches every property for every
unit at once. So the cost is flat in the number of units, and remotely it is
two ssh round trips over a shared connection rather than one per command.

Logs come from one `journalctl -f -o json` per selected unit, restarted only
when the selection actually changes.
