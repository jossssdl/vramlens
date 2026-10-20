package render

import (
	"fmt"
	"strings"
	"time"

	"vramlens/internal/bench"
)

func pctStyle(p float64) string {
	s := fmt.Sprintf("%.0f%%", p)
	switch {
	case p >= 99.5:
		return green.Render(s)
	case p >= 80:
		return amber.Render(s)
	}
	return red.Render(s)
}

// PctBar draws a 0-100 % bar.
func PctBar(p float64, width int) string {
	return Bar(uint64(p*100), 10000, width)
}

// BenchSummary renders aggregate statistics.
func BenchSummary(cfg bench.Config, st bench.Stats) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	w("%s", title.Render("Benchmark"))
	w("  %s  model=%s  provider=%s  runs=%d  concurrency=%d  temp=%g", cfg.Endpoint, cfg.Model, cfg.Provider, cfg.N, cfg.Concurrency, cfg.Temperature)
	w("")
	w("%s", title.Render("Latency & throughput"))
	w("  TTFT   mean %7.0f ms   p50 %7.0f ms   p95 %7.0f ms", st.TTFTMean, st.TTFTP50, st.TTFTP95)
	w("  TPS    mean %7.1f t/s  min %7.1f      max %7.1f", st.TPSMean, st.TPSMin, st.TPSMax)
	w("")
	w("%s", title.Render("Structured output"))
	w("  JSON parses        %s  %s", PctBar(st.ParseRate, 30), pctStyle(st.ParseRate))
	w("  Schema valid       %s  %s", PctBar(st.SchemaRate, 30), pctStyle(st.SchemaRate))
	w("  Determinism        %s  %s  (%d distinct output(s))", PctBar(st.Determinism, 30), pctStyle(st.Determinism), st.Distinct)
	if st.Errors > 0 {
		w("  %s", red.Render(fmt.Sprintf("%d request(s) failed (counted as invalid, excluded from latency)", st.Errors)))
	}
	return b.String()
}

// BenchRows renders the most recent samples as a table (newest last).
func BenchRows(samples []bench.Sample, max int) string {
	if len(samples) > max {
		samples = samples[len(samples)-max:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n", header.Render(fmt.Sprintf("%-4s %9s %8s %7s  %s", "run", "ttft", "tps", "tokens", "result")))
	for _, s := range samples {
		res := green.Render("valid")
		switch {
		case s.Err != "":
			res = red.Render("error: " + trunc(s.Err, 50))
		case !s.ParseOK:
			res = red.Render("invalid JSON")
		case !s.SchemaOK:
			res = amber.Render("schema: " + trunc(strings.Join(s.Violations, "; "), 50))
		}
		fmt.Fprintf(&b, "  %-4d %9s %8.1f %7d  %s\n", s.Index+1, s.TTFT.Round(time.Millisecond), s.TPS, s.Tokens, res)
	}
	return b.String()
}

func trunc(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
