---
name: monotonic-vs-boottime
description: systemd's *TimestampMonotonic stamps are CLOCK_MONOTONIC but /proc/uptime is CLOCK_BOOTTIME — suspend time makes "monotonic minus uptime" wrong, so cross-machine clock work must use realtime plus a sampled offset
type: project
---

The tempting way to skew-proof a remote unit age — take
`ActiveEnterTimestampMonotonic`, subtract the remote's `/proc/uptime`, and
avoid wall clocks entirely — is quietly wrong: systemd's monotonic stamps
count CLOCK_MONOTONIC, which stops during suspend, while `/proc/uptime`
counts CLOCK_BOOTTIME, which does not. Every suspended minute drives the two
apart, one-directionally.

**How to apply:** cross-machine time arithmetic here uses the remote's
REALTIME clock plus a freshly sampled offset (`date +%s` in the framed poll;
`parseEpochLine` is the one strict reader), applied uniformly so ordering
survives, with negative ages clamped only at display (`ageOf`). Verified in
triage for UT-007 (2026-08); the regressions live in `src/clock_test.go`.

Related: [[proc-stat-guest]], [[proc-stat-iowait]]
