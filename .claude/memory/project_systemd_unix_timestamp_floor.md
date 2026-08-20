---
name: systemd-unix-timestamp-floor
description: "`systemctl show --timestamp=unix` exists only from systemd v251 — v250 has --timestamp with pretty/us/utc/us+utc only; validate version floors against the exact enum value, not the option's existence"
type: project
---

systemd v247 (and still v250) had `--timestamp=` with exactly
pretty/us/utc/us+utc; the **`unix` choice arrived in v251** (May 2022 — its
release notes say so explicitly).
unitop's every detailed poll sends `systemctl show --timestamp=unix`, and its
gate said 247 for a while: hosts on 247–250 passed startup and then failed
every poll with `unrecognized option`-shaped errors.

**How to apply:** when a version floor guards a specific flag *value*, verify
the release that introduced the value, not the flag — read the release notes
for the enum member, or run the flag with that value against the candidate
version. `minSystemd` lives in `src/collect.go`; the boundary regressions are
`TestCheckVersion` (229/247/250 rejected, 251 accepted) and
`TestLocalProbeRetriesAndCachesThroughUpgrades` (asserts the live show argv).

Found as TODO UT-001 (2026-08 review); the local probe's cache/retry
semantics fixed alongside it are UT-016, recorded in TODO.md only.

Related: [[systemd-state-semantics]], [[journalctl-traps]]
