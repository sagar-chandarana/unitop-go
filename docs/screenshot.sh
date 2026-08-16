#!/usr/bin/env bash
# Render the readme's screenshots: run unitop in tmux, capture the styled
# screen, swap this machine's hostname for a generic one, and hand the result
# to termshot.
#
#   docs/screenshot.sh <out.png> <cols> <rows> [unitop args…] -- [tmux keys…]
#
# Run it somewhere the journal is readable — as root, or as a member of
# systemd-journal — or the log pane comes out empty. The four in docs/ are:
#
#   docs/screenshot.sh docs/main.png 132 30 -s name -f systemd- -- Down Down Down
#   docs/screenshot.sh docs/full.png 132 30 -s cpu -- Down Enter
#   docs/screenshot.sh docs/tree.png 132 30 -t --
#   docs/screenshot.sh docs/menu.png 132 30 -s cpu -- Down x Down
#
# Needs: tmux, perl, awk, and nix (for termshot and pngquant).
set -euo pipefail

UNITOP=${UNITOP:-./unitop}
TMP=${TMPDIR:-/tmp}/unitop-shot
mkdir -p "$TMP"

OUT=$1 COLS=$2 ROWS=$3
shift 3
ARGS=()
while [ $# -gt 0 ]; do [ "$1" = "--" ] && { shift; break; }; ARGS+=("$1"); shift; done
KEYS=("$@")

tmux kill-session -t shot 2>/dev/null || true
tmux new-session -d -s shot -x "$COLS" -y "$ROWS" "$UNITOP ${ARGS[*]}"
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

nix run nixpkgs#termshot -- -f "$OUT" -C "$COLS" --raw-read "$TMP/padded.txt" >/dev/null
nix run nixpkgs#pngquant -- --force --skip-if-larger --output "$OUT" -- "$OUT" || true
echo "wrote $OUT"
