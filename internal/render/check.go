package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"vramlens/internal/calculator"
	"vramlens/internal/hardware"
	"vramlens/internal/probe"
)

// HardwarePanel shows detected GPUs and RAM with usage gauges.
func HardwarePanel(hw hardware.Info, vramFree uint64, overridden bool, width int) string {
	inner := width - 4
	bar := 24
	if inner < 70 {
		bar = 12
	}
	var l []string
	for _, g := range hw.GPUs {
		l = append(l, Bold.Render(g.Name))
		used := g.VRAMTotal - g.VRAMFree
		l = append(l, fmt.Sprintf("%s %s  %s free of %s",
			Pad(Dim.Render("VRAM"), 5), Bar(used, g.VRAMTotal, bar), GiB(g.VRAMFree), GiB(g.VRAMTotal)))
	}
	if len(hw.GPUs) == 0 {
		l = append(l, Warn.Render("No GPU detected — everything would run on the CPU"))
	}
	l = append(l, fmt.Sprintf("%s %s  %s free of %s",
		Pad(Dim.Render("RAM"), 5), Bar(hw.RAMTotal-hw.RAMFree, hw.RAMTotal, bar), GiB(hw.RAMFree), GiB(hw.RAMTotal)))
	budget := "Budget used for the fit: " + GiB(vramFree) + " of VRAM"
	if overridden {
		budget += " (manual override)"
	}
	l = append(l, Dim.Render(budget))
	for _, w := range hw.Warnings {
		l = append(l, Dim.Render("! "+w))
	}
	return Box("Hardware", strings.Join(l, "\n"), width, CBlue)
}

// ModelPanel summarises the model and how much of the VRAM its weights take.
func ModelPanel(r *probe.CheckResult, width int) string {
	m := r.Meta
	inner := width - 4
	shards := ""
	if r.Shards > 1 {
		shards = fmt.Sprintf(" · %d shards", r.Shards)
	}
	kv := kvHeadsSummary(m.NKVHeads)
	l := []string{
		Bold.Render(r.Name),
		Dim.Render(fmt.Sprintf("%s · %s%s", m.Arch, m.Quant, shards)),
		fmt.Sprintf("%s layers · %s KV heads · head dim %d · embedding %d · trained for %s tokens",
			Dim.Render(fmt.Sprint(m.NLayers)), Dim.Render(kv), m.KeyLength, m.NEmbd, Int(int(m.ContextTrain))),
	}
	bar := inner - 52
	if bar > 28 {
		bar = 28
	}
	if bar < 8 {
		bar = 8
	}
	res := r.Result
	pct := 0.0
	if r.VRAMFree > 0 {
		pct = 100 * float64(res.WeightsBytes) / float64(r.VRAMFree)
	}
	l = append(l, fmt.Sprintf("%s %s  %s weights · %.0f%% of free VRAM · +%s overhead",
		Pad(Dim.Render("Weights"), 8),
		Gauge([]Seg{{res.WeightsBytes, CViolet}, {res.OverheadBytes, CDim}}, r.VRAMFree, bar),
		Bold.Render(GiB(res.WeightsBytes)), pct, GiB(res.OverheadBytes)))
	return Box("Model", strings.Join(l, "\n"), width, CViolet)
}

func fitGlyph(f calculator.Fit, frac float64) (string, lipgloss.TerminalColor) {
	switch f {
	case calculator.FitGPU:
		return "✓ GPU", CGreen
	case calculator.FitPartial:
		return fmt.Sprintf("◐ %d%%", int(frac*100+0.5)), CAmber
	}
	return "✗ CPU", CRed
}

// KVPanel is the context × cache-type table with stacked memory bars.
func KVPanel(r *probe.CheckResult, width int) string {
	inner := width - 4
	res := r.Result
	barW := (inner-7-6)/3 - 14
	if barW > 14 {
		barW = 14
	}
	if barW < 4 {
		barW = 0
	}
	cellW := barW + 14
	if barW == 0 {
		cellW = 13
	}
	var l []string
	hdr := Pad("", 7)
	for _, t := range calculator.KVTypes {
		hdr += Pad(Bold.Render(string(t)+" cache"), cellW+2)
	}
	l = append(l, hdr)
	for _, row := range res.Rows {
		label := fmtCtx(row.Ctx)
		if row.OverTrain {
			label += "*"
		}
		line := Pad(Bold.Render(label), 7)
		for _, t := range calculator.KVTypes {
			f := row.Fit[t]
			glyph, col := fitGlyph(f, row.GPULayerFrac[t])
			size := fmt.Sprintf("%5.1fG", float64(row.Total[t])/(1<<30))
			cell := ""
			if barW > 0 {
				var bar string
				if f == calculator.FitGPU {
					kvb := row.KVBytes[t]
					bar = Gauge([]Seg{{res.WeightsBytes, CViolet}, {kvb, CBlue}, {res.OverheadBytes, CDim}}, r.VRAMFree, barW)
				} else {
					bar = Paint(col, strings.Repeat("█", barW))
				}
				cell = bar + " "
			}
			cell += Dim.Render(size) + " " + Paint(col, glyph)
			line += Pad(cell, cellW+2)
		}
		l = append(l, line)
	}
	l = append(l, "")
	legend := Paint(CViolet, "█") + Dim.Render(" weights  ") + Paint(CBlue, "█") + Dim.Render(" KV cache  ") +
		Paint(CDim, "█") + Dim.Render(" overhead  ") + Dim.Render("░ free   ") +
		Paint(CGreen, "✓") + Dim.Render(" all on GPU  ") + Paint(CAmber, "◐") + Dim.Render(" % of layers on GPU  ") +
		Paint(CRed, "✗") + Dim.Render(" CPU")
	l = append(l, legend, Dim.Render("Size = weights + KV cache + overhead.  * = beyond the model's training context."))
	return Box("KV cache vs. your VRAM", strings.Join(l, "\n"), width, CAccent)
}

