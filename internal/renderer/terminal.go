package renderer

import (
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TRNOG/rp/internal/config"
	"github.com/TRNOG/rp/internal/metrics"
)

// ANSI escape codes
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiWhite  = "\033[97m"
	ansiDim    = "\033[2m"

	clearLine  = "\033[2K"
	cursorHome = "\033[H"
	hideCursor = "\033[?25l"
	showCursor = "\033[?25h"

	// Alternate screen buffer — keeps the main terminal scrollback clean and
	// gives us a guaranteed fresh viewport to overwrite on every frame.
	altScreenEnter = "\033[?1049h"
	altScreenExit  = "\033[?1049l"
)

// Sparkline characters — 8 levels, lowest to highest bar
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Fixed column widths
const (
	colHop      = 4
	colIP       = 18
	colIP6      = 26 // compact IPv6
	colIP6Full  = 40 // full IPv6
	colHost     = 28
	colLoss     = 7
	colDiffLoss = 8
	colLast     = 8
	colAvg      = 8
	colDiffAvg  = 8
	colMin      = 8
	colMax      = 8
	colJitter   = 8
	colGraph    = 20
)

type TerminalRenderer struct {
	out         io.Writer
	cfg         *config.Config
	diffEnabled bool
	viewMode    string
	ipv6Format  string
	useIPv6     bool
}

// NewTerminalRenderer creates a TerminalRenderer writing to stdout.
// It enters the alternate screen buffer so the main scrollback is not
// polluted; Close() restores everything on exit.
func NewTerminalRenderer(cfg *config.Config) *TerminalRenderer {
	enableANSI()
	useIPv6 := cfg.UseIPv6 || cfg.IPv6Only
	r := &TerminalRenderer{out: os.Stdout, cfg: cfg, diffEnabled: cfg.DiffFile != "", viewMode: cfg.ViewMode, ipv6Format: cfg.IPv6Format, useIPv6: useIPv6}
	fmt.Fprint(r.out, altScreenEnter+hideCursor)
	return r
}

// Close exits the alternate screen and restores the cursor.
func (r *TerminalRenderer) Close() {
	fmt.Fprint(r.out, altScreenExit+showCursor)
}

// Render draws the full table, overwriting the previous frame in place.
// Because we're in the alternate screen buffer, cursor-home + overwrite is
// guaranteed to be clean — no scrollback pollution, no off-by-one on Windows.
func (r *TerminalRenderer) Render(panels []Panel) {
	var b strings.Builder

	// Jump to top-left of the alternate screen on every frame.
	b.WriteString(cursorHome)

	for idx, panel := range panels {
		if idx > 0 {
			b.WriteString(clearLine)
			b.WriteString(r.buildPanelDivider())
			b.WriteString("\r\n")
			b.WriteString(clearLine)
			b.WriteString("\r\n")
		}

		// ── Header ────────────────────────────────────────────────────────────
		for _, line := range r.buildHeader(panel.Title, panel.Summary, panel.RouteChanged, panel.Paused) {
			b.WriteString(clearLine)
			b.WriteString(line)
			b.WriteString("\r\n")
		}

		// ── Column titles ──────────────────────────────────────────────────────
		b.WriteString(clearLine)
		b.WriteString(r.buildColumnHeader())
		b.WriteString("\r\n")

		b.WriteString(clearLine)
		b.WriteString(r.buildSeparator())
		b.WriteString("\r\n")

		// ── Hop rows ───────────────────────────────────────────────────────────
		for _, snap := range panel.Snaps {
			if snap.TTL == 0 {
				continue
			}
			neverReplied := snap.IP == nil && snap.Recv == 0

			b.WriteString(clearLine)
			if neverReplied {
				b.WriteString(r.buildNoReplyRow(snap))
			} else {
				b.WriteString(r.buildHopRow(snap))
			}
			b.WriteString("\r\n")
		}

		// ── Footer ─────────────────────────────────────────────────────────────
		b.WriteString(clearLine)
		b.WriteString(r.buildFooter(panel.Snaps, panel.Summary, panel.Paused))
		b.WriteString("\r\n")
	}

	// Erase any leftover lines from a previously longer frame.
	b.WriteString("\033[J")

	fmt.Fprint(r.out, b.String())
}

// ── Private helpers ──────────────────────────────────────────────────────────

