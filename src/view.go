package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// paneGap is what sits between the two panes contents: each pane's own border
// and the column of air inside it, twice over, plus a column between the boxes.
const paneGap = 9

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

// paneInner is the height inside a pane's box: the content area minus the
// framing lines above and below it.
func (m model) paneInner() int {
	return max(1, m.contentHeight()-2)
}

// listRows is the number of unit rows on screen: the pane's inside, minus the
// column titles and the rule under them.
func (m model) listRows() int {
	return max(1, m.paneInner()-2)
}

// detailLines is how much of the unit description fits above the log. The full
// view has the room for all of it; the side pane trades against the log and
// gives ground first on a short terminal.
func (m model) detailLines() int {
	if m.fullView {
		return 7
	}
	switch {
	case m.contentHeight() >= 24:
		return 6
	case m.contentHeight() >= 16:
		return 4
	default:
		return 3
	}
}

// detailHeight is the description block plus the rule under it.
func (m model) detailHeight() int { return m.detailLines() + 1 }

func (m model) logHeight() int {
	return max(1, m.paneInner()-m.detailHeight())
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
		return max(10, m.width-4) // the whole screen, less its own box
	}
	return max(36, m.width*42/100)
}

func (m model) tableWidth() int {
	if m.fullView && m.logPaneVisible() {
		return 0
	}
	if !m.logPaneVisible() {
		return max(10, m.width-4)
	}
	return max(10, m.width-m.logPaneWidth()-paneGap)
}

func (m model) logInnerWidth() int {
	return max(10, m.logPaneWidth())
}

func (m model) countDisplayLines(lines []logLine) int {
	w := m.logInnerWidth()
	n := 0
	for _, l := range lines {
		_, segs := m.logSegments(l, w)
		n += len(segs)
	}
	return n
}

// logTotals memoises the wrapped height of the whole buffer. Every scroll key
// needs it, and re-wrapping 20k retained entries per keypress made scrolling
// visibly slow. Keyed on the buffer's epoch so a trim-and-append that leaves
// the length unchanged still invalidates it.
type logTotals struct {
	epoch, n, width, total int
	wrap                   bool
}

// shifted adjusts the memo for lines added at the end and dropped from the
// front, rather than throwing it away. Invalidating on every batch meant
// re-wrapping the whole buffer each time one arrived — and the pane asks for
// the total on every frame.
//
// Both halves matter. Extending alone still recounted everything once the
// buffer reached maxLogLines, because from then on every batch also trims: at
// the cap a frame cost 34ms and 27MB, which is where a chatty service ends up
// and stays. Both ends of the buffer are known at the call site, so both can be
// accounted for.
//
// It refuses unless the memo describes exactly the buffer that changed; a page
// prepended at the top, or a change of width or wrapping, still falls back to a
// full recount.
func (c *logTotals) shifted(prevLen, newLen, delta, width, epoch int, wrap bool) bool {
	if c == nil || c.epoch != epoch || c.n != prevLen || c.width != width || c.wrap != wrap {
		return false
	}
	c.total += delta
	c.n = newLen
	return true
}

func (m model) logDisplayTotal() int {
	c := m.totals
	if c == nil {
		return m.countDisplayLines(m.logs)
	}
	// Keyed on both the epoch and the length: the epoch catches a trim-and-
	// append that leaves the length unchanged, the length catches anything that
	// edits the buffer without bumping the epoch.
	w, wrap, n := m.logInnerWidth(), m.logWrap, len(m.logs)
	if c.epoch != m.logEpoch || c.n != n || c.width != w || c.wrap != wrap {
		c.epoch, c.n, c.width, c.wrap = m.logEpoch, n, w, wrap
		c.total = m.countDisplayLines(m.logs)
	}
	return c.total
}

// ---------- top level ----------

// The smallest terminal unitop will draw on. Below this there is no useful
// layout to be had — at 30 columns the unit names alone do not fit, and at 8
// rows the table is two units deep — so it says so instead of rendering
// something misshapen and pretending.
const (
	minWidth  = 40
	minHeight = 10
)

