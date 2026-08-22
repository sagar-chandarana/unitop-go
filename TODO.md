# TODO

## Completed review — Codex

| Field | Value |
| --- | --- |
| Queue status | Complete — triaged (all outcomes recorded) |
| Submitted by | OpenAI Codex |
| Review model | GPT-5 |
| Submitted | 2026-08-19 UTC |
| Reviewed revision | `799cfebcb8a7db2bc7b9036b065ddb5e6d78725d` (`v0.3.1`) |
| Scope | Committed contents at the reviewed revision only; working-tree changes were deliberately excluded |
| Automated checks | `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt -l` passed under `nix develop` |

These code-review findings have all been triaged; accepted findings were
actioned. Each records its review outcome — accepted and implemented (checked),
duplicate, or rejected — and accepted items carry their named regression
coverage as applicable; the few explicit deferrals remain recorded as such and
unchecked. This section is the retained historical record, not a live queue.

Priority guide: **P1** is high-impact or security-sensitive, **P2** is a
normal correctness/reliability defect, and **P3** is an edge case or
hardening opportunity.

### P1 — high priority

#### [x] UT-001 — Align the minimum systemd version with `--timestamp=unix`

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/collect.go:196` always invokes `systemctl show` with
  `--timestamp=unix`, while `src/collect.go:239-244` accepts systemd 247 and
  newer. The timestamp style was added in systemd 251.
- **Impact:** Hosts running systemd 247–250 pass the startup gate and then fail
  every detailed poll, despite being documented as supported.
- **Suggested resolution:** Either require systemd 251 or avoid the newer
  timestamp style on older versions. Update `readme.md` at the same time.
- **Regression coverage:** Exercise the version gate and generated arguments
  for 247, 250, and 251.
- **Reference:** [systemd v251 release notes](https://github.com/systemd/systemd/releases/tag/v251)
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Refuse systemd 247-250 up front, and make the refusal honest"). Triage pinned the true floor: v250 advertises --timestamp with only pretty/us/utc/us+utc; the v251 release notes introduce the unix choice. minSystemd is 251 (raised, per triage, rather than adding an old-format parser); README, messages and tests updated. Regressions: TestCheckVersion (229/247/250 rejected, 251+ accepted) and TestLocalProbeRetriesAndCachesThroughUpgrades asserts the detailed show argv still opens with exactly [show --timestamp=unix].

#### [x] UT-002 — Sanitize filter text before it reaches the terminal

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/model.go:396-423` appends arbitrary `KeyRunes` directly.
  Bubble Tea bracketed-paste events can contain newlines and terminal control
  sequences. The value is rendered in `src/view.go:548-550` and
  `src/view.go:1462-1485`; the initial `-f` value enters through
  `src/main.go:22,53` without sanitization as well.
- **Impact:** A pasted filter can add physical rows, break width calculations,
  or emit ESC/OSC/CSI sequences to the user's terminal.
- **Suggested resolution:** Flatten and sanitize user-entered and flag-provided
  filters at ingestion. Keep the journalctl value as one argument; this is a
  terminal-safety issue, not shell injection.
- **Regression coverage:** Paste multiline text, C0 controls, ESC/CSI/OSC
  sequences, and ordinary Unicode into both filter editors. Invalid UTF-8
  enters via the initial -f flag — corrected during triage: real Bubble Tea
  bracketed-paste decoding drops malformed bytes before the model sees them.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Sanitize every terminal ingress and give quitting one exit"). Sanitized at ingress: the shared KeyRunes branch (sanitizeText, so a bracketed paste's newlines/C0/escapes are neutralized before the editor holds them; KeySpace kept separate per UT-014; the journal grep stays one argv), the -f flag, and — the adjacent accepted gap review found — the hostLabel: -H or the local hostname was rendered raw on the startup screen, the header and inside troubleshooting advice; all three now render the sanitized label via sshTarget(), with the raw value retained only for the ssh transport. A raw -H that is nothing but a dropped escape sequence sanitizes to an empty label; it falls back to "remote" (remote-ness stays keyed on r.host, the transport keeps the raw value) — TestAllEscapeHostLabelFallsBackToRemote. Regressions: TestPasteIsSanitizedInBothEditors (hostile payload with exact expected text, no raw ESC in the editor, exact-height frame with the pasted sequence absent, ordinary Unicode untouched), TestInitialFilterFlagIsSanitized (invalid UTF-8 through -f, which real paste decoding cannot carry), TestHostLabelNeverReachesTheScreenRaw (remote -H, hostname-like local value, startup, failure+advice, connected header).

#### [x] UT-003 — Bound memory used by finite journal reads

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/journal.go:94-104` and `src/journal.go:278-285` use
  `Cmd.Output()` and parse the complete result after it exits. This bypasses
  the live reader's explicit 4 MiB per-entry cap at `src/journal.go:483-514`.
  The backward-page request at `src/journal.go:99-103` also omits
  `journalFields`, so journalctl returns every JSON field.
- **Impact:** Initial backlog and backward paging can allocate hundreds of
  large JSON entries at once and exhaust memory.
- **Suggested resolution:** Stream finite reads through the same bounded entry
  parser used by follow mode, with a bounded stderr capture.
- **Regression coverage:** Feed many near-limit entries and an oversized entry
  through both backlog and paging paths; assert bounded memory and a useful
  placeholder/error.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Stream the finite journal reads and pump every stderr"). One streamed primitive (runFinite) serves backlog and backwards paging: newest-first through StdoutPipe, the 4 MiB per-record cap kept, a 16 MiB aggregate retained-page cap added, discard LATCHED after the first overflow so the page stays the contiguous newest prefix, and everything older drained through EOF — peak retention is the budget plus one bounded record, and the live 20k-entry model's count-based bound is a separate thing, unclaimed here. Truncation is partial success: an honest cursorless boundary meta line, the oldest retained cursor still anchoring the next page. Paging carries journalFields; the anchor is dropped by position (an oversized anchor is a cursorless placeholder but still the anchor); atEnd only when untruncated and fewer than n records followed; an unanchorable page returns blocked — terminal via atEnd plus the explanation, warnings preserved. Regressions: TestFiniteReadTruncatesHonestly, TestAggregateTruncationLatches (small-after-overflow), TestOversizedRecordsBecomePlaceholders, TestPageArgvCarriesJournalFields, TestAllCursorlessPageIsBlocked, TestBlockedPageDoesNotRelaunch.

### P2 — correctness and reliability

#### [x] UT-004 — Put the SSH control socket in a private directory

- **Status:** Accepted — implemented
- **Confidence:** High — the interference half was reproduced during triage
  (a squatted listener at the predictable path; rc=124); impersonation was
  never proven and is not claimed
- **Evidence:** `src/collect.go:26-30` builds the predictable path
  `/tmp/.unitop-<pid>.sock`, and `src/collect.go:40-44` enables opportunistic
  connection sharing. OpenSSH recommends a directory not writable by other
  users for this use.
- **Impact:** On a multi-user host, another local user can pre-bind the
  predictable mux endpoint: a squatted socket makes every real
  ControlMaster=auto connection hang out its attempt (reproduced: rc=124 at
  the three-second timeout). Narrowed during triage from the original
  "interfering with or impersonating" — no protocol impersonation is claimed.
- **Suggested resolution:** Create a mode-0700 temporary directory and place
  the control socket inside it; clean up the owned directory on exit.
- **Regression coverage:** Assert that the socket parent is private and unique,
  and attempt a pre-bind from a different UID where CI permits it.
- **Reference:** [OpenSSH `ControlPath` guidance](https://man.openbsd.org/ssh_config#ControlPath)
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Give the ssh mux a private home and distrust the remote framing"). Impact narrowed during triage to the PROVEN pre-bind interference: a squatted mode-0777 listener at the predictable ControlPath made a real ControlMaster=auto connection hang out its attempt (rc=124 at 3s); no cross-UID impersonation is claimed. Each remote runner now owns a unique MkdirTemp (0700) parent with a fixed socket name inside; if the directory cannot be made, unitop adds no mux options of its own (a user's ssh config may still share safely) rather than falling back to a public path; close() sends ssh -O exit, then removes only the owned directory, and is idempotent — main reaches it twice. Regressions: TestRunnerMuxSocketLivesInAPrivateParent (unique parents, 0700, socket inside), TestRunnerCloseIsIdempotent, TestLocalRunnerCreatesNothing, TestUnusableTempDirOmitsUnitopMuxOptions (a unique missing TMPDIR fixture: no dir, no socket, no ControlMaster/ControlPath options), and every test constructing a remote runner now closes it via the testRunner helper — the full suite leaves zero unitop-mux-* directories behind. This item remains unrelated to the previously rejected post-host `ssh host -- command` report: that syntax is valid, and the local-sshd rig re-confirmed it during this group's end-to-end probe.

#### [x] UT-005 — Handle a decreasing `/proc/stat` iowait counter

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/host.go:154-160` subtracts `cur.cpuIdle-p.cpuIdle` as
  `uint64` after checking only that total CPU time increased. `cpuIdle` includes
  iowait, which Linux documents can decrease.
- **Impact:** The subtraction wraps and can produce an enormous negative CPU
  percentage for one sample.
- **Suggested resolution:** Treat a backwards idle component as a reset or
  clamp that sample before unsigned subtraction.
- **Regression coverage:** Use a sample whose total increases while iowait, and
  therefore combined idle, decreases.