func (r *TerminalRenderer) buildHeader(title string, sum metrics.SessionSummary, routeChanged bool, paused bool) []string {
	header := r.color(ansiBold+ansiCyan, "rp") + " — " +
		r.color(ansiWhite, title) +
		"  │  uptime: " + r.color(ansiGreen, formatDuration(sum.Duration))

	if paused {
		header += "  " + r.color(ansiRed, "⏸ PAUSED")
	}
	if routeChanged {
		header += "  " + r.color(ansiYellow, "⚠ ROUTE CHANGED")
	}
	return []string{header, ""}
}

func (r *TerminalRenderer) buildColumnHeader() string {
	type col struct {
		w int
		s string
	}
	ipColWidth := colIP
	if r.useIPv6 {
		if r.ipv6Format == "full" {
			ipColWidth = colIP6Full
		} else {
			ipColWidth = colIP6
		}
	}
	cols := []col{
		{colHop, "Hop"}, {ipColWidth, " IP Address"}, {colHost, "Hostname"},
	}
	if r.showLoss() {
		cols = append(cols, col{colLoss, "Loss%"})
		if r.showDiffLoss() {
			cols = append(cols, col{colDiffLoss, "ΔLoss"})
		}
		if r.showLossGraph() {
			cols = append(cols, col{colGraph, "Graph (last 20)"})
		}
	}
	if r.showLast() {
		cols = append(cols, col{colLast, "Last"})
	}
	if r.showAvg() {
		cols = append(cols, col{colAvg, "Avg"})
		if r.showDiffAvg() {
			cols = append(cols, col{colDiffAvg, "ΔAvg"})
		}
	}
	if r.showMinMaxJitter() {
		cols = append(cols, col{colMin, "Min"}, col{colMax, "Max"}, col{colJitter, "Jitter"})
	}
	if r.showGraph() {
		cols = append(cols, col{colGraph, "Graph (last 20)"})
	}
	var b strings.Builder
	for _, c := range cols {
		b.WriteString(r.color(ansiBold+ansiWhite, padRight(c.s, c.w)))
	}
	return b.String()
}

func (r *TerminalRenderer) buildSeparator() string {
	return r.color(ansiDim, strings.Repeat("─", r.tableWidth()))
}

func (r *TerminalRenderer) buildPanelDivider() string {
	return r.color(ansiDim, strings.Repeat("─", r.tableWidth()))
}

// buildNoReplyRow renders a "* * *" row for a hop that never sent TTL-exceeded.
// These routers simply don't respond; this is policy, not packet loss.
// A leading space in the IP cell visually separates the hop number from the *.
func (r *TerminalRenderer) buildNoReplyRow(snap metrics.HopSnapshot) string {
	var b strings.Builder
	colIPWidth := r.colIPWidth(snap.IP)
	b.WriteString(r.color(ansiDim, padLeft(fmt.Sprintf("%d", snap.TTL), colHop)))
	b.WriteString(r.color(ansiDim, padRight(" *", colIPWidth)))
	b.WriteString(r.color(ansiDim, padRight("(no reply)", colHost)))
	if r.showLoss() {
		b.WriteString(r.color(ansiDim, padRight("-", colLoss)))
		if r.showDiffLoss() {
			b.WriteString(r.color(ansiDim, padRight("-", colDiffLoss)))
		}
		if r.showLossGraph() {
			b.WriteString(strings.Repeat(" ", colGraph))
		}
	}
	if r.showLast() {
		b.WriteString(r.color(ansiDim, padRight("-", colLast)))
	}
	if r.showAvg() {
		b.WriteString(r.color(ansiDim, padRight("-", colAvg)))
		if r.showDiffAvg() {
			b.WriteString(r.color(ansiDim, padRight("-", colDiffAvg)))
		}
	}
	if r.showMinMaxJitter() {
		b.WriteString(r.color(ansiDim, padRight("-", colMin)))
		b.WriteString(r.color(ansiDim, padRight("-", colMax)))
		b.WriteString(r.color(ansiDim, padRight("-", colJitter)))
	}
	if r.showGraph() {
		b.WriteString(strings.Repeat(" ", colGraph))
	}
	return b.String()
}