func (m model) View() string {
	if !m.ready && m.width == 0 {
		return "starting…"
	}
	if m.width < minWidth || m.height < minHeight {
		return strings.Join(m.viewTooSmall(), "\n")
	}
	// The startup screen owns the whole terminal, but it goes through the same
	// tail as everything else — it is exactly the screen you see when something
	// is wrong, so it is the last one that should be allowed to break its own
	// layout with a long hostname or a long error.
	var lines []string
	if !m.connected {
		lines = m.viewStartup()
	} else {
		lines = m.viewHost()
		if m.help {
			lines = append(lines, m.viewHelp()...)
		} else {
			lines = append(lines, m.viewBody()...)
		}
		lines = append(lines, m.viewFooter())
	}

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	lines = lines[:m.height]
	if m.menu.open && m.connected {
		lines = m.overlayMenu(lines)
	}

	for i, l := range lines {
		// Nothing may be wider than the terminal. A line that overruns wraps,
		// and a wrapped line pushes every line below it down one — so a single
		// long string does not spoil itself, it spoils the whole screen. Each
		// composer is expected to fit its own line; this is the backstop for
		// when one does not, and it is cheap because it only cuts what is
		// already too long.
		lines[i] = truncANSI(l, m.width)

		// And start every styled line from a clean slate. Each line we compose
		// is already balanced, but the trailing reset does not always survive
		// the renderer, and one bold error line in the log pane then bleeds
		// bold into the row beneath it.
		if strings.Contains(lines[i], "\x1b[") {
			lines[i] = "\x1b[0m" + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// viewTooSmall replaces the whole UI on a terminal there is no laying out. It
// says what is wrong and what would fix it, in whatever room there is, and it
// is the one screen allowed to assume almost none: each line degrades to a
// shorter form and then to nothing rather than wrapping.
func (m model) viewTooSmall() []string {
	size := fmt.Sprintf("%d×%d", m.width, m.height)
	need := fmt.Sprintf("%d×%d", minWidth, minHeight)

	var body []string
	add := func(alts ...string) {
		for _, a := range alts {
			if lipgloss.Width(a) <= m.width {
				body = append(body, a)
				return
			}
		}
	}
	add(stBad.Render("terminal too small"), stBad.Render("too small"), stBad.Render("small"))
	add(stFaint.Render(size+", unitop needs "+need), stFaint.Render(size+" < "+need), stFaint.Render(need))
	add("", " ")
	add(stFaint.Render("resize the window, or q to quit"), stFaint.Render("resize, or q"), stKey.Render("q"))

	lines := make([]string, m.height)
	top := max(0, (m.height-len(body))/2)
	for i := range lines {
		if i < top || i-top >= len(body) {
			continue
		}
		l := body[i-top]
		lines[i] = strings.Repeat(" ", max(0, (m.width-lipgloss.Width(l))/2)) + l
	}
	return lines
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
		// The spinner and its two spaces are three cells the host name cannot
		// have; a long one is otherwise exactly what pushes this off the edge.
		body = append(body,
			stBase.Render(frame)+"  "+
				stBase.Render(truncRunes(what+"…", max(6, m.width-3))),
			"",
			stFaint.Render("q  quit"),
		)
	} else {
		// Reaching a host and finding it unusable is a different fact from not
		// reaching it; saying "cannot reach" for the former sends people off
		// debugging their network.
		head := "cannot read systemd on " + target
		switch {
		case m.fatal:
			head = target + " cannot be watched"
		case m.r.host != "":
			head = "cannot reach " + target
		}
		// Every part of this wraps to the terminal, not to a comfortable
		// width: a long hostname, a long ssh error and a long suggestion are
		// exactly what this screen exists to show, and it is the screen you are
		// looking at when something is already wrong.
		wrap := min(72, max(12, m.width-6))
		for i, l := range wrapWords("✗  "+head, wrap) {
			if i == 0 {
				body = append(body, stBad.Render(l))
				continue
			}
			body = append(body, stBad.Render("   "+l))
		}
		body = append(body, "")
		for _, l := range wrapWords(m.err, wrap) {
			body = append(body, lipgloss.NewStyle().Foreground(colRed).Render(l))
		}
		body = append(body, "", stColHead.Render("try:"))
		for _, s := range troubleshoot(m.err, m.r.host) {
			for i, l := range wrapWords(s, max(8, wrap-4)) {
				if i == 0 {
					body = append(body, stFaint.Render("  • ")+stBase.Render(l))
					continue
				}
				body = append(body, stBase.Render("    "+l))
			}
		}
		status := fmt.Sprintf("attempt %d · retrying every %gs", m.attempts, m.interval.Seconds())
		if m.fatal {
			status = "not retrying" // nothing about this will change on its own
		}
		keys := stKey.Render("R") + stFaint.Render(" retry now    ") +
			stKey.Render("q") + stFaint.Render(" quit")
		if lipgloss.Width(status)+4+lipgloss.Width(keys) <= wrap {
			body = append(body, "", stFaint.Render(status+"    ")+keys)
		} else {
			body = append(body, "", stFaint.Render(status), keys)
		}
	}

	// Centre the block, and left-align its lines with each other so the
	// wrapped error and the bullet list do not stagger. Everything above wraps
	// to a comfortable width; this is the floor, for a terminal so narrow that
	// even a key hint does not fit on one line.
	inner := 0
	for i, l := range body {
		body[i] = truncANSI(l, m.width)
		inner = max(inner, lipgloss.Width(body[i]))
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

func sshPrefix(ssh bool, target string) string {
	if ssh {
		return "ssh " + target + " "
	}
	return ""
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
	case strings.Contains(e, "too old"), strings.Contains(e, "unrecognized option"):
		return []string{
			fmt.Sprintf("unitop needs systemd %d or newer on the machine it watches", minSystemd),
			"check with: " + sshPrefix(ssh, target) + "systemctl --version",
		}
	case strings.Contains(e, "no systemd on"):
		return []string{
			"systemctl did not report a version — is systemd running there?",
			"inside a container, the host's systemd is usually not reachable",
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
			ident = append(ident, stFaint.Render(fmt.Sprintf("%d cpu", h.NCPU)))
		}
		if h.Uptime > 0 {
			ident = append(ident, stFaint.Render("up "+humanDur(h.Uptime)))
		}
		ident = append(ident, stFaint.Render("load ")+
			heat(h.LoadPct(), 20, 70, 100, 150).
				Render(fmt.Sprintf("%.2f %.2f %.2f", h.Load[0], h.Load[1], h.Load[2])))
	}

	usage := []string{
		stFaint.Render("cpu ") + heat(h.CPUPct, 5, 40, 70, 90).
			Render(fmt.Sprintf("%.0f%%", h.CPUPct)),
		stFaint.Render("mem ") + heat(h.MemPct(), 25, 60, 80, 92).
			Render(humanBytes(h.MemUsed)) + stFaint.Render("/"+humanBytes(h.MemTotal)),
	}
	if h.SwapTotal > 0 && h.SwapUsed > 0 {
		usage = append(usage, stFaint.Render("swap ")+
			heat(h.SwapPct(), 1, 20, 50, 80).Render(humanBytes(h.SwapUsed)))
	}
	usage = append(usage, stFaint.Render("net ")+
		lipgloss.NewStyle().Foreground(colCyan).Render("↓"+humanRateFull(h.NetIn)+" ↑"+humanRateFull(h.NetOut)))

	units := fmt.Sprintf("%s%s%s units",
		stGood.Render(fmt.Sprint(active)), stFaint.Render("/"), stFaint.Render(fmt.Sprint(len(m.units))))
	if failed > 0 {
		units += sep + stBad.Render(fmt.Sprintf("%d failed", failed))
	}

	arrow := "↓"
	if m.reverse {
		arrow = "↑"
	}
	mode := []string{stFaint.Render("sort ") + stHeader.Render(m.sortBy.String()+arrow)}
	if m.tree {
		mode = append(mode, stAccent.Render("tree"))
	}
	if m.showAll {
		mode = append(mode, stAccent.Render("all"))
	}
	if m.filter != "" {
		mode = append(mode, stFilter.Render("/"+m.filter))
	}
	switch {
	case m.fatal:
		// The tick suppresses itself while fatal, so the screen has stopped
		// updating. Say so, and name the key, because the footer is showing the
		// error instead of the hints.
		mode = append(mode, stBad.Render("NOT POLLING — R to retry"))
	case m.paused:
		mode = append(mode, stWarn.Render("PAUSED"))
	default:
		mode = append(mode, stFaint.Render(fmt.Sprintf("%gs", m.interval.Seconds())))
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
	inner := m.paneInner()
	logFocused := m.focus == focusLogs && m.logPaneVisible()

	var left, right []string
	if tw := m.tableWidth(); tw > 0 && !m.fullView {
		left = framed(m.viewTable(tw, inner), tw, inner, m.tableTitle(tw), !logFocused)
	}
	if m.logPaneVisible() {
		lw := m.logPaneWidth()
		right = framed(m.viewLogPane(lw, inner), lw, inner, m.logTitle(lw), logFocused)
	}

	lines := make([]string, inner+2)
	for i := range lines {
		var row string
		if len(left) > 0 {
			row = left[i]
			if len(right) > 0 {
				row += " " // the boxes do not touch
			}
		}
		if len(right) > 0 {
			row += right[i]
		}
		lines[i] = row
	}
	return lines
}

// framed draws a pane's box. Both panes are always boxed and always the same
// size, so focus moves without the layout shifting under it; the focused box is
// heavy and coloured and the other light and faint, which reads at a glance and
// still reads on a terminal with no colour. The title lives in the top edge,
// where it can also say what is filtering the pane.
func framed(body []string, w, h int, title string, focused bool) []string {
	tl, tr, bl, br, hz, edge := "╭", "╮", "╰", "╯", "─", "│"
	st := stBorder
	if focused {
		tl, tr, bl, br, hz, edge = "┏", "┓", "┗", "┛", "━", "┃"
		st = stBase
	}

	head, used := st.Render(tl+hz), 2
	if t := truncANSI(title, max(0, w-4)); t != "" {
		head += " " + t + " "
		used += lipgloss.Width(t) + 2
	}
	head += st.Render(strings.Repeat(hz, max(0, w-used+3)) + tr)

	out := make([]string, 0, h+2)
	out = append(out, head)
	for i := 0; i < h; i++ {
		cell := ""
		if i < len(body) {
			cell = truncANSI(body[i], w)
		}
		out = append(out, st.Render(edge)+" "+cell+
			strings.Repeat(" ", max(0, w-lipgloss.Width(cell)))+" "+st.Render(edge))
	}
	return append(out, st.Render(bl+strings.Repeat(hz, w+2)+br))
}

// tableTitle names the pane and says plainly what the filter is doing to it. A
// filtered table otherwise looks like a machine with very few services on it.
// The wording gives ground as the pane narrows rather than being cut off
// mid-sentence, which says less than the short form would have.
func (m model) tableTitle(width int) string {
	shown := 0
	for _, r := range m.rows {
		if r.kind == rowUnit {
			shown++
		}
	}
	head := stColHead.Render("units ") + stBase.Render(strconv.Itoa(shown))
	if shown != len(m.units) {
		head += stFaint.Render(" of " + strconv.Itoa(len(m.units)))
	}
	if m.filter == "" {
		return head
	}
	q := strconv.Quote(m.filter)
	return fitTitle(head, width, "name or description contains "+q, "matching "+q, "filtered")
}

// logTitle names the unit whose journal is on screen, and what has been left
// out of it.
func (m model) logTitle(width int) string {
	head := stColHead.Render("log")
	if u, ok := m.selectedUnit(); ok {
		head += " " + stBase.Render(shortUnit(u.Name))
	}
	if m.logFilt.empty() {
		return head
	}
	return fitTitle(head, width, m.logFilt.label(), "filtered")
}

// fitTitle appends the longest of the given descriptions that still fits in the
// pane's top edge, and the shortest if none of them do.
func fitTitle(head string, width int, alts ...string) string {
	for i, alt := range alts {
		t := head + stFaint.Render(" · ") + stFilter.Render(alt)
		if lipgloss.Width(t) <= width-4 || i == len(alts)-1 {
			return t
		}
	}
	return head
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
		return append(out, stFaint.Render(msg))
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
			// One uniform bar, as htop draws it. Keeping the per-cell colours
			// over a background would put grey text on a grey ground, and the
			// row already stands out by being highlighted at all.
			style = lipgloss.NewStyle().Foreground(colSelFg).Background(colSelBg)
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
// Decoding is exactly how a control byte could get back into a name that
// parseShow already cleaned, so the result goes through sanitize again.
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
	return sanitizeText(b.String())
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
			st := stHeader
			if r.nFailed > 0 {
				st = st.Foreground(colRed)
			}
			return indent + twisty + sliceLabel(r.slice), st
		}
		st := stBase
		if u.Failed() {
			st = st.Foreground(colRed).Bold(true)
		} else if u.Active == "inactive" {
			st = stFaint
		}
		return indent + shortUnit(u.Name), st
	}

	if r.kind == rowSlice && c.title == "STATE" {
		if r.nFailed > 0 {
			return fmt.Sprintf("%d fail", r.nFailed), lipgloss.NewStyle().Foreground(colRed).Bold(true)
		}
		return fmt.Sprintf("%d unit", r.nUnits), stFaint
	}

	switch c.title {
	case "STATE":
		return u.StateLabel(), stateStyle(u)

	case "CPU%":
		if u.CPUNSec == unsetU64 || !u.HasRates {
			return "-", stFaint
		}
		return fmt.Sprintf("%.1f", u.CPUPct), heat(u.CPUPct, 1, 20, 60, 150)

	case "MEM":
		if u.MemCurrent == unsetU64 {
			return "-", stFaint
		}
		mb := float64(u.MemCurrent) / (1024 * 1024)
		return humanBytes(u.MemCurrent), heat(mb, 16, 128, 512, 2048)

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
		return humanRate(v), heat(v, 1024, 64*1024, 1024*1024, 16*1024*1024)

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
		return humanRate(v), heat(v, 1024, 256*1024, 4*1024*1024, 64*1024*1024)

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
		st := stFaint
		if d < 2*time.Minute {
			st = lipgloss.NewStyle().Foreground(colYellow)
		}
		return humanDur(d), st

	case "TASK":
		if u.Tasks == unsetU64 {
			return "-", stFaint
		}
		return humanCount(u.Tasks), stFaint
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
		out = append(out, stFaint.Render(truncRunes(head, width)))
		for len(out) < m.detailHeight()-1 {
			out = append(out, "")
		}
		return append(out, stBorder.Render(strings.Repeat("─", width)))
	}

	detail := m.unitDetail(u, width)
	for len(detail) < m.detailLines() {
		detail = append(detail, "")
	}
	out = append(out, detail[:m.detailLines()]...)
	out = append(out, stBorder.Render(strings.Repeat("─", width)))
	return append(out, m.renderLogWindow(width, m.logHeight())...)
}

// unitDetail describes the selected service, most useful first, so a short
// pane can simply take the first few lines. The full view has room for all of
// it; the side pane takes what fits beside the table.
func (m model) unitDetail(u Unit, width int) []string {
	title := stateStyle(u).Bold(true).
		Render(truncRunes(shortUnit(u.Name), max(1, width/2)))
	title += "  " + stateText(u)

	// Wide enough, the lifecycle facts sit opposite the name; in a narrow pane
	// hjoin would drop them entirely, so they get a line of their own.
	stats := stFaint.Render(strings.Join(m.unitStats(u), " · "))
	lines := []string{truncANSI(title, width), stFaint.Render(truncRunes(u.Desc, width))}
	if width >= 90 {
		lines[0] = hjoin(width, title, stats)
	} else {
		lines = append(lines, truncANSI(stats, width))
	}
	lines = append(lines, truncANSI(m.unitLive(u), width))

	// How it is configured: enough to answer "will this come back on its own,
	// and does it start at boot".
	var cfg []string
	if u.Type != "" {
		cfg = append(cfg, stFaint.Render("type ")+stBase.Render(u.Type))
	}
	if u.FileState != "" {
		cfg = append(cfg, fileStateStyle(u.FileState).Render(u.FileState))
	}
	if u.RestartPol != "" && u.RestartPol != "no" {
		cfg = append(cfg, stFaint.Render("restart ")+stBase.Render(u.RestartPol))
	}
	if u.User != "" {
		cfg = append(cfg, stFaint.Render("user ")+stBase.Render(u.User))
	}
	if u.Slice != "" && u.Slice != "system.slice" {
		cfg = append(cfg, stFaint.Render("slice ")+stBase.Render(sliceLabel(u.Slice)))
	}
	if u.TriggeredBy != "" {
		by := strings.Fields(u.TriggeredBy)
		cfg = append(cfg, stFaint.Render("triggered by ")+stBase.Render(shortUnit(by[0])))
	}
	if len(cfg) > 0 {
		lines = append(lines, truncANSI(strings.Join(cfg, stFaint.Render(" · ")), width))
	}

	// What it actually runs, and what it says about itself.
	if u.StatusText != "" {
		lines = append(lines, truncANSI(stFaint.Render("status ")+
			lipgloss.NewStyle().Foreground(colCyan).Render(u.StatusText), width))
	}
	if u.ExecStart != "" {
		lines = append(lines, truncANSI(stFaint.Render("exec ")+stFaint.Render(u.ExecStart), width))
	}
	if u.Fragment != "" {
		lines = append(lines, truncANSI(stFaint.Render(u.Fragment), width))
	}
	return lines
}

// fileStateStyle flags the states worth noticing: a masked or disabled unit
// will not come back on its own.
func fileStateStyle(s string) lipgloss.Style {
	switch s {
	case "masked", "masked-runtime":
		return lipgloss.NewStyle().Foreground(colRed).Bold(true)
	case "disabled":
		return lipgloss.NewStyle().Foreground(colYellow)
	case "enabled", "enabled-runtime":
		return lipgloss.NewStyle().Foreground(colGreen)
	}
	return stFaint
}

// stateText names the state and keeps systemd's own wording alongside when the
// two differ — "exited (inactive/dead)" — so the friendlier label never hides
// what systemctl would tell you.
func stateText(u Unit) string {
	label := u.StateLabel()
	s := stateStyle(u).Render(label)
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
	field := func(label, value string, st lipgloss.Style) string {
		return stFaint.Render(label+" ") + st.Render(value)
	}

	cpu, mem := "-", "-"
	if u.HasRates && u.CPUNSec != unsetU64 {
		cpu = fmt.Sprintf("%.1f%%", u.CPUPct) // same precision as the CPU% column
	}
	if u.MemCurrent != unsetU64 {
		mem = humanBytes(u.MemCurrent)
		// A MemoryMax= turns the number into a fraction of something.
		if u.MemMax != unsetU64 {
			mem += "/" + humanBytes(u.MemMax)
		}
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
			"↓"+humanRateFull(u.NetInRate)+" ↑"+humanRateFull(u.NetOutRate), stAccent))
	} else {
		parts = append(parts, stFaint.Render("net ")+stFaint.Render("off"))
	}
	if u.IORead != unsetU64 {
		parts = append(parts, field("io",
			"↓"+humanRateFull(u.IORRate)+" ↑"+humanRateFull(u.IOWRate), stKey))
	}
	return strings.Join(parts, stFaint.Render(" · "))
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
		tasks := fmt.Sprintf("tasks %d", u.Tasks)
		// The default TasksMax is in the tens of thousands and tells you
		// nothing. Show it only when it was deliberately lowered, or when the
		// unit is close enough to it to matter.
		if lim := u.TasksLimit; lim != unsetU64 && lim > 0 &&
			(lim < 4096 || u.Tasks*5 >= lim*4) {
			tasks += "/" + humanCount(lim)
		}
		stats = append(stats, tasks)
	}
	// In the full view the live line already says "net off".
	if !u.IPAccount && !m.fullView {
		stats = append(stats, stFaint.Render("ip-accounting off"))
	}
	return stats
}

