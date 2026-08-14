---
name: systemd-state-semantics
description: systemd renders four different situations as inactive/dead; ExecMainCode + ConditionResult tell them apart
type: project
---

`SubState` alone is not enough to describe a unit. Every oneshot that has run to
completion sits in `inactive/dead` — the same state as one that never started —
which reads as "something is wrong" when it means "it finished". `unitop`
resolves this in `src/state.go` (`Unit.StateLabel()`).

The discriminators are **`ExecMainCode`** (the main process's `wait(2)` si_code:
0 = no main process has exited, 1 = `CLD_EXITED`, 2 = `CLD_KILLED`,
3 = `CLD_DUMPED`) and **`ConditionResult`**.

Real tuples sampled from live hosts, all of which systemd shows as dead:

| unit | Result | Code | Status | Cond | label |
|---|---|---|---|---|---|
| `fetch-certs` | success | 1 | 0 | yes | `exited` |
| `emergency`, `nix-daemon` (socket-activated) | success | 0 | 0 | yes | `dead` |
| `systemd-boot-random-seed`, `prepare-kexec` | success | 0 | 0 | **no** | `skipped` |
| `net-setup@*` | **exec-condition** | 0 | 0 | no | `skipped` |
| (stopped long-running service) | success | 2 | 15 | yes | `stopped` |
| `deploy-config` | exit-code | 1 | 1 | yes | `exit 1` |

**The trap:** a *running* `Type=simple` service also reports `ExecMainCode=0`,
because its main process has not exited yet. `ExecMainCode == 0` therefore means
"never ran" **only for non-active units**. `Ran()` is only consulted inside the
`inactive` branch of `StateLabel()` and `stateColor()` for exactly this reason.

Failure results are named rather than all collapsing to "failed":
`exit-code` → `exit N`, `signal`/`core-dump` → `sig TERM`, plus `timeout`,
`watchdog`, `oom-kill`, `start-limit-hit` → `limit`, `resources`, `protocol`.
All labels must fit the 9-column STATE field; a test asserts it.

**How to apply:** when you meet a tuple that produces a confusing label, add it
to the case table in `src/state_test.go` rather than special-casing at a call
site. Every case in that table came from a real host.

Related: [[verification-hosts]]