func (r *TerminalRenderer) buildHopRow(snap metrics.HopSnapshot) string {
	var b strings.Builder

	b.WriteString(r.color(ansiDim, padLeft(fmt.Sprintf("%d", snap.TTL), colHop)))

	ipStr := r.formatIP(snap.IP)
	colIPWidth := r.colIPWidth(snap.IP)
	b.WriteString(padRight(" "+ipStr, colIPWidth))

	host := snap.DisplayName()
	if host == ipStr {
		host = ""
	}
	b.WriteString(r.color(ansiDim, padRight(truncate(host, colHost-1), colHost)))

	if snap.Sent == 0 {
		width := r.tableWidthForIP(snap.IP) - (colHop + colIPWidth + colHost)
		b.WriteString(r.color(ansiDim, strings.Repeat("·", width)))
		return b.String()
	}

	if r.showLoss() {
		lossStr := fmt.Sprintf("%.1f%%", snap.Loss*100)
		b.WriteString(r.color(r.lossColor(snap.Loss), padRight(lossStr, colLoss)))
		if r.showDiffLoss() {
			b.WriteString(r.color(r.diffColor(snap.DiffLoss), padRight(r.formatDiffLoss(snap), colDiffLoss)))
		}
	}

	if r.showLast() || r.showAvg() || r.showMinMaxJitter() {
		if snap.Recv == 0 {
			if r.showLast() {
				b.WriteString(r.color(ansiRed, padRight("???", colLast)))
			}
			if r.showAvg() {
				b.WriteString(r.color(ansiRed, padRight("???", colAvg)))
				if r.showDiffAvg() {
					b.WriteString(r.color(ansiDim, padRight("-", colDiffAvg)))
				}
			}
			if r.showMinMaxJitter() {
				b.WriteString(r.color(ansiRed, padRight("???", colMin)))
				b.WriteString(r.color(ansiRed, padRight("???", colMax)))
				b.WriteString(r.color(ansiRed, padRight("???", colJitter)))
			}
		} else {
			latColor := r.latencyColor(snap.AvgRTT)
			if r.showLast() {
				b.WriteString(r.color(latColor, padRight(fmtDur(snap.LastRTT), colLast)))
			}
			if r.showAvg() {
				b.WriteString(r.color(latColor, padRight(fmtDur(snap.AvgRTT), colAvg)))
				if r.showDiffAvg() {
					b.WriteString(r.color(r.diffColorDuration(snap.DiffAvgRTT), padRight(r.formatDiffAvg(snap), colDiffAvg)))
				}
			}
			if r.showMinMaxJitter() {
				b.WriteString(r.color(ansiDim, padRight(fmtDur(snap.MinRTT), colMin)))
				b.WriteString(r.color(ansiDim, padRight(fmtDur(snap.MaxRTT), colMax)))
				b.WriteString(r.color(ansiDim, padRight(fmtDur(snap.Jitter), colJitter)))
			}
		}
	}

	if r.showGraph() || r.showLossGraph() {
		if snap.Loss >= r.cfg.WarnLoss && len(snap.RecentLosses) > 0 {
			b.WriteString(r.color(r.lossGraphColor(snap.Loss), r.lossSparkline(snap.RecentLosses, colGraph)))
		} else {
			b.WriteString(r.color(r.graphColor(snap.AvgRTT, snap.Loss), r.sparkline(snap.RecentRTTs, colGraph)))
		}
	}

	return b.String()
}

// buildFooter shows end-to-end loss using only the LAST responding hop.
// Using the session total (all hops) would give a falsely high loss % because
// intermediate routers that don't send TTL-exceeded look like 100% loss.
func (r *TerminalRenderer) buildFooter(snaps []metrics.HopSnapshot, sum metrics.SessionSummary, paused bool) string {
	var last *metrics.HopSnapshot
	for i := len(snaps) - 1; i >= 0; i-- {
		if snaps[i].Recv > 0 || (snaps[i].Sent > 0 && snaps[i].IP != nil) {
			cp := snaps[i]
			last = &cp
			break
		}
	}

	silent := 0
	for _, s := range snaps {
		if s.IP == nil && s.Recv == 0 {
			silent++
		}
	}

	pauseIndicator := ""
	if paused {
		pauseIndicator = r.color(ansiRed, "[PAUSED] ")
	}

	info := ""
	if last != nil && last.Sent > 0 {
		info = fmt.Sprintf("  e2e loss: %.1f%%  (%d sent, %d recv)  route changes: %d",
			last.Loss*100, last.Sent, last.Recv, sum.RouteChanges)
	}
	silentNote := ""
	if silent > 0 {
		silentNote = fmt.Sprintf("  [%d hop(s) show *]", silent)
	}

	keys := "Keys: P=Pause  S=Sort  V=View  +/-=Zoom  R=Reset  Q=Quit"
	return pauseIndicator + r.color(ansiDim, keys+info+silentNote)
}

