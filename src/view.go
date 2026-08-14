package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const paneGap = 3 // " │ "

type colDef struct {
	key   sortKey
	title string
	width int
	right bool
	drop  int // higher number is dropped first as the terminal narrows; 0 never drops
}

// Columns in display order. UNIT is flexible and never dropped.
var columns = []colDef{
	{key: sortName, title: "UNIT", width: 18, drop: 0},
	{key: sortState, title: "STATE", width: 9, drop: 1},
	{key: sortCPU, title: "CPU%", width: 6, right: true, drop: 2},
	{key: sortMem, title: "MEM", width: 7, right: true, drop: 3},
	{key: sortNetIn, title: "NET↓", width: 9, right: true, drop: 5},
	{key: sortNetOut, title: "NET↑", width: 9, right: true, drop: 6},
	{key: sortRestarts, title: "RST", width: 4, right: true, drop: 4},
	{key: sortUptime, title: "UP", width: 8, right: true, drop: 7},
	{key: sortTasks, title: "TASK", width: 5, right: true, drop: 8},
	{key: sortIORead, title: "IO↓", width: 8, right: true, drop: 9},
	{key: sortIOWrite, title: "IO↑", width: 8, right: true, drop: 10},
}

// ---------- geometry ----------

// headerLines is the height of the host block, including its divider. It is
// also the screen row of the column-title line.
func (m model) headerLines() int {
	if m.height < 20 || m.width < 76 {
		return 2 // one host line plus the divider
	}
	return 3
}

func (m model) contentHeight() int {
	return max(1, m.height-m.headerLines()-1)
}

// listRows is the number of unit rows on screen: content minus the column
// titles and the rule under them.
func (m model) listRows() int {
	return max(1, m.contentHeight()-2)
}

// detailHeight is the unit summary above the log: title, description, stats,
// and the rule under them.
func (m model) detailHeight() int { return 4 }

func (m model) logHeight() int {
	return max(1, m.contentHeight()-m.detailHeight())
}

func (m model) logPaneVisible() bool {
	if !m.showLogs {
		return false
	}
	if m.fullView {
		return true
	}
	return m.width >= 84
}

func (m model) logPaneWidth() int {
	if !m.logPaneVisible() {
		return 0
	}
	if m.fullView {
		return m.width
	}
	return max(36, m.width*42/100)
}

func (m model) tableWidth() int {
	if m.fullView && m.logPaneVisible() {
		return 0
	}
	if !m.logPaneVisible() {
		return m.width
	}
	return m.width - m.logPaneWidth() - paneGap
}

func (m model) logInnerWidth() int {
	return max(10, m.logPaneWidth())
}

func (m model) countDisplayLines(lines []logLine) int {
	w := m.logInnerWidth()
	n := 0
	for _, l := range lines {
		n += len(m.formatLog(l, w))
	}
	return n
}

// ---------- top level ----------

func (m model) View() string {
	if !m.ready && m.width == 0 {
		return "starting…"
	}
	if !m.connected {
		return strings.Join(m.viewStartup(), "\n")
	}
	lines := m.viewHost()
	if m.help {
		lines = append(lines, m.viewHelp()...)
	} else {
		lines = append(lines, m.viewBody()...)
	}
	lines = append(lines, m.viewFooter())

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	lines = lines[:m.height]
	if m.menu.open {
		lines = m.overlayMenu(lines)
	}
	return strings.Join(lines, "\n")
}

