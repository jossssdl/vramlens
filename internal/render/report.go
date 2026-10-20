// Package render draws the terminal report for the check command.
package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"vramlens/internal/calculator"
	"vramlens/internal/gguf"
	"vramlens/internal/hardware"
)

var (
	title  = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Faint(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	amber  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	header = lipgloss.NewStyle().Bold(true).Underline(true)
)

func gib(b uint64) string { return fmt.Sprintf("%.2f GiB", float64(b)/(1<<30)) }

// Bar renders a fixed-width usage bar; used/total in bytes.
func Bar(used, total uint64, width int) string {
	if total == 0 {
		return strings.Repeat("░", width)
	}
	frac := float64(used) / float64(total)
	n := int(frac*float64(width) + 0.5)
	if n > width {
		n = width
	}
	st := green
	switch {
	case frac > 1:
		st = red
	case frac > 0.85:
		st = amber
	}
	return st.Render(strings.Repeat("█", n)) + dim.Render(strings.Repeat("░", width-n))
}

func fmtCtx(n int) string {
	if n%1024 == 0 {
		return fmt.Sprintf("%dk", n/1024)
	}
	return fmt.Sprint(n)
}

// Report builds the full text report.
func Report(hw hardware.Info, vramTotal, vramFree uint64, name string, m *gguf.Metadata, shards int, res calculator.Result) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }

	w("%s", title.Render("Hardware"))
	w("  RAM   %s  %s free of %s", Bar(hw.RAMTotal-hw.RAMFree, hw.RAMTotal, 30), gib(hw.RAMFree), gib(hw.RAMTotal))
	for _, g := range hw.GPUs {
		w("  VRAM  %s  %s free of %s  %s", Bar(g.VRAMTotal-g.VRAMFree, g.VRAMTotal, 30), gib(g.VRAMFree), gib(g.VRAMTotal), dim.Render(g.Name))
	}
	if len(hw.GPUs) == 0 {
		w("  %s", amber.Render("no GPU detected — everything runs on CPU"))
	}
	for _, s := range hw.Warnings {
		w("  %s", dim.Render("! "+s))
	}
	if vramTotal != 0 || vramFree != 0 {
		w("  Budget used for fit: %s of VRAM", gib(vramFree))
	}

	w("")
	w("%s", title.Render("Model"))
	w("  %s", name)
	extra := ""
	if shards > 1 {
		extra = fmt.Sprintf(" (%d shards)", shards)
	}
	w("  arch=%s  quant=%s  size=%s%s", m.Arch, m.Quant, gib(m.FileSize), extra)
	w("  layers=%d  embd=%d  heads=%d  kv_heads=%s  head_dim=%d/%d  train_ctx=%d",
		m.NLayers, m.NEmbd, m.NHeads, kvHeadsSummary(m.NKVHeads), m.KeyLength, m.ValueLength, m.ContextTrain)
	w("  weights %s + runtime overhead %s", gib(res.WeightsBytes), gib(res.OverheadBytes))

	w("")
	w("%s", title.Render("KV cache vs. VRAM"))
	w("  %s", header.Render(fmt.Sprintf("%-8s", "context"))+"  "+
		header.Render(fmt.Sprintf("%-22s", "FP16"))+"  "+
		header.Render(fmt.Sprintf("%-22s", "Q8_0"))+"  "+
		header.Render(fmt.Sprintf("%-22s", "Q4_0")))
	for _, r := range res.Rows {
		label := fmtCtx(r.Ctx)
		if r.OverTrain {
			label += "*"
		}
		line := fmt.Sprintf("%-8s", label)
		for _, t := range calculator.KVTypes {
			cell := fmt.Sprintf("%s %s", gib(r.Total[t]), tag(r.Fit[t], r.GPULayerFrac[t]))
			line += "  " + styleFor(r.Fit[t]).Render(fmt.Sprintf("%-22s", cell))
		}
		w("  %s", line)
	}
	w("  %s", dim.Render("total = weights + KV cache + overhead;  * exceeds the model's training context"))

	w("")
	w("%s", title.Render("CPU offload threshold"))
	for _, t := range calculator.KVTypes {
		n := res.OffloadCtx[t]
		if n <= 0 {
			w("  %-5s %s", t, red.Render("weights + overhead alone exceed VRAM: offload even at minimal context"))
			continue
		}
		msg := fmt.Sprintf("fits fully in VRAM up to %d tokens", n)
		if m.ContextTrain > 0 && n >= int(m.ContextTrain) {
			w("  %-5s %s", t, green.Render(msg+" (>= training context: no offload)"))
		} else {
			w("  %-5s %s", t, amber.Render(msg+"; beyond that layers spill to CPU"))
		}
	}
	return b.String()
}

func tag(f calculator.Fit, frac float64) string {
	switch f {
	case calculator.FitGPU:
		return "GPU"
	case calculator.FitPartial:
		return fmt.Sprintf("%d%% GPU", int(frac*100))
	}
	return "CPU"
}

func styleFor(f calculator.Fit) lipgloss.Style {
	switch f {
	case calculator.FitGPU:
		return green
	case calculator.FitPartial:
		return amber
	}
	return red
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
	return fmt.Sprintf("%d-%d", lo, hi)
}
