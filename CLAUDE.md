# CLAUDE.md

This file provides guidance to coding agents working in this repository — Claude
Code (claude.ai/code) and any other agent. `AGENTS.md` is a symlink to this file,
so the two are always identical; edit this one.

`.claude/memory/` is the project's **shared agent memory**, not Claude-only —
every agent reads it at the start of a session and updates it when it learns
something durable (see `## Memory`).

## What this repo is

`unitop` — a terminal UI for watching systemd services. Per-unit CPU, memory,
network and disk I/O rates, state and restart counts in a sortable table; a live
journal tail for the selected unit beside it; an optional slice tree; and
start/stop/restart/kill from a context menu. It works against the local machine
or, with `-H user@host`, against a remote one over ssh.

Single Go binary, no daemon, no config file, no state on disk. It reads systemd
through `systemctl`/`journalctl` and the host summary from `/proc`. Built static
so it can be copied onto any host regardless of libc.

Layout follows the convention used by the sibling `*-go` repos: `flake.nix` at
the root, Go sources in `src/`, dependencies vendored with `vendorHash = null`.

## Common commands

```bash
# Build (static) and run
nix build .                       # -> ./result/bin/unitop
nix run .
nix run . -- -H root@server1  # a remote host over ssh

# Iterate without nix rebuilding each time
nix develop                       # provides go, gopls, gotools, delve
cd src && go run .
cd src && go test ./...           # the fast loop; no host or network needed
cd src && go vet ./... && gofmt -l *.go

# After changing dependencies
cd src && go mod tidy && go mod vendor
git add src/                      # flakes only see git-tracked files
```

**`nix build` runs the test suite** (`buildGoModule` defaults `doCheck = true`),
inside a sandbox with no network, no dbus and no running systemd. A test that
shells out to `systemctl` would therefore break the build — see `## Testing`.

## Architecture

One bubbletea program. The data flow per tick:

```
tickMsg ──▶ Collector.Poll ──▶ unitsMsg{units, host} ──▶ model.rebuild()
                │                                              │
                │ pollBase: unit list + /proc                   │ filter → sort →
                │ systemctl show: all properties, one batch     │ tree grouping
                ▼                                              ▼
          rates = delta vs previous sample                  []row ──▶ View()

selection change ──▶ syncJournal ──▶ journalctl -f -o json ──▶ journalBatch ──▶ log pane
```

- **Collect** (`collect.go`, `host.go`) — polls, and turns systemd's monotonic
  counters into rates. Owns the previous sample; nothing else does arithmetic on
  raw counters.
- **Interpret** (`state.go`, `sort.go`, `tree.go`) — pure functions over
  `[]Unit`. No I/O, no rendering. This is where "what does inactive/dead
  actually mean" and "what order do rows go in" live.
- **Model** (`model.go`, `actions.go`) — bubbletea state, key and mouse
  handling, the journal stream's lifecycle, the action menu.
- **Render** (`view.go`, `theme.go`, `format.go`) — everything that produces a
  string. `View()` composes a frame of exactly `m.height` lines.

## Key files to know

| File | Purpose |
|---|---|
| `src/collect.go` | `runner` (local vs ssh), `Unit`, `Collector.Poll`, `systemctl show` parsing, rate derivation |
| `src/host.go` | `/proc` parsing into `HostStats` for the header block |
| `src/state.go` | **`Unit.StateLabel()`** — what the STATE column says; the `ExecMainCode`/`ConditionResult` logic |
| `src/sort.go` | `sortKey`, per-column comparators, `stateRank` |
| `src/tree.go` | `row`, slice hierarchy, aggregation, `buildRows` (flat and tree) |
| `src/model.go` | bubbletea `Update`, keys, mouse, `rebuild`, `syncJournal`, geometry consumers |
| `src/actions.go` | unit action list, context menu state machine, `systemctl <verb>` execution |
| `src/journal.go` | `journalctl -f -o json` streaming and entry decoding |
| `src/view.go` | geometry, host block, table, log pane, menu overlay, footer, help |
| `src/theme.go` | palette, `heat()`, `stateColor()` |
| `src/format.go` | byte/rate/duration formatting, padding, wrapping, unit-name unescaping |

