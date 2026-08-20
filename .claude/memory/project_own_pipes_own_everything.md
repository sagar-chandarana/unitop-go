---
name: own-pipes-own-everything
description: Taking a command's pipes forfeits everything Output() gave for free — classification, reaping order, backpressure, and prefix contiguity all become your job
type: project
---

When code takes `StdoutPipe`/`StderrPipe` instead of `Output()`, four
guarantees silently become its own responsibility:

- **`ExitError.Stderr` is permanently empty** — classify failures from what
  YOU captured, over the whole drained stream: a diagnostic that was
  clipped or discarded still counts as the command having spoken. Make the
  blank test Unicode-aware (an NBSP line fakes silence otherwise) and pick
  the safe side for its limits — a multibyte space split across a read
  boundary should read as "spoke", never as "silent".
- **`Wait()` is safe only after both pipe readers reach EOF and are
  joined** — and cancellation only kills; reaping is still Wait's.
- **The child must never block on a full pipe** — every cap is a cap on
  what you RETAIN; reading continues to EOF regardless.
- **Bounded retention must LATCH** once anything is dropped, or a smaller
  later item slips in and the "contiguous newest prefix" silently isn't.

Where: `runFinite`/`stderrPump` in `src/journal.go`; the regressions are
the `journal_io_test.go` battery (latch, flood, clip, classification).
From TODO UT-003/UT-013 (2026-08 review).

Related: [[journal-ownership-on-exit]], [[journalctl-traps]]
