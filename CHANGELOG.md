# Changelog

Notable changes to unitop. Versions follow [semantic versioning][semver],
though while the project is pre-1.0 the flags, keys and output are still open
to change.

[semver]: https://semver.org/spec/v2.0.0.html

## [Unreleased]

### Fixed

- **A journal stream that dies comes back.** When journalctl exited — a
  transient failure, an ssh blip — the pane kept its final lines but nothing
  ever restarted the tail: the model held the dead stream and mistook it for
  live until the unit or filter changed. The stream is retired on its final
  batch now. The first successful poll after a one-second retry gate starts
  exactly one replacement for that same unit, and never off a failed poll,
  so a broken journalctl cannot hot-loop. A selection that moved on
  reconciles at once, and R — or any deliberate change — brings the tail
  back immediately.
- **Reading a journal no longer gambles memory on its contents.** The backlog
  and every backwards page were parsed whole before the first line reached
  the pane — hundreds of near-limit entries at once. They stream now: the
  newest 16 MiB are kept contiguously, the boundary is announced in the
  pane, everything older is drained so journalctl can finish, and paging
  carries the field allowlist the backlog always had. An entry past 4 MiB
  becomes a placeholder without taking its page with it, and a page with
  nothing to anchor on says so once instead of refetching forever.
- **journalctl's warnings reach the pane — from a live follow too.** stderr
  used to be discarded on successful finite reads and held until death on
  -f. It is pumped concurrently now, bounded by bytes and lines with one
  suppression marker, surfaced while the process runs, and a silent nonzero
  exit is named instead of hiding behind "journal stream ended". A -g that
  matches nothing is still silence, not an error.
- **Pasting into a filter cannot repaint the screen.** A bracketed paste is
  one event carrying whatever was on the clipboard — newlines, control
  bytes, whole escape sequences — and the editors appended it raw, where the
  header and the editor rendered it straight onto the terminal. Everything
  typed or pasted is sanitized at ingress now, and so are the -f filter, the
  -H value and the local hostname: the startup screen, the header and the
  troubleshooting advice all render only the sanitized name, while ssh keeps
  the raw one.
- **Ctrl-C quits from anywhere, and no journalctl outlives the screen.** The
  action menu swallowed it, the confirmation dialog treated it as cancel,
  and the filter editors quit without stopping the journal stream — leaving
  a journalctl -f, and the ssh carrying a remote one, running after exit.
  Every quit gesture now leaves through one exit that waits for the stream
  — follow and any page fetch in flight, reaped, not just signalled — a
  pasted ^C stays data in the editor, replacing a stream (changing unit or
  filter) reaps the old one before letting go of it, and the program cleans
  up after the event loop returns too — bubbletea can consume an interrupt
  before the model ever sees it.
- **A host on systemd 247–250 is refused up front, by name, instead of
  passing the version gate and failing every poll.** The gate said 247, but
  `--timestamp=unix` — which every detailed poll sends — arrived in v251;
  v250's `--timestamp` knows only pretty/us/utc/us+utc. The gate's failures
  are also the right kind now: a version probe that could not run at all
  (systemctl missing, a broken library) is an ordinary retryable error
  carrying its stderr rather than a fatal "no systemd" verdict; a too-old
  verdict is no longer cached, so R after an upgrade re-probes instead of
  re-reading the stale rejection; and the troubleshooting advice for a
  missing binary names the binary that failed instead of blaming the ssh
  client for a missing local systemctl.
- **A backwards iowait tick can no longer show an absurd CPU percentage.**
  idle includes iowait, which the kernel lets run backwards; subtracted as an
  unsigned integer it wrapped, and one glitched sample reported an enormous
  negative figure. Such a sample's CPU number is dropped — the same tick's
  network rates survive — and the next well-formed sample recovers.
- **Unit actions can no longer summon a password prompt on the terminal.**
  systemctl runs with --no-ask-password in every form, matching the sudo
  path's existing -n: authorization now fails into the toast, as promised,
  instead of a polkit agent seizing the screen.