// renderLogWindow walks the buffer backwards so only the visible tail is ever
// wrapped, regardless of how many lines are retained.
// renderLogWindow draws the slice of the buffer that is on screen: the `height`
// display lines ending `logScroll` lines above the newest.
//
// It walks back from the newest entry, measuring each without rendering it, and
// styles only the entries the window actually touches. The obvious version —
// format each entry and prepend its lines to an accumulator until there are
// enough — is quadratic, because every prepend copies everything built so far,
// and it renders every line between the window and the bottom whether it is
// visible or not. Scrolled ten thousand lines back in a full buffer, one frame
// took 129ms and allocated 430MB.
func (m model) renderLogWindow(width, height int) []string {
	if len(m.logs) == 0 {
		return m.emptyLogNotice()
	}

	skip := m.logScroll // display lines below the window, not drawn
	win := make([]string, 0, height)
	for i := len(m.logs) - 1; i >= 0 && len(win) < height; i-- {
		_, segs := m.logSegments(m.logs[i], width)
		if skip >= len(segs) {
			skip -= len(segs) // this entry is entirely below the window
			continue
		}
		lines := m.formatLog(m.logs[i], width)
		if skip > 0 { // it straddles the bottom edge
			lines = lines[:len(lines)-skip]
			skip = 0
		}
		for j := len(lines) - 1; j >= 0 && len(win) < height; j-- {
			win = append(win, lines[j]) // newest first for now
		}
	}
	for l, r := 0, len(win)-1; l < r; l, r = l+1, r-1 {
		win[l], win[r] = win[r], win[l]
	}
	if len(win) == 0 {
		return m.emptyLogNotice()
	}

	// The bottom line says how far behind the live end you are.
	if m.logScroll > 0 {
		marker := stWarn.Render(fmt.Sprintf("── paused, %d lines below (f or end to follow) ──", m.logScroll))
		win[len(win)-1] = truncANSI(marker, width)
	}
	// The top line says where this buffer came from, so its first entry is
	// never mistaken for the start of the journal.
	if m.atTopOfLog() {
		win[0] = truncANSI(m.logTopMarker(width), width)
	}
	return win
}