- **Reference:** [Linux `/proc` documentation](https://docs.kernel.org/filesystems/proc.html)
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Never let a glitched sample lie about the host"). A sample whose idle component ran backwards (or outran the total) has its CPU figure rejected rather than wrapped through uint64; the baseline still advances so the next well-formed sample recovers, the same tick's network rates are untouched, and the figure can never leave 0..100. Regression: TestHostCPUSurvivesBackwardsIowait — both guards (backwards idle, and monotonic idle outrunning the total) each rejected to exactly zero with the same tick's network rates asserted exactly, each followed by a sample proving the baseline recovered.

#### [x] UT-006 — Make systemctl actions genuinely noninteractive

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/actions.go:53-66` promises never to invoke interactive
  polkit, but direct `systemctl` arguments omit `--no-ask-password`.
- **Impact:** Authorization can prompt, stall, or seize the TUI. `sudo -n` only
  protects the optional sudo path.
- **Suggested resolution:** Pass `--no-ask-password` for every systemctl action
  and keep `sudo -n` when sudo mode is enabled.
- **Regression coverage:** Assert the exact local, remote, direct, and sudo
  argument lists.
- **Reference:** [systemctl option defaults in systemd v250](https://github.com/systemd/systemd/blob/v250/src/systemctl/systemctl.c)
- **Review outcome:** Accepted and implemented (same split, same commit). actionCommand builds every action's argv with --no-ask-password immediately after systemctl (present since v247, the supported floor), for direct and sudo forms alike — sudo keeps -n — and the runner receives that argv unchanged locally and over ssh (r.command(name, args...) is transport-independent). Regressions: TestActionCommandsNeverPrompt (exact direct and sudo argv including kill --signal option order; flag position and unit-last for every action) and TestActionArgvSurvivesBothTransports (local exec.Cmd.Args verbatim; the remote ssh tail carries the identical order, the tokens being shell-safe).

#### [x] UT-007 — Stop mixing client and remote wall clocks

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** Remote `ActiveEnterTimestamp` is parsed in
  `src/collect.go:463-464`, then aged with the viewer's clock in
  `src/view.go:900-909` and `src/view.go:1093-1094`. When an empty backlog is
  followed, `src/journal.go:211-215` records client `time.Now()` and
  `src/journal.go:258-265` sends that epoch to remote journalctl as `--since`.
- **Impact:** Clock skew makes remote uptime wrong. If the remote clock is
  behind, a newly followed empty or filtered journal can remain silent until
  the remote clock catches up.
- **Suggested resolution:** Capture an appropriate remote-clock reference in
  the existing poll round trip and use it consistently for display and empty
  backlog handoff.
- **Regression coverage:** Cover remote clocks both ahead of and behind the
  client for uptime and for the backlog-to-follow transition.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Keep remote clocks out of the arithmetic"). Both halves: (1) the framed remote poll gains a leading `date +%s || exit` clock section behind a third strict marker line; the offset (client minus remote) is re-sampled each poll and anchored at the LAUNCH instant, wall-only — measured after the round trip it would fold the whole script and return latency into every age; anchored at launch the error is bounded by one-way outbound latency plus the floor-second, overstating age by at most that, and normalizeClocks applies it UNIFORMLY to ActiveEnterTimestamp/StateChangeTimestamp — no per-value clamping, which would collapse distinct near-future stamps and scramble sort/tree order; the displayed age clamps to zero in the shared ageOf helper instead, and the stamps stay wall-only time.Times. The monotonic alternative (ActiveEnterTimestampMonotonic minus /proc/uptime) was rejected in triage: systemd stamps are CLOCK_MONOTONIC, /proc/uptime is CLOCK_BOOTTIME, and suspend drives them apart. (2) With an empty backlog, the follow's --since boundary is the REMOTE's now, probed with `date +%s` before the backlog on remote streams only; floor-second favors replay over loss; a failed or babbling probe is a terminal visible meta failure that the UT-012 dead-stream recovery retries; the cursor handoff is untouched. Both probes share one strict parser (parseEpochLine: bare decimal digits plus one exact LF or CRLF terminator — a bare CR is not one — positive int64, no surrounding whitespace, no extra lines). Regressions: TestNormalizeClocksShiftsUniformly (both fields, order kept, locals untouched), TestAgeOfClampsDisplayOnly, TestParseEpochLine, TestPollClockOffsetBothSigns (±1h and the babbling-clock retryable case), TestEmptyBacklogFollowsFromTheRemoteClock (±90s, filtered, --since equals the probed epoch), TestCursorHandoffIsUnchanged, TestLocalStreamIssuesNoClockProbe (canary), TestBrokenClockProbeFailsVisiblyAndRetryably. Review widened coverage, all in: TestUptimeRendersTrueAgeUnderSkew (raw remote-frame stamps through the real normalizeClocks, both skew signs; the aged unit's true ~90s and the fresh unit's literal "0s"/"up 0s" clamp asserted in the table row AND in unitStats), TestUptimeSortSurvivesNearFutureStamps (raw remote stamps normalized, then real buildRows both flat and tree=true: strict newest-first flat order, the busy slice leading via its newest-child aggregate, in-slice order intact; whole-second stamps since the key is Unix()-grained by design), TestGapEntryIsDeliveredUnderSkew (the fake follow honours --since like journalctl, so a client-clock boundary would fail the ahead case; filtered and not, both signs, entry delivered; the fake exits finitely so teardown owes nothing to nested children), TestNonzeroClockProbeAndLocalPoll (exit-status failures for poll and stream; the local poll runs no date), TestSlowPollDoesNotInflateTheOffset (a 2s remote script moves clockOff by ~nothing), TestClockProbeChildIsOwned (the recorded pid is fake ssh's own — the direct CommandContext child — which blocks in place of the probe; teardown mid-probe reaps exactly it before stopAndWait returns, and journalctl never launches — UT-015's contract extends to the probe).

#### [x] UT-008 — Reconcile log focus and lifecycle when the terminal resizes

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/model.go:233-239` handles `WindowSizeMsg` by storing the
  dimensions and clamping only the table cursor. Log visibility changes at the
  84-column threshold in `src/view.go:92-100`.
- **Impact:** Shrinking can leave focus on an invisible log: table keys are
  rejected while arrows scroll the hidden pane. The journal also keeps running
  while hidden, or fails to start after expansion while paused.
- **Suggested resolution:** Reconcile focus with pane visibility and call the
  journal lifecycle synchronizer whenever resize crosses the visibility
  threshold.
- **Regression coverage:** Resize across 84 columns in both directions while
  focused on logs, following, and paused.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Reconcile the log pane when the terminal resizes"). The WindowSizeMsg branch captures pane visibility before the geometry change, keeps both clamps, then enforces two things: focus may not rest on an invisible pane (healed unconditionally — any resize repairs an already-invalid state, not only an 84-boundary crossing), and an actual visibility flip is a deliberate pane transition through DIRECT syncJournal — hiding stopAndWaits the stream and clears its pane state, showing starts the selected unit immediately, paused or fatal notwithstanding, bypassing the dead-stream poll gate. Height-only and visible→visible changes churn nothing; fullView is visible at any width and showLogs=false at none, so neither creates a false transition. Regressions (spied streams cleaned synchronously; commands and pointers asserted, not geometry): TestShrinkUnderSplitReconcilesFocusAndStream (exact 84→83, focused and unfocused; nil command — the teardown is synchronous; exactly one teardown; pointer AND seeded logs/scroll/follow cleared; the visible table proven live via keyApplies plus a moving cursor), TestExpandAcrossSplitStartsTheStream (exact 83→84 for paused AND fatal models: exactly one stream, command returned, then a visible→visible resize churns nothing), TestNoFalsePaneTransitions (height-only, wide→wider, fullView crossing 84 with seeded content/generation/focus/scroll all surviving, user-hidden crossing), TestExpandWithNoSelectionStartsNothing, TestAnyResizeHealsInvalidFocus (hidden→hidden repair), TestExpansionBypassesTheRetryGate (a fresh same-target death: the deliberate transition restarts instantly and settles the debt the poll gate was deferring).

#### [x] UT-009 — Rebase log scroll offsets after geometry changes

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** The resize path at `src/model.go:233-239` and split-to-full-view
  transition at `src/model.go:640-647` retain `logScroll` even though available
  width changes wrapping. `src/view.go:1126-1152` can then skip the entire
  newly wrapped buffer.
- **Impact:** Existing journal entries are replaced by the empty-log notice
  after widening or changing views while deeply scrolled.
- **Suggested resolution:** Preserve a logical entry/line anchor or clamp the
  offset against the recomputed display height after geometry changes.
- **Regression coverage:** Deep-scroll wrapped entries, widen and narrow the
  terminal, and enter/leave full view without losing visible content.
- **Review outcome:** Accepted (implemented by Claude Code/Fable 5, 2026-08-19; commit "Keep the log window honest about what the buffer holds"). UT-021 pinned the unclamped sites: the tea.WindowSizeMsg handler, activateRow's full-view toggle, and escape()'s full-view exit — all three now call clampLogScroll, and renderLogWindow re-aims a stale offset at the buffer top instead of rendering the empty-pane notice. Regressions: TestGeometryChangesReclampTheLogScroll, TestOverScrolledWindowShowsTheBufferNotTheEmptyNotice.

#### [x] UT-010 — Keep every action-menu choice visible and positioned

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** The popup created by `src/actions.go:146-168` requires 12 rows,
  while `src/view.go:186-193` supports usable heights down to 10. Coordinates
  are clamped only when the menu opens, not on later resize.
- **Impact:** Lower actions or the whole popup can be invisible while keyboard
  selection remains active; Enter can execute a choice the user cannot see.
- **Suggested resolution:** Scroll or compact the menu at short heights and
  reposition or close it after resize. Never allow an invisible selection to
  execute.
- **Regression coverage:** Open and navigate every action at each supported
  height, then resize in every direction with the menu and confirmation open.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Keep the action menu visible, and cut text on graphemes"). menuGeometry is the one current-geometry answer: clamped anchor, drawn width, and an action VIEWPORT (first/visible) chosen so the cursor is always inside it — draw (menuBox renders exactly the viewport, with honest ↑n / "n more ↓" markers), the overlay anchor, and the mouse hit-test (viewport offset mapped back) all ask it, and openMenu stores only the wish, so a resize between open and render cannot strand anything: the geometry is recomputed at every use. The confirmation dialog was confirmed NOT stale in triage (it recentres each render) and was left alone. Regressions: TestEveryActionVisibleBeforeExecuteAtAllHeights (heights 10..17, every action visibly selected before Enter can fire, viewport containment at each step), TestMenuWrapHomeEndStayVisible (at the tightest supported height), TestMouseMapsThroughTheViewport (k-th drawn row = first+k, border and beyond = none), TestMenuSurvivesResizeInEveryDirection (five directions, selection proven on its exact drawn row — "start" can never pass by matching inside "restart" — popup contained), TestConfirmationSurvivesResizeCentred (a destructive confirmation stays centred, fully visible, same action pending through four resize directions — pinning the dynamic positioning triage confirmed correct), TestHostileAnchorSurvivesShrink (a bottom-right click anchor at 200×40 shrunk to 100×12 and 40×10: contained, selection on its drawn row — the previously confirmed staleness mechanism), and TestMenuStaysInsideThePane re-anchored to assert through menuGeometry. All selection assertions go through a shared selectedMenuRow helper that inspects the exact drawn row.

#### [x] UT-011 — Do not overwrite the oldest visible journal line

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/view.go:1155-1164` replaces `win[0]` with the top marker
  whenever `atTopOfLog` is true. `src/model.go:722-724` also considers a short
  buffer to be at the top.
- **Impact:** A one-entry journal displays only the marker. At the true journal
  beginning, the oldest retained display line is permanently inaccessible.
- **Suggested resolution:** Give the marker its own display row and reduce the
  data window accordingly instead of replacing data.
- **Regression coverage:** Cover zero, one, and several entries when the whole
  buffer fits and when scrolled to the absolute beginning.
- **Review outcome:** Accepted (implemented by Claude Code/Fable 5, 2026-08-19; commit "Keep the log window honest about what the buffer holds"). The top marker is a virtual display line above the buffer: clampLogScroll/atTopOfLog gain one step and renderLogWindow prepends the marker into the room the walk leaves. No existing test encoded the replacement in the end; TestLogWindowMatchesTheReference passed unchanged. Regression: TestTopMarkerDoesNotEatData.

#### [x] UT-012 — Clear or restart a journal stream after it ends

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** A matching-generation `journalBatch{done:true}` returns from
  `src/model.go:331-334` without clearing `m.journal`. The synchronization path
  at `src/model.go:1032-1035` treats that non-nil stream as live indefinitely.
- **Impact:** A transient journalctl or SSH failure permanently disables live
  logs until the unit, filter, or pane state is changed.
- **Suggested resolution:** Mark the stream stopped on `done` and restart it on
  the next synchronization opportunity, with backoff if needed.
- **Regression coverage:** End a same-generation stream, trigger normal and
  manual polls, and assert that exactly one replacement stream starts.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Let a dead journal stream come back"). A matching-generation terminal batch now retires the stream after its final lines land — stopAndWait (already-reaped, so effectively immediate; it exists for owned page fetches), pointer cleared, loading state cleared, death time recorded — and deliberately returns no restart command: a persistently failing journalctl must not hot-loop. Recovery is via postPollSync: the FIRST successful poll after the one-second retry gate starts exactly one replacement of the SAME dead unit/filter — a successful poll inside the gate deliberately starts nothing for it, and failed polls never restart it; a target that changed or vanished reconciles immediately, failed poll or not. Explicit gestures — R (now batching syncJournal), selection/filter changes, pane transitions — go through syncJournal directly, past the gate, which any deliberate sync resets. Stale-generation terminal batches cannot touch the live stream; hidden-pane polls start nothing. Review added two hardenings, both in: retirement bumps the generation (an owned page result already queued in bubbletea bounces instead of resurrecting a retired stream's errors or output, and duplicate terminal batches are inert the same way), and the gate holds the dead target's identity (unit+filter) so it defers ONLY the automatic same-target restart — a selection that moved on, or a poll that removed the unit entirely, reconciles immediately inside the gate window. Regressions: TestDeadStreamRetiresWithoutRestart, TestDeadStreamRestartsOnNextSuccessfulPoll (exactly one, no churn after), TestDeadStreamGateHoldsUnderFastPolls, TestFailedPollDoesNotRestartDeadTarget, TestExplicitRetryBypassesTheGate, TestStaleDoneCannotKillTheLiveStream, TestNoRestartWhilePaneHidden (plus the deliberate reopen recovering), TestQueuedPageResultBouncesOffRetirement, TestDuplicateTerminalBatchIsInert, TestGateDoesNotDeferReconciliation (selection moved via the real key path), TestRemovedUnitReconcilesImmediately (the poll removes the dead unit; rebuild reanchors; immediate reconciliation inside the gate). No memory added: the same-target gate rule is stated at postPollSync and derivable from it.

#### [x] UT-013 — Surface journal stderr while a follow remains alive

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** Backlog uses `Cmd.Output()` at `src/journal.go:278-285`, which
  discards stderr on success. Follow mode attaches a `bytes.Buffer` at
  `src/journal.go:314-321` and emits it only after `Wait` at
  `src/journal.go:400-408`.
- **Impact:** A permissions or visibility warning printed once by a continuing
  `journalctl -f` is hidden indefinitely, making an empty/partial pane look
  trustworthy. The stderr buffer can also grow without bound.
- **Suggested resolution:** Drain stderr concurrently through a bounded buffer
  and surface sanitized diagnostics while the process is running.
- **Regression coverage:** Use a fake journalctl that writes a warning and then
  follows indefinitely; verify prompt display and bounded capture.
- **Review outcome:** Accepted and implemented (same split, same commit). Every finite and follow command gets a concurrent stderrPump: 4 KiB per line (clipped lines raise the marker and keep their prefix), 64 KiB and 128 lines lifetime with exactly one "diagnostics suppressed" marker, draining forever after the caps so the child can never block, and notify muted for discarded lines so a flood cannot keep the UI select hot. Follow warnings surface while -f still runs; the tail keeps listening after stdout closes until the pump finishes; a silent nonzero exit is named in the done message; warn/flush/final all gate on cancellation. no-matches is classified from the captured result — exit 1, no records, no nonblank stdout notice, and no nonblank stderr anywhere in the drained stream, clipped and discarded bytes included. The blank predicate is Unicode-aware per 8 KiB read chunk, with one proven and accepted gap (Codex reproduced it): a multibyte space split across the chunk boundary reads as text, erring toward a real error message rather than a silent false no-match. Regressions: TestSuccessfulReadSurfacesStderr, TestStderrFloodIsBoundedAndMarked, TestStderrByteCapBindsAlone, TestClippedDiagnosticStillCounts, TestExitOneClassification, TestExitOneWithNoticeIsAnError, TestFollowWarningSurfacesWhileAlive, TestStderrOutlivesStdout, TestLiveFloodGoesQuietAfterSuppression, TestSilentNonzeroFollowExitIsNamed, TestCancelRacingNaturalEOF.

#### [x] UT-014 — Insert one space per space keypress in filters

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/model.go:419-423` first appends `msg.Runes`, which already
  contains `' '` for Bubble Tea's `KeySpace`, then appends another literal
  space.
- **Impact:** A phrase such as `timed out` becomes `timed  out`, silently
  changing table matching and journal regular expressions.
- **Suggested resolution:** Normalize the key event once and avoid the second
  append when `Runes` already contains the space.
- **Regression coverage:** Feed a real decoded space event, bracketed paste,
  and synthetic key events to both editors.
- **Review outcome:** Accepted and implemented (same split, same commit). KeyRunes and KeySpace are separate cases: a space event inserts exactly one space whether it carries the real " " rune or none (synthetic), and pasted KeyRunes text passes through as one payload. Regression: TestSpaceInsertsExactlyOneSpace (each editor table-tested with a real-shaped KeySpace, a rune-less synthetic one, and a Paste:true KeyRunes payload).

#### [x] UT-015 — Centralize Ctrl-C quit and child cleanup

- **Status:** Accepted — implemented
- **Confidence:** High — proven by the follow-mode child regression: the
  child is reaped and the channel closed before handleKey returns
- **Evidence:** The filter branch at `src/model.go:424-425` returns `tea.Quit`
  without `m.journal.stop()`, unlike the normal quit path at
  `src/model.go:448-450`. `src/actions.go:105-143` consumes unhandled keys, so
  an open action menu swallows Ctrl-C; confirmation treats it as cancellation
  while leaving the menu open.
- **Impact:** Ctrl-C can fail to quit or leave a quiet local `journalctl -f`
  process alive after the TUI exits.
- **Suggested resolution:** Route every quit gesture through one cleanup helper
  before modal handlers consume it.
- **Regression coverage:** Exercise Ctrl-C in the table, both filter editors,
  the action menu, and confirmation while tracking child termination.
- **Review outcome:** Accepted and implemented (same split, same commit). Ownership is complete, not just the follow: page fetches register with the stream via beginPage (refused once stopping — a Cmd scheduled after the quit launches nothing) and are waited alongside; syncJournal replaces streams with stopAndWait too, since replacement drops the last pointer. journalStream gained a done channel closed after the stream goroutine has reaped its children and closed the batch channel, and stopAndWait() (nil-safe both ways) blocks on it — cancel alone only asks, with exec killing and reaping on other goroutines. handleKey recognizes tea.KeyCtrlC by TYPE at its very top — before the too-small, disconnected, menu and filter dispatches that used to swallow or mishandle it — and routes it through model.quit(), the single exit, which stopAndWaits the stream — children reaped and channel closed before the keypress returns; q/esc quits go through the same helper, modal q semantics unchanged; a pasted ETX stays KeyRunes and is sanitized, not obeyed. main() additionally stopAndWaits unconditionally after p.Run() returns (not a defer: the receiver would be evaluated early and os.Exit runs no defers) because bubbletea can consume an OS interrupt before Update sees it. Regressions: TestCtrlCQuitsFromEveryState (seven states with a cancel-spy journal, each asserting tea.QuitMsg and the stop, plus the pasted-ETX case), TestQQuitsThroughTheSameExit, TestCtrlCKillsTheJournalChild (a real quiet-follow child AND a blocked --cursor page child: synchronously after handleKey returns, both are ESRCH exactly — reaped, not zombies — and the channel is closed, no polling), and TestStreamReplacementReapsTheOldChildren (a filter change reaps the old follow and page before syncJournal returns). Post-Run cleanup itself is unexercised by tests (main is not testable without refactoring; judged not practical, per the handoff's "if practical").

### P3 — edge cases and hardening

#### [x] UT-016 — Re-probe a cached unsupported local systemd version

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/collect.go:316-323` probes the local version only while the
  cached value is zero. A nonzero unsupported result remains cached across the
  manual retry path.
- **Impact:** Upgrading systemd while unitop is open cannot recover with `R`;
  the application must be restarted. Probe execution errors are also reported
  as a fatal unsupported-systemd verdict.
- **Suggested resolution:** Invalidate/re-probe on explicit retry and preserve
  the distinction between an unavailable probe and a parsed old version.
- **Regression coverage:** Inject old, then supported, version responses and
  verify that retry succeeds without restarting the model.
- **Review outcome:** Accepted and implemented, local scope (same split, same commit; narrowed per the 02:52:56 amendment — no model-side cache invalidation). The local probe parses into a temporary and caches only a version that passes: a rejected or malformed result stays uncached (still UnsupportedError), so the existing R/Enter retry re-probes the same collector and an upgraded host recovers without a restart; a probe that could not run propagates through wrapExec as an ordinary retryable error with its stderr; accepted versions stay cached across timer polls. The adjacent troubleshoot advice now names the failed binary (local systemctl / remote systemctl / ssh client). Regressions: TestLocalProbeRetriesAndCachesThroughUpgrades (a real scripted systemctl on PATH: fail→retryable+stderr, 250→UnsupportedError uncached, 251→same collector succeeds, then two polls with zero further probe calls against a failing stub), TestExplicitRetryRecoversAfterUpgrade (the user-visible half: one model takes the fatal verdict, the stub is upgraded, R's returned tea.Cmd is executed and fed back — fatal cleared, connected, units delivered), and TestTroubleshootNamesTheMissingBinary.

#### [x] UT-017 — Do not claim a trimmed buffer starts at the journal beginning

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/model.go:307-315` drops oldest buffered entries without
  clearing `logAtStart` after the true beginning had previously been reached.
- **Impact:** `src/view.go:1203-1218` chooses the "beginning" marker before the
  "buffer full" marker, so the pane falsely claims it still holds the journal
  beginning after discarding history. Paging is independently disabled while
  the buffer is at its configured cap.
- **Suggested resolution:** Clear `logAtStart` whenever trimming removes the
  first retained entry so the buffer-full marker wins. Treat paging while full
  as a separate retention-policy decision.
- **Regression coverage:** Reach the true beginning, append beyond the trim
  threshold, then return to the top and assert that the pane reports discarded
  history rather than the true beginning.
- **Review outcome:** Accepted (implemented by Claude Code/Fable 5, 2026-08-19; commit "Keep the log window honest about what the buffer holds"). The trim branch clears logAtStart; paging-while-full left untouched as a separate retention decision. Regression: TestTrimmingForgetsTheJournalBeginning.

#### [x] UT-018 — Preserve critical host status on narrow terminals

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/view.go:473-480` drops the right-hand side of `hjoin` when
  both sides do not fit. The compact header at `src/view.go:563-567` puts
  `PAUSED` and `NOT POLLING — R to retry` on that expendable side.
- **Impact:** Polling can be frozen or fatally stopped with no visible status
  at otherwise supported widths.
- **Suggested resolution:** Prioritize and shorten status before expendable
  identity or usage fields.
- **Regression coverage:** Assert visible paused and fatal status across the
  full supported width matrix.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Never let the header hide that polling stopped"). hjoinStatus is the focused helper: the right side's LAST element is critical status and never drops — expendable metadata ahead of it (sort/tree/all/filter) yields first, then the left identity/usage side gives up cells via truncation; only a status wider than the whole line would be cut, which no supported width does to PAUSED or the full "NOT POLLING — R to retry" phrase. It is used only when fatal or paused, so ordinary headers render exactly as before (hjoin untouched — unit-detail stats keep their existing semantics, per the do-not-globalize instruction). Regressions: TestCriticalStatusSurvivesEveryHeaderShape (the worst expendable crowd — long host label, live stats, long filter, tree/all/reverse, failed units — across width-compact 40 and 75, boundary 76, height-compact 120×19, and roomy 200×40, asserting exact frame height, no row over width, PAUSED visible or BOTH stopped fragments visible) and TestNormalHeaderInventsNoStatus (healthy headers at three shapes claim nothing and keep their shape).

#### [x] UT-019 — Disable SSH pseudo-terminal allocation explicitly

- **Status:** Accepted — implemented
- **Confidence:** Medium; configuration-dependent
- **Evidence:** `src/collect.go:34-45` does not pass `-T`. A user SSH setting
  such as `RequestTTY force` can produce CRLF output. Section parsing at
  `src/collect.go:339-340` expects an exact marker followed by `\n` and ignores
  whether `strings.Cut` succeeded.
  Additional evidence (2026-08-20, UT-001/016 pass): the remote probe
  pipeline `systemctl --version 2>/dev/null | head -1` discards the version
  command's exit status — when the rest of the script succeeds, a probe
  failure parses as version 0 and becomes a fatal "no systemd" verdict
  rather than a retryable error.
- **Impact:** The version may parse while the `/proc` and unit sections become
  empty, yielding a successful-looking zero-unit poll. And via the pipeline
  defect above: a remote probe failure whose later sections succeed parses
  as version 0 and lands as a fatal "no systemd" verdict instead of a
  retryable error.
- **Suggested resolution:** Add `-T`, check both marker splits, and tolerate or
  reject CRLF explicitly. Preserve and validate the version subcommand's own
  exit status through the pipeline rather than letting `head -1` mask it.
- **Regression coverage:** Test the generated SSH arguments and parse marker
  streams with LF, CRLF, missing, and duplicated delimiters. Also a remote
  script where the version command fails while later sections succeed: the
  result must be a retryable error, not a fatal version-0 verdict.
- **Review outcome:** Accepted and implemented (same split, same commit). Triage proved both halves: `ssh -G -o RequestTTY=force` resolves to `requesttty force` and against the local sshd a forced tty destroyed the command framing, while prepending -T resolved to `requesttty false` and restored it — so every runner invocation now carries -T. The remote poll pipeline preserves the version command's own status (`systemctl --version || exit`, no head, stderr riding the ssh exit error) so a probe failure is a retryable "remote poll" error instead of a fatal version-0 verdict even when later commands would succeed. parseRemotePoll replaces the boolean-ignoring Cuts: CRLF normalized, and a delimiter counts only when a WHOLE line equals the marker — unit descriptions are arbitrary text and may contain marker tokens, which stay data — exactly one of each, in order; anything else is a retryable malformed-poll error, never a successful zero-unit poll. Verified end to end over docs/helpers/local-sshd.sh with a real sshd: 105 units, host stats and a live journal through the private mux with -T. Regressions: TestRemoteArgvShape (incl. the valid host/--/single-command shape and quoting), TestParseRemotePollFraming (LF, CRLF, missing/duplicated/reversed marker lines), TestMarkerTokensInsideDataStayData (marker tokens embedded in and suffixing unit descriptions parse as data; duplicated standalone marker lines still fail), TestRemoteVersionFailureIsRetryable (fake ssh executes the real joined command; version fails with stderr while later commands would succeed → retryable with the message; then a healthy fake polls one unit through the strict framing).

#### [x] UT-020 — Make all clipping and menu sizing grapheme-aware

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** `src/view.go:1314-1350` slices styled text rune by rune and can
  split combining or ZWJ grapheme clusters. `src/format.go:199-206` can retain
  a whole double-width glyph after a left truncation and exceed its target.
  `src/actions.go:183-190` sizes menu titles by rune count rather than terminal
  cells.
- **Impact:** Combining text can attach to popup borders, emoji sequences can
  be corrupted, the filter caret can disappear, and CJK/emoji unit names can
  make menus too narrow or prematurely truncated.
- **Suggested resolution:** Use one ANSI- and grapheme-aware cell slicer for
  both directions, and use terminal cell width for menu sizing.
- **Regression coverage:** Add combining marks, CJK, flags, skin-tone emoji,
  and family ZWJ sequences at every clipping boundary; assert both content and
  exact frame width.
- **Review outcome:** Accepted and implemented for the narrowed triage scope (same split, same commit): sliceANSI walks grapheme clusters with cell widths — a cut inside a wide cluster skips it whole and pads the overshoot, the boundary style survives onto the tail; tailCells drops whole clusters until the suffix measures within budget (the caret's spare cell is the point); menuWidth counts terminal cells. Other clipping helpers already use grapheme-aware x/ansi and were not claimed or touched. Regressions: TestSliceANSIRespectsGraphemes (CJK straddle+pad, aligned cut, combining marks both sides, ZWJ family, flag AND skin-tone-modified emoji emitting padding never fragments, styled boundary), TestTerminalStraddlePadSurvives (the review-found production edge: a cut inside the line's FINAL wide cluster keeps its load-bearing pad, plain and styled, and a composed overlay row stays exactly the original width), TestOverlayStaysAlignedOverWideText (CJK/emoji under the popup: nothing exceeds the width and every overlaid row is exactly full; the unpadded footer is by design), TestTailCellsKeepsWholeClusters (CJK/ZWJ/skin-tone/combining suffix ≤ budget and a true suffix; caret room), TestNarrowEditorKeepsTheCaret (the original failure end to end: a 40-column View with twenty CJK glyphs in the live filter — caret present, no row over 40 cells), TestMenuWidthCountsCells (CJK title width, the wide title landing EXACTLY on the 40-cell cap, and every drawn line agreeing).

## Completed review — Claude Code

| Field | Value |
| --- | --- |
| Queue status | Complete — triaged (all outcomes recorded) |
| Submitted by | Claude Code |
| Review model | Fable 5 |
| Submitted | 2026-08-19 UTC |
| Reviewed revision | `799cfebcb8a7db2bc7b9036b065ddb5e6d78725d` (`v0.3.1`) |
| Scope | The `v0.3.0..v0.3.1` diff (`ffe984d..799cfeb`: the log-pane rework and the screenshot tooling), verified against the full sources at the reviewed revision; the working tree was clean |
| Automated checks | `go test ./...`, `go vet ./...`, and `gofmt -l` passed under `nix develop` |

These code-review findings have all been triaged; accepted findings were
actioned, with every review outcome recorded per item. Every finding survived an
adversarial verification pass; a Confidence of "High; confirmed" means the
verifier proved the mechanism from the code, "kept as plausible" means it could
not be refuted but was not reproduced end to end. IDs continue the shared
`UT-###` sequence from the queue above. Retained historical record, not a live
queue.

### P2 — correctness and reliability

#### [x] UT-021 — Do not claim an over-scrolled pane has an empty journal

- **Status:** Duplicate of UT-009
- **Confidence:** High; confirmed
- **Evidence:** `src/view.go:1152` — `renderLogWindow` now returns
  `emptyLogNotice()` whenever `skip` consumes every entry, but `logScroll` can
  legitimately exceed the re-wrapped total: the `tea.WindowSizeMsg` handler
  (`src/model.go:634-639`) only calls `clampCursor`, the Enter/full-view
  toggle (`src/model.go:628-647`) never re-clamps, `syncJournal` early-returns
  for the same unit (`src/model.go:1033`), and the value-receiver render path
  cannot clamp. The pre-0.3.1 code rendered a blank pane in this state.
- **Impact:** Scroll far back in the narrow wrapped side pane, then widen the
  terminal or press Enter: the pane asserts "this unit has written nothing to
  the journal" while 20k lines are held — until the next keypress or batch,
  indefinitely for a quiet unit.
- **Suggested resolution:** Call `clampLogScroll` on resize and after
  full-view toggles, and/or return a blank window instead of the notice when
  `len(m.logs) > 0`. Overlaps UT-009: this independently confirms that item's
  mechanism and pins the exact call sites that fail to re-clamp.
- **Regression coverage:** Deep-scroll a wrapped buffer, then cross the resize
  and full-view transitions; assert content (or blank), never the empty-log
  notice, whenever the buffer is non-empty.
- **Review outcome:** Duplicate of UT-009 — recorded there; the call-site evidence (resize handler, full-view toggle and exit, syncJournal early-return) is folded into UT-009's outcome.

#### [x] UT-022 — Stop `fake-journalctl.sh` eval'ing hand-quoted arguments

- **Status:** Accepted — implemented
- **Confidence:** High; kept as plausible (defect proven from the code; the
  four scripted screenshot invocations never press `/`, so it needs an
  interactive run to trigger)
- **Evidence:** `docs/helpers/fake-journalctl.sh:128` — pass-through arguments
  are hand-single-quoted (`args="$args '$a'"`) and re-parsed via
  `eval exec "$REAL" -D "$dir" -q $args`. unitop passes the user's `/` search
  pattern verbatim as `-g <pattern>` (`src/journal.go:36-40`, fed from the
  interactive filter editor at `src/model.go:401`).
- **Impact:** Running unitop interactively under the fake rig (which the
  script's header invites) and searching for `can't` kills the shim with an
  unbalanced-quote syntax error; a value that smuggles a closing quote plus a
  `$(…)` substitution executes it as the screenshotting user (reproduced —
  a plain `';id;'` does not run, since the leading exec succeeds). Dev/docs
  tooling only — the shipped binary is not affected.
- **Suggested resolution:** Rebuild positional parameters with
  `set -- "$@" "$a"` and `exec "$REAL" -D "$dir" -q "$@"` — no eval, no
  re-quoting.
- **Regression coverage:** Drive the shim with arguments containing single
  quotes, spaces, and `;` and assert they arrive as single argv entries.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-19; commit "Pass the screenshot rig's arguments as argv, not through shells"). Triage reproduced the quote-break and the command execution (a quoted $(touch …) ran; a plain ';id;' after a successful exec does not, amending the original example). Fixed by rebuilding the positional parameters with set -- and exec'ing without eval; only the FIRST -u <unit> pair is the selector, so a later value that is literally -u passes through, and a trailing -u is a loud error. Probe-verified: -g "can't $(touch …)" arrives as one argv entry and nothing executes; -g -u survives.

#### [x] UT-023 — Quote the PATH and args spliced into the tmux session

- **Status:** Accepted — implemented
- **Confidence:** High; confirmed
- **Evidence:** `docs/helpers/screenshot.sh:45` —
  `tmux new-session ... "PATH=$PATH $UNITOP ${ARGS[*]}"` splices PATH unquoted
  into the string the tmux server's `sh -c` parses. The fake-journalctl bin
  dir is built under `${TMPDIR:-/tmp}` (line 23) and prepended to PATH (lines
  34-37); there is no error checking between `new-session` and `capture`
  (lines 45-50).
- **Impact:** A TMPDIR (or any PATH component) containing a space, `;`, or a
  quote makes `sh` parse `PATH=/home/u/my` and execute the remainder — the
  pane dies or the shim silently drops off PATH, and termshot screenshots the
  real journalctl's permission error: the exact trap the adjacent comment
  claims this line prevents. `${ARGS[*]}` also flattens any unitop argument
  containing a space.
- **Suggested resolution:**
  `tmux new-session -e PATH="$PATH" ... -- "$UNITOP" "${ARGS[@]}"`, or
  `printf %q` quoting, plus a liveness check on the pane before capturing.
- **Regression coverage:** Run the screenshot rig with `TMPDIR` set to a path
  containing a space and assert the shim journal (not the real one) is what
  gets captured.
- **Review outcome:** Accepted and implemented (same split, same commit). Triage reproduced the PATH splice (exit 127 on a spaced PATH) and argv flattening. Fixed: PATH crosses as a tmux environment entry (-e) and the command as argv, prefixed `env --` so tmux never falls back to sh -c on a single-argument command (zero unitop args with a spaced executable path — probe-verified); pane death — including under remain-on-exit, via #{pane_dead} — aborts the run, checked after the settle sleep and again immediately before the capture. Review widened the scope and the fix follows it: each run gets a mktemp -d workspace and a tmux session named after it (no fixed "shot" session to kill an unrelated run's, or a user's own), a trap kills only that session and removes only that workspace on normal, error, and signal exits, and no global umask — the 0700 workspace comes from mktemp, so the output PNG keeps its normal mode (probe-verified 644). Requires a tmux with new-session -e and multi-argument commands, stated feature-wise in the header; the locked nix app supplies 3.7b.

### P3 — edge cases and hardening

#### [x] UT-024 — Show the retention cap, not the live length, in the top marker

- **Status:** Accepted — implemented
- **Confidence:** High; confirmed
- **Evidence:** `src/view.go:1216` — the top-of-buffer marker prints
  `len(m.logs)` alongside "the most unitop keeps", but block trimming now lets
  the buffer ride up to `maxLogLines+logTrimSlack` (22048). It also disagrees
  with `logBufferFull()` (`src/model.go:727`, `len >= maxLogLines` = 20000),
  which gates backwards paging at a different number than the marker shows.
- **Impact:** Watch a chatty service until the buffer caps and scroll to the
  top: the marker reads e.g. "21532 lines held, the most unitop keeps", then
  "20003" after the next trim — a claim that is both wrong (real ceiling
  22048) and jittering between frames.
- **Suggested resolution:** Display `maxLogLines` — the retention contract —
  rather than the momentary buffer length.
- **Regression coverage:** Fill past the trim threshold and assert the marker
  text is stable across appends and equal to the documented cap.
- **Review outcome:** Accepted (implemented by Claude Code/Fable 5, 2026-08-19; commit "Keep the log window honest about what the buffer holds"). The marker states the retention policy ("buffer full: unitop keeps the newest 20000 lines") rather than any count — per Codex's precision review, printing maxLogLines as "lines held" would still be false while the buffer rides above the cap. Regression: TestBufferFullMarkerIsStable.

#### [x] UT-025 — Make the fake-journal cache atomic and content-keyed

_Narrowed 2026-08-20: the shared-directory/"private" portion is NOT shipped —
the cross-UID variant stays deferred; see the review outcome._

- **Status:** Accepted — implemented (stale/partial caching); cross-UID variant deferred
- **Confidence:** High for the partial-write and stale-cache claims; kept as
  plausible for the shared-/tmp planting variant (partially mitigated by
  `ln -sf` overwriting)
- **Evidence:** `docs/helpers/fake-journalctl.sh:38` — the per-unit journal
  cache is keyed on bare file existence. The generator
  (`"$REMOTE" --output="$dir/system.journal" - < ... >/dev/null 2>&1`, line
  ~125) writes in place under `set -eu` with all diagnostics discarded, and
  `CACHE=${TMPDIR:-/tmp}/unitop-shot-journals` (line 19) plus screenshot.sh's
  `$TMP` are fixed shared paths with no `mktemp`/`$UID`/cleanup.
- **Impact:** An interrupted or failed run (Ctrl-C, disk full) leaves a
  partial `system.journal` that every later run serves silently; if `$REMOTE`
  is missing (non-NixOS, `JOURNAL_REMOTE` unset) the shim dies silently and
  the log pane is just empty; editing the `MSGS` tables never invalidates the
  cache; another local user can pre-create the shared dirs and plant journal
  content that lands in the committed README screenshots.
- **Suggested resolution:** Write to a temp name and rename on success, key
  the cache on a content hash of the message tables, and namespace the dirs
  (`mktemp -d` or `$UID`).
- **Regression coverage:** Kill the generator mid-write and assert the next
  run regenerates; edit `MSGS` and assert the cache misses.
- **Review outcome:** Accepted and implemented for the stale/partial-cache scope (same split, same commit). The cache key is the unit plus a checksum of its message table; each generation builds in its own mktemp -d under the cache root — outside any -D directory — cleaned by a trap, and only a finished journal is renamed into place, so a failed or concurrent run leaves nothing a retry can trip over even when journal-remote refuses existing output files (probe-verified). The cross-UID planting variant remains open and deferred: per-user paths and umask 077 narrow accidental collisions, they do not make a predictable path private.

#### [x] UT-026 — Provide `hostname` in the screenshots app's runtimeInputs

- **Status:** Accepted — implemented
- **Confidence:** High; verified that nixpkgs coreutils ships only `hostid`,
  not `hostname` (it comes from net-tools/inetutils)
- **Evidence:** `flake.nix:60` — `runtimeInputs = [ tmux perl gawk gnused
  coreutils termshot pngquant systemd procps ]` omits any provider of
  `hostname`, which `docs/helpers/screenshot.sh:55` calls
  (`HOST=$(hostname)`) under `set -euo pipefail`. It works today only via
  ambient PATH leakage.
- **Impact:** `nix run .#screenshots` in an environment whose ambient PATH
  lacks `hostname` (minimal container, `nix develop -i`, CI) dies at line 55 —
  after the 5s+ tmux capture already ran — leaving the tmux server and
  half-written temp files behind.
- **Suggested resolution:** Add `nettools` (or `inetutils`) to
  `runtimeInputs`, or use `uname -n`.
- **Regression coverage:** Run the app with an empty ambient PATH and assert
  it completes.
- **Review outcome:** Accepted and implemented (same split, same commit): uname -n, which coreutils already provides — runtimeInputs untouched, no new dependency. The project_runtimeinputs_coverage memory and its index line ride with this commit.

#### [x] UT-027 — Wrap each visible log entry once per frame, not twice

- **Status:** Accepted — implemented
- **Confidence:** Medium; kept as plausible (efficiency, not correctness)
- **Evidence:** `src/view.go:1134` — `renderLogWindow` measures each visible
  entry with `logSegments` and then calls `formatLog`, which re-runs
  `logSegments` internally before styling. The 0.3.1 refactor split measuring
  from rendering but the render half discards the measurement it computed.
- **Impact:** The ~30 on-screen entries are word-wrapped twice per frame — a
  measured constant overhead of ~460 allocs/op and ~18 KB per frame
  (ScrollDeep/100: 4965→4503 allocs/op). It is not dominant: a 200-column CPU
  profile puts renderLogWindow at 9.93% cumulative against viewTable's
  52.61%, so the original "dominant remaining per-frame cost" claim is
  withdrawn.
- **Suggested resolution:** Add a styling helper taking the already-computed
  segments, e.g. `formatSegs(l, prefix, segs)`, keeping `formatLog` as the
  thin wrapper for other callers.
- **Regression coverage:** The render benchmarks; assert identical frames
  before and after.
- **Review outcome:** Accepted (implemented by Claude Code/Fable 5, 2026-08-19; commit "Style the log lines that were already measured"). formatLog = logSegments + formatSegs; the window walk styles the segments it measured. Evidence: ScrollDeep/100 4965→4503 allocs/op, ViewWide 7631→6839, ViewFullBuffer 6231→5769; ns/op within machine noise. Codex's 200-column CPU profile rejects the finding's "dominant remaining per-frame cost" claim (renderLogWindow 9.93% cumulative vs viewTable 52.61%) — accepted on the deterministic allocation reduction alone. Frames unchanged (TestLogWindowMatchesTheReference).

#### [ ] UT-028 — Give the log buffer an owner so memo invalidation is one act

- **Status:** Deferred
- **Confidence:** Medium; kept as plausible (fragility, not a live bug)
- **Evidence:** `src/model.go:139` — the display-total memo's key is
  `(epoch, len, width, wrap)`, and keeping it valid is a distributed invariant
  across five sites (append+trim, prepend, clear, `shifted()`,
  `logDisplayTotal`). The comment at `src/model.go:134-138` already begs
  future code to "keep the two in step."
- **Impact:** Any future length-preserving buffer mutation — in-place edit of
  a meta line, dedupe-and-replace, a filter that keeps the count — bumps
  neither key, so every scroll computation (`clampLogScroll`, `atTopOfLog`,
  the scroll marker) silently works against a stale buffer height.
- **Suggested resolution:** A `logBuffer` type whose append/prepend/clear
  methods own `logs`, `logEpoch` and the totals, so mutation and memo update
  are one operation.
- **Regression coverage:** A mutation-through-methods-only compile-time
  boundary (unexported fields), plus the existing paging tests.
- **Review outcome:** Deferred (2026-08-19, both agents) — not a live bug; revisit only with separate justification if a length-preserving buffer mutation is ever introduced. The memo contract comments were made accurate under UT-030.

#### [x] UT-029 — Reset benchmark state so ns/op measures one thing

- **Status:** Accepted — implemented
- **Confidence:** High for the mechanism; kept as plausible (measurement
  integrity — the CHANGELOG cites these numbers)
- **Evidence:** `src/bench_render_test.go:173` — `BenchmarkScrollFullBuffer`
  does `m.logKey("pgup")` every iteration with no reset, so it walks deeper
  until clamping at the top (~1300 iterations) and then re-renders the deepest
  window forever — identical to `BenchmarkScrollDeep/10000`. The light
  `BenchmarkView` cases (Wide/Tree) start at 500 logs and grow 4 per
  iteration, crossing `maxLogLines+logTrimSlack` around iteration ~5400 and
  then paying the 20k trim/copy every 512th iteration.
- **Impact:** ns/op is an average over a moving target and shifts with
  `-benchtime` — the "steady light traffic" benchmark partially measures the
  full-buffer state exactly when numbers get compared across commits.
- **Suggested resolution:** Pin scroll/buffer state at the top of each
  iteration (`b.StopTimer()`/reset/`b.StartTimer()`).
- **Regression coverage:** Compare `-benchtime=100x` against `-benchtime=10000x`
  and assert ns/op is stable.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commits "Give every benchmark iteration the same work to measure" + follow-up "Restore the benchmark memo from a snapshot instead of recounting"). Triage measured the drift: ScrollFullBuffer 10.16/3.58/8.25 ms/op at 10x/100x/500x. Every benchmark now holds its state per iteration: ScrollFullBuffer resets to the live end (its 20k memo recount had moved inside the first timed pgup once renderLogWindow stopped calling logDisplayTotal — primed off-timer); View/ViewWide/ViewTree restore 500 lines via holdBufferAt; backing arrays are pre-grown off-timer everywhere; ViewFullBuffer is split into stationary append and trim frames (the old mixture bought different fractions of the 512:1 cycle per -benchtime). Post-commit audit added the follow-up: resets restore the memo from one primeBuffer snapshot instead of recounting 20k entries off-timer per iteration — that hidden work allocated, drove GC and warmed the cache (7.46s wall for 0.52s timed; now 0.93s wall for the same 100x split pair). Figures, raw frames, 1x/10x/100x: append 785/796/777µs, 105KB, 3574/3570/3569 allocs/op; trim 5.13/4.16/4.36ms, 2.70MB, 71297/71287/71286; ScrollFullBuffer 3875/3869/3868; ViewWide 6789/6784/6784. These are raw frame costs; any production amortisation must subtract the ordinary append-frame baseline before dividing the incremental trim cost across the 513-frame cycle.

#### [x] UT-030 — Remove the stale doc comments the 0.3.1 rework left behind

- **Status:** Accepted — implemented
- **Confidence:** High; confirmed (factual)
- **Evidence:** `src/view.go:1114` — `renderLogWindow` carries two consecutive
  doc comments both starting "renderLogWindow"; the pre-0.3.1 two-line one
  ("walks the buffer backwards...") was left above the new block. And
  `src/view.go:155` — `logTotals`' comment still claims a trim-and-append
  "still invalidates" the memo, the exact opposite of the new `shifted()`
  behavior, which deliberately does NOT bump the epoch on trim-and-append.
- **Impact:** A future editor trusting the `logTotals` comment adds an
  epoch-keyed-only consumer that silently sees stale state on the
  highest-frequency (at-cap) path. Correctness-adjacent stale docs on a memo
  whose own comments warn the contract is fragile (see UT-028).
- **Suggested resolution:** Delete the stale `renderLogWindow` comment and
  correct the `logTotals` comment to describe the `shifted()` contract.
- **Regression coverage:** None needed; documentation only.
- **Review outcome:** Accepted (implemented by Claude Code/Fable 5, 2026-08-19; commit "Keep the log window honest about what the buffer holds"). The duplicate renderLogWindow doc is gone and logTotals' comment now describes the shifted() contract. Documentation only; no regression test. Addendum 2026-08-20: two production comments still claimed every batch trims at the cap — the trim site in model.go and the shifted() doc in view.go (which also framed the 34ms/27MB recount as the permanent at-cap frame rather than the periodic trim frame). False under block trimming: the buffer rides to cap+slack and trims every few hundred batches. Both corrected in the UT-029 commit. Second addendum: a third copy lived in the Unreleased changelog text itself ("every batch discards the oldest lines") — reworded to block-trimming in the UT-005/006/014 commit.

## Completed review — joint follow-ups

Found by adversarial review during implementation of earlier fixes.

#### [x] UT-031 — Own every poll and action child at exit

- **Status:** Accepted — implemented
- **Confidence:** High (triaged by Codex/GPT-5, 2026-08-20, during the
  UT-015 ownership audit)
- **Evidence:** `pollCmd` (25s timeout) and `runAction` (90s) derive their
  contexts from `context.Background()` and launch systemctl/sudo — or the
  ssh carrying them — inside Bubble Tea Cmd goroutines, which Program does
  not await at shutdown.
- **Impact:** Quitting during a slow startup poll or a unit action can
  leave a child alive — and an action still mutating a unit — after the UI
  exits.
- **Suggested resolution:** A stable program-work owner: one root context,
  a mutex-guarded closing gate, and a WaitGroup. Begin before launch and
  refuse after closing; derive the 25s/90s timeouts from the root; cancel
  and wait on model quit and after p.Run() returns; close the SSH mux only
  after the wait completes.
- **Regression coverage:** Local and remote quiet poll and action
  children; a Cmd queued after shutdown is refused and launches nothing;
  exact reaping before shutdown returns; mux-close ordering.
- **Review outcome:** Accepted and implemented (triage Codex/GPT-5, implementation Claude Code/Fable 5, 2026-08-20; commit "Own every poll and action child at exit"). progWork is the stable owner pointer newModel creates: root context/cancel, one mutex guarding a closing flag, and a WaitGroup. begin runs INSIDE each Cmd closure immediately before the external work — registering at construction would deadlock shutdown if bubbletea never scheduled the Cmd — and under the mutex either refuses (closing) or Adds before unlocking; shutdown marks closing and cancels before unlocking, then Waits, so Add can never race Wait and a late Cmd launches nothing (returns nil). pollCmd derives its 25s and runAction its 90s timeout from the root. model.shutdown() is the one idempotent teardown for BOTH systems — drain progWork, then stopAndWait the journal — used by quit() before tea.Quit and by the factored runProgram helper (which main calls) unconditionally after p.Run, BEFORE runner.close tears the mux down, so ssh connections drain before their control socket goes away. Journal replacement/page semantics untouched. Regressions (direct exec.Cmd children — `$$` then exec sleep, no nesting): TestQuitReapsThePollChild (local systemctl and fake-ssh remote: ESRCH before the ctrl-c keypress returns), TestShutdownReapsTheActionChild (local and remote: no action keeps mutating after the screen is gone), TestLateCmdIsRefused (constructed before shutdown, invoked after: canary never runs, nil returned), TestBeginShutdownRace (×200 under -race, plus idempotent re-shutdown), TestMuxOutlivesTheDrain (the mux dir survives the drain and only the subsequent close removes it), and TestPostRunShutdownReapsThroughARealProgram — the unconditional post-Run route against a REAL bubbletea Program (no renderer, silent pipe input, no signal handler), externally Killed, holding the factored runProgram helper main uses to reaped-before-return.

## Completed review — Security (Claude Code)

| Field | Value |
| --- | --- |
| Queue status | Complete — all accepted items shipped |
| Submitted by | Claude Code (Fable 5), three focused security reviewers |
| Cross-checked with | OpenAI Codex (GPT-5), independent parallel pass |
| Submitted | 2026-08-20 UTC |
| Reviewed revision | `23e18730921ab1fc5653024dacdb5f6cbd753c4e` (clean tree) |
| Scope | Whole-tree security audit: command-exec/shell-injection/SSH, untrusted-input + terminal-escape safety, resource-exhaustion/concurrency, and CI/release integrity |
| Automated checks | `go test -race ./...`, `go vet`, `gofmt`, ShellCheck (helper scripts), govulncheck (zero reachable) all pass at the reviewed revision |

Security findings from a four-pass review (three focused reviewers plus
Codex's independent pass), cross-checked and each EXPLOITABLE item verified
against the code. Classification: **EXPLOITABLE** = a real DoS/crash within
the tool's threat model (`-H` connects to arbitrary/untrusted hosts, and a
monitored unit controls its own journal/property bytes); **HARDENING** =
defense-in-depth; **CORRECTNESS** = a modal-integrity bug, not a vuln.

Two proposed IDs were rejected in triage and are recorded here so they are
not silently recycled:

- **UT-036 (rejected)** — `-H`'s `--` placed after the host token
  (collect.go:74/85) lets a `-o…` host be parsed as an ssh option, BUT the
  reviewer proved no execution (unitop's always-spaced remote command lands
  in the hostname slot and ssh aborts on invalid characters before
  ProxyCommand fires), it requires a self-supplied hostile `-H`, and this
  repo has a durable warning against reviving the post-host `--` report. Not
  a security issue; a leading-dash CLI-hygiene check may be added separately.
- **UT-037 (rejected)** — no `--` before the remote unit-name list to
  `systemctl show` (collect.go:224). A valid systemd unit name is escaped
  (`\x2dfoo.service`), never a literal leading hyphen, and a hostile remote
  fabricating list output already controls the subsequent remote systemctl;
  at most it fails its own poll. Not actionable absent a supported-host
  reproduction with a real loaded unit.

### P1 — exploitable

#### [x] UT-032 — Guard the `TriggeredBy` render against a whitespace-only value

- **Status:** Accepted — implemented
- **Confidence:** High — runtime-proven by both agents independently
- **Evidence:** `view.go:1037` indexed `strings.Fields(u.TriggeredBy)[0]`
  guarded only by `u.TriggeredBy != ""`. `parseShow` sanitizes but keeps
  whitespace (a tab becomes four spaces, NBSP survives), so a monitored host
  returning `TriggeredBy=\t` yields a non-empty value whose `Fields()` is
  empty — `by[0]` panics.
- **Impact:** A hostile/compromised/MITM'd `-H` host crashes the operator's
  TUI when the affected unit is on the cursor (row 0 is auto-selected on
  connect, and the attacker controls the sort metrics).
- **Suggested resolution:** Render the first trigger only when
  `len(Fields) > 0`; render nothing otherwise.
- **Regression coverage:** parseShow→rebuild→View over empty / ASCII spaces
  / tab / NBSP / CR / real single / real multi / leading-space, in split AND
  full view; no panic, whitespace renders nothing, real renders the first
  trigger. Plus a sweep proving every other terminal-bound `Fields(...)[0]`
  is length-guarded.
- **Review outcome:** Accepted and implemented (Claude Code/Fable 5,
  2026-08-20; commit "Do not panic on a hostile unit's whitespace
  TriggeredBy"). The render guards on `len(by) > 0`; a whitespace-only value
  renders nothing. Verified the ONLY unguarded untrusted index: parseSystemdVersion
  and parseUnitList both length-check before `f[0]`, and wrapWords' `parts[0]`
  is on already-sanitized non-empty text. Regression: TestTriggeredByNeverPanics
  (eight shapes × split/full, no panic, correct render). Both agents
  runtime-reproduced the original panic.

#### [x] UT-033 — Bound the live journal buffer by bytes, not only lines

- **Status:** Accepted — implemented
- **Confidence:** High — reproduced by runtime probe (1.2 GiB from 300 lines)
- **Evidence:** `model.go:354-368` trims `m.logs` by count only
  (`maxLogLines` 20000 + `logTrimSlack` 2048); `journal.go:904` allows a
  4 MiB per-entry cap in follow mode with no aggregate byte accounting
  (`maxPageBytes` 16 MiB protects only `runFinite`); the parsed-line channel
  (`journal.go:697`, 512 deep) and batch channel (`startJournal`, 64 deep)
  are also byte-unbounded. Rendering compounds it: a single ~4 MiB entry is
  fully grapheme-wrapped by `countDisplayLines`→`logSegments`→`wrapWords`
  every frame (view.go:127-134, 1306-1331; format.go:118-121).
- **Impact:** A monitored unit (or hostile `-H` remote) emitting multi-MiB
  entries drives multi-GiB retention → OOM, and a single large entry makes
  every `View()` cost ~530 ms — a CPU freeze. This is the live-model byte
  bound explicitly deferred in the UT-003 commit; this item supersedes that
  deferral.
- **Suggested resolution:** A per-entry DISPLAY byte cap far below 4 MiB
  (a screen shows a few KiB), plus aggregate byte trimming next to the line
  trim, byte-bounded live queues, and per-line wrapped-height memoization so
  a large entry is not re-wrapped every frame. Byte accounting must count
  SANITIZED bytes (tab/C0 expansion) and combine all `logLine` string fields.
- **Regression coverage:** test-injectable small budgets (never allocate
  real GiB); uneven-size floods across cap-1/cap/cap+1; assert oldest-trim,
  follow/scroll invariants, honest marker, no queued path exceeds the budget
  by more than one explicitly bounded record; a safe benchmark showing a
  max-size entry is not re-wrapped every frame.
- **Review outcome:** Accepted and implemented (Claude Code/Fable 5, 2026-08-20; commit "Bound the live journal buffer by bytes, and cap one entry"). Two layers. (1) A per-entry cap at the parse chokepoint bounds EVERY retained field: capMessage truncates the message to maxLineBytes (8 KiB) MARKER INCLUDED (a fixed elisionReserve held back) on a grapheme boundary; capField truncates ident/pid to maxFieldBytes (256); an oversized __CURSOR (over maxCursorBytes 512) is dropped so the entry becomes cursorless rather than retained-and-fed-to-journalctl — so every downstream buffer (the 512 parsed-line channel, the batch, the model) inherits the bound, AND a single entry wraps to a bounded height instead of the ~50k display lines a 4 MiB entry produced. (2) An aggregate model byte budget beside the line cap: model tracks logBytes (sum of every held logLine string field), kept exact across append+trim / prepend / clear; trimCut drops oldest until BOTH the line and byte caps hold, block-trimming past a slack high-water so the shifted() memo is not recounted per batch; logBufferFull and the honest top marker are byte-aware (says "size limit" when the byte cap bound first, the line count otherwise). Bytes are counted post-sanitize (tab/C0 expansion included). Regressions (src/journal_bytes_test.go): TestCapMessageBoundary (cap±1, wide-cluster not split, result never exceeds the cap with its marker), TestParseBoundsEveryField (a 100 KB message/ident/pid/cursor all bounded, oversized cursor dropped, cursor-at-limit kept, lineBytes under the per-entry ceiling), TestBackwardPageRespectsTheByteBudget (loadOlder refuses while byte-full and a prepend overshoots by at most one page with exact accounting), TestTrimCutByteBudget (uneven sizes, smallest-prefix drop, idle under budget), TestModelByteAccountingAndTrim (200-batch uneven flood: logBytes stays exactly in sync with the buffer and never rides past cap+slack, oldest trimmed, follow/scroll invariants, and the selection-change clear zeroes it), and BenchmarkViewLargestEntry (a max-size entry renders in ~0.9 ms vs the original ~530 ms — the per-entry re-wrap is gone, ~575x). The absolute retained ceiling is maxLogBytes + one backward page (~20.4 MiB at production caps — journalBacklog 500 × the 9216 B per-entry ceiling — measured), NOT a strict 16 MiB: a prepend adds one bounded page when under the cap and rides the slack until the next forward trim, and the bounded live queues (the 512-deep channel, batches) hold capped records outside m.logs. Supersedes the UT-003 live-model byte deferral.

#### [x] UT-034 — Bound poll and action command output by bytes

- **Status:** Accepted — implemented
- **Confidence:** High
- **Evidence:** Every poll/action path buffers the child's whole stdout with
  no byte cap via `Cmd.Output()` / `CombinedOutput()`:
  `collect.go:225` (`systemctl show`), `:351` (version), `:370` (list-units),
  `:395` (the remote `sh -c` framed poll), `actions.go:69` (90 s action), and
  `journal.go:297` (the `date +%s` clock probe). Only the journal reads
  (`runFinite`) were bounded (UT-003/013).
- **Impact:** A hostile/compromised `-H` endpoint answering the poll or an
  action with a fast unbounded stream buffers gigabytes (≈2.5 GB at 100 MB/s
  over the 25 s poll, ≈9 GB over the 90 s action, unbounded on the
  deadline-less probe) — every poll interval. Timeouts bound duration, not
  bytes.
- **Suggested resolution:** One streamed bounded-output primitive (the
  `runFinite` machinery is the in-repo precedent): drain excess so the child
  cannot block, always wait/reap, surface an explicit oversized/truncated
  error rather than a malformed parse, and keep a small separate cap for
  useful first stderr diagnostics.
- **Regression coverage:** local + fake-SSH remote for version, list-units,
  framed base poll, detailed show, clock probe, and action `CombinedOutput`;
  stdout and stderr independently at cap-1/cap/cap+1; endless finite-rate
  flood until cancellation; child cannot block on a full pipe; PID gone when
  the command returns; explicit error surfaced. No real-GB fixtures.
- **Review outcome:** Accepted and implemented (Claude Code/Fable 5, 2026-08-20; commit "Bound the bytes a poll or action command can return"). New boundedRun primitive (src/boundedrun.go): streams stdout to a 1 MiB cap (maxCmdOutput), drains everything past it so the child never blocks, pumps stderr through the existing stderrPump caps, and always waits/reaps; a non-EOF stdout read error is retained and folded into the result so a partial reply cannot parse as a whole one; an over-cap reply is errOversized (a plain retryable error, not UnsupportedError), and a nonzero exit folds the pumped stderr into the error since a pipe leaves ExitError.Stderr empty. wrapExec removed — boundedRun subsumes it. Wired at every unbounded site: systemctl show/version/list-units and the remote sh -c framed poll (collect.go), the remote date +%s clock probe (journal.go), and the action CombinedOutput (actions.go, stdout+stderr both feed the sanitized toast). Timeouts unchanged (the deadline gap is UT-035). Regressions (src/boundedrun_test.go, all -race, no GB fixtures): TestBoundedRunStdoutCapBoundary (cap-1/cap/cap+1, exact retained length, errOversized at +1), TestBoundedRunStderrIsBoundedNotFatal (flood bounded + marker, stdout survives, exit 0 not an error), TestBoundedRunDrainsSoChildNeverBlocks (8 MiB flood then a sentinel the child only reaches if drained; PID ESRCH by return), TestBoundedRunCancellationReaps (endless flood returns promptly on cancel, child reaped), TestBoundedRunFoldsStderrOnFailure, TestOversizedPollSurfacesAnError (local + fake-ssh remote poll flood → error not zero-unit parse), and TestOversizedShowAndActionSurfaceErrors (the detailed `systemctl show` batch via Poll surfaces a "systemctl show" error, and a flooding unit action returns errOversized in its actionResult — not a truncated success).

### P2 — hardening / reliability

#### [x] UT-035 — Give journal phase-one a deadline

- **Status:** Accepted — implemented
- **Confidence:** High — not a leak/deadlock; a user-recoverable stall
- **Evidence:** `journal.go:297` (remote clock probe) and `:309`
  (`readBacklog`) run on the stream ctx, rooted at `context.Background()`
  (model.go) with NO timeout — unlike `fetchOlder`'s 30 s (`journal.go:104`).
- **Impact:** A remote that accepts the session but never emits output
  (application-level stall; `ServerAliveInterval` only catches dead
  transport) pins the pane on "reading the journal…" forever — the stream
  never starts and never dies, so dead-stream recovery never fires. Only the
  follow tail should be unbounded.
- **Suggested resolution:** `context.WithTimeout` around the probe and
  backlog, matching `fetchOlder`.
- **Regression coverage:** fake journalctl + fake ssh that connect then block
  in (a) `date +%s`, (b) backlog stdout, (c) backlog stderr/Wait: require a
  finite phase-one deadline, a visible terminal batch, dead-stream
  retirement/retry without a hot loop, direct-child ESRCH before teardown
  returns; a healthy slow response below the deadline still succeeds and
  follow mode stays long-lived.
- **Review outcome:** Accepted and implemented (Claude Code/Fable 5, 2026-08-20; commit "Give the journal bootstrap a deadline"). Phase one — the remote clock probe and the backlog read — now runs under a context.WithTimeout(ctx, backlogTimeout) (30s, shared with fetchOlder's page), so a remote that connects but never answers dies with a visible terminal batch that the UT-012 dead-stream recovery retries, instead of pinning the pane on the spinner forever. Only the follow tail (phase two) stays unbounded. backlogTimeout is a var so tests use a small deadline. Regressions (src/journal_deadline_test.go): TestPhaseOneDeadlineOnASilentRemote (a fake ssh that connects then blocks: a "clock probe" terminal batch within the deadline, direct child ESRCH after stopAndWait), TestPhaseOneDeadlineOnABlockingBacklog (a blocking local journalctl backlog: bounded the same way, child reaped), TestSlowButHealthyPhaseOneSucceeds (a 100ms response under a 1s deadline: the backlog lands, the seed is delivered, and the follow tail begins and stays alive — the deadline does not leak into phase two), TestPhaseOneDeadlineOnBlockedStderrWait (a backlog that closes stdout but hangs stderr/Wait is killed at the deadline, child reaped), and TestPhaseOneTimeoutFeedsDeadStreamRecovery (a timeout emits a genuine done batch that retires the stream and records journalDiedAt — the retry-without-hot-loop itself is UT-012 machinery, proven exhaustively there, referenced not re-tested here).

#### [x] UT-038 — Harden the release/CI workflows

- **Status:** Accepted — implemented
- **Confidence:** High — supply-chain / release-integrity hardening
- **Evidence:** `.github/workflows/release.yml` grants `contents: write`,
  pins actions only by mutable major tags (`actions/checkout@v7`,
  `setup-go@v7`, `cachix/install-nix-action@v31`; likewise in `ci.yml`), and
  uses `workflow_dispatch.inputs.tag` as both the checkout `ref` and the
  `gh release create` name with no `--verify-tag` and no existing-tag proof.
- **Impact:** A compromised action tag runs with write scope; an authorized
  but mistaken dispatch can build one ref and create/mislabel a release tag.
- **Suggested resolution:** Pin every `uses:` to a reviewed full commit SHA;
  scope release permissions to only what is required; reject non-`v*` /
  branch / SHA / missing-tag dispatch inputs; assert the checked-out commit
  equals dereferenced `refs/tags/$TAG`; publish with `--verify-tag`.
- **Regression coverage:** `actionlint` (and `zizmor` if available) plus
  static tests for SHA-pinning, scoped permissions, dispatch-input validation,
  tag-equals-checkout, and `--verify-tag`; annotated and lightweight existing
  tags exercised without publishing.
- **Review outcome:** Accepted and implemented (Claude Code/Fable 5, 2026-08-20; commit "Pin and harden the CI and release workflows"). Every workflow `uses:` is pinned to a reviewed full commit SHA (actions/checkout, setup-go, cachix/install-nix-action — resolved via git ls-remote, `# vN` comment kept). release.yml: repo-default permissions dropped to contents: read with contents: write scoped to the release job alone; a Validate step refuses any workflow_dispatch tag that is not v*, that does not exist as refs/tags/, or whose commit is not exactly the checkout; publish uses `gh release create --verify-tag`. Both linters clean: actionlint (fixed an SC2046 unquoted word-split in the gofmt step via `-exec … +`) and zizmor (persist-credentials: false on every checkout — artipacked; cache: false on the contents:write release build — cache-poisoning HIGH). Regression: src/workflows_test.go asserts every uses is a 40-hex SHA, the scoped permissions, the tag guard (v* / refs/tags deref / HEAD compare), --verify-tag, a persist-credentials guard per checkout, and cache:false — skipping when ../.github is absent so the src-only nix build sandbox is unaffected (verified: nix build green). actionlint + zizmor were run manually and both report clean. TestReleaseTagValidationShell additionally EXTRACTS the actual validate-step shell from release.yml (so it cannot drift from a copy) and runs it in a throwaway git repo: annotated and lightweight v-tags on HEAD accept; a non-v ref, a v-prefixed branch (v* namespace but not a tag), a bare SHA, a missing tag, and a real tag whose commit is not HEAD all reject.

### P3 — correctness

#### [x] UT-039 — The help screen must own its input

- **Status:** Accepted — implemented
- **Confidence:** High — modal state-corruption, not a security vuln
- **Evidence:** `handleKey` dispatches the whole command switch before its
  `m.help` branch (model.go:653), and `handleMouse` (model.go:427) has no
  help guard. The help footer advertises only scroll/close/quit
  (view.go:1526-1535).
- **Impact:** With help open, Enter toggles/collapses the hidden row,
  table/log keys mutate hidden state, `x` opens the action menu, and the
  wheel/clicks drive the hidden table/log/menu behind the overlay. State
  corruption behind a modal screen (the menu does become visible, so it is
  not a silent destructive action).
- **Suggested resolution:** Route all input through the help branch first —
  allow only its documented keys, map the wheel to `helpScroll` with
  clamping, and drop everything else.
- **Regression coverage:** with help open, snapshot every pane/model field
  and feed all normal command keys plus left/right/header/right clicks and
  wheel; only documented close/quit/scroll may act; assert no
  poll/action/journal command returned and no hidden state change; cover
  too-short and full-height help at min/wide geometries.
- **Review outcome:** Accepted and implemented (Claude Code/Fable 5, 2026-08-20; commit "Let the help screen own its input"). handleKey now checks m.help immediately after the ctrl-c and too-small guards, BEFORE the connected/filter/menu/command dispatch: only ?/esc (close), q (quit, via the shared exit), and the scroll keys act; every other key is swallowed. handleMouse gains a matching guard at its top: the wheel scrolls help (clamped), every click is inert. The late redundant help branch is removed. Regressions (src/help_modal_test.go): TestHelpSwallowsEveryCommandKey (19 command keys × four geometries — min/wide × short/full — each returns no command and a full field snapshot proves no hidden cursor/focus/menu/filter/sort/tree/all/pause/interval/stream change), TestHelpOwnKeysStillAct (?/esc close, q quits with tea.Quit, scroll keys move helpScroll clamped without touching the panes), TestHelpOwnsTheMouse (left/right/header clicks inert, wheel scrolls help clamped both ways).

## Maintainer feature requests

Requested by the maintainer directly (not a review finding), tracked in the
same ID sequence and worked through the same held-diff → independent-verify →
ack → commit loop.

#### [x] UT-040 — Let a mouse-wheel scroll settle before the journal follows

- **Status:** Accepted — implemented
- **Requested by:** maintainer (owner@ipburger.com), 2026-08-20 UTC
- **Base revision:** `e05fe643decb27d36651cf5f0cb04901205860fa` (current HEAD)
- **Problem:** Scrolling the unit list with the mouse wheel makes the log pane
  rush to fetch on every notch. Each wheel notch moved the cursor and called
  `afterCursorMove → syncJournal`, which tears down the live `journalctl`
  child (`stopAndWait`) and spawns a new one — so a quick scroll spawns and
  reaps a process for every unit the pointer flies past.
- **Resolution:** Debounce the fetch on the wheel path only. A wheel notch now
  goes through `scrollCursor`, which moves the cursor and updates `m.selected`
  (so the highlight tracks the wheel immediately) but, instead of syncing,
  bumps `m.journalSettleGen` and schedules a `journalSettleMsg` after the
  `journalSettle` const (150 ms). Only the settle whose gen still matches — the
  last notch, after the wheel goes still — calls `syncJournal`; earlier notches
  are superseded and do nothing. A deliberate move (`afterCursorMove`: click,
  keyboard nav) also bumps the gen, cancelling any pending wheel settle, and
  keeps its immediate sync. Keyboard list navigation is intentionally left
  immediate (single presses are deliberate; Codex concurred).
- **Regression coverage:** `src/scroll_settle_test.go` —
  TestWheelScrollDefersTheJournalFetch (wheel six notches: journal object
  unchanged mid-scroll while the selection moves; stale-gen settle is a no-op;
  latest-gen settle switches the stream to the resting unit),
  TestDeliberateMoveCancelsAPendingWheelSettle (wheel schedules gen N, a
  keyboard move runs afterCursorMove, switches the journal immediately and
  bumps the gen, and the old settle N is then inert),
  TestWheelOverLogDoesNotTouchTheSettle (seed >pane-height log lines, WheelUp
  over the log pane lifts logScroll off the bottom and stops following, while
  journalSettleGen and the stream stay unchanged).
- **CHANGELOG:** entry added under Unreleased → Fixed before the hold.
- **Gates (explicit exit codes):** gofmt clean (our tree, vendor excluded);
  `go vet` exit 0; full `go test` exit 0; `go test -race` exit 0; `nix build`
  exit 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-20). Codex accepted the
  production design outright (wheel-only debounce; immediate selection;
  generation invalidation race-free on the model event loop; every direct sync
  caller intentionally immediate; keyboard nav kept immediate). Two review
  rounds refined the record only: (1) added the CHANGELOG entry, the
  gen-cancel and wheel-over-log regressions, and made `journalSettle` a const;
  (2) fixed TestWheelOverLogDoesNotTouchTheSettle, which began at logScroll==0
  and sent WheelDown (clamps to zero, proving nothing) — now seeds scrollback
  and WheelUps so the offset provably moves. Committed the four held paths.

#### [x] UT-041 — Colour the sort direction only where it means high/low; reconcile the host-name docs

- **Status:** Accepted — implemented in two commits (per the maintainer:
  sort-direction fix, then host-name doc reconciliation)
- **Source:** Codex audit of the maintainer's `e05fe64` landing; maintainer
  authorized the fix (full-fix option, 2026-08-20 UTC). `e05fe64` stays
  immutable.
- **Finding A — sort-direction colour lies on three columns.**
  `sortStyle(reverse)` (theme.go) hardcoded reverse=false → red "high to low",
  true → green "low to high". But the natural (unreversed) order per key
  (sort.go): name = A→Z (low→high); state = most-alarming-first by attention
  rank (not a magnitude); uptime = `ActiveSince.Unix()` high→low = newest start
  = **shortest displayed age first** (low→high as displayed, inverted from the
  key). Only cpu/mem/net/io/restarts/tasks are truly high→low. So red/green was
  false on name, state, and uptime.
  - **Resolution:** `sortStyle(key, reverse)` is now key-aware. Magnitude
    columns keep red=high→low / green=low→high. Uptime is coloured by displayed
    age (unreversed newest-first = shortest age = green; reversed = red). Name
    and state are neutral — `stSortNeutral` (bold, no hue) so the sorted column
    still carries weight while the arrow alone says direction. Both render sites
    (header `sort` chip view.go:592, table title view.go:792) share the one
    helper.
- **Finding B — host-name docs contradict the code.** Ground truth: `stHost =
  Foreground(colCyan).Bold(true)` (theme.go), rendered `stHost.Render(hostLabel)`
  (view.go) — the host name **is** cyan. theme.go duplicated the "host name is
  cyan" paragraph and also listed the host name among the colourless
  "what must be read"; CHANGELOG's colourless list did the same.
  - **Resolution:** de-duplicate the theme.go paragraph and drop the host name
    from the colourless lists in theme.go and CHANGELOG. The "host name is cyan"
    statements are the correct ones and stay.
- **Regression coverage:** `src/theme_test.go` TestSortStyleShowsDirection now
  covers metric columns (red/green by reverse), uptime (green unreversed, red
  reversed — the inversion), and name/state (never red/green, always bold),
  across both reverse values.
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-20). Codex accepted the
  production mapping, both call sites and the prose corrections; two pre-commit
  fixes applied — the neutral regression now asserts foreground is exactly
  `lipgloss.NoColor{}` (not merely "not red/green"), and the magnitude table
  enumerates all eight keys. Landed as two commits per the maintainer:
  `2744b5ef50fb1f7f287db3da901e80ea7627d9b0` "Colour the sort direction only
  where it means high to low" (sort code + view + test + sort CHANGELOG bullet),
  then this commit for the host-name doc reconciliation (theme.go paragraph
  dedup + colourless-list correction, CHANGELOG colourless line, this record).

#### [x] UT-042 — Close the four UT-040 settle-window seams

- **Status:** Accepted — implemented in two commits (log-pane naming;
  filter-editor input ownership)
- **Source:** Codex audit of UT-040 (`0b70c78`). Four variants, each reproduced
  with a failing test before its fix. `e05fe64`/`0b70c78` stay immutable —
  these are follow-on fixes.
- **Seam 1 — the log pane titled another unit's lines.** During the 150 ms
  settle window `m.selected` has moved but `m.journal`/`m.logs` still belong to
  the streamed unit; `logTitle` named `selectedUnit()`, so the streamed unit's
  lines rendered under the new selection's name (test proved "log bad" over
  nginx's lines).
  - **Fix:** `logUnitName()` — the open stream's unit, falling back to the
    selection only before any stream — so the title always names the unit whose
    lines are on screen. The detail block keeps tracking the selection (it is
    free to compute and is what the cursor points at).
- **Seam 2 — a settle restarted journalctl mid-filter-edit (keyboard).** The
  log-filter editor mutates `m.logFilt.grep` live and defers `syncJournal` to
  Enter/Esc, but a pending `journalSettleMsg` fired independently and restarted
  the stream with the half-typed filter.
  - **Fix:** the settle handler returns early while `m.filterInput &&
    m.filterLogs`; the editor's close-sync (Enter/Esc) picks up the selection
    and finished filter together. Not the already-reconciled table-filter case.
- **Seam 3 — the same editor invariant, but via the mouse (Codex adjacent
  path).** `tea.MouseMsg` bypasses `handleKey`'s editor branch, so while the log
  filter was open a table click reached `afterCursorMove` → `syncJournal` with
  the partial filter (restart), and a right-click opened the action menu over
  the editor.
  - **Fix:** `handleMouse` gains a `m.filterInput` guard mirroring the keyboard
    ownership — the wheel still scrolls the log being read, but clicks, the list
    wheel and right-click are inert until Enter/Esc. This is general editor
    ownership: it covers BOTH the table and log filters (unlike the seam-2
    settle guard, which is log-filter-specific). Reproduced red first.
- **Seam 1b — retained dead-stream lines mislabelled.** A stream that ends on
  its own sets `m.journal = nil` but keeps its lines and records
  `journalDiedUnit`; `logUnitName()` then fell back to the selection, so a wheel
  scroll after the death re-mislabelled the retained lines (Codex variant).
  - **Fix:** `logUnitName()` uses `journalDiedUnit` while lines are retained and
    no stream is live, before falling back to the selection.
- **Regression coverage:** `src/scroll_settle_test.go` —
  TestLogTitleNamesTheStreamedUnitDuringSettle (title names the streamed unit,
  never the unloaded selection, AND the streamed unit's seeded line is rendered
  beneath that title — not the helper over an empty buffer),
  TestLogTitleNamesTheDeadUnitWhoseLinesRemain (a real `done` transition retires
  the stream and retains its line; a following wheel keeps the title on the dead
  unit and the retained line rendered under it — the live + dead coverage Codex
  asked for), TestSettleDoesNotRestartJournalWhileEditingLogFilter (clamped
  WheelUp schedules a settle without moving the unit, type into the log filter,
  fire the settle → stream unchanged; Enter then applies the filter), and
  TestFilterEditorOwnsMouseInput (table-driven across BOTH editor modes: left-
  click, right-click and the list wheel are inert, while the log wheel still
  scrolls). All fail before their fix.
- **CHANGELOG:** two Unreleased → Fixed entries, one per commit (log-pane
  naming with the title fix; filter-editor input ownership with the guard fix).
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-20/21). Codex accepted the
  production + regression diff across all four variants (seams 1, 1b, 2, 3):
  the retained dead-stream identity is gated by actual retained lines with the
  live stream winning; both editor modes have coherent mouse ownership; and the
  log-wheel exception is proved. Landed as two commits per the maintainer:
  `202a061509dfdcc3d749e148b892c0d27d00a1d1` "Name the log pane by the unit
  whose lines are shown" (view.go + the two title regressions + log-pane
  CHANGELOG), then this commit for filter-editor input ownership (model.go
  settle + mouse guards, the settle/mouse regressions, filter CHANGELOG, this
  record).