// ---------- startup ----------

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// viewStartup owns the screen until the first poll succeeds. Rendering the
// normal UI before then would show an empty table of a host we have not
// reached yet, with the reason buried in the footer.
func (m model) viewStartup() []string {
	var body []string
	target := m.hostLabel
	if m.r.host != "" {
		target = m.r.host
	}

	if m.err == "" {
		frame := string(spinnerFrames[m.spinner%len(spinnerFrames)])
		what := "reading systemd on " + target
		if m.r.host != "" {
			what = "connecting to " + target
		}
		body = append(body,
			lipgloss.NewStyle().Foreground(colMauve).Render(frame)+"  "+stBase.Render(what+"…"),
			"",
			stFaint.Render("q  quit"),
		)
	} else {
		head := "cannot read systemd on " + target
		if m.r.host != "" {
			head = "cannot reach " + target
		}
		body = append(body, stBad.Render("✗  "+head), "")
		for _, l := range wrapWords(m.err, min(72, max(20, m.width-6))) {
			body = append(body, lipgloss.NewStyle().Foreground(colRed).Render(l))
		}
		body = append(body, "", stColHead.Render("try:"))
		for _, s := range troubleshoot(m.err, m.r.host) {
			body = append(body, stSubtle.Render("  • ")+stBase.Render(s))
		}
		attempt := fmt.Sprintf("attempt %d · retrying every %gs", m.attempts, m.interval.Seconds())
		body = append(body, "", stFaint.Render(attempt+"    ")+stKey.Render("R")+stFaint.Render(" retry now    ")+
			stKey.Render("q")+stFaint.Render(" quit"))
	}

	// Centre the block, and left-align its lines with each other so the
	// wrapped error and the bullet list do not stagger.
	inner := 0
	for _, l := range body {
		inner = max(inner, lipgloss.Width(l))
	}
	left := max(0, (m.width-inner)/2)
	title := stHeader.Render("unitop")
	block := append([]string{title, ""}, body...)

	lines := make([]string, m.height)
	top := max(0, (m.height-len(block))/2)
	for i := range lines {
		if i < top || i-top >= len(block) {
			lines[i] = ""
			continue
		}
		lines[i] = strings.Repeat(" ", left) + block[i-top]
	}
	return lines
}

// troubleshoot turns a connection failure into the next thing to try. The
// strings systemd, ssh and the shell produce here are stable enough to match on.
func troubleshoot(err, host string) []string {
	e := strings.ToLower(err)
	ssh := host != ""
	target := host
	if target == "" {
		target = "this host"
	}

	switch {
	case strings.Contains(e, "permission denied"), strings.Contains(e, "publickey"):
		return []string{
			"unitop only does key auth (BatchMode); it never prompts for a password",
			"check it works by hand: ssh -o BatchMode=yes " + target + " true",
			"install your key if that fails: ssh-copy-id " + target,
		}
	case strings.Contains(e, "host key verification failed"):
		return []string{
			"the host key is unknown or changed, and BatchMode cannot prompt",
			"connect once by hand to review and accept it: ssh " + target,
		}
	case strings.Contains(e, "could not resolve"), strings.Contains(e, "name or service not known"),
		strings.Contains(e, "nodename nor servname"):
		return []string{
			"the name does not resolve — check the spelling, DNS, or your ssh config",
			"an IP works too: unitop -H root@192.0.2.10",
		}
	case strings.Contains(e, "connection timed out"), strings.Contains(e, "no route to host"),
		strings.Contains(e, "network is unreachable"):
		return []string{
			"the host is not answering on port 22 — check it is up and reachable",
			"a firewall or a VPN you are not on can look exactly like this",
		}
	case strings.Contains(e, "connection refused"):
		return []string{
			"port 22 is closed — sshd may not be running, or it listens elsewhere",
			"for a non-standard port, set it in ~/.ssh/config for this host",
		}
	case strings.Contains(e, "connection closed"), strings.Contains(e, "broken pipe"),
		strings.Contains(e, "kex_exchange"):
		return []string{
			"the connection dropped during setup — often rate limiting or a flaky link",
			"retry, and check the host's auth log if it keeps happening",
		}
	case strings.Contains(e, "command not found"), strings.Contains(e, "executable file not found"):
		if ssh && !strings.Contains(e, "ssh:") {
			return []string{
				"systemctl is missing on " + target + " — is it actually a systemd host?",
			}
		}
		return []string{
			"the ssh client is not installed, or not on PATH",
		}
	case strings.Contains(e, "no /proc"), strings.Contains(e, "list-units"):
		return []string{
			"systemd did not answer — check: systemctl list-units --type=service",
			"inside a container, the host's systemd is usually not reachable",
		}
	}

	if ssh {
		return []string{
			"reproduce it by hand: ssh " + target + " systemctl list-units --type=service",
			"unitop needs key auth and a systemd host at the other end",
		}
	}
	return []string{
		"reproduce it by hand: systemctl list-units --type=service",
	}
}