// emptyLogNotice explains an empty pane. "waiting for journal…" was the only
// answer, and it was usually a lie: journalctl -f produces nothing at all when
// the filter matches nothing, so the pane sat there apparently stuck when in
// fact the search had already finished and come up empty.
func (m model) emptyLogNotice() []string {
	switch {
	case m.journal == nil:
		return []string{stFaint.Render("no journal for this row")}

	case m.logStarting():
		// Only for the moment before the first entries land, so a slow remote
		// does not flash "nothing matches" and then fill in.
		frame := string(spinnerFrames[m.spinner%len(spinnerFrames)])
		return []string{stBase.Render(frame) +
			stFaint.Render(" reading the journal…")}

	case !m.logFilt.empty():
		return []string{
			stWarn.Render("no entries " + m.logFilt.label()),
			"",
			stFaint.Render("esc clears it · e changes the level · / searches for something else"),
		}
	}
	return []string{stFaint.Render("this unit has written nothing to the journal")}
}

// logStarting is true while the backlog is still being read. The stream says
// when that ends — the backlog is its own command, which terminates — so this
// is a fact, not a guess at how long a first batch might take.
func (m model) logStarting() bool {
	return m.journal != nil && !m.logBacklogDone
}

// logTopMarker reports the state of the backwards paging at the top of the
// buffer: loading, failed, exhausted, or more available.
func (m model) logTopMarker(width int) string {
	switch {
	case m.loadingOlder:
		frame := string(spinnerFrames[m.spinner%len(spinnerFrames)])
		return stBase.Render(frame) +
			stWarn.Render(" loading earlier entries…")
	case m.logLoadErr != "":
		return stBad.Render("── could not load earlier entries: ") +
			lipgloss.NewStyle().Foreground(colRed).Render(truncRunes(m.logLoadErr, max(10, width-40)))
	case m.logAtStart:
		return stFaint.Render("── beginning of this unit's journal ──")
	case m.logBufferFull():
		return stWarn.Render(fmt.Sprintf(
			"── %d lines held, the most unitop keeps; use journalctl for more ──", len(m.logs)))
	default:
		return stFaint.Render("── earlier entries exist; keep scrolling to load ──")
	}
}

