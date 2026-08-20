# Memory

## Product direction

- [No charts — they were built and removed on request](feedback_no_charts.md) — the full view shows live numbers, not history; don't reintroduce sparklines
- [Keys are deliberate: enter = full view, x = actions, f = follow](feedback_key_bindings.md) — earlier arrangements were rejected; check here before rebinding
- [Only red and blue are readable on both real themes](project_palette_contrast.md) — measured contrast per ANSI index; why headings, the sorted column and frames stopped using hue and use weight instead

## systemd semantics

- [inactive/dead means four different things](project_systemd_state_semantics.md) — `ExecMainCode` + `ConditionResult` tell them apart; includes real sampled tuples and the "code=0 while running" trap
- [Four journalctl behaviours no man page states](project_journalctl_traps.md) — `-g` is not a seek (which is why the log pane is two commands), `-n 0` defeats `--after-cursor`, `-n N` order depends on the filter, `-g` exits 1 on no match
- [--timestamp=unix needs systemd 251 — validate floors against the enum value, not the flag](project_systemd_unix_timestamp_floor.md) — v250 has --timestamp without unix; 247–250 passed the old gate and failed every poll
- [/proc/stat counts guest time twice on purpose](project_proc_stat_guest.md) — the man page is silent, the kernel source is the authority, and only hypervisors show the bug
- [iowait can run backwards — validate monotonicity, reject the interval, advance the baseline](project_proc_stat_iowait.md) — a backwards uint64 delta is ~1.8e19; the fix pattern and the synthetic regression
- [systemd monotonic stamps vs /proc/uptime: suspend splits CLOCK_MONOTONIC from CLOCK_BOOTTIME](project_monotonic_vs_boottime.md) — skew-proof cross-machine ages with realtime + a sampled offset, never monotonic-minus-uptime
- `systemctl show '*.service'` matches only ~60% of loaded units — the unit list must come from `list-units` first
- Unit/slice names carry `\xNN` escapes (`my\x2dapp.slice`); parent derivation splits on literal `-`, so escaped names nest correctly by luck as well as by design

## Working on this repo

- [Code-review findings start as a provenance-pinned pending queue](feedback-review-todo-workflow.md) — record model, date, exact revision, evidence and eventual outcome in `TODO.md` before treating a finding as accepted
- [Two agents: Claude implements, Codex reviews, over one comms file](feedback-two-agent-collaboration.md) — held diff -> independent verify -> ack -> commit; reconcile parallel findings before assigning IDs; explicit exit codes, EOF appends
- [Verifying the TUI needs a pty, and frames arrive as diffs](feedback_verify_tui_with_pty.md) — the `script` recipe plus why captured output looks like overlapping half-screens
- [Rendering bugs hide in sizes nobody tests](feedback_layout_invariant.md) — one over-wide line spoils the whole screen; the matrix that found six, and why to re-run it with the backstop off
- [Sanitize at ingress, never at render](project_sanitize_at_ingress.md) — the render layer assumes it; new inputs join the ingress list; raw -H lives only in the transport
- [Every exit stopAndWaits the journal children](project_journal_ownership_on_exit.md) — cancel only asks; bubbletea eats interrupts, defer evaluates a nil receiver, os.Exit runs no defers
- [Bubble Tea never waits Cmds — own their children](project_program_work_ownership.md) — begin inside the closure, mutex-gated closing + WaitGroup; Add can never race Wait; drain owners before the mux
- [Own the pipes, own everything Output() gave for free](project_own_pipes_own_everything.md) — classification from captured results, Wait after reader joins, drain past every cap, latch the retention
- [`-H` needs no second machine](feedback_verify_remote_with_local_sshd.md) — `docs/helpers/local-sshd.sh`; it settled a wrong report about ssh's `--` that reading could not
- [Go + nix loop: vendor, git add, then build](feedback_go_nix_workflow.md) — `nix build` also runs the tests, in a sandbox with no systemd/network
- [runtimeInputs must cover every command — hostname is not in coreutils](project_runtimeinputs_coverage.md) — gaps hide behind ambient PATH and only die in minimal environments; prefer `uname -n`
- [Remote poll is one shell line and two ssh round trips](project_remote_poll.md) — a `#` in the marker once silently emptied the unit list
- [Why exec and not D-Bus, with the measurements](project_dbus_vs_exec.md) — 1.7× at best, the journal and `-H` both still need exec; a poll costs ~265ms so `-i 250ms` is already saturated

## Reference

- [Picking a host to verify against, read-only](reference_verification_hosts.md) — what makes one useful, and the unit shapes that pin each code path