// hjoin puts left and right on one line of exactly m.width columns, right
// aligned, falling back to a plain truncation when they cannot both fit.
func hjoin(width int, left, right string) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncANSI(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---------- host block ----------

func (m model) viewHost() []string {
	sep := stFaint.Render(" · ")
	h := m.host

	var failed, active int
	for _, u := range m.units {
		if u.Failed() {
			failed++
		}
		if u.Active == "active" {
			active++
		}
	}

	name := stHeader.Render(m.hostLabel)
	if m.r.host != "" {
		name += stFaint.Render(" (ssh)")
	}

	// Machine identity and the numbers that describe its load.
	ident := []string{name}
	if h.OK {
		if h.NCPU > 0 {
			ident = append(ident, stSubtle.Render(fmt.Sprintf("%d cpu", h.NCPU)))
		}
		if h.Uptime > 0 {
			ident = append(ident, stSubtle.Render("up "+humanDur(h.Uptime)))
		}
		ident = append(ident, stSubtle.Render("load ")+
			lipgloss.NewStyle().Foreground(heat(h.LoadPct(), 20, 70, 100, 150)).
				Render(fmt.Sprintf("%.2f %.2f %.2f", h.Load[0], h.Load[1], h.Load[2])))
	}

	usage := []string{
		stSubtle.Render("cpu ") + lipgloss.NewStyle().Foreground(heat(h.CPUPct, 5, 40, 70, 90)).
			Render(fmt.Sprintf("%.0f%%", h.CPUPct)),
		stSubtle.Render("mem ") + lipgloss.NewStyle().Foreground(heat(h.MemPct(), 25, 60, 80, 92)).
			Render(humanBytes(h.MemUsed)) + stFaint.Render("/"+humanBytes(h.MemTotal)),
	}
	if h.SwapTotal > 0 && h.SwapUsed > 0 {
		usage = append(usage, stSubtle.Render("swap ")+
			lipgloss.NewStyle().Foreground(heat(h.SwapPct(), 1, 20, 50, 80)).Render(humanBytes(h.SwapUsed)))
	}
	usage = append(usage, stSubtle.Render("net ")+
		lipgloss.NewStyle().Foreground(colTeal).Render("↓"+humanRateFull(h.NetIn)+" ↑"+humanRateFull(h.NetOut)))

	units := fmt.Sprintf("%s%s%s units",
		stGood.Render(fmt.Sprint(active)), stFaint.Render("/"), stSubtle.Render(fmt.Sprint(len(m.units))))
	if failed > 0 {
		units += sep + stBad.Render(fmt.Sprintf("%d failed", failed))
	}

	arrow := "↓"
	if m.reverse {
		arrow = "↑"
	}
	mode := []string{stSubtle.Render("sort ") + stHeader.Render(m.sortBy.String()+arrow)}
	if m.tree {
		mode = append(mode, stAccent.Render("tree"))
	}
	if m.showAll {
		mode = append(mode, stAccent.Render("all"))
	}
	if m.filter != "" {
		mode = append(mode, stFilter.Render("/"+m.filter))
	}
	if m.paused {
		mode = append(mode, stWarn.Render("PAUSED"))
	} else {
		mode = append(mode, stSubtle.Render(fmt.Sprintf("%gs", m.interval.Seconds())))
	}

	rule := stBorder.Render(strings.Repeat("━", m.width))
	if m.headerLines() == 2 {
		one := strings.Join(append(ident, usage...), sep)
		return []string{hjoin(m.width, one, strings.Join(mode, sep)), rule}
	}
	return []string{
		hjoin(m.width, strings.Join(ident, sep), strings.Join(mode, sep)),
		hjoin(m.width, strings.Join(usage, sep), units),
		rule,
	}
}

// ---------- body ----------

func (m model) viewBody() []string {
	h := m.contentHeight()
	var left, right []string
	if tw := m.tableWidth(); tw > 0 {
		left = m.viewTable(tw, h)
	}
	if m.logPaneVisible() {
		right = m.viewLogPane(m.logPaneWidth(), h)
	}

	lines := make([]string, h)
	for i := 0; i < h; i++ {
		var row string
		if len(left) > 0 {
			cell := ""
			if i < len(left) {
				cell = left[i]
			}
			row = cell + strings.Repeat(" ", max(0, m.tableWidth()-lipgloss.Width(cell)))
		}
		if len(right) > 0 {
			if len(left) > 0 {
				if m.focus == focusLogs {
					row += " " + lipgloss.NewStyle().Foreground(colMauve).Render("┃") + " "
				} else {
					row += " " + stBorder.Render("│") + " "
				}
			}
			if i < len(right) {
				row += right[i]
			}
		}
		lines[i] = row
	}
	return lines
}

// layout drops the lowest-priority columns until the table fits, then hands
// the leftover width to UNIT.
func (m model) layout(width int) []colDef {
	cols := append([]colDef(nil), columns...)
	fits := func(cs []colDef) int {
		total := 0
		for _, c := range cs {
			total += c.width + 1
		}
		return total - 1
	}
	for fits(cols) > width && len(cols) > 1 {
		worst, wi := -1, -1
		for i, c := range cols {
			if c.drop > 0 && c.drop > worst {
				worst, wi = c.drop, i
			}
		}
		if wi < 0 {
			break
		}
		cols = append(cols[:wi], cols[wi+1:]...)
	}
	if slack := width - fits(cols); slack > 0 {
		cols[0].width += slack
	}
	return cols
}

func (m model) viewTable(width, height int) []string {
	cols := m.layout(width)
	out := make([]string, 0, height)

	var head []string
	for _, c := range cols {
		t := c.title
		if c.key == m.sortBy {
			if m.reverse {
				t += "↑"
			} else {
				t += "↓"
			}
		}
		cell := pad(t, c.width)
		if c.right {
			cell = padLeft(t, c.width)
		}
		if c.key == m.sortBy {
			head = append(head, stSortCol.Render(cell))
		} else {
			head = append(head, stColHead.Render(cell))
		}
	}
	out = append(out, strings.Join(head, " "))
	out = append(out, stBorder.Render(strings.Repeat("─", width)))

	if len(m.rows) == 0 {
		msg := "no matching units"
		if m.err != "" {
			msg = "no data"
		}
		return append(out, stSubtle.Render(msg))
	}

	end := min(m.topRow+height-2, len(m.rows))
	for i := m.topRow; i < end; i++ {
		out = append(out, m.viewRow(m.rows[i], cols, i == m.cursor))
	}
	return out
}

func (m model) viewRow(r row, cols []colDef, selected bool) string {
	cells := make([]string, 0, len(cols))
	for ci, c := range cols {
		text, style := cellFor(r, c, ci)
		s := pad(text, c.width)
		if c.right {
			s = padLeft(text, c.width)
		}
		if selected {
			style = style.Background(colSelBg)
		}
		cells = append(cells, style.Render(s))
	}
	sep := " "
	if selected {
		sep = lipgloss.NewStyle().Background(colSelBg).Render(" ")
	}
	return strings.Join(cells, sep)
}

// sliceLabel shortens system.slice to "system", keeps the root readable, and
// undoes systemd's \xNN escaping so my\x2dapp reads as my-app.
func sliceLabel(name string) string {
	if name == "-.slice" {
		return "/"
	}
	return unescapeUnit(strings.TrimSuffix(name, ".slice"))
}

// unescapeUnit decodes the \xNN escapes systemd puts in unit and slice names.
func unescapeUnit(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+3 < len(s) && s[i] == '\\' && s[i+1] == 'x' {
			if n, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func cellFor(r row, c colDef, idx int) (string, lipgloss.Style) {
	u := r.unit
	indent := strings.Repeat("  ", r.depth)

	if idx == 0 { // UNIT
		if r.kind == rowSlice {
			twisty := "▾ "
			if !r.expanded {
				twisty = "▸ "
			}
			st := lipgloss.NewStyle().Foreground(colMauve).Bold(true)
			if r.nFailed > 0 {
				st = st.Foreground(colRed)
			}
			return indent + twisty + sliceLabel(r.slice), st
		}
		st := lipgloss.NewStyle().Foreground(colText)
		if u.Failed() {
			st = st.Foreground(colRed).Bold(true)
		} else if u.Active == "inactive" {
			st = st.Foreground(colSubtle)
		}
		return indent + shortUnit(u.Name), st
	}

	if r.kind == rowSlice && c.title == "STATE" {
		if r.nFailed > 0 {
			return fmt.Sprintf("%d fail", r.nFailed), lipgloss.NewStyle().Foreground(colRed).Bold(true)
		}
		return fmt.Sprintf("%d unit", r.nUnits), stSubtle
	}

	switch c.title {
	case "STATE":
		return u.StateLabel(), lipgloss.NewStyle().Foreground(stateColor(u))

	case "CPU%":
		if u.CPUNSec == unsetU64 || !u.HasRates {
			return "-", stFaint
		}
		return fmt.Sprintf("%.1f", u.CPUPct), lipgloss.NewStyle().Foreground(heat(u.CPUPct, 1, 20, 60, 150))

	case "MEM":
		if u.MemCurrent == unsetU64 {
			return "-", stFaint
		}
		mb := float64(u.MemCurrent) / (1024 * 1024)
		return humanBytes(u.MemCurrent), lipgloss.NewStyle().Foreground(heat(mb, 16, 128, 512, 2048))

	case "NET↓", "NET↑":
		if !u.IPAccount {
			return "-", stFaint
		}
		v := u.NetInRate
		if c.title == "NET↑" {
			v = u.NetOutRate
		}
		if !u.HasRates {
			return "·", stFaint
		}
		return humanRate(v), lipgloss.NewStyle().Foreground(heat(v, 1024, 64*1024, 1024*1024, 16*1024*1024))

	case "IO↓", "IO↑":
		v, raw := u.IORRate, u.IORead
		if c.title == "IO↑" {
			v, raw = u.IOWRate, u.IOWrite
		}
		if raw == unsetU64 {
			return "-", stFaint
		}
		if !u.HasRates {
			return "·", stFaint
		}
		return humanRate(v), lipgloss.NewStyle().Foreground(heat(v, 1024, 256*1024, 4*1024*1024, 64*1024*1024))

	case "RST":
		if u.NRestarts == unsetU64 {
			return "-", stFaint
		}
		st := stFaint
		switch {
		case u.NRestarts >= 5:
			st = lipgloss.NewStyle().Foreground(colRed).Bold(true)
		case u.NRestarts > 0:
			st = lipgloss.NewStyle().Foreground(colYellow)
		}
		return fmt.Sprint(u.NRestarts), st

	case "UP":
		if u.ActiveSince.IsZero() || (r.kind == rowUnit && u.Active != "active") {
			return "-", stFaint
		}
		d := time.Since(u.ActiveSince)
		st := stSubtle
		if d < 2*time.Minute {
			st = lipgloss.NewStyle().Foreground(colPeach)
		}
		return humanDur(d), st

	case "TASK":
		if u.Tasks == unsetU64 {
			return "-", stFaint
		}
		return humanCount(u.Tasks), stSubtle
	}
	return "", stBase
}

// ---------- log pane ----------

func (m model) viewLogPane(width, height int) []string {
	out := make([]string, 0, height)
	r, haveRow := m.selectedRow()
	u, ok := m.selectedUnit()

	if !ok {
		head := "no unit selected"
		if haveRow && r.kind == rowSlice {
			head = sliceLabel(r.slice) + " — a slice has no journal of its own"
		}
		out = append(out, stSubtle.Render(truncRunes(head, width)))
		for len(out) < m.detailHeight()-1 {
			out = append(out, "")
		}
		return append(out, stBorder.Render(strings.Repeat("─", width)))
	}

	if m.fullView {
		out = append(out, m.viewUnitFull(u, width)...)
	} else {
		out = append(out, m.viewUnitSummary(u, width)...)
	}
	out = append(out, stBorder.Render(strings.Repeat("─", width)))
	return append(out, m.renderLogWindow(width, m.logHeight())...)
}

// viewUnitFull is the full-view header. It has the width to put the identity
// on the left, the lifecycle facts on the right, and a line of live counters
// underneath — the same numbers the table shows, for the unit whose log has
// taken over the screen.
func (m model) viewUnitFull(u Unit, width int) []string {
	title := lipgloss.NewStyle().Foreground(stateColor(u)).Bold(true).Render(shortUnit(u.Name))
	title += "  " + stateText(u)

	return []string{
		hjoin(width, title, stSubtle.Render(strings.Join(m.unitStats(u), " · "))),
		stSubtle.Render(truncRunes(u.Desc, width)),
		truncANSI(m.unitLive(u), width),
	}
}

// stateText names the state and keeps systemd's own wording alongside when the
// two differ — "exited (inactive/dead)" — so the friendlier label never hides
// what systemctl would tell you.
func stateText(u Unit) string {
	label := u.StateLabel()
	s := lipgloss.NewStyle().Foreground(stateColor(u)).Render(label)
	if label == u.Sub {
		return s
	}
	raw := u.Active + "/" + u.Sub
	if u.Active == u.Sub {
		raw = u.Active
	}
	return s + stFaint.Render(" ("+raw+")")
}

// unitLive is the current CPU/memory/network/disk of one unit, coloured on the
// same scales as the table columns.
func (m model) unitLive(u Unit) string {
	field := func(label, value string, c lipgloss.AdaptiveColor) string {
		return stSubtle.Render(label+" ") + lipgloss.NewStyle().Foreground(c).Render(value)
	}

	cpu, mem := "-", "-"
	if u.HasRates && u.CPUNSec != unsetU64 {
		cpu = fmt.Sprintf("%.1f%%", u.CPUPct) // same precision as the CPU% column
	}
	if u.MemCurrent != unsetU64 {
		mem = humanBytes(u.MemCurrent)
	}
	parts := []string{
		field("cpu", cpu, heat(u.CPUPct, 1, 20, 60, 150)),
		field("mem", mem, heat(float64(orZero(u.MemCurrent))/(1<<20), 16, 128, 512, 2048)),
	}
	if u.MemPeak != unsetU64 && u.MemPeak > 0 {
		parts = append(parts, stFaint.Render("peak "+humanBytes(u.MemPeak)))
	}
	if u.IPAccount {
		parts = append(parts, field("net",
			"↓"+humanRateFull(u.NetInRate)+" ↑"+humanRateFull(u.NetOutRate), colTeal))
	} else {
		parts = append(parts, stSubtle.Render("net ")+stFaint.Render("off"))
	}
	if u.IORead != unsetU64 {
		parts = append(parts, field("io",
			"↓"+humanRateFull(u.IORRate)+" ↑"+humanRateFull(u.IOWRate), colBlue))
	}
	return strings.Join(parts, stFaint.Render(" · "))
}

// viewUnitSummary is the three-line header above the log.
func (m model) viewUnitSummary(u Unit, width int) []string {
	title := lipgloss.NewStyle().Foreground(stateColor(u)).Bold(true).
		Render(truncRunes(shortUnit(u.Name), max(1, width-16)))
	title += "  " + stateText(u)
	return []string{
		truncANSI(title, width),
		stSubtle.Render(truncRunes(u.Desc, width)),
		truncANSI(stSubtle.Render(strings.Join(m.unitStats(u), " · ")), width),
	}
}

func (m model) unitStats(u Unit) []string {
	var stats []string
	if u.MainPID > 0 && u.MainPID != unsetU64 {
		stats = append(stats, fmt.Sprintf("pid %d", u.MainPID))
	}
	if u.NRestarts != unsetU64 {
		s := fmt.Sprintf("restarts %d", u.NRestarts)
		if u.NRestarts > 0 {
			s = stWarn.Render(s)
		}
		stats = append(stats, s)
	}
	if u.Result != "" && u.Result != "success" {
		stats = append(stats, stBad.Render("result "+u.Result))
	}
	if code, ok := u.ExitCode(); ok && code != 0 {
		stats = append(stats, stBad.Render(fmt.Sprintf("exit %d", code)))
	}
	if !u.ActiveSince.IsZero() && u.Active == "active" {
		stats = append(stats, "up "+humanDur(time.Since(u.ActiveSince)))
	}
	if u.Tasks != unsetU64 {
		stats = append(stats, fmt.Sprintf("tasks %d", u.Tasks))
	}
	// In the full view the live line already says "net off".
	if !u.IPAccount && !m.fullView {
		stats = append(stats, stFaint.Render("ip-accounting off"))
	}
	return stats
}

// renderLogWindow walks the buffer backwards so only the visible tail is ever
// wrapped, regardless of how many lines are retained.
func (m model) renderLogWindow(width, height int) []string {
	need := height + m.logScroll
	var acc []string
	for i := len(m.logs) - 1; i >= 0 && len(acc) < need; i-- {
		acc = append(m.formatLog(m.logs[i], width), acc...)
	}
	if len(acc) == 0 {
		if m.journal == nil {
			return []string{stSubtle.Render("no journal for this row")}
		}
		return []string{stSubtle.Render("waiting for journal…")}
	}
	end := min(len(acc), len(acc)-m.logScroll)
	if end < 0 {
		end = 0
	}
	win := acc[max(0, end-height):end]
	if m.logScroll > 0 && len(win) > 0 {
		marker := stWarn.Render(fmt.Sprintf("── paused, %d lines below (f or G to follow) ──", m.logScroll))
		win = append(append([]string(nil), win[:len(win)-1]...), truncANSI(marker, width))
	}
	return win
}

// formatLog renders one journal entry into the wrapped display lines it needs.
func (m model) formatLog(l logLine, width int) []string {
	prefix := l.ts.Format("15:04:05") + " "
	body := l.msg
	if l.meta {
		body = "⟨unitop⟩ " + l.msg
	} else if l.ident != "" {
		tag := l.ident
		if l.pid != "" {
			tag += "[" + l.pid + "]"
		}
		body = tag + ": " + l.msg
	}
	body = strings.ReplaceAll(body, "\t", "    ")

	avail := max(4, width-len(prefix))
	var segs []string
	if m.logWrap {
		segs = wrapWords(body, avail)
	} else {
		segs = []string{truncRunes(body, avail)}
	}

	style := lipgloss.NewStyle().Foreground(prioColor(l.prio))
	if l.prio <= 3 {
		style = style.Bold(true)
	}
	out := make([]string, 0, len(segs))
	for i, s := range segs {
		p := stFaint.Render(prefix)
		if i > 0 {
			p = strings.Repeat(" ", len(prefix))
		}
		out = append(out, p+style.Render(s))
	}
	return out
}

// ---------- context menu ----------

func (m model) overlayMenu(lines []string) []string {
	var box []string
	if m.menu.confirm {
		box = m.confirmBox()
	} else {
		box = m.menuBox()
	}
	x, y := m.menu.x, m.menu.y
	if m.menu.confirm {
		x = max(0, (m.width-lipgloss.Width(box[0]))/2)
		y = max(0, m.height/2-len(box)/2)
	}
	for i, bl := range box {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		prefix := truncANSI(lines[row], x)
		prefix += strings.Repeat(" ", max(0, x-lipgloss.Width(prefix)))
		lines[row] = prefix + bl
	}
	return lines
}

func (m model) menuBox() []string {
	w := menuWidth(m.menu.unit)
	border := lipgloss.NewStyle().Foreground(colMauve)
	out := []string{border.Render("╭") + stHeader.Render(pad(" "+truncRunes(shortUnit(m.menu.unit), w-3)+" ", w-2)) + border.Render("╮")}
	for i, a := range unitActions {
		label := pad(" "+a.label, w-2)
		st := stBase
		if a.confirm {
			st = lipgloss.NewStyle().Foreground(colPeach)
		}
		if i == m.menu.cursor {
			st = st.Background(colSelBg).Bold(true)
		}
		out = append(out, border.Render("│")+st.Render(label)+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

func (m model) confirmBox() []string {
	a := m.menu.action()
	text := fmt.Sprintf(" %s %s? ", a.label, shortUnit(m.menu.unit))
	hint := " y = yes, any other key = cancel "
	w := max(len([]rune(text)), len([]rune(hint))) + 2
	border := lipgloss.NewStyle().Foreground(colRed)
	return []string{
		border.Render("╭" + strings.Repeat("─", w-2) + "╮"),
		border.Render("│") + stBad.Render(pad(text, w-2)) + border.Render("│"),
		border.Render("│") + stSubtle.Render(pad(hint, w-2)) + border.Render("│"),
		border.Render("╰" + strings.Repeat("─", w-2) + "╯"),
	}
}

// ---------- footer & help ----------

func (m model) viewFooter() string {
	if m.filterInput {
		return stFilter.Render("/") + stBase.Render(m.filter) + lipgloss.NewStyle().Foreground(colMauve).Render("▏") +
			stFaint.Render("  enter=apply  esc=clear")
	}
	if m.toast != "" {
		st, mark := stGood, "✓ "
		if m.toastErr {
			st, mark = stBad, "! "
		}
		return truncANSI(st.Render(mark+m.toast), m.width)
	}
	if m.err != "" {
		return truncANSI(stBad.Render("! ")+lipgloss.NewStyle().Foreground(colRed).Render(m.err), m.width)
	}

	keys := [][2]string{
		{"↑↓", "move"}, {"enter", "full view"}, {"x", "actions"}, {"tab", "focus"},
		{"s", "sort"}, {"r", "rev"}, {"t", "tree"}, {"/", "filter"}, {"a", "all"},
		{"f", "follow"}, {"l", "log"}, {"?", "help"}, {"q", "quit"},
	}
	if m.fullView {
		keys[1] = [2]string{"enter/esc", "back"}
		// l does nothing here, so it is not offered.
		keys = slices.DeleteFunc(keys, func(k [2]string) bool { return k[0] == "l" })
	}
	var parts []string
	for _, k := range keys {
		d := k[1]
		st := stFaint
		if k[0] == "f" && !m.logFollow {
			st = stWarn
			d = "follow off"
		}
		parts = append(parts, stKey.Render(k[0])+st.Render(" "+d))
	}
	return truncANSI(strings.Join(parts, stFaint.Render(" · ")), m.width)
}

func (m model) viewHelp() []string {
	rows := [][2]string{
		{"↑/k ↓/j", "move selection (or scroll logs when focused)"},
		{"pgup/pgdn", "page"},
		{"g / G", "top / bottom"},
		{"←/h →", "collapse / expand a slice in tree mode"},
		{"tab", "switch focus between the unit list and the log pane"},
		{"enter", "on a unit: full view (esc returns); on a slice: expand/collapse"},
		{"x", "start / stop / restart / kill the selected unit"},
		{"s / S", "sort by the next / previous visible column"},
		{"r", "reverse the sort"},
		{"click", "on a column header sorts by it; again reverses"},
		{"right-click", "on a unit opens start/stop/restart/kill"},
		{"t", "tree view, grouped by slice"},
		{"/", "filter by unit name or description (esc clears)"},
		{"a", "include inactive/dead units"},
		{"f", "follow the log (auto-scroll); scrolling up turns it off"},
		{"l", "toggle the log pane (no effect in the full view)"},
		{"w", "toggle log wrapping"},
		{"p", "pause polling"},
		{"R", "refresh now"},
		{"+ / -", "faster / slower refresh"},
		{"q", "quit"},
	}
	out := []string{stHeader.Render("unitop — keys"), ""}
	for _, r := range rows {
		out = append(out, "  "+stKey.Render(padLeft(r[0], 11))+"  "+stBase.Render(r[1]))
	}
	out = append(out, "",
		stSubtle.Render("  CPU%, NET and IO are rates between polls; MEM is the current cgroup total."),
		stSubtle.Render("  NET needs IPAccounting=yes on the unit (or DefaultIPAccounting=yes system-wide)."),
		stSubtle.Render("  Reading logs needs membership of systemd-journal, or root."),
		stSubtle.Render("  Unit actions need privilege: run as root, or pass -sudo for sudo -n."),
	)
	h := m.contentHeight()
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

// truncANSI cuts a styled string to w visible columns.
func truncANSI(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	visible, inEsc := 0, false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
		}
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if visible >= w {
			break
		}
		b.WriteRune(r)
		visible++
	}
	return b.String() + "\x1b[0m"
}
