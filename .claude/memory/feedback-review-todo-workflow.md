---
name: review-todo-workflow
description: Record agent code-review findings in TODO.md as a provenance-pinned pending-review queue before treating them as accepted bugs
type: feedback
---

When asked to submit code-review findings, put them in the repository-root
`TODO.md` under **Pending review** rather than presenting them as already
accepted defects. Record the submitting agent, model, UTC date, exact reviewed
revision, whether working-tree changes were included, and the automated checks
that passed. Give each item a stable ID, priority, confidence, evidence, impact,
suggested resolution, regression coverage, and an explicit review outcome.

Each submitter gets its own `## Pending review — <agent>` section, but item IDs
continue the single shared `UT-###` sequence across all sections (Codex used
UT-001..020, Claude Code continued at UT-021) — never restart numbering or
invent a second prefix, so cross-references between queues stay unambiguous.
When a later review confirms or overlaps an earlier queue's item, say so in the
new item rather than silently resubmitting it (see UT-021 vs UT-009).

A pending finding becomes project truth only after maintainer triage or a
targeted reproduction. Accepted fixes move to `CHANGELOG.md` when they ship;
rejected findings keep the evidence that disproved them so another reviewer
does not revive the same claim. Do not duplicate the individual findings in
shared memory: the queue and source code already contain them.

Related: [[go-nix-workflow]], [[verify-remote-with-local-sshd]]