- **Typing a space into a filter inserts one space, not two.** A real space
  key carries its rune and was appended twice over, so "timed out" silently
  searched for "timed  out" — in the unit filter and the journal grep alike.
- **A deeply scrolled log pane no longer claims the unit has written nothing
  to the journal.** The scroll offset is measured in wrapped display lines, so
  widening the terminal or opening the full view — both of which re-wrap the
  buffer — could leave it pointing past everything held, and the pane rendered
  the empty-journal notice over a full buffer. The offset is re-clamped at
  every geometry change, and a window handed a stale offset lands on the
  buffer's top rather than past it.
- **The marker at the top of the buffer sits on a row of its own.** It was
  painted over the oldest visible line, which the scroll clamp then made
  unreachable — and a one-entry journal showed only the marker. The scroll
  range gains exactly one step to pay for the row.
- **A trimmed buffer no longer claims to start at the journal's beginning.**
  A full buffer periodically block-trims away its oldest lines, but the
  "beginning of this unit's journal" marker survived the discard, presenting
  incomplete history as complete.
- **The buffer-full marker states the retention policy** ("unitop keeps the
  newest 20000 lines") instead of a live count, which deliberately rides up to
  2048 lines above the cap between trims — so the old number was usually wrong
  and jittered from frame to frame.
- **The screenshot rig no longer trusts a shell with anything.** Its
  fake-journalctl shim passed arguments through eval — a search pattern
  holding a quote broke it, and a command substitution inside one executed —
  and tmux got PATH and the unitop command spliced into one shell string,
  which broke on a PATH holding a space and silently screenshotted the real
  journalctl's permission error. Both now travel as argv (PATH as a tmux
  environment entry), the shim's journal cache is keyed on the message
  content and built in a per-run directory renamed into place only when
  finished, a dead pane aborts the run loudly, and the hostname comes from
  uname -n, which coreutils provides.

### Changed

- **The entries on screen are word-wrapped once per frame, not twice.** The
  0.3.1 split of measuring from rendering left the renderer re-wrapping what
  it had just measured; styling now reuses the measured segments — a constant
  ~460 allocations and ~18 KB per frame back, no change to any frame's
  content.

## [0.3.2] — 2026-08-19

A readability release: unitop no longer leans on colours that some themes are
entitled to render unreadably.

### Changed

- **Headings, the sorted column, the focused pane frame and the filter no
  longer use a colour.** They were magenta and yellow; they are now the
  terminal's own foreground, bold. Rules, timestamps, labels and idle values
  are that same foreground dimmed, rather than colour 8. Colour is left to
  carry meaning only — green healthy, yellow watch, red wrong, cyan finished,
  blue keys.
- On a dark theme that treats colour 8 as a background shade, every timestamp
  and idle number had been a smudge at 1.71:1; on a light theme whose palette
  was drawn for a dark one, headings sat at 1.65:1. An ANSI index is a name,
  not a promise about contrast, so nothing that must be read depends on one.
- The screenshots in `docs/` are rendered in a real terminal theme now.
  `THEME=latte nix run .#screenshots` renders a light one.

## [0.3.1] — 2026-08-19

A performance release. Watching a service that logs steadily could take a whole
core; it now takes less than 0.1.3 did, holding five times as much history.

### Fixed

- **The log pane no longer saturates a core once its buffer fills.** With a full
  20k buffer and a service logging 20 lines a second, unitop reached 120% of one
  core. 0.1.3 was 13.5% for the same work, and it is 12.1% now.

  Two causes, both older than they looked. Counting the buffer's height went
  through the renderer — building a lipgloss style and rendering every segment
  of every held entry, to take the length of the result and throw the strings
  away. That alone was three quarters of all CPU. And the count was redone from
  scratch on every batch, which never stopped once the buffer reached its cap,
  because from then on every batch also trims. **0.2.0 raised the cap from 4000
  lines to 20000 while keeping that design**, which is where this came from;
  0.3.0 inherited it and added render cost on top.

  Measuring and rendering are separate now, the memoised height is adjusted for
  lines added and dropped rather than thrown away, and trimming happens in
  blocks rather than moving every retained entry per arriving line. That last
  one trades a little memory for the saving: the buffer rides up to 2048 entries
  over its 20,000 cap between trims, around half a megabyte.