#### [x] UT-043 — Separate the log-filter draft from the applied filter

- **Status:** Accepted — implemented
- **Source:** Codex bounded audit batch 1 (+ addendum) and batch 2/3;
  maintainer authorized the open-ended audit directly. `m.logFilt` served as
  both the editor's live buffer and the filter every journal path reads, so a
  half-typed grep leaked into journal work.
- **Confirmed leaks (each real):** paging (`loadOlder → fetchOlder` used
  `m.logFilt`), poll (`postPollSync → syncJournal` restarted with the draft
  because `m.journal.filter != m.logFilt`), resize (same via the resize sync),
  and rendering (`logTitle`/empty-notice read the mutable `m.logFilt`, so old
  lines sat under a title claiming the draft). The UT-042 mouse guard permitting
  the log-wheel during edit is what exposed the paging path.
- **Fix:** a new `m.logDraft` holds the grep while typing; the editor edits the
  draft and applies it to `m.logFilt` only on Enter (Esc discards it, leaving
  the applied filter untouched). `loadOlder` pages on `m.journal.filter`
  explicitly. The footer shows the draft; the title keeps naming the applied
  filter. This resolves batch1/1, the batch1 addendum, and batch2/3 at the
  source, and makes the UT-042 seam-2 settle guard **redundant** — removed it;
  a settle during edit now reconciles to the same applied filter and no-ops.
