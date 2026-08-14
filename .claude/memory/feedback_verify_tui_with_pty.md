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

For a *whole* frame rather than a diff stream, use tmux and capture the pane
state — `-e` keeps the colour:

```sh
tmux new-session -d -s shot -x 118 -y 24 './result/bin/unitop -no-logs'
sleep 7; tmux send-keys -t shot 't'; sleep 2      # optional keystrokes
tmux capture-pane -p -e -t shot > frame.ans; tmux kill-session -t shot
nix shell nixpkgs#charm-freeze --command freeze --language ansi \
  --theme charm --font.size 13 --line-height 1.15 --padding 16 --window \
  --output docs/tree.png frame.ans
nix shell nixpkgs#pngquant --command pngquant --quality 65-92 --force \
  --output docs/tree.png docs/tree.png          # ~1MB -> ~130KB
```

Traps:

- **`nixpkgs#freeze` is the wrong package** — it is a shellcode loader by a
  different author. The charmbracelet one is **`nixpkgs#charm-freeze`**.
- **Sanitise before rendering.** Captures contain the real hostname and unit
  names. Replace them in the `.ans` with **equal-length** strings or the column
  alignment shifts; check with
  `sed 's/\x1b\[[0-9;?]*[a-zA-Z]//g' f | awk '{print length($0)}'` before and
  after.
- Screenshots of the log pane need journal access, which a plain user does not
  have; without it the pane honestly shows journalctl's permission complaint.
  Capture those on a host where you are root, or use `-no-logs`.

Related: [[go-nix-workflow]], [[verification-hosts]]
