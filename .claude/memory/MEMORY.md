# Memory

## Product direction

- [No charts — they were built and removed on request](feedback_no_charts.md) — the full view shows live numbers, not history; don't reintroduce sparklines
- [Keys are deliberate: enter = full view, x = actions, f = follow](feedback_key_bindings.md) — earlier arrangements were rejected; check here before rebinding

## systemd semantics

- [inactive/dead means four different things](project_systemd_state_semantics.md) — `ExecMainCode` + `ConditionResult` tell them apart; includes real sampled tuples and the "code=0 while running" trap
- `systemctl show '*.service'` matches only ~60% of loaded units — the unit list must come from `list-units` first
- Unit/slice names carry `\xNN` escapes (`my\x2dapp.slice`); parent derivation splits on literal `-`, so escaped names nest correctly by luck as well as by design

## Working on this repo

- [Verifying the TUI needs a pty, and frames arrive as diffs](feedback_verify_tui_with_pty.md) — the `script` recipe plus why captured output looks like overlapping half-screens
- [Go + nix loop: vendor, git add, then build](feedback_go_nix_workflow.md) — `nix build` also runs the tests, in a sandbox with no systemd/network
- [Remote poll is one shell line and two ssh round trips](project_remote_poll.md) — a `#` in the marker once silently emptied the unit list
- [Why exec and not D-Bus, with the measurements](project_dbus_vs_exec.md) — 1.7× at best, the journal and `-H` both still need exec; a poll costs ~265ms so `-i 250ms` is already saturated

## Reference

- [Picking a host to verify against, read-only](reference_verification_hosts.md) — what makes one useful, and the unit shapes that pin each code path
