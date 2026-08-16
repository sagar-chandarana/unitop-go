# Changelog

Notable changes to unitop. Versions follow [semantic versioning][semver],
though while the project is pre-1.0 the flags, keys and output are still open
to change.

[semver]: https://semver.org/spec/v2.0.0.html

## [Unreleased]

### Added

- The side pane and full view describe the service, not just name it. Alongside
  the existing pid, uptime, tasks, restarts and live counters they now show
  `type`, whether the unit is `enabled` (masked and disabled are flagged), its
  restart policy, the user and slice it runs in, the socket or timer that
  triggers it, whatever it reports of itself through `sd_notify`, the command
  it actually runs, and the unit file it came from.
- `MemoryMax` and a deliberately lowered `TasksMax` appear beside the current
  values, so `mem 76M/512M` says what the number is a fraction of. The default
  `TasksMax` — tens of thousands — stays hidden, as it means nothing.

- The log can be searched and filtered by level. `/` follows the focus — it
  filters the table, or searches the log when that is what you are reading —
  and `e` cycles everything → warning and above → error and above. Both are
  passed to journalctl as `-g` and `-p`, so they search the **whole journal**
  rather than the entries already fetched, and the follow stream and the
  backwards paging both honour them. An active filter is shown beside the unit
  name, so a filtered log is never mistaken for a quiet one.
- The log pages backwards. It still opens on the last 500 entries, but
  scrolling to the top now fetches the previous 500 and keeps going, so the
  buffer is a window onto the journal instead of all you can reach. The top
  line says which state you are in: loading, the genuine beginning of the
  unit's journal, or more available. Paging uses journald cursors, so pages
  join exactly — nothing duplicated or skipped.

### Changed

- The side pane grows its detail block on a tall terminal and shrinks it on a
  short one, giving ground to the log first.

## [0.1.3] — 2026-08-14

### Fixed

- Scrolling the log back down to the newest line resumes following. Scrolling
  up stopped following, as it should, but walking back down left it off — so
  the log sat frozen at the bottom while new entries piled up out of view. The
  mouse wheel already got this right; the keyboard did not. Both now share one
  rule: resting at the live end *is* following.
- The full view no longer offers or accepts keys that need a table. Sorting,
  reversing, tree, show-all, filter and pane focus (`s` `S` `r` `t` `a` `/`
  `tab`) silently did nothing there while still being listed in the footer.
- The action menu opens in a fixed place in the full view. It was anchored to
  the selected row's screen position, which is arbitrary once the table is
  hidden, so it appeared at a different height depending on where the invisible
  cursor sat.

## [0.1.2] — 2026-08-14

### Fixed

- The action menu draws as a popup instead of a curtain. It replaced each
  covered line from its left edge onward, so everything to the right of it
  vanished and half the table blanked out whenever the menu was open.
- The menu screenshot in the readme now actually shows the menu.

## [0.1.1] — 2026-08-14

### Changed

- unitop requires **systemd 247 or newer** on the machine it watches, and says
  so up front. Pointed at an older host it used to die with
  `unrecognized option '--timestamp=unix'`, which explains nothing. The version
  is read as part of the poll that was already happening, so this costs no
  extra round trip. The verdict is terminal: polling stops rather than retrying
  a host that can never work, and `R` still forces a retry.

### Fixed

- The readme's checksum step verified nothing. It had you save the binary as
  `unitop` while `SHA256SUMS` refers to `unitop-linux-amd64`, so
  `sha256sum -c --ignore-missing` skipped every entry and reported
  `no file was verified` — easy to read as success.
- The action menu no longer stretches across the screen for long template unit
  names; it is capped and the title truncated.
- The footer drops whole key hints on a narrow terminal instead of cutting one
  in half, which read as a rendering fault.

## [0.1.0] — 2026-08-14

First release.

- Sortable table of systemd services: CPU, memory, network and disk I/O rates,
  state, restart count, tasks and uptime. Rates are deltas between polls; a
  counter going backwards (a restart) reads as zero rather than a spike.
- Live journal tail for the selected unit, with priority colouring, wrapping
  and follow.
- `STATE` reports what actually happened rather than echoing systemd's
  `SubState`, which calls four different situations `dead`: `exited`, `dead`,
  `skipped`, `stopped`, and named failures such as `exit 1` or `sig KILL`.
- Tree view grouping units by slice, with each slice totalling everything
  beneath it.
- Unit actions — start, stop, restart, reload, kill, freeze, thaw,
  reset-failed — with confirmation for anything that interrupts a running
  service. Interactive polkit is never used; `-sudo` and `-read-only` are
  available.
- Full view: one unit's log at the full width with its live counters above it.
- Remote monitoring with `-H user@host` over a single multiplexed ssh
  connection; a poll is two round trips regardless of unit count.
- Mouse support: wheel scrolling, click-to-sort on column headers, right-click
  for the action menu.
- Static `linux/amd64` and `linux/arm64` binaries, a flake with an overlay, and
  nothing to configure.

[Unreleased]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/sagar-chandarana/unitop-go/releases/tag/v0.1.0
