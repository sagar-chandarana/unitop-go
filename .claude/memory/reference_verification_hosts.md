---
name: verification-hosts
description: How to pick a host to verify unitop against, and which unit shapes exercise the interesting cases
type: reference
---

`go test` proves the frame composes; only a real host proves the tool reads
systemd correctly. Verify against one, **read-only** — polling and following
logs only. Never exercise the action menu against a machine you care about.

A good verification host is not the workstation you develop on:

- **IP accounting on** (`DefaultIPAccounting=yes`) — otherwise the NET columns
  are all `-` and you are not testing them at all. Most machines have it off.
- **A few hundred units**, including oneshots and template instances, so the
  tree, the column-drop layout and the batching all get exercised.
- **Reachable with non-interactive key auth**, since `-H` sets `BatchMode=yes`
  and will never prompt.
- A **different systemd major** from the local one is a bonus; property
  availability does shift between releases.

Conversely, a machine where the journal is *not* readable (no `systemd-journal`
group, not root) is the right place to check that the log pane surfaces
journalctl's permission complaint instead of sitting blank.

Unit shapes worth hunting for, each of which pins a different code path:

| shape | how to spot it | what it tests |
|---|---|---|
| oneshot that ran and finished | `Type=oneshot`, `inactive/dead`, `ExecMainCode=1 ExecMainStatus=0` | the `dead` → `exited` labelling; hidden by default, so use `-a` |
| skipped by a condition | `ConditionResult=no`, or `Result=exec-condition` | the `skipped` label |
| genuinely failed | `ActiveState=failed`, `Result=exit-code` | failure colouring and `exit N` |
| escaped names | a slice or unit containing `\x2d` | `unescapeUnit()` |
| nested slices | anything under a `foo-bar.slice` | tree depth and aggregation |
| busy network service | non-zero `IPIngressBytes` | NET rates and their heat colours |

Keep concrete host addresses out of this repo; pass them on the command line.

Related: [[systemd-state-semantics]], [[remote-poll]], [[verify-tui-with-pty]]
