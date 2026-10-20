package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"vramlens/internal/bench"
)

func pctCol(p float64) lipgloss.TerminalColor {
	switch {
	case p >= 99.5:
		return CGreen
	case p >= 80:
		return CAmber
	}
	return CRed
}

func ms(v float64) string { return Dur(time.Duration(v * float64(time.Millisecond))) }

// BenchCards renders the four headline metrics side by side.
func BenchCards(st bench.Stats, width int) string {
	w := (width - 3) / 4
	if w < 18 {
		w = 18
	}
	big := func(s string, c lipgloss.TerminalColor) string {
		return lipgloss.NewStyle().Bold(true).Foreground(c).Render(s)
	}
	cards := []string{
		Box("TTFT", big(ms(st.TTFTP50), CBlue)+Dim.Render(" median")+"\n"+Dim.Render("p95 "+ms(st.TTFTP95)), w, CBlue),
		Box("Speed", big(fmt.Sprintf("%.1f t/s", st.TPSMean), CAccent)+"\n"+Dim.Render(fmt.Sprintf("%.1f – %.1f", st.TPSMin, st.TPSMax)), w, CAccent),
		Box("Valid JSON", big(fmt.Sprintf("%.0f%%", st.SchemaRate), pctCol(st.SchemaRate))+Dim.Render(" schema")+"\n"+Dim.Render(fmt.Sprintf("%.0f%% parse", st.ParseRate)), w, pctCol(st.SchemaRate)),
		Box("Determinism", big(fmt.Sprintf("%.0f%%", st.Determinism), pctCol(st.Determinism))+"\n"+Dim.Render(fmt.Sprintf("%d distinct output(s)", st.Distinct)), w, pctCol(st.Determinism)),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cards[0], " ", cards[1], " ", cards[2], " ", cards[3])
}

// RunsTable lists the most recent samples (newest last).
func RunsTable(samples []bench.Sample, maxRows, width int) string {
	if maxRows < 1 {
		maxRows = 1
	}
	shown := samples
	if len(shown) > maxRows {
		shown = shown[len(shown)-maxRows:]
	}
	resW := width - 4 - (5 + 10 + 10 + 8 + 10)
	if resW < 10 {
		resW = 10
	}
	var l []string
	l = append(l, Dim.Render(Pad("run", 5)+PadLeft("TTFT", 9)+" "+PadLeft("t/s", 9)+" "+PadLeft("tokens", 7)+" "+PadLeft("total", 9)+"  result"))
	for _, s := range shown {
		res := Good.Render("✓ valid")
		switch {
		case s.Err != "":
			res = Bad.Render("✗ " + Trunc(s.Err, resW-2))
		case !s.ParseOK:
			res = Bad.Render("✗ not valid JSON")
		case !s.SchemaOK:
			res = Warn.Render("◐ " + Trunc(strings.Join(s.Violations, "; "), resW-2))
		}
		l = append(l, fmt.Sprintf("%s%s %s %s %s  %s",
			Pad(fmt.Sprint(s.Index+1), 5), PadLeft(Dur(s.TTFT), 9), PadLeft(fmt.Sprintf("%.1f", s.TPS), 9),
			PadLeft(fmt.Sprint(s.Tokens), 7), PadLeft(Dur(s.Total), 9), res))
	}
	if len(samples) > len(shown) {
		l = append(l, Dim.Render(fmt.Sprintf("… %d earlier run(s) not shown", len(samples)-len(shown))))
	}
	return Box("Runs", strings.Join(l, "\n"), width, CDim)
}

// Trend draws TPS and TTFT sparklines across runs.
func Trend(samples []bench.Sample, width int) string {
	var tps, ttft []float64
	for _, s := range samples {
		if s.Err == "" {
			tps = append(tps, s.TPS)
			ttft = append(ttft, float64(s.TTFT)/float64(time.Millisecond))
		}
	}
	w := width - 4 - 14
	if w > 60 {
		w = 60
	}
	return Box("Trend", Pad(Dim.Render("tokens/s"), 12)+"  "+Spark(tps, w)+"\n"+Pad(Dim.Render("TTFT"), 12)+"  "+Spark(ttft, w), width, CDim)
}

// Verdict summarises the benchmark in one sentence.
func Verdict(st bench.Stats) string {
	switch {
	case st.Runs == 0:
		return Dim.Render("No runs completed.")
	case st.Errors == st.Runs:
		return Bad.Render("✗ Every request failed — check that the server is running and the model name is right.")
	case st.SchemaRate >= 99.5 && st.Determinism >= 99.5:
		return Good.Render("✓ Excellent: every answer matched the schema and all outputs were identical.")
	case st.SchemaRate >= 99.5:
		return Good.Render("✓ Reliable: every answer matched the schema") + Dim.Render(fmt.Sprintf(" (%d distinct outputs — vary the seed/temperature to taste).", st.Distinct))
	case st.SchemaRate >= 80:
		return Warn.Render(fmt.Sprintf("◐ Mostly reliable: %.0f%% valid. Expect occasional broken JSON; add validation + retry in your app.", st.SchemaRate))
	}
	return Bad.Render(fmt.Sprintf("✗ Unreliable: only %.0f%% valid. Try a larger model, a higher max-tokens, or a different quantization.", st.SchemaRate))
}

// BenchReport is the plain-CLI final report.
func BenchReport(cfg bench.Config, samples []bench.Sample, st bench.Stats, width int) string {
	if width > 110 {
		width = 110
	}
	if width < 60 {
		width = 60
	}
	head := Bold.Render(cfg.Model) + Dim.Render(fmt.Sprintf("  ·  %s @ %s  ·  %d runs × %d parallel  ·  temp %g", cfg.Provider, cfg.Endpoint, cfg.N, cfg.Concurrency, cfg.Temperature))
	return strings.Join([]string{
		Box("Benchmark", head, width, CViolet),
		BenchCards(st, width),
		RunsTable(samples, len(samples), width),
		Trend(samples, width),
		Verdict(st),
	}, "\n")
}