// OffloadPanel plots, per cache type, where the context stops fitting.
func OffloadPanel(r *probe.CheckResult, width int) string {
	inner := width - 4
	scale := int(r.Meta.ContextTrain)
	if scale <= 0 {
		scale = 131072
	}
	bar := inner - 7 - 24
	if bar > 44 {
		bar = 44
	}
	if bar < 10 {
		bar = 10
	}
	var l []string
	for _, t := range calculator.KVTypes {
		n := r.Result.OffloadCtx[t]
		line := Pad(Bold.Render(string(t)), 7)
		switch {
		case n <= 0:
			line += Paint(CRed, strings.Repeat("─", bar)) + "  " + Bad.Render("no room: weights exceed VRAM")
		case n >= scale:
			line += Paint(CGreen, strings.Repeat("━", bar-1)+"●") + "  " + Good.Render("full "+Int(scale)+" fits ✓")
		default:
			pos := int(float64(n)/float64(scale)*float64(bar-1) + 0.5)
			line += Paint(CGreen, strings.Repeat("━", pos)) + Paint(CAmber, "●") +
				Dim.Render(strings.Repeat("┄", bar-1-pos)) + "  " + Paint(CAmber, Int(n)+" tokens")
		}
		l = append(l, line)
	}
	l = append(l, Dim.Render(fmt.Sprintf("%s0%s%s tokens", strings.Repeat(" ", 7), strings.Repeat(" ", bar-len(Int(scale))-1), Int(scale))))
	l = append(l, "", Dim.Render("Beyond the ● marker, layers spill to the CPU and generation slows down sharply."))
	return Box("CPU offload threshold", strings.Join(l, "\n"), width, CAmber)
}

// TipsPanel turns the numbers into a recommendation.
func TipsPanel(r *probe.CheckResult, width int) string {
	res := r.Result
	var l []string
	if res.OffloadCtx[calculator.KVF16] <= 0 {
		l = append(l, Bad.Render("✗ ")+"This file does not fit in VRAM even with a minimal context.",
			"  Choose a smaller quantization (e.g. Q4_K_M → Q3_K_M) or a smaller model.")
	} else {
		fp16, q8, q4 := res.OffloadCtx[calculator.KVF16], res.OffloadCtx[calculator.KVQ8], res.OffloadCtx[calculator.KVQ4]
		l = append(l, Good.Render("✓ ")+fmt.Sprintf("Fully on GPU up to %s tokens (FP16 cache), %s (Q8_0) or %s (Q4_0).", Int(fp16), Int(q8), Int(q4)))
		if fp16 < 8192 {
			l = append(l, Warn.Render("! ")+"The default FP16 cache limits you below 8k tokens; a Q8_0 cache roughly doubles the usable context.")
		}
		l = append(l, Dim.Render("  Ollama:    OLLAMA_FLASH_ATTENTION=1  OLLAMA_KV_CACHE_TYPE=q8_0"),
			Dim.Render("  llama.cpp: -fa --cache-type-k q8_0 --cache-type-v q8_0"))
	}
	return Box("What to do", strings.Join(l, "\n"), width, CGreen)
}

// CheckReport renders the full check output for the given width.
func CheckReport(r *probe.CheckResult, overridden bool, width int) string {
	if width > 110 {
		width = 110
	}
	if width < 60 {
		width = 60
	}
	parts := []string{
		HardwarePanel(r.HW, r.VRAMFree, overridden, width),
		ModelPanel(r, width),
		KVPanel(r, width),
		OffloadPanel(r, width),
		TipsPanel(r, width),
	}
	return strings.Join(parts, "\n")
}

func fmtCtx(n int) string {
	if n%1024 == 0 {
		return fmt.Sprintf("%dk", n/1024)
	}
	return fmt.Sprint(n)
}

func kvHeadsSummary(h []uint32) string {
	if len(h) == 0 {
		return "0"
	}
	lo, hi := h[0], h[0]
	for _, v := range h {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if lo == hi {
		return fmt.Sprint(lo)
	}
	return fmt.Sprintf("%d–%d", lo, hi)
}
