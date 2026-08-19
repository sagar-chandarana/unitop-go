# Rewrite a captured screen's 16-colour SGR codes into 24-bit truecolor using a
# named palette, so a screenshot shows the tool as it looks in a real theme
# rather than in whatever the renderer happens to ship.
#
#   tmux capture-pane -p -e | awk -f palette.awk -v theme=duskfox
#
# Left alone, the images come out in termshot's own palette, which is nobody's
# terminal, and it flatters or maligns colour choices at random: duskfox renders
# magenta as a light lavender close to its own foreground where termshot uses a
# dark purple, so a contrast problem that is glaring on the real theme is
# invisible in the picture of it.
BEGIN {
  # background, foreground, then the sixteen.
  split("232136 e0def4 393552 eb6f92 a3be8c f6c177 569fba c4a7e7 9ccfd8 e0def4 " \
        "47407d f083a2 b1d196 f9cb8c 65b1cd ccb1ed a6dae3 e2e0f7", dusk, " ")
  split("eff1f5 4c4f69 5c5f77 db0b38 47bd2d f8a93c 3779fa f6a3df 17afb8 acb0be " \
        "6c6f85 e3283f 53c546 ffba54 6486ff ffbcea 2fbac5 bcc0cc", latte, " ")
  if (theme == "latte") { for (i in latte) p[i] = latte[i] } else { for (i in dusk) p[i] = dusk[i] }
  bg = p[1]; fg = p[2]
  for (i = 0; i < 16; i++) c[i] = p[i + 3]
  dim = mix(fg, bg, 0.45)
  ground = "\033[48;2;" rgb(bg) "m\033[38;2;" rgb(fg) "m"
  # `-v want=bg` just reports the ground, for a caller that has to repaint
  # something the escape codes cannot reach.
  if (want != "") { print (want == "fg" ? fg : bg); exit }
}

function rgb(hex) {
  return strtonum("0x" substr(hex, 1, 2)) ";" strtonum("0x" substr(hex, 3, 2)) ";" \
         strtonum("0x" substr(hex, 5, 2))
}

# Faint (SGR 2) is a dimmed *foreground* in every real terminal, but termshot
# treats it as a background tint and paints blocks no user would ever see.
# Resolve it to an explicit colour partway to the ground instead.
function mix(a, b, t,   i, ca, cb, out) {
  out = ""
  for (i = 1; i <= 5; i += 2) {
    ca = strtonum("0x" substr(a, i, 2)); cb = strtonum("0x" substr(b, i, 2))
    out = out sprintf("%02x", int(ca + (cb - ca) * t))
  }
  return out
}

{
  line = $0
  for (i = 0; i < 8; i++) {
    gsub("\033\\[" (30 + i) "m", "\033[38;2;" rgb(c[i]) "m", line)
    gsub("\033\\[" (40 + i) "m", "\033[48;2;" rgb(c[i]) "m", line)
    gsub("\033\\[" (90 + i) "m", "\033[38;2;" rgb(c[i + 8]) "m", line)
    gsub("\033\\[" (100 + i) "m", "\033[48;2;" rgb(c[i + 8]) "m", line)
  }
  gsub("\033\\[2m", "\033[38;2;" rgb(dim) "m", line)
  gsub("\033\\[39m", "\033[38;2;" rgb(fg) "m", line)
  gsub("\033\\[49m", "\033[48;2;" rgb(bg) "m", line)

  # A reset drops the ground painted below, and everything after it on the line
  # falls back to the renderer's own near-black — which is where the banding
  # across the table came from. Re-assert the ground after every reset.
  out = ""
  while (match(line, /\033\[0(;[0-9;]*)?m/)) {
    out = out substr(line, 1, RSTART + RLENGTH - 1) ground
    line = substr(line, RSTART + RLENGTH)
  }

  # Paint the ground explicitly, so unstyled cells are the theme's background
  # and not the renderer's idea of black.
  print ground out line "\033[0m"
}