- **Regression coverage:** `src/filter_ownership_test.go` —
  TestLogFilterDraftNeverLeaksIntoJournalWork drives the full matrix with a
  NONEMPTY applied filter A and a distinct draft B: the title and empty-notice
  show A while the footer shows B; a successful poll AND a failed poll leave the
  stream on A; paging's source is A (loadOlder reads m.journal.filter) while the
  draft is B; a resize hide→show (140↔83, crossing the 84-col pane threshold)
  restarts the stream on A, never B; Esc preserves A and the live stream; Enter
  alone applies B. TestPagingUsesTheStreamFilterNotTheDraft proves paging by
  EXECUTED argv (fakeFiniteJournalctl + applyCmd, sandbox-safe): with the stream
  filter and the model filter set to different values, loadOlder's recorded `-g`
  carries the stream's, never the model's (red-proven by reverting the call
  site to m.logFilt). Plus the updated
  TestSettleDoesNotRestartJournalWhileEditingLogFilter (draft in logDraft,
  applied unchanged, settle no-ops, Enter applies). Draft-leak test proven red
  (editor pointed back at m.logFilt → draft never captured). Existing tests that
  typed into the log editor mid-edit updated to read the draft (logfilter,
  ingress); the escape tests pass unmodified (Esc-restore outcome unchanged).