## Where the data comes from

Everything is derived from a handful of systemd properties fetched in one
batched `systemctl show` (see `showProperties` in `collect.go`). The
non-obvious ones:

| Property | Why it is fetched |
|---|---|
| `CPUUsageNSec` | CPU%, as a delta over wall-clock between polls (200% = two cores) |
| `MemoryCurrent` / `MemoryPeak` | cgroup totals, **not** deltas |
| `IPIngressBytes` / `IPEgressBytes` | NET columns. Only populated when `IPAccounting=yes` |
| `IOReadBytes` / `IOWriteBytes` | IO columns |
| `ExecMainCode` | **whether the unit's main process ever ran** — the only way to tell a finished oneshot from one that never started |
| `ExecMainStatus` | the exit code, or the signal number when `ExecMainCode` says killed |
| `ConditionResult` | `no` means systemd deliberately skipped the unit |
| `Slice` | tree grouping |

`systemd` reports `[not set]` for counters it is not tracking; that and the u64
sentinel both parse to `unsetU64`. Use `orZero()` before doing arithmetic —
letting the sentinel into a sum produces spectacular garbage.

## Directions — things to preserve

These are decisions, not accidents. Change them deliberately, not incidentally.

**Product**

- **No charts.** Sparklines were built and then removed on request. The full
  view shows live numbers, not history. Don't reintroduce them.
- **Prefer simple over clever** in the UI. Every added key and pane is a cost.
- **A key that cannot do anything is not offered.** Keys belong to a pane:
  `tableKeys` are inert unless the table has focus, `logKeys` unless the log
  does, and `keyApplies()` is the single answer both the handler and
  `footerKeys()` consult — they must never disagree. Anything anchored to a
  table row needs a second answer for the full view — see `menuAnchor()`, which
  otherwise put the popup wherever the invisible cursor happened to be.
- **One key per motion, and motion is named keys.** The vim and readline
  aliases were removed on purpose: every letter an alias holds is a letter
  unavailable for a command, and each one is a second thing to document and
  keep working. `F`/`f` are the single exception — the log's two ends, bound
  there because it is the only pane with ends worth naming, and inert
  everywhere else. Do not re-bind either outside `logKeys`.
- **Focus is drawn on the whole pane.** Both panes are always boxed, at the same
  size; the focused box is heavy and coloured and the other light and faint. A
  single marker on the divider is not where anyone is looking, and a box that
  appears only when focused shifts the layout under the reader.
- **`esc` pops one thing, innermost first, and never reaches past it.** The
  order lives in `escape()`: editor, menu, help, the *focused pane's* filter,
  the full view, focus. Two rules keep it honest — it clears the filter of the
  pane you can see (clearing the table's from inside the full view threw away
  something invisible), and in the editor it cancels rather than clears
  (`filterWas`). Adding a mode means adding a rung, in the right place.
