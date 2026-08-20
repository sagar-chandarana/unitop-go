---
name: two-agent-collaboration
description: Reviewing/fixing this repo as two agents (Claude implements, Codex reviews) over a shared append-only comms file — the held-diff → independent-verify → ack → commit loop
type: feedback
---

The 2026-08 review ran as a two-agent collaboration: Claude owned
implementation, Codex ran an independent adversarial review, and the two
coordinated through one append-only log at `/tmp/unitop/agent-communication.md`.
It worked — a 30-item correctness queue and a 6-item security queue (2
rejected), 23 commits, every one reviewed before it landed. Real bugs
surfaced only in the exchange: a height-1 render panic, an exec-vs-EXIT-trap
leak, tmux's single-argument shell fallback, an orphaned-grandchild test that
hung, and a falsely-green gate report Claude posted because `| tail -1`
masked a non-zero exit.

The loop, per item:
- The finding is a pending `TODO.md` entry first, not an accepted bug (see
  [[review-todo-workflow]] for the provenance fields). When both agents review
  in parallel, reconcile the two lists — dedupe, agree exploitable-vs-hardening,
  record rejects with reasoning — before assigning `UT-###` IDs. IDs are one
  shared sequence; a rejected ID is never recycled.
- Hold the whole diff in the working tree (code + tests + TODO outcome +
  changelog); do not commit until the other agent acks the exact path set.
- Report gates with EXPLICIT exit codes — `echo "exit=$?"`, never a `go test`
  piped through `tail`, which hides the real status.
- Runtime-prove the load-bearing claims (a panic, an OOM, a reap-before-return)
  rather than reasoning; the reviewer independently reproduces.
- Commit exactly the acked paths, co-authored, and append the hash to the
  comms log.

Comms hygiene: append strictly at physical EOF; entries can still land out of
chronological order, so watch a whole-file diff of the log, not a header
count, or an inserted reply is missed. Restate the other agent's point before
acting on it. When ownership is contested and the reviewer won't take the
implementation, Claude keeps it and the reviewer stays read-only.

Who led: two seats, not co-implementation. Codex led the *process* — it
authored the protocol, was queue-master (per-item handoffs), ran an immutable
post-commit audit of every landing, and held the ack that gated each commit.
Claude led the *code* — held the diffs, produced the runtime proofs, made the
commits Codex then audited. Message volume (Codex 129, Claude 87 of 217)
measures role, not dominance: the reviewer speaks on every item twice
(handoff + audit). Leadership is "who set the protocol and gated the commits",
and that was Codex. The one inversion: at the UT-003/013 "takeover" Codex
stalled on taking implementation, the user said "you do", and Claude reclaimed
the implementer seat for good.

Value derived: the loop is slower than solo, and what it bought was the class
of bug an implementer's own green suite is blind to — each of these surfaced
only in the exchange, when Codex demanded a proof: a height-1 render panic, an
exec-vs-EXIT-trap child leak, an orphaned-grandchild test that hung to 3s,
tmux's single-argument shell fallback, the `| tail -1` false-green (which
became the explicit-exit-code rule), and a docs memory-ceiling overstatement
corrected to a measured 20.39 MiB. Net: 37 findings closed (2 rejected), 23
independently-verified commits. Reach for it when correctness matters more
than speed; skip it for throwaway work.

Related: [[review-todo-workflow]], [[verify-remote-with-local-sshd]]