// logSegments splits one journal entry into the display lines it occupies,
// unstyled, and returns the timestamp column that goes in front of the first.
//
// It is separate from formatLog because counting is not rendering. The pane's
// scroll arithmetic needs the *height* of every line in the buffer — all 20k of
// them, recomputed whenever the buffer changes — while only the ~30 on screen
// need to be styled. Going through formatLog for the count built a lipgloss
// style and rendered every segment of every buffered entry just to take len()
// of the result, and threw the strings away: three quarters of unitop's CPU,
// with a chatty service selected.
func (m model) logSegments(l logLine, width int) (prefix string, segs []string) {
	prefix = l.ts.Format("15:04:05") + " "
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
	// A journal entry can be several lines — a stack trace, a boot log read off
	// a serial console. sanitizeMessage keeps those breaks; honour them here
	// rather than emitting a newline into the middle of a rendered row, which
	// puts the rest of the pane wherever the terminal's cursor lands.
	avail := max(4, width-len(prefix))
	for _, para := range strings.Split(body, "\n") {
		if m.logWrap {
			segs = append(segs, wrapWords(para, avail)...)
		} else {
			segs = append(segs, truncRunes(para, avail))
		}
	}
	return prefix, segs
}

// formatLog renders one journal entry into the wrapped display lines it needs.
func (m model) formatLog(l logLine, width int) []string {
	prefix, segs := m.logSegments(l, width)

	style := prioStyle(l.prio)
	if l.prio <= 3 {
		style = style.Bold(true)
	}
	faded := stFaint.Render(prefix)
	blank := strings.Repeat(" ", len(prefix))
	out := make([]string, 0, len(segs))
	for i, s := range segs {
		p := faded
		if i > 0 {
			p = blank
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
	// The popup covers its own width and no more: the table keeps rendering on
	// both sides of it, as a popup should.
	boxW := 0
	for _, bl := range box {
		boxW = max(boxW, lipgloss.Width(bl))
	}
	for i, bl := range box {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		prefix := truncANSI(lines[row], x)
		prefix += strings.Repeat(" ", max(0, x-lipgloss.Width(prefix)))
		lines[row] = prefix + bl + sliceANSI(lines[row], x+boxW)
	}
	return lines
}

// sliceANSI returns the part of s from visible column `from` onward, carrying
// the styling that was in effect there so the tail keeps its colours.
func sliceANSI(s string, from int) string {
	var style, out strings.Builder
	var esc strings.Builder
	visible, inEsc := 0, false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			esc.Reset()
			esc.WriteRune(r)
			continue
		}
		if inEsc {
			esc.WriteRune(r)
			if r == 'm' {
				inEsc = false
				// Sequences before the cut set the state we must restore;
				// after it they belong to the output verbatim.
				if visible < from {
					style.WriteString(esc.String())
				} else {
					out.WriteString(esc.String())
				}
			}
			continue
		}
		if visible >= from {
			out.WriteRune(r)
		}
		// Cells, not runes. Counting runes put the cut in the wrong place on any
		// row with a double-width name — the overlay is positioned in cells, so
		// the tail resumed from a column that was not the one it covered.
		visible += ansi.StringWidth(string(r))
	}
	if strings.TrimSpace(stripSGR(out.String())) == "" {
		return "" // nothing but padding out there
	}
	return style.String() + out.String()
}

