// Package render draws vramlens' terminal output: panels, gauges and reports
// shared by the plain CLI and the interactive TUI.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette (adaptive to light/dark terminals; lipgloss downgrades colors and
// honours NO_COLOR automatically).
var (
	CAccent = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#5EEAD4"}
	CViolet = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
	CBlue   = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#60A5FA"}
	CGreen  = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	CAmber  = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	CRed    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	CDim    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B95A5"}
)

// Text styles.
var (
	Bold   = lipgloss.NewStyle().Bold(true)
	Dim    = lipgloss.NewStyle().Foreground(CDim)
	Accent = lipgloss.NewStyle().Foreground(CAccent)
	Good   = lipgloss.NewStyle().Foreground(CGreen)
	Warn   = lipgloss.NewStyle().Foreground(CAmber)
	Bad    = lipgloss.NewStyle().Foreground(CRed)
)

// Paint renders s with a foreground color.
func Paint(c lipgloss.TerminalColor, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// Width is the display width of s, ignoring ANSI sequences.
func Width(s string) int { return lipgloss.Width(s) }

// Pad right-pads (or truncates) s to exactly w display cells.
func Pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-Width(s))
}

// PadLeft left-pads s to w cells.
func PadLeft(s string, w int) string {
	if n := w - Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// Box draws a rounded panel with a title embedded in the top border.
func Box(title, body string, width int, col lipgloss.TerminalColor) string {
	if width < 12 {
		width = 12
	}
	inner := width - 4
	edge := lipgloss.NewStyle().Foreground(col)
	var b strings.Builder
	if title == "" {
		b.WriteString(edge.Render("╭" + strings.Repeat("─", width-2) + "╮"))
	} else {
		tw := Width(title)
		fill := width - 5 - tw
		if fill < 0 {
			title, fill = ansi.Truncate(title, width-5, "…"), 0
		}
		b.WriteString(edge.Render("╭─ ") + lipgloss.NewStyle().Bold(true).Foreground(col).Render(title) +
			edge.Render(" "+strings.Repeat("─", fill)+"╮"))
	}
	b.WriteString("\n")
	for _, ln := range strings.Split(body, "\n") {
		b.WriteString(edge.Render("│") + " " + Pad(ln, inner) + " " + edge.Render("│") + "\n")
	}
	b.WriteString(edge.Render("╰" + strings.Repeat("─", width-2) + "╯"))
	return b.String()
}

// Seg is one colored part of a Gauge.
type Seg struct {
	Val uint64
	Col lipgloss.TerminalColor
}

// Gauge draws a bar of width cells where the segments share a capacity of
// total; whatever is left is drawn as an empty track.
func Gauge(segs []Seg, total uint64, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	if total > 0 {
		for _, s := range segs {
			n := int(float64(s.Val)/float64(total)*float64(width) + 0.5)
			if s.Val > 0 && n == 0 {
				n = 1
			}
			if used+n > width {
				n = width - used
			}
			if n > 0 {
				b.WriteString(Paint(s.Col, strings.Repeat("█", n)))
				used += n
			}
		}
	}
	b.WriteString(Dim.Render(strings.Repeat("░", width-used)))
	return b.String()
}

// Bar renders a single-color usage bar (used of total); the color shifts to
// amber/red as it fills.
func Bar(used, total uint64, width int) string {
	col := CGreen
	if total > 0 {
		switch f := float64(used) / float64(total); {
		case f > 1, f > 0.95:
			col = CRed
		case f > 0.8:
			col = CAmber
		}
	}
	return Gauge([]Seg{{used, col}}, total, width)
}

// PctBar draws a 0–100 bar in a color that reflects "higher is better".
func PctBar(p float64, width int) string {
	col := CRed
	switch {
	case p >= 99.5:
		col = CGreen
	case p >= 80:
		col = CAmber
	}
	return Gauge([]Seg{{uint64(p * 100), col}}, 10000, width)
}

// Spark draws a sparkline for positive values.
func Spark(vals []float64, width int) string {
	if len(vals) == 0 || width <= 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	hi := 0.0
	for _, v := range vals {
		if v > hi {
			hi = v
		}
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if hi > 0 {
			i = int(v / hi * float64(len(levels)-1))
		}
		b.WriteRune(levels[i])
	}
	return Accent.Render(b.String())
}

// GiB formats bytes as "x.xx GiB".
func GiB(b uint64) string { return fmt.Sprintf("%.2f GiB", float64(b)/(1<<30)) }

// Int formats n with thousands separators.
func Int(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// Dur formats a duration compactly (850 ms, 3.4 s, 2m05s).
func Dur(d time.Duration) string {
	switch {
	case d <= 0:
		return "–"
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// Clock formats a duration as m:ss.
func Clock(d time.Duration) string {
	d = d.Round(time.Second)
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// Trunc shortens s to n runes with an ellipsis, flattening newlines.
func Trunc(s string, n int) string {
	return ansi.Truncate(strings.ReplaceAll(s, "\n", " "), n, "…")
}
