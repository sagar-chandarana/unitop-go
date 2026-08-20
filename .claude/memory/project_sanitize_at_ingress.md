---
name: sanitize-at-ingress
description: Every terminal-bound string is sanitized where it ENTERS, never at render — journal/property parse, -f/-H/hostname in newModel, key and paste payloads in the editor branch
type: project
---

The render layer measures and draws on the assumption that sanitizing has
already happened; a raw escape that slips in moves the cursor and breaks
every width. So the rule is positional: `sanitizeText`/`sanitizeMessage` run
at each ingress — journal fields and systemd properties at parse, `-f`, `-H`
and the local hostname in `newModel`, typed and pasted payloads in the
editor's `KeyRunes` branch. A new input joins that list; it does not get its
own cleaning at render time. The raw `-H` value survives only inside the ssh
transport (`runner.host`); every screen shows `hostLabel`/`sshTarget()`.

Regressions: TestPasteIsSanitizedInBothEditors,
TestHostLabelNeverReachesTheScreenRaw. From TODO UT-002 (2026-08 review).

Related: [[layout-invariant]], [[journal-ownership-on-exit]]