- **CHANGELOG:** Unreleased → Fixed entry added.
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-21). Codex accepted the
  production diff and CHANGELOG; two review rounds strengthened the matrix
  (nonempty applied vs draft; poll success+failure; real resize hide→show) and
  then required an executed-argv paging proof (fakeFiniteJournalctl) in place of
  a field read — added and red-proven. Landed as one commit + pushed.

#### [x] UT-044 — Make the too-small notice modal to the mouse

- **Status:** Accepted — implemented (landed as two commits; see outcome)
- **Source:** Codex bounded audit batch 1/2.
- **Problem:** `handleKey` swallows all input but q/esc below `minWidth`/
  `minHeight`, but `handleMouse` had no such guard: below the minimum, only the
  "too small" notice is drawn, yet clicks and the wheel still hit-tested the
  hidden table/menu/log — a right-click opened an invisible action menu, the
  wheel scrolled the hidden list.
- **Fix:** a `m.width < minWidth || m.height < minHeight` guard at the very top
  of `handleMouse` (before help/menu/filter/table/log), mirroring the keyboard
  threshold. The `||` covers width-only and height-only too-small states.
- **Regression coverage:** `src/geometry_input_test.go` —
  TestTooSmallNoticeSwallowsTheMouse (table-driven over both-dimensions,
  width-only and height-only; right-click/left-click/wheel over former-content
  coords all return no command, open no menu, move no selection; red-proven
  before the guard) and TestTooSmallNoticeSwallowsClicksIntoAnOpenMenu (a menu
  opened while large is inert to clicks once shrunk — no action, menu unmoved).
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-21). Codex's immutable audit
  accepted the net result. Landed as two commits — split by a staging error,
  not by design: the regression `src/geometry_input_test.go` landed early in
  `9ca5193` (folded in by a stray already-staged path from a gofmt check), which
  briefly left HEAD red (test without guard); the guard + CHANGELOG + this
  record landed in `9c239cd` "Make the too-small notice own the mouse, like the
  keyboard", restoring green. The accidental red intermediate is recorded here
  as the disclosed staging error; pushed history is not rewritten.

