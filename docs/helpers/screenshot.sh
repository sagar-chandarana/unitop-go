#!/usr/bin/env bash
# Render the readme's screenshots: run unitop in tmux, capture the styled
# screen, swap this machine's hostname for a generic one, and hand the result
# to termshot.
#
#   docs/helpers/screenshot.sh <out.png> <cols> <rows> [unitop args…] -- [tmux keys…]
#
# The log pane needs a readable journal. Where there is not one — a sandbox, an
# unprivileged account — set FAKE_JOURNAL=1 and docs/helpers/fake-journalctl.sh serves an
# invented one, chosen to suit whichever unit is selected. The unit table and the
# host stats stay real. The four in docs/ are:
#
#   D="Down Down Down Down Down"
#   FAKE_JOURNAL=1 docs/helpers/screenshot.sh docs/main.png 132 30 -s name -f systemd- -- $D
#   FAKE_JOURNAL=1 docs/helpers/screenshot.sh docs/full.png 132 30 -s name -f systemd- -- $D Enter
#   FAKE_JOURNAL=1 docs/helpers/screenshot.sh docs/tree.png 132 30 -t -s cpu -- Down Down Down
#   FAKE_JOURNAL=1 docs/helpers/screenshot.sh docs/menu.png 132 30 -s name -f systemd- -- $D x Down
#
# Needs: tmux, perl, awk, and nix (for termshot and pngquant).
set -euo pipefail

UNITOP=${UNITOP:-./unitop}
HELPERS=$(cd "$(dirname "$0")" && pwd)
TMP=${TMPDIR:-/tmp}/unitop-shot
mkdir -p "$TMP"

OUT=$1 COLS=$2 ROWS=$3
shift 3
ARGS=()
while [ $# -gt 0 ]; do [ "$1" = "--" ] && { shift; break; }; ARGS+=("$1"); shift; done
KEYS=("$@")

# An invented journal, for taking these where the real one cannot be read.
if [ -n "${FAKE_JOURNAL:-}" ]; then
	mkdir -p "$TMP/bin"
	ln -sf "$HELPERS/fake-journalctl.sh" "$TMP/bin/journalctl"
	PATH="$TMP/bin:$PATH"
	export PATH
fi

tmux kill-session -t shot 2>/dev/null || true
# PATH is passed explicitly: the command runs under the tmux *server's*
# environment, not this shell's, so a fake-journalctl put on PATH here would
# otherwise be invisible — and silently, with the real journalctl's permission
# error in the pane instead of the invented log.
tmux new-session -d -s shot -x "$COLS" -y "$ROWS" "PATH=$PATH $UNITOP ${ARGS[*]}"
sleep 5
for k in "${KEYS[@]}"; do tmux send-keys -t shot "$k"; sleep 1.2; done
sleep 2
tmux capture-pane -p -e -t shot > "$TMP/raw.txt"
tmux kill-session -t shot

# The host line carries the real hostname. A longer stand-in would push the
# right-hand half off the edge, so take the difference back out of the widest
# run of spaces on that line.
HOST=$(hostname)
perl -i -pe '
  BEGIN { $h = shift; $w = shift }
  next unless $. == 1 && s/\Q$h\E/server1.local/;
  my $vis = $_; $vis =~ s/\e\[[0-9;]*m//g; chomp $vis;
  my $over = length($vis) - $w;
  s/( {10,})/" " x (length($1) - $over)/e if $over > 0;
' "$HOST" "$COLS" "$TMP/raw.txt"

# tmux re-serialises the screen minimally: it sets the selected row's background
# once and leaves it set across the cells that follow. That is correct ANSI, but
# termshot drops the background at the first ESC[39m, so re-assert it.
perl -i -pe '
  my $out = ""; my $bg = "";
  while (/\G(.*?)(\e\[([0-9;]*)m)/gcs) {
    my ($txt, $seq, $code) = ($1, $2, $3);
    $out .= $txt . $seq;
    $bg = "" if $code eq "0" || $code eq "49" || $code eq "";
    $bg = $seq if $code =~ /^(4[0-7]|10[0-7])$/;
    $out .= $bg if $code eq "39" && $bg ne "";
  }
  $out .= substr($_, pos($_) // 0);
  $_ = $out;
' "$TMP/raw.txt"

# termshot crops to the longest line, which would take the right edge off the
# boxes; pad every line to the full width instead.
awk -v w="$COLS" '{ v = $0; gsub(/\033\[[0-9;]*m/, "", v);
  printf "%s%*s\n", $0, (w - length(v) > 0 ? w - length(v) : 0), "" }' \
  "$TMP/raw.txt" > "$TMP/padded.txt"

# Repaint into a real theme if one was asked for. Left alone, the images
# come out in the renderer's own palette, which is nobody's terminal.
if [ -n "${THEME:-}" ]; then
	awk -f "$HELPERS/palette.awk" -v theme="$THEME" \
		"$TMP/padded.txt" > "$TMP/themed.txt" && mv "$TMP/themed.txt" "$TMP/padded.txt"
fi

# Prefer the tools on PATH — the nix app supplies them — and fall back to
# fetching them, so the script still works when run by hand.
run() { if command -v "$1" >/dev/null 2>&1; then "$@"; else
	local t=$1; shift; nix run "nixpkgs#$t" -- "$@"; fi; }

run termshot -f "$OUT" -C "$COLS" --raw-read "$TMP/padded.txt" >/dev/null

# termshot paints a line background only as tall as the glyphs, so the gaps
# between lines keep the window's own near-black and a themed image comes out
# striped. Repaint that colour to the theme's background; it is the window
# chrome too, so the window ends up the colour a real terminal would be.
if [ -n "${THEME:-}" ] && command -v magick >/dev/null 2>&1; then
	bg=$(awk -f "$HELPERS/palette.awk" -v theme="$THEME" -v want=bg </dev/null)
	magick "$OUT" -fuzz 4% -fill "#$bg" -opaque "#151515" "$OUT"
fi
run pngquant --force --skip-if-larger --output "$OUT" -- "$OUT" || true
echo "wrote $OUT"