- **A filter says what it does, in words.** Its pane's title carries `name or
  description contains "x"` or `matching "x", error and above` — not the flag
  that implements it. A filtered pane that stays silent reads as a quiet one.
- **Nothing from the far end reaches the terminal unsanitised.** Journal
  messages, systemd property values, ssh and systemctl stderr all pass through
  `sanitizeText`/`sanitizeMessage` at the point they enter the model — see
  `sanitize.go`. A service can write escape sequences into its own log, and a
  unit whose output goes to a serial console leaves carriage returns in it;
  raw, they move the cursor, repaint the screen and make every width
  calculation wrong. When adding a field, sanitise it at ingest, not at render.
- **An overlay covers its own width, not the rest of the line.** `overlayMenu`
  rebuilds each covered row as prefix + box + `sliceANSI(rest)`. Dropping the
  tail blanks out half the screen.
- **Read-only is the safe default posture.** Actions are opt-in via `x` /
  right-click, destructive ones confirm, and `-read-only` removes them entirely.
- **Never invoke interactive polkit.** It would seize the terminal. Actions run
  `systemctl` directly and report the privilege error; `-sudo` uses `sudo -n`.
- **Nothing renders before the first successful poll.** `connected` gates the
  whole UI; until then `viewStartup()` owns the screen. Drawing an empty table
  for a host we have not reached yet, with the reason one line deep in the
  footer, is exactly what this replaced. A failure *after* connecting stays in
  the footer — there is real data on screen worth keeping.
- **A failure says what to do next.** `troubleshoot()` turns an ssh or systemd
  error into concrete next steps. Add a case there rather than leaving a new
  failure mode to the generic advice.
- **Report what happened, not what systemd literally said.** `StateLabel()`
  exists because `inactive/dead` covers four different situations. When the
  friendly label differs from systemd's wording, the detail pane shows both.

**Rendering**

- `View()` returns **exactly `m.height` lines**, always. Overlays replace lines;
  they never add or remove them.
- Every line must fit its pane. `truncANSI` is ANSI-aware; `pad`/`padLeft` are
  **not** — pad the plain string first, then style it, never the reverse.
- **The geometry contract**: `headerLines()` is both the height of the host
  block *and* the screen row of the column-title line. Rows begin at
  `headerLines() + 2` (titles, then the rule). `rowAt()` and `handleMouse()`
  depend on this; change the header and you must change them together.
  `TestRowAtMatchesRenderedRows` guards it.
- `layout()` is the single source of truth for which columns exist at a given
  width. `columnAt()` (click-to-sort) and `nextVisibleSort()` (`s`) both walk
  its output, so they cannot drift out of sync with what is drawn.
- **The palette is the terminal's own sixteen ANSI colours**, as htop's is.
  Naming a colour by index means it is whatever the user's theme says, so
  unitop matches the rest of their terminal and needs no light/dark handling.
  Never write a hex value or a 256-colour index: it would override the theme
  and force the light/dark problem back in. Six hues carry meaning — green
  healthy, yellow watch, red wrong, cyan finished-or-rate, blue keys, magenta
  headings — everything else is grey or dim. Magnitudes ramp through five
  steps via `heat()` — grey, green, yellow, orange, red — where orange is
  bright yellow, the one step that has no hue of its own. State comes from
  `stateColor()`. Tests assert both stay inside the palette.

**Talking to systemd**

- **Shelling out to `systemctl` is a measured decision, not laziness.** systemd's
  D-Bus API was benchmarked: ~1.7× faster at best, because the cost is systemd
  computing the properties (216 ms of a 265 ms poll is the floor for touching
  129 units at all), and both the journal and `-H` would still need exec. The
  numbers and the one thing that would justify switching — `Subscribe()` for
  event-driven state — are in
  `.claude/memory/project_dbus_vs_exec.md`. Read it before "optimising" this.
- **A poll costs ~265 ms on a 129-unit host**, so the low end of `-i` is already
  saturated. `m.polling` skips a tick while one is in flight, so it degrades to
  "as fast as it can" instead of piling up.

**Correctness**

- **A counter going backwards is a restart, not a spike.** `rate()` returns 0.
- **`ExecMainCode == 0` does not mean "never ran" for an active unit** — it
  means no main process has exited yet. Only consult it for non-active units.
- Sorting always breaks ties by name, or rows jitter between polls as equal
  values swap around.
- The journal stream carries a generation counter; lines from a unit you have
  navigated away from are discarded rather than appended to the wrong pane.
- Labels must fit the 9-column STATE field. There is a test asserting it.

**Remote (`-H`)**

- A poll is **two ssh round trips**, over one multiplexed `ControlMaster`
  connection unitop sets up itself. Adding a third command per poll is a
  regression — fold it into the existing shell line instead.
- That shell line is a **single line**, so a `#` anywhere in it comments out
  everything after. This shipped as a bug once; `remote_test.go` now runs the
  real script through a real shell.
- `BatchMode=yes` always: unitop must never sit waiting for a password prompt
  behind the alt-screen.

## Testing

`cd src && go test ./...` is the whole suite — fast, and it needs no host, no
network and no systemd. Keep it that way: `nix build` runs these tests in a
sandbox that has none of those things.

- Parsing is tested against **fixtures captured from real hosts**, not invented
  ones (`showFixture` in `collect_test.go`, the case table in `state_test.go`).
  When you meet a new real-world tuple, add it there.