#### [x] UT-046 — Keep a log row in the full view at short heights

- **Status:** Accepted — implemented
- **Source:** Codex bounded audit batch 2/4.
- **Problem:** `detailLines()` is a fixed 7 in full view, but at supported
  heights 10–13 `paneInner()` is only 5–8; `viewLogPane` emits seven detail
  rows plus the rule before the logs, and `framed` keeps only the first
  `paneInner` rows — so the detail block overflows and every log row is clipped.
  The pane is titled "log" and accepts log controls while showing zero lines.
- **Fix:** in full view `detailLines()` returns `max(1, min(7, paneInner()-2))`,
  reserving the rule and at least one log row. No cycle: paneInner →
  contentHeight → headerLines, none call detailLines.
- **Regression coverage:** `src/fullview_height_test.go`
  (TestFullViewKeepsALogRowAtShortHeights): enter full view, seed a sentinel log
  line, and assert it renders in `View()` at every supported height 10–14.
  Red-proven (h=10–13 showed no rows before the cap; h=14 already fit).
- **CHANGELOG:** Unreleased → Fixed entry added.
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-21). Codex accepted the
  production formula outright; one review round made the regression process-free
  (set fullView/focus directly instead of starting five real follows) and added
  a per-height geometry lock (View() is exactly h rows, sentinel below a
  separator rule). Committed as one commit + pushed.

