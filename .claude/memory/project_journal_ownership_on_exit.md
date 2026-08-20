---
name: journal-ownership-on-exit
description: Journal streams are rooted at context.Background(), and cancel only ASKS — every exit must stopAndWait, because three traps make anything less silently leak children
type: project
---

Nothing cancels a journal stream's `journalctl -f` (or the ssh carrying a
remote one) but us, and `cancel()` merely requests it: exec kills and reaps
on other goroutines. Three traps make "just stop on the way out" wrong:

- bubbletea can consume an OS interrupt before `Update` ever sees the key;
- `defer m.journal.stop()` evaluates its receiver while it is still nil;
- `os.Exit` runs no defers at all.

Hence `stopAndWait()` — mark stopping, cancel, block on the stream's `done`
channel (closed only after the goroutine has reaped its child and closed
the batch channel), then wait the page WaitGroup: backwards page fetches
are separate tea.Cmds with their own children, registered via `beginPage`,
which refuses once stopping is set. `model.quit()` (the only in-model
exit; Ctrl-C is matched by TYPE at the top of `handleKey` so no modal
swallows it), `main`'s explicit post-`Run` cleanup, AND `syncJournal`'s
stream replacement all use it — replacement drops the stream's last
pointer, so the promise has to hold there too.

Regressions: TestCtrlCQuitsFromEveryState, TestCtrlCKillsTheJournalChild
(follow and page children ESRCH-reaped and channel closed before
`handleKey` returns), TestStreamReplacementReapsTheOldChildren. From TODO
UT-015 (2026-08 review).

Related: [[sanitize-at-ingress]], [[journalctl-traps]], [[program-work-ownership]]