func stripSGR(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (m model) menuBox() []string {
	// Never wider than the terminal: a box cut off at the right edge reads as a
	// broken frame rather than a popup.
	w := m.menuBoxWidth()
	border := stBase
	out := []string{border.Render("╭") + stHeader.Render(pad(" "+truncRunes(shortUnit(m.menu.unit), w-3)+" ", w-2)) + border.Render("╮")}
	for i, a := range unitActions {
		label := pad(" "+a.label, w-2)
		st := stBase
		if a.confirm {
			st = lipgloss.NewStyle().Foreground(colYellow)
		}
		if i == m.menu.cursor {
			st = lipgloss.NewStyle().Foreground(colSelFg).Background(colSelBg).Bold(true)
		}
		out = append(out, border.Render("│")+st.Render(label)+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

func (m model) confirmBox() []string {
	a := m.menu.action()
	text := fmt.Sprintf(" %s %s? ", a.label, shortUnit(m.menu.unit))
	hint := " y = yes, any other key = cancel "
	w := min(max(ansi.StringWidth(text), ansi.StringWidth(hint))+2, max(8, m.width-2))
	border := lipgloss.NewStyle().Foreground(colRed)
	return []string{
		border.Render("╭" + strings.Repeat("─", w-2) + "╮"),
		border.Render("│") + stBad.Render(pad(text, w-2)) + border.Render("│"),
		border.Render("│") + stFaint.Render(pad(hint, w-2)) + border.Render("│"),
		border.Render("╰" + strings.Repeat("─", w-2) + "╯"),
	}
}

// ---------- footer & help ----------

// footerKeys lists what works from where the focus is, and nothing else. The
// keys that belong to the other pane are inert (see keyApplies), so offering
// them would be a lie about what the next keystroke does.
func (m model) footerKeys() [][2]string {
	// The help covers both panes, so their keys are not what the next press
	// does — the motion keys scroll the help instead.
	if m.help {
		keys := [][2]string{}
		if m.helpScrollMax() > 0 {
			keys = append(keys, [2]string{"↑↓", "scroll"})
		}
		return append(keys, [2]string{"?/esc", "close"}, [2]string{"q", "quit"})
	}

	keys := [][2]string{{"↑↓", "move"}}
	if m.fullView {
		keys = append(keys, [2]string{"enter/esc", "back"})
	} else {
		keys = append(keys, [2]string{"enter", "full view"})
	}
	keys = append(keys, [2]string{"x", "actions"})

	if m.focus == focusLogs && m.logPaneVisible() {
		keys = append(keys,
			[2]string{"/", "search log"},
			[2]string{"F/f", "top/bottom"},
			[2]string{"e", "level"},
			[2]string{"f", "follow"},
			[2]string{"w", "wrap"})
	} else {
		keys = append(keys,
			[2]string{"/", "filter units"},
			[2]string{"s", "sort"},
			[2]string{"r", "rev"},
			[2]string{"t", "tree"},
			[2]string{"a", "all"})
	}

	if !m.fullView {
		if m.logPaneVisible() {
			keys = append(keys, [2]string{"tab", "focus"})
		}
		keys = append(keys, [2]string{"l", "log"})
	}
	return append(keys, [2]string{"?", "help"}, [2]string{"q", "quit"})
}

func (m model) viewFooter() string {
	if m.filterInput {
		// Say what the text will do, not which pane it belongs to: the two
		// filters do genuinely different things and neither is guessable. But
		// what you are typing matters more than the explanation of it, so the
		// wording gives ground first, then the hint, and the text itself is the
		// last thing to go.
		long, short, text := "show units whose name or description contains", "filter units:", m.filter
		if m.filterLogs {
			long, short, text = "show journal lines matching", "search log:", m.logFilt.grep
		}
		caret := stBase.Render("▏")
		typed := stFilter.Render(text)
		for _, v := range []struct {
			label string
			hint  bool
		}{{long, true}, {short, true}, {short, false}, {"", false}} {
			line := typed + caret
			if v.label != "" {
				line = stFaint.Render(v.label+" ") + line
			}
			if v.hint {
				line += stFaint.Render("  enter apply · esc clear")
			}
			if lipgloss.Width(line) <= m.width {
				return line
			}
		}
		// Narrower than the text itself: keep the end of it, which is where the
		// cursor is and what was just typed.
		return stFilter.Render(tailCells(text, max(1, m.width-1))) + caret
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

	keys := m.footerKeys()
	// Fit whole hints, dropping the ones that do not fit. Cutting the line at
	// the width instead would leave a half-written key, which reads as a
	// rendering fault rather than a narrow terminal.
	//
	// "q quit" is rendered last but reserved first: dropping from the end would
	// take how-to-quit before anything else, which is the one hint that must
	// survive a narrow terminal.
	sep := stFaint.Render(" · ")
	render := func(k [2]string) string {
		d, st := k[1], stFaint
		if k[0] == "f" && !m.logFollow {
			st, d = stWarn, "follow off"
		}
		return stKey.Render(k[0]) + st.Render(" "+d)
	}

	quit := render([2]string{"q", "quit"})
	keys = slices.DeleteFunc(keys, func(k [2]string) bool { return k[0] == "q" })
	budget := m.width - lipgloss.Width(quit) - 3 // the separator before it

	var line string
	used := 0
	for _, k := range keys {
		hint := render(k)
		w := lipgloss.Width(hint)
		if line != "" {
			w += 3
		}
		if used+w > budget {
			break
		}
		if line != "" {
			line += sep
		}
		line += hint
		used += w
	}
	if line == "" {
		return quit
	}
	return line + sep + quit
}

// helpLines is the whole help, however tall that comes out. It groups the keys
// by the pane they belong to, because that is how they behave: the focused
// pane, drawn in a heavy box, is the one taking them.
func (m model) helpLines() []string {
	rows := [][2]string{
		{"", "— either pane —"},
		{"↑ ↓", "move the selection, or scroll the log when it has focus"},
		{"pgup/pgdn", "page"},
		{"home / end", "top / bottom"},
		{"tab", "move focus between the unit list and the log"},
		{"enter", "on a unit: full view; on a slice: expand/collapse"},
		{"x", "start / stop / restart / kill the selected unit"},
		{"esc", "step back one thing per press, innermost first:"},
		{"", "cancel what you are typing, close the menu or this help,"},
		{"", "clear the focused pane's filter, leave the full view, unfocus"},
		{"", ""},
		{"", "— the unit list —"},
		{"/", "show units whose name or description contains the text"},
		{"s / S", "sort by the next / previous visible column"},
		{"r", "reverse the sort"},
		{"t", "tree view, grouped by slice"},
		{"a", "include inactive/dead units"},
		{"← →", "collapse / expand a slice in tree mode"},
		{"click", "on a column header sorts by it; again reverses"},
		{"right-click", "on a unit opens start/stop/restart/kill"},
		{"", ""},
		{"", "— the log —"},
		{"/", "show journal lines matching the text (journalctl regex)"},
		{"e", "level: everything, warning and above, error and above"},
		{"F / f", "top / bottom — the only letters bound to motion"},
		{"f", "the bottom is the live end, so f follows; scrolling up stops it"},
		{"w", "wrap long lines"},
		{"", "scroll to the top to load the previous 500 entries"},
		{"", ""},
		{"", "— anywhere —"},
		{"l", "show or hide the log pane"},
		{"p", "pause polling"},
		{"R", "refresh now"},
		{"+ / -", "faster / slower refresh"},
		{"q", "quit"},
	}
	body := make([]string, 0, len(rows))
	for _, r := range rows {
		switch {
		case r[1] == "":
			body = append(body, "")
		case strings.HasPrefix(r[1], "—"):
			body = append(body, "  "+stHeader.Render(r[1]))
		default:
			body = append(body, "  "+stKey.Render(padLeft(r[0], 11))+"  "+stBase.Render(r[1]))
		}
	}
	// Prose, so it wraps rather than running off a narrow screen.
	notes := []string{""}
	for _, n := range []string{
		"A key belonging to the other pane does nothing; the footer lists only what applies.",
		"CPU%, NET and IO are rates between polls; MEM is the current cgroup total.",
		"NET needs IPAccounting=yes on the unit (or DefaultIPAccounting=yes system-wide).",
		"Reading logs needs membership of systemd-journal, or root.",
		"Unit actions need privilege: run as root, or pass -sudo for sudo -n.",
	} {
		for _, l := range wrapWords(n, max(8, m.width-4)) {
			notes = append(notes, stFaint.Render("  "+l))
		}
	}

	out := []string{stHeader.Render("unitop — keys"), ""}
	h := m.contentHeight()
	// Two columns on a wide screen, which is usually enough to fit the lot.
	// The key rows are written to a comfortable width rather than the terminal's,
	// so they are cut to it here — the key is on the left, so what goes is the
	// tail of the description.
	if len(out)+len(body) > h && m.width >= 110 {
		half := (len(body) + 1) / 2
		for i := 0; i < half; i++ {
			right := ""
			if j := i + half; j < len(body) {
				right = body[j]
			}
			out = append(out, sideBySide(m.width/2, body[i], right))
		}
	} else {
		out = append(out, body...)
	}
	return append(out, notes...)
}

// viewHelp windows the help onto the screen. On a terminal too small for all of
// it — 80x24 is not unusual — it scrolls rather than being cut off at the
// bottom, which took the last group with it, and the last group is where quit
// lives.
func (m model) viewHelp() []string {
	all := m.helpLines()
	h := m.contentHeight()
	for i, l := range all {
		all[i] = truncANSI(l, m.width)
	}
	if len(all) <= h {
		for len(all) < h {
			all = append(all, "")
		}
		return all[:h]
	}

	start := min(max(m.helpScroll, 0), len(all)-h)
	out := make([]string, 0, h)
	for _, l := range all[start : start+h] {
		out = append(out, truncANSI(l, m.width))
	}
	// Say which way there is more, on a line of its own at the edge it is at.
	// The hint about how to scroll goes first if it does not fit: which way
	// there is more is the part worth the space.
	if start > 0 {
		out[0] = stFaint.Render(truncRunes("  ↑ more above", m.width))
	}
	if start+h < len(all) {
		below := "  ↓ more below — ↑↓ or pgup/pgdn to scroll"
		if lipgloss.Width(below) > m.width {
			below = "  ↓ more below"
		}
		out[len(out)-1] = stFaint.Render(truncRunes(below, m.width))
	}
	return out
}

// helpScrollMax is how far the help can be scrolled, given the room it has.
func (m model) helpScrollMax() int {
	return max(0, len(m.helpLines())-m.contentHeight())
}

// sideBySide places b at column w. Both halves are cut to their own column:
// the help rows are written to fit, but one long enough to run past the screen
// would wrap, and a wrapped line pushes everything below it down a row.
func sideBySide(w int, a, b string) string {
	a = truncANSI(a, w-1)
	return a + strings.Repeat(" ", max(1, w-lipgloss.Width(a))) + truncANSI(b, w)
}

// truncANSI cuts a styled string to w visible columns.
func truncANSI(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "") + "\x1b[0m"
}