#### [x] UT-047 — Keep the filtered-title indicator on a narrow pane

- **Status:** Accepted — implemented
- **Source:** Codex bounded audit batch 2/5.
- **Problem:** `framed` admits only `width-4` title cells and truncates from the
  right. `fitTitle`'s final fallback returned `head + " · filtered"` even when
  it overran that budget, so a long title — a long unit name in the log pane, or
  the unit-count head in the table pane — let the frame cut the indicator off
  the right edge, hiding that a filter was active. Both callers
  (`fitTitle(..., "filtered")`).
- **Fix:** on the final `fitTitle` fallback, reserve the styled suffix
  ` · <alt>` and truncate only the expendable head with the ANSI/grapheme-aware
  `truncANSI`, then append the suffix; so the indicator always fits within
  `width-4` and framed never clips it. Boundary: `room > 0` truncates the head;
  `room == 0` (suffix fills the budget exactly) returns the suffix alone; only a
  negative room is truly too narrow (unchanged).
- **Regression coverage:** `src/filtered_title_test.go`
  (TestFilteredTitleKeepsItsIndicatorWhenNarrow): a long Unicode unit name with
  an active filter at the 84-col split pane and 40-col full view; asserts
  "filtered" is visible and every framed row is exactly the terminal width.
  Red-proven at both geometries (the indicator was cut off before the fix). Plus
  TestFitTitleReservesTheSuffixAtTheBoundary — a direct helper test at width
  `suffixWidth+4` (room==0) and +1 (room==1), asserting the result stays within
  `width-4` and keeps "filtered".
