# TODO

## Pending review — Codex

| Field | Value |
| --- | --- |
| Queue status | Pending review |
| Submitted by | OpenAI Codex |
| Review model | GPT-5 |
| Submitted | 2026-08-19 UTC |
| Reviewed revision | `799cfebcb8a7db2bc7b9036b065ddb5e6d78725d` (`v0.3.1`) |
| Scope | Committed contents at the reviewed revision only; working-tree changes were deliberately excluded |
| Automated checks | `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt -l` passed under `nix develop` |

These are code-review findings awaiting maintainer triage and targeted
reproduction. They are not accepted bugs or release notes yet. Check an item
only after recording the review outcome and, when accepted, adding the named
regression coverage.

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

#### [ ] UT-007 — Stop mixing client and remote wall clocks

- **Status:** Pending review
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
- **Review outcome:** _Pending._

#### [ ] UT-008 — Reconcile log focus and lifecycle when the terminal resizes

- **Status:** Pending review
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
- **Review outcome:** _Pending._

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

#### [ ] UT-010 — Keep every action-menu choice visible and positioned

- **Status:** Pending review
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
- **Review outcome:** _Pending._

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

#### [ ] UT-018 — Preserve critical host status on narrow terminals

- **Status:** Pending review
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
- **Review outcome:** _Pending._

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

#### [ ] UT-020 — Make all clipping and menu sizing grapheme-aware

- **Status:** Pending review
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
- **Review outcome:** _Pending._

## Pending review — Claude Code

| Field | Value |
| --- | --- |
| Queue status | Pending review |
| Submitted by | Claude Code |
| Review model | Fable 5 |
| Submitted | 2026-08-19 UTC |
| Reviewed revision | `799cfebcb8a7db2bc7b9036b065ddb5e6d78725d` (`v0.3.1`) |
| Scope | The `v0.3.0..v0.3.1` diff (`ffe984d..799cfeb`: the log-pane rework and the screenshot tooling), verified against the full sources at the reviewed revision; the working tree was clean |
| Automated checks | `go test ./...`, `go vet ./...`, and `gofmt -l` passed under `nix develop` |

These are code-review findings awaiting maintainer triage and targeted
reproduction. They are not accepted bugs or release notes yet. Every finding
survived an adversarial verification pass; a Confidence of "High; confirmed"
means the verifier proved the mechanism from the code, "kept as plausible"
means it could not be refuted but was not reproduced end to end. IDs continue
the shared `UT-###` sequence from the queue above.

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

## Pending review — joint follow-ups

Found by adversarial review during implementation of earlier fixes.

#### [ ] UT-031 — Own every poll and action child at exit

- **Status:** Pending review
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
- **Review outcome:** _Pending._

## Review process

For each item, replace `_Pending._` with one of:

- **Accepted** — include owner/target release and link the regression test.
- **Rejected** — record the reproduction or design evidence that disproves it.
- **Duplicate** — link the canonical item.
- **Deferred** — record why and when it should be reconsidered.

When an accepted item ships, remove it from this queue and describe the
user-visible result under `CHANGELOG.md`'s Unreleased section.
