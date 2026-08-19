---
name: verify-tui-with-pty
description: How to actually see unitop's output when you cannot run it interactively — script(1) plus a timeout, and how to read the result
type: feedback
---

`go test` proves the frame composes; it does not prove the thing looks right in
a terminal. To see real output when you cannot sit in front of it:

```bash
export TERM=xterm-256color
( sleep 8; printf 't'; sleep 4 ) |
  timeout -s KILL 14 script -q -c "stty cols 150 rows 26; ./result/bin/unitop" /dev/null > out.raw 2>&1

# strip ANSI to read it
sed -e 's/\x1b\[[0-9;?]*[a-zA-Z]//g' -e 's/\x1b[()][B0]//g' out.raw | tail -30
```

Pitfalls, all of which cost time at least once:

- **A pty is required.** `script -q -c "…" /dev/null` provides one; a plain pipe
  gives no useful output at all. `stty cols/rows` inside the `-c` string sets the
  size, since `script` has no flag for it.
- **Always wrap in `timeout -s KILL`.** Sending `q` on stdin does not reliably
  quit it under `script`, and the command will otherwise hang until the tool
  timeout.
- **Allow ~5s before the first frame.** termenv queries the terminal for its
  background colour and waits for a reply the pty never sends. A 4-second budget
  captures nothing.
- **Captured output is overlapping partial frames, not screens.** bubbletea
  repaints only changed lines, so a static row (or the column header, or the
  rule under it) appears once near the start and never again. `tail` looking
  "wrong" is usually this, not a rendering bug — `grep` for the specific line
  instead of eyeballing the tail.
- Feed keystrokes with `( sleep N; printf 'x'; sleep M ) | …`; `\r` is enter.

**How to apply:** verify visually after any change to layout, colour or a key
binding, and prefer `grep -n` for the specific element you changed over reading
the raw capture.

## Regenerating the README screenshots

`nix run .#screenshots` does the whole job — tmux capture, hostname sanitising,
padding, termshot, pngquant, all four images — and brings its own tools. It
needs no readable journal: `docs/helpers/fake-journalctl.sh` invents one that
suits whichever unit is selected. Use it rather than rebuilding the pipeline.

Traps it exists to encode:

- **`charm-freeze` does not render ANSI backgrounds at all** — the selected row
  came out invisible. `termshot` does. (`nixpkgs#freeze` is a different package
  entirely, a shellcode loader by another author.)
- **tmux's `capture-pane -e` re-serialises minimally.** It sets the selection's
  background once and leaves it set across the cells that follow, which is
  correct ANSI, but termshot drops the background at the first `ESC[39m`. The
  script re-asserts it.
- **termshot crops to the longest line**, taking the right edge off the pane
  boxes. Pad every line to the full width first.
- **Sanitise before rendering.** Captures contain the real hostname. A longer
  replacement pushes the right-hand half of the host line off the edge, so take
  the difference back out of the run of spaces in the middle.
- Screenshots of the log pane need journal access, which a plain user does not
  have; without it the pane honestly shows journalctl's permission complaint,
  and the hero shot sells the tool short. Capture on a host where you are root.

Related: [[go-nix-workflow]], [[verification-hosts]]