- Rendering is tested by calling `View()` and asserting on the frame — line
  count, content, and that no line exceeds its pane.
- `remote_test.go` covers the remote code path without a remote: it runs the
  actual generated shell script through `sh` and parses the result.

**Seeing the TUI for real** needs a pty; piping the binary produces nothing
useful. See `.claude/memory/feedback_verify_tui_with_pty.md` for the recipe and
its pitfalls (bubbletea only repaints changed lines, so a captured stream is
overlapping partial frames, not whole screens).

## Releasing

Tagging publishes static `linux/amd64` and `linux/arm64` binaries plus
`SHA256SUMS` to a GitHub release, via `.github/workflows/release.yml`:

```sh
# 1. move the Unreleased entries under a new "## [x.y.z] — <date>" heading in
#    CHANGELOG.md, and add its compare link at the bottom
# 2. bump `version` in flake.nix and src/main.go to match
# 3. commit, then:
git push origin master
git tag -a v0.2.0 -m 'v0.2.0' && git push origin v0.2.0
```

**Release notes are the matching `CHANGELOG.md` section**, extracted by the
workflow. A version with no entry falls back to generated notes rather than
blocking the release — but write the entry. Record what changed for someone
using the tool, not the commit subjects.

The workflow injects the tag into `main.version` with `-ldflags -X`, so a
release binary reports the tag and a `nix build` reports the flake's value —
keep the two in step. The README's download URLs point at
`releases/latest/download/`, so they keep working without edits.

The screenshots in `docs/` are regenerated by `docs/screenshot.sh`, which
carries the four commands that produce them; further pitfalls are in
`.claude/memory/feedback_verify_tui_with_pty.md`. It sanitises the hostname —
keep it that way. Run it where the journal is readable, or the log pane comes
out empty and the hero shot sells the tool short. (`charm-freeze`, used
earlier, does not render ANSI backgrounds at all; `termshot` does.)

## Gotchas

- New files must be `git add`ed before `nix build`/`nix run` — flakes only see
  git-tracked files. This fails confusingly, as a compile error about a missing
  symbol.
- `go mod vendor` must be re-run after any dependency change, and the vendor
  tree committed; `vendorHash = null` means nix trusts what is checked in.
- Reading the journal needs the `systemd-journal` group or root. Without it the
  log pane shows journalctl's own complaint — that is deliberate, not a bug.
- The NET columns are empty on most hosts because IP accounting is off by
  default. `DefaultIPAccounting=yes` (or per-unit `IPAccounting=yes`) enables
  them.
- `systemctl show '*.service'` does **not** match every loaded unit (62 of 105
  on one host), which is why the unit list comes from `list-units` first.
- Unit and slice names carry `\xNN` escapes (`my\x2dapp`); `unescapeUnit()`
  decodes them for display.

## Memory

`.claude/memory/` is the project's shared, cross-session memory — maintained by
**all** agents, not just Claude Code. It is committed to the repo so knowledge
persists across clones and tools.

**Every agent must:**
- **Read `.claude/memory/MEMORY.md` at the start of each session** — it's the
  index (one line per memory) of context learned in previous sessions. Follow
  its links into the individual memory files when relevant.
- **Update it when you learn something durable** — a non-obvious gotcha, a
  design decision and its rationale, a fact that isn't derivable from the code
  or git history. Add or edit the relevant file under `.claude/memory/` and keep
  the one-line pointer in `MEMORY.md` current. Don't record what the code, git
  history, or this file already state.
- **Commit memory changes together with the code change they describe**, in the
  same commit.

Format: one fact per file, kebab-case name, with a short frontmatter block
(`name`, `description`, `type: user|feedback|project|reference`) and the fact in
the body; link related memories with `[[other-name]]`. Match the existing files
for style.

Claude Code specifics: its auto-memory feature reads/writes a per-project
directory; on a fresh clone, symlink it into the repo so those writes land here:
```bash
ln -sfn "$(pwd)/.claude/memory" ~/.claude/projects/-$(pwd | tr '/' '-' | cut -c2-)/memory
```
Other agents just read/write `.claude/memory/` in the repo directly.