- **CHANGELOG:** Unreleased → Fixed entry added.
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0.
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-21). Codex accepted the
  reserve/truncate-head design; one review round fixed an exact-fit boundary
  (`room == 0` returns the suffix alone, not head+suffix, so it never overruns)
  and added the direct room-0/room-1 boundary regression plus corrected the
  CHANGELOG/TODO wording (table titles carry count text, not a unit name).
  Committed as one commit + pushed.

#### [x] UT-045 — Reap follow streams in tests (close the leaked-child hole)

- **Status:** Accepted — implemented
- **Source:** the CI flake root cause (see 9ca5193) + Codex's exact ownership
  checklists. Test-only; no production change.
- **Problem:** `syncJournal` starts follows on `context.Background()`, so a test
  that opens a stream and returns without `stopAndWait` leaks the goroutine and,
  where `journalctl` exists (CI, dev hosts), its child process. Demonstrated by
  the recorded child PID (the fakes record each follow/page pid) surviving the
  owning test — see the exact-PID proof below; name-based `pgrep` is NOT how
  this is measured (the fakes `exec sleep 300`, and this host churns unrelated
  `sleep`s). That leaked child is what shadowed the recorder in the CI flake.
- **Fix:** a shared `stopJournalOnCleanup(t, *model)` helper
  (`src/journal_cleanup_test.go`) — `t.Cleanup` that reads `m.journal` at
  cleanup time (so a replaced stream is still reaped) and calls the idempotent,
  nil-safe `stopAndWait`. Registered immediately after model setup so it
  protects assertion-failure returns too. Applied to every stream-opening test
  per Codex's normal-return + failure-path checklists: added inside the
  pointer-returning builders (escModel, focusModel, fatalModel, actionModel,
  leakModel — covers their tests wholesale) and to the named inline/caller tests
  (startup, geometry, paging replacements, logfilter, view full-view, resize,
  quit, journal_bytes, filter_ownership, scroll_settle). Fixed
  `journal_e2e_test.go:e2eModel`, whose `t.Cleanup(m.journal.stop)` was
  cancel-only and bound the original pointer — now uses the helper. Hot loops
  (`layout_test.go`) reap synchronously per cell. The direct `js :=
  startJournal` I/O tests (clock/journal_deadline) and the fake-follow recovery
  tests (journal_recovery) get an immediate `defer js.stopAndWait()` / dynamic
  model cleanup right after the stream starts, so an early `Fatal` before their
  existing late stop still reaps the child; `resize_test.go`'s retry-gate case
  too. Only genuinely inert synthetic/spy streams are left untouched.
- **Proof:** a focused regression locks the helper —
  journal_cleanup_test.go:TestStopJournalOnCleanupReapsTheFollowChild: an owner
  subtest registers the cleanup while `m.journal` is nil, starts a real
  fake-follow, records its pid, and returns without stopping it; after `t.Run`
  returns, the parent asserts that exact pid is reaped via `kill(0)→ESRCH`. This
  is name-independent, so it holds even though the fake `exec sleep 300`s
  (renaming the child away from journalctl — which is why a `pgrep journalctl`
  count is NOT a valid residual check). It locks both late-bound final-pointer
  ownership and synchronous reaping. stopAndWait's reaping is additionally
  covered by quit_test.go:TestCtrlCKillsTheJournalChild.
  - **Whole-suite check (noise-free):** run under `setsid` so the suite has its
    own session; after it exits, `pgrep -g <pgid>` for `sleep`/`journalctl`/`sh`
    returns NONE — no fake follow/page child (the fakes `exec sleep 300`)
    survives. A plain `pgrep -x sleep` before/after count is INVALID on this
    host: it runs 4–6 unrelated `sleep` processes that churn several per second,
    so it cannot distinguish an owned leak — the same name-blindness that made
    `pgrep journalctl` invalid.
  - **Pre-existing orphan accounted for:** an earlier gate found a `sleep 300`
    (pid 3932622) reparented to user systemd. It was my own red-check artifact —
    the run where I deliberately neutered the cleanup (to prove the focused test
    goes red) left that child unreaped. Killed; not a suite residual.
  The recorder's `--cursor` narrowing (9ca5193) is kept as belt-and-braces.
- **Gates (explicit exit codes):** gofmt clean; `go vet` 0; full `go test` 0;
  `go test -race` 0; `nix build` 0 (helper file git-tracked so the sandbox sees
  it).
- **Review outcome:** Accepted and implemented (triage/review Codex/GPT-5,
  implementation Claude Code/Opus 4.8, 2026-08-21). Test-only; no production
  change and no CHANGELOG entry. Codex's multi-round ownership audit drove it to
  full coverage: the normal-return checklist, the early-Fatal direct-stream and
  recovery families, per-cell layout teardown, the fixed e2eModel replacement
  cleanup, a compile-order fix, a duplicate-defer removal, and a narrowed helper
  comment. The exact-PID focused regression plus the isolated-process-group
  whole-suite evidence close the proof; the residual scare was a red-check
  artifact + host `sleep` churn, both accounted for above. Committed as one
  commit + pushed.

## Post-0.3.4 review scope (Codex implements, Claude reviews)

Feature specs the maintainer directed after 0.3.4. Codex authors; Claude
reviews + gates + commits per the flipped precedent recorded in the comms log.

#### [ ] UT-048 — Show a slice's aggregated logs when a slice is selected

- **Status:** Spec — awaiting Codex implementation
- **Requested by:** maintainer, 2026-08-22 UTC. Base: HEAD after v0.3.4.
- **Now:** selecting a `rowSlice` shows "a slice has no journal of its own"
  (`view.go:1047`); `journalTarget()` returns "" for a slice.
- **Native basis:** journald tags every entry with the trusted field
  `_SYSTEMD_SLICE`, so `journalctl _SYSTEMD_SLICE=<slice>` streams all units in
  that slice — one stream, no per-unit fan-out. Immediate slice only; nested
  sub-slices carry their own value, so the full subtree needs one
  `_SYSTEMD_SLICE=` match per descendant (journalctl OR-s repeated field
  matches), and unitop already knows the hierarchy (`sliceParent`/tree).
- **Shape:** generalize the journal "target" from a bare unit name to a
  selector that is either `-u <unit>` or one-or-more `_SYSTEMD_SLICE=<slice>`
  matches. `startJournal`/`readBacklog`/`fetchOlder` (which hardcode
  `-u unit` at journal.go:110/346/363) build argv from that selector;
  `journalTarget()` returns it for a slice row; `syncJournal` keys the stream
  on the selector so unit↔slice switches restart correctly; the log title
  names the slice (`logUnitName`/`sliceLabel`). grep/priority filters still
  apply on top.
- **Decisions for Codex to make (and justify):** immediate slice vs full
  subtree (recommend subtree, since that matches what "select the slice" means);
  how the selector threads through the stream identity used for reap/restart.
- **Acceptance:** a selected slice streams its units' logs; paging and the log
  filter still work; unit↔slice switches restart the stream (and reap the old
  child — the UT-045 ownership still holds); a slice with no logged units shows
  the empty-notice, not a crash.
- **Tests:** fake-journalctl argv assertion that the slice selector carries the
  right `_SYSTEMD_SLICE=` match(es); unit↔slice restart; title names the slice.

#### [ ] UT-049 — Cumulative slice accounting from the slice's own cgroup

- **Status:** Spec — awaiting Codex implementation
- **Requested by:** maintainer, 2026-08-22 UTC. Base: HEAD after v0.3.4.
- **Now:** the slice row aggregate (`tree.go aggregate`) sums *visible* child
  RATES + mem/tasks but initializes the new cumulative byte totals
  (`IPIn/IPOut/IORead/IOWrite`) to 0 and never sums them — so a slice shows no
  cumulative transfer totals — and summing misses inactive/unfetched children.
- **Native basis:** a slice is a cgroup and cgroup v2 accounting is
  hierarchical, so `systemctl show <slice>` returns whole-subtree totals:
  `MemoryCurrent`, `CPUUsageNSec`, `TasksCurrent`, `IOReadBytes`/`IOWriteBytes`
  (IOAccounting), `IPIngressBytes`/`IPEgressBytes` (IPAccounting). Gated on that
  accounting being enabled — IP accounting is off by default; IO/CPU/memory are
  usually on but distro/version-dependent. Unavailable → the unset sentinel,
  already hidden by the render (F2).
- **Shape:** for the SELECTED slice, query its own cgroup accounting via
  `systemctl show <slice> -p …` and show accurate cumulative io/net (+
  mem/tasks/cpu) in the detail pane, rather than trusting the child sum.
- **Perf constraint (hard):** do NOT query every slice's accounting every tick —
  that reintroduces exactly the PID-1 `systemctl show` cost UT (this release)
  just removed. Query the slice's accounting only when a slice is selected (its
  detail is on screen), not for every slice row in the table each poll.
- **Decisions for Codex:** whether to also fix the row aggregate to sum child
  cumulative totals (cheap, approximate, for the table) while the detail uses
  the true cgroup number; whether slice live rates come from delta-over-time on
  the slice's own counters or stay the summed child rates.
- **Acceptance:** a selected slice with accounting enabled shows cumulative
  io/net/mem/tasks from its own cgroup; cleanly hidden when accounting is off;
  no per-tick systemctl-show cost added for unselected slices.
- **Tests:** fake `systemctl show` for a slice returning IO/IP totals → detail
  renders them; accounting-off sentinel → hidden; a guard/test proving the
  slice-accounting query does not fire for every slice each poll.

## Record convention

Each item's **Review outcome** is one of:

- **Accepted** — implemented; the box is checked and, as applicable, the named
  regression test is linked (a documentation-only fix has none), with
  owner/commit recorded in the outcome.
- **Rejected** — the reproduction or design evidence that disproves it is
  recorded.
- **Duplicate** — links the canonical item.
- **Deferred** — records why and when it should be reconsidered.

A checked box means a resolved outcome (accepted/duplicate/…); explicit
deferrals stay unchecked. A shipped accepted item **stays checked here** — this
file is the retained historical record of what was reviewed and why, not a live
queue to prune. Its user-visible result is *also* described under
`CHANGELOG.md`'s Unreleased section; the two are kept in step, not substitutes.