- Scrolling far back in a large buffer was quadratic: each entry's lines were
  prepended to a growing accumulator, copying everything already built, and
  every line between the window and the bottom was rendered whether it was on
  screen or not. Ten thousand lines back, a single frame cost 129ms and
  allocated 430MB. Only the entries that appear are styled now — 10ms, and 56×
  less memory.

### Changed

- The readme screenshots show the current UI: the boxed panes, the filter in
  the pane title, the pane-scoped footer. `nix run .#screenshots` regenerates
  all four from the build in your tree, on any machine — the unit table and host
  stats are the real ones, the journal is invented so it works where the real
  one cannot be read, and the hostname is replaced on the way out.

## [0.3.0] — 2026-08-18

The log pane was reading the journal wrongly, and a service could scribble on
your terminal through it. Both are fixed. Alongside that, a pass over the keys
and the layout: every key now belongs to a pane and says so, and no line can
overrun the terminal.

### Added

- **Each pane is drawn in its own box, and the focused one's is heavy and
  coloured.** Focus used to be a single character on the divider between the
  panes, which is not where you are looking. Both boxes are always present, so
  moving focus does not shift the layout, and the difference reads without
  colour as well as with it.
- **Each box's title says what its filter is doing, in words** — `units 12 of
  340 · name or description contains "nginx"`, or `log nginx.service · matching
  "denied", error and above`. A filtered pane that does not say so reads as a
  quiet one.
- A minimum terminal size, 40×10. Below it unitop says so — the size it has,
  the size it needs, and how to quit — instead of drawing a layout that cannot
  exist at that size.

### Changed

- **Keys belong to the pane they act on.** With the log focused, `s` `S` `r`
  `t` `a` do nothing; with the table focused, `f` `F` `e` `w` do nothing. The
  footer lists only the keys that apply where you are, so what is on screen is
  what the next keystroke will do. `s` used to resort the table silently behind
  a log you were reading.
- **One key per motion.** Movement is the arrows, `pgup`/`pgdn` and
  `home`/`end`, in the table, the log and the action menu alike. `F` and `f`
  are the log's two ends — `f` resumes following, and the live end *is* the
  bottom — and they are the only letters bound to motion anywhere. The vim
  aliases (`j` `k` `h`, `g`, `G`) and the readline ones (`ctrl+b`, `ctrl+f`)
  are gone, along with `=`/`_`, which duplicated `+`/`-`, and `r` as a third
  way to retry a failed connection alongside `R` and `enter`. Every letter an
  alias held is a letter unavailable for a command, and `ctrl+f` was fighting
  the `f` that follows the log.
- **`esc` steps back exactly one thing per press**, innermost first: cancel
  what you are typing, close the menu or the help, clear the focused pane's
  filter, leave the full view, return focus to the table. It never reaches past
  the first that applies, so nothing you cannot see is cleared. Three faults
  fall out of that. An applied log search could not be cleared at all — the old
  cascade only ever looked at the unit filter, so `esc` in the log pane did
  nothing. Pressing it in the full view cleared the *table's* filter, something
  not on screen, and left you still in the full view. And `esc` while typing
  threw the filter away rather than restoring what you were amending, so
  thinking better of an edit cost you the filter. `esc` is now documented, too;
  it did five things and had no line of its own anywhere.
- The `/` prompt says what the text will do rather than which pane owns it:
  "show units whose name or description contains", or "show journal lines
  matching". It gives ground as the terminal narrows — the explanation first,
  then the hint — but never what you have typed.
- The help is grouped by pane, sets itself in two columns on a wide screen, and
  scrolls when the terminal is too small for it. At 80×24 it used to be cut off
  at the bottom, taking its last group with it — and the last group is where
  quit lives.
- The heat ramp is back to five steps — grey, green, yellow, orange, red —
  after the move to terminal colours had flattened it to four. Orange is bright
  yellow, which every theme renders warmer than plain yellow, so the gradient
  still comes entirely from the sixteen.

### Fixed

- **A log line can no longer escape its pane.** Journal messages are arbitrary
  bytes: a unit whose output goes to a serial console leaves carriage returns
  in them, a boot log arrives with embedded newlines, and any service at all
  can write escape sequences into its own journal. Rendered raw, those moved
  the cursor, repainted the screen, left a background colour set for everything
  drawn afterwards, and made every width calculation wrong — a Proxmox console
  log tore the pane's box apart. Everything from the far end — journal fields,
  systemd property values, ssh and systemctl stderr — is now sanitised where it
  enters: escape sequences dropped whole, other control bytes shown as their
  Unicode pictures (`␇`, `␡`), tabs expanded, carriage returns treated as the
  line breaks they meant, invalid UTF-8 replaced.
- **The log search really does search the whole journal now.** `journalctl -n
  500 -f -g PATTERN` looks like it does, but with `-f` journalctl seeks back
  500 **raw** entries and only then applies the pattern — so a search found
  nothing older than the last 500 lines of the unit, however many matches were
  sitting in the journal. Measured on a 1200-entry journal whose 100 matches
  were the oldest: it returned none of them, with the boundary exactly at the
  500th entry from the end. `-p` was never affected, because `PRIORITY` is
  indexed and journalctl can seek by it.

  The stream is now two commands: a backlog that terminates — so it can search
  properly, and so it has an end — then a tail resuming from the last entry's
  cursor. `-n 0` is deliberately absent from that tail: it reads as "start with
  nothing" but journalctl takes it as "replay nothing", which silently defeats
  `--after-cursor` and would drop whatever the unit wrote between the two
  commands.
- **Host CPU% no longer double-counts guest time.** The kernel adds each unit
  of guest cputime to `CPUTIME_USER` (or `CPUTIME_NICE`) *and* to
  `CPUTIME_GUEST` (or `CPUTIME_GUEST_NICE`) — see `account_guest_time()` — so
  summing all ten `/proc/stat` fields counted it twice. That inflated the busy
  time and the total both, and since busy is the smaller of the two it pushed
  the percentage up: a hypervisor spending half a second in a guest and half
  idle reported 67%, not 50%. Only hosts running VMs are affected; elsewhere
  the fields are zero.
- **Nothing can overrun the terminal any more.** A line wider than the screen
  wraps, and a wrapped line pushes every line below it down one — so a single
  long string did not spoil itself, it spoiled the whole screen. Six places did
  it: the filter prompt (at any width narrower than the prompt, so 60 or 75
  columns, not just absurd ones), the help in one-column mode, its "more below"
  marker, the action menu and its confirmation, and the startup screen's error,
  suggestions and key hints. Each gives ground in its own way now, and `View()`
  truncates as a last resort so the invariant holds whatever is added later.
- Journal messages and service text containing wide Unicode — CJK, emoji —
  respect terminal cell widths instead of overflowing their panes.
- A multi-line journal entry, such as a stack trace or a boot log, is rendered
  as the several lines it is rather than one line with newlines inside it.
- Switching units opens the new log at the live end. The previous unit's scroll
  position carried over, so the pane came up empty — and because follow was
  still off, every batch that arrived pushed the view further up instead of
  filling it in. The view can no longer float above the buffer at all.
- The last line of a burst is no longer stranded. The reader handed lines to
  the UI only when it could see the model had caught up, so a line arriving
  while it had not sat in the buffer until another line turned up — on a quiet
  unit, for as long as it stayed quiet. It coalesces on a clock now, so nothing
  waits more than 50ms.
- One oversized journal entry can no longer end the tail. `bufio.Scanner` stops
  with `ErrTooLong` past its buffer and nothing checked `Err()`, so an entry
  over 4 MiB would have ended the stream for good, with "journal stream ended"
  the only clue. An entry past the cap is now dropped, with a note in its
  place, and the tail carries on.
- An empty log pane says why. `journalctl -f` prints nothing at all when the
  filter matches nothing, so the pane sat on `waiting for journal…` as though
  it were stuck when the search had in fact finished and come up empty. It now
  distinguishes still reading, nothing matching the filter — naming the filter
  and how to clear it — and a unit that has genuinely written nothing.
- A log search that matches nothing no longer shows a red error. `journalctl
  -g` exits 1 when its pattern matches nothing and says nothing on stderr about
  it, so an empty result read as a failure to open the journal. A real failure
  — permissions, the usual one — exits with something to say, and still
  reports. Paging backwards had the same fault, and claimed a page could not be
  loaded when it had simply reached the beginning.
- Backwards page fetches are cancelled with the stream that asked for them.
  They ran detached, so changing unit or filter left a `journalctl` running — a
  remote one, over ssh — for up to 30s, to produce an answer that would be
  discarded on arrival.
- The initial poll is marked in flight before its command starts. On a slow
  connection the first refresh tick could otherwise launch a second poll over
  the same collector, racing its samples and producing wrong rates or a
  concurrent-map panic.
- Changing units or journal filters no longer leaves cancelled `journalctl` or
  remote `ssh` children unreaped.
- A fatal verdict no longer latches. If an unsupported-systemd error arrived
  after unitop had already connected, the tick suppressed itself and nothing
  ever cleared the flag, so the display froze permanently and only manual
  refreshes moved it. A successful poll clears it, `R` clears it, and while it
  is set the host bar says `NOT POLLING — R to retry` rather than the screen
  quietly going stale.
- The action popup stays inside the pane it belongs to instead of overrunning
  the bottom of the list onto the footer, and the tail beneath it is sliced by
  terminal cell rather than by rune, so a row with a double-width name resumes
  at the column the popup actually covered.
- `q` quits from the too-small notice even with the filter editor or the action
  menu open underneath. The notice says `q` quits; with the editor up it was a
  character to type, and with the menu up it closed a menu that is not on
  screen.
- Rows in the table no longer turn bold at random. An error-priority line in
  the log pane is rendered bold, and the attribute bled into the row drawn
  beneath it — so which units looked bold depended on which unit was selected
  and what its journal happened to contain.

## [0.2.0] — 2026-08-16

### Added

- The side pane and full view describe the service, not just name it. Alongside
  the existing pid, uptime, tasks, restarts and live counters they now show
  `type`, whether the unit is `enabled` (masked and disabled are flagged), its
  restart policy, the user and slice it runs in, the socket or timer that
  triggers it, whatever it reports of itself through `sd_notify`, the command
  it actually runs, and the unit file it came from.
- `MemoryMax` and a deliberately lowered `TasksMax` appear beside the current
  values, so `mem 76M/512M` says what the number is a fraction of. The default
  `TasksMax` — tens of thousands — stays hidden, as it means nothing.
- The log can be searched and filtered by level. `/` follows the focus — it
  filters the table, or searches the log when that is what you are reading —
  and `e` cycles everything → warning and above → error and above. Both are
  passed to journalctl as `-g` and `-p`, so they search the **whole journal**
  rather than the entries already fetched, and the follow stream and the
  backwards paging both honour them. An active filter is shown beside the unit
  name, so a filtered log is never mistaken for a quiet one.
- The log pages backwards. It still opens on the last 500 entries, but
  scrolling to the top now fetches the previous 500 and keeps going, so the
  buffer is a window onto the journal instead of all you can reach. The top
  line says which state you are in: loading, the genuine beginning of the
  unit's journal, or more available. Paging uses journald cursors, so pages
  join exactly — nothing duplicated or skipped.

### Changed

- **Colours are now the terminal's own sixteen**, as htop's are, instead of a
  fixed palette of hex values. unitop matches whatever theme you run rather
  than imposing one, and the light/dark handling it used to need is gone. Six
  hues carry meaning — green healthy, yellow watch, red wrong, cyan
  finished-or-rate, blue keys, magenta headings — and everything else is grey
  or dim. The selected row is black on cyan, as htop draws it.
- The footer keeps `q quit` at every width. It used to drop hints from the end,
  which took how-to-quit before anything else.
- The side pane grows its detail block on a tall terminal and shrinks it on a
  short one, giving ground to the log first.

## [0.1.3] — 2026-08-14

### Fixed

- Scrolling the log back down to the newest line resumes following. Scrolling
  up stopped following, as it should, but walking back down left it off — so
  the log sat frozen at the bottom while new entries piled up out of view. The
  mouse wheel already got this right; the keyboard did not. Both now share one
  rule: resting at the live end *is* following.
- The full view no longer offers or accepts keys that need a table. Sorting,
  reversing, tree, show-all, filter and pane focus (`s` `S` `r` `t` `a` `/`
  `tab`) silently did nothing there while still being listed in the footer.
- The action menu opens in a fixed place in the full view. It was anchored to
  the selected row's screen position, which is arbitrary once the table is
  hidden, so it appeared at a different height depending on where the invisible
  cursor sat.

## [0.1.2] — 2026-08-14

### Fixed

- The action menu draws as a popup instead of a curtain. It replaced each
  covered line from its left edge onward, so everything to the right of it
  vanished and half the table blanked out whenever the menu was open.
- The menu screenshot in the readme now actually shows the menu.

## [0.1.1] — 2026-08-14

### Changed

- unitop requires **systemd 247 or newer** on the machine it watches, and says
  so up front. Pointed at an older host it used to die with
  `unrecognized option '--timestamp=unix'`, which explains nothing. The version
  is read as part of the poll that was already happening, so this costs no
  extra round trip. The verdict is terminal: polling stops rather than retrying
  a host that can never work, and `R` still forces a retry.

### Fixed

- The readme's checksum step verified nothing. It had you save the binary as
  `unitop` while `SHA256SUMS` refers to `unitop-linux-amd64`, so
  `sha256sum -c --ignore-missing` skipped every entry and reported
  `no file was verified` — easy to read as success.
- The action menu no longer stretches across the screen for long template unit
  names; it is capped and the title truncated.
- The footer drops whole key hints on a narrow terminal instead of cutting one
  in half, which read as a rendering fault.

## [0.1.0] — 2026-08-14

First release.

- Sortable table of systemd services: CPU, memory, network and disk I/O rates,
  state, restart count, tasks and uptime. Rates are deltas between polls; a
  counter going backwards (a restart) reads as zero rather than a spike.
- Live journal tail for the selected unit, with priority colouring, wrapping
  and follow.
- `STATE` reports what actually happened rather than echoing systemd's
  `SubState`, which calls four different situations `dead`: `exited`, `dead`,
  `skipped`, `stopped`, and named failures such as `exit 1` or `sig KILL`.
- Tree view grouping units by slice, with each slice totalling everything
  beneath it.
- Unit actions — start, stop, restart, reload, kill, freeze, thaw,
  reset-failed — with confirmation for anything that interrupts a running
  service. Interactive polkit is never used; `-sudo` and `-read-only` are
  available.
- Full view: one unit's log at the full width with its live counters above it.
- Remote monitoring with `-H user@host` over a single multiplexed ssh
  connection; a poll is two round trips regardless of unit count.
- Mouse support: wheel scrolling, click-to-sort on column headers, right-click
  for the action menu.
- Static `linux/amd64` and `linux/arm64` binaries, a flake with an overlay, and
  nothing to configure.

[Unreleased]: https://github.com/sagar-chandarana/unitop-go/compare/v0.3.2...HEAD
[0.3.2]: https://github.com/sagar-chandarana/unitop-go/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/sagar-chandarana/unitop-go/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/sagar-chandarana/unitop-go/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.3...v0.2.0
[0.1.3]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/sagar-chandarana/unitop-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/sagar-chandarana/unitop-go/releases/tag/v0.1.0
