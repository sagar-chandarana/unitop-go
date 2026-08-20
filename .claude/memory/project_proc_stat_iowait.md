---
name: proc-stat-iowait
description: Linux documents iowait as unreliable — it can DECREASE between samples; unsigned deltas must validate monotonicity, reject the interval, and let the baseline advance
type: project
---

`/proc/stat`'s `iowait` field is documented (proc(5), docs/filesystems/proc)
as "not reliable": the kernel keeps it per-CPU against nohz idle bookkeeping,
and the value **can go backwards** between two reads. `idle` as unitop sums it
includes iowait, so the combined idle component inherits the misbehaviour.

Consuming such counters as `uint64` deltas therefore needs three things, in
this order:

- **Validate component monotonicity before subtracting** — `cur >= prev` per
  component, not merely for the total. `float64(cur-prev)` on a backwards
  uint64 is ~1.8e19, which rendered as an enormous negative CPU percentage.
- **Reject the interval as a reset** — drop the derived figure for that one
  sample rather than clamp it into a plausible-looking lie; unrelated derived
  values from the same sample (network rates) must survive.
- **Advance the baseline anyway**, so the next well-formed sample measures a
  clean interval and recovers by itself.

The synthetic regression is `TestHostCPUSurvivesBackwardsIowait` in
`src/hostcpu_test.go` (iowait falls 50→20 while user works; then a recovery
sample). Found as TODO UT-005 (2026-08 review).

Related: [[proc-stat-guest]]