// sparkline builds a Unicode bar chart from the given RTT slice.
func (r *TerminalRenderer) sparkline(rtts []time.Duration, w int) string {
	if len(rtts) == 0 {
		return strings.Repeat(" ", w)
	}
	if len(rtts) > w {
		rtts = rtts[len(rtts)-w:]
	}

	var minV, maxV float64
	minV = math.MaxFloat64
	for _, d := range rtts {
		v := float64(d.Nanoseconds())
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}

	runes := make([]rune, w)
	for i := range runes {
		runes[i] = ' '
	}
	rangeV := maxV - minV
	for i, d := range rtts {
		idx := 0
		if rangeV > 0 {
			idx = int((float64(d.Nanoseconds()) - minV) / rangeV * float64(len(sparkChars)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkChars) {
			idx = len(sparkChars) - 1
		}
		runes[i] = sparkChars[idx]
	}
	return string(runes)
}

func (r *TerminalRenderer) lossSparkline(losses []float64, w int) string {
	if len(losses) == 0 {
		return strings.Repeat(" ", w)
	}
	if len(losses) > w {
		losses = losses[len(losses)-w:]
	}
	values := make([]time.Duration, 0, len(losses))
	for _, v := range losses {
		if v <= 0 {
			values = append(values, 0)
		} else {
			values = append(values, time.Millisecond)
		}
	}
	return r.sparkline(values, w)
}

func (r *TerminalRenderer) color(code, s string) string {
	if r.cfg.NoColor {
		return s
	}
	return code + s + ansiReset
}

func (r *TerminalRenderer) latencyColor(rtt time.Duration) string {
	switch {
	case rtt == 0:
		return ansiDim
	case rtt < r.cfg.WarnLatency:
		return ansiGreen
	case rtt < r.cfg.CriticalLatency:
		return ansiYellow
	default:
		return ansiRed
	}
}

func (r *TerminalRenderer) lossColor(loss float64) string {
	if loss == 0 {
		return ansiGreen
	}
	if loss < r.cfg.CriticalLoss {
		return ansiYellow
	}
	return ansiRed
}

func (r *TerminalRenderer) diffColor(delta float64) string {
	if delta == 0 {
		return ansiDim
	}
	if delta > 0 {
		return ansiRed
	}
	return ansiGreen
}

func (r *TerminalRenderer) diffColorDuration(delta time.Duration) string {
	if delta == 0 {
		return ansiDim
	}
	if delta > 0 {
		return ansiRed
	}
	return ansiGreen
}

func (r *TerminalRenderer) lossGraphColor(loss float64) string {
	if loss >= r.cfg.CriticalLoss {
		return ansiRed
	}
	if loss >= r.cfg.WarnLoss {
		return ansiYellow
	}
	if loss > 0 {
		return ansiYellow
	}
	return ansiGreen
}

func (r *TerminalRenderer) graphColor(rtt time.Duration, loss float64) string {
	if loss >= r.cfg.CriticalLoss {
		return ansiRed
	}
	if loss >= r.cfg.WarnLoss {
		return ansiYellow
	}
	return r.latencyColor(rtt)
}

func (r *TerminalRenderer) formatDiffLoss(s metrics.HopSnapshot) string {
	if !s.HasDiff {
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", s.DiffLoss*100)
}

func (r *TerminalRenderer) formatDiffAvg(s metrics.HopSnapshot) string {
	if !s.HasDiff || s.Recv == 0 {
		return "-"
	}
	return fmtDiffDur(s.DiffAvgRTT)
}

func (r *TerminalRenderer) tableWidth() int {
	ipColWidth := colIP
	if r.useIPv6 {
		if r.ipv6Format == "full" {
			ipColWidth = colIP6Full
		} else {
			ipColWidth = colIP6
		}
	}
	total := colHop + ipColWidth + colHost
	if r.showLoss() {
		total += colLoss
		if r.showDiffLoss() {
			total += colDiffLoss
		}
		if r.showLossGraph() {
			total += colGraph
		}
	}
	if r.showLast() {
		total += colLast
	}
	if r.showAvg() {
		total += colAvg
		if r.showDiffAvg() {
			total += colDiffAvg
		}
	}
	if r.showMinMaxJitter() {
		total += colMin + colMax + colJitter
	}
	if r.showGraph() {
		total += colGraph
	}
	return total
}

func (r *TerminalRenderer) showLoss() bool {
	return r.viewMode == "all" || r.viewMode == "loss"
}

func (r *TerminalRenderer) showAvg() bool {
	return r.viewMode == "all" || r.viewMode == "avg"
}

func (r *TerminalRenderer) showLast() bool {
	return r.viewMode == "all"
}

func (r *TerminalRenderer) showMinMaxJitter() bool {
	return r.viewMode == "all"
}

func (r *TerminalRenderer) showGraph() bool {
	return r.viewMode == "all" || r.viewMode == "avg"
}

func (r *TerminalRenderer) showDiffLoss() bool {
	return r.diffEnabled && r.showLoss()
}

func (r *TerminalRenderer) showDiffAvg() bool {
	return r.diffEnabled && r.showAvg()
}

func (r *TerminalRenderer) showLossGraph() bool {
	return r.viewMode == "loss"
}

// ── Utilities ────────────────────────────────────────────────────────────────

func padRight(s string, n int) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	return s + strings.Repeat(" ", n-l)
}

func padLeft(s string, n int) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	return strings.Repeat(" ", n-l) + s
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return "0ms"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds()) // no decimals → always < 8 runes
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func fmtDiffDur(d time.Duration) string {
	sign := "+"
	if d < 0 {
		sign = "-"
		d = -d
	}
	return sign + fmtDur(d)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

func (r *TerminalRenderer) formatIP(ip net.IP) string {
	if ip == nil {
		return "???"
	}
	if ip.To4() != nil {
		return ip.String()
	}
	if r.ipv6Format == "full" {
		return ip.String()
	}
	return compactIPv6(ip)
}

func (r *TerminalRenderer) colIPWidth(ip net.IP) int {
	if ip == nil {
		if r.useIPv6 {
			if r.ipv6Format == "full" {
				return colIP6Full
			}
			return colIP6
		}
		return colIP
	}
	if ip.To4() != nil {
		return colIP
	}
	if r.ipv6Format == "full" {
		return colIP6Full
	}
	return colIP6
}

func (r *TerminalRenderer) tableWidthForIP(ip net.IP) int {
	total := colHop + r.colIPWidth(ip) + colHost
	if r.showLoss() {
		total += colLoss
		if r.showDiffLoss() {
			total += colDiffLoss
		}
		if r.showLossGraph() {
			total += colGraph
		}
	}
	if r.showLast() {
		total += colLast
	}
	if r.showAvg() {
		total += colAvg
		if r.showDiffAvg() {
			total += colDiffAvg
		}
	}
	if r.showMinMaxJitter() {
		total += colMin + colMax + colJitter
	}
	if r.showGraph() {
		total += colGraph
	}
	return total
}

func compactIPv6(ip net.IP) string {
	s := ip.String()
	maxZeros := 0
	maxStart := -1
	currentZeros := 0
	currentStart := -1

	for i := 0; i < 8; i++ {
		start := i * 4
		if i > 0 {
			start += i
		}
		end := start + 4
		if end > len(s) {
			break
		}
		part := s[start:end]
		if part == "0000" || part == "0" {
			if currentZeros == 0 {
				currentStart = i
			}
			currentZeros++
		} else {
			if currentZeros > maxZeros {
				maxZeros = currentZeros
				maxStart = currentStart
			}
			currentZeros = 0
		}
	}
	if currentZeros > maxZeros {
		maxZeros = currentZeros
		maxStart = currentStart
	}

	if maxZeros > 1 {
		parts := strings.Split(s, ":")
		result := make([]string, 0, 8)
		for i := 0; i < len(parts); i++ {
			if i == maxStart && maxZeros > 0 {
				result = append(result, "")
				i += maxZeros - 1
			} else {
				result = append(result, parts[i])
			}
		}
		return strings.Join(result, ":")
	}
	return s
}
