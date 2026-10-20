// Package probe orchestrates a model check (resolve, read header, detect
// hardware, calculate) so the CLI and the TUI share one code path.
package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vramlens/internal/calculator"
	"vramlens/internal/gguf"
	"vramlens/internal/hardware"
)

// CheckParams selects a model and the memory assumptions.
type CheckParams struct {
	Target      string  // HF repo[:quant], URL or local .gguf path
	File        string  // exact file inside a repo
	Quant       string  // filename substring
	VRAMGB      float64 // override free VRAM; 0 = detect
	OverheadMiB uint64
	OverheadPct float64
	HW          *hardware.Info // optional pre-detected hardware
}

// CheckResult is everything the reports need.
type CheckResult struct {
	Display   string            `json:"target"`
	Name      string            `json:"name"`
	Meta      *gguf.Metadata    `json:"model"`
	Shards    int               `json:"shards"`
	HW        hardware.Info     `json:"hardware"`
	VRAMTotal uint64            `json:"vram_total"`
	VRAMFree  uint64            `json:"vram_free"`
	Result    calculator.Result `json:"result"`
	Fetched   int64             `json:"bytes_fetched,omitempty"`
}

// LooksLocal reports whether target is clearly a filesystem path rather than
// a Hugging Face repo or URL (drive letter, backslash, or a bare .gguf name).
func LooksLocal(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	return filepath.VolumeName(target) != "" || strings.Contains(target, `\`) ||
		strings.HasSuffix(strings.ToLower(target), ".gguf")
}

// Check resolves and analyses one model. A *gguf.AmbiguousError means the
// repo holds several GGUF files and the caller must narrow the choice.
func Check(p CheckParams) (*CheckResult, error) {
	token := os.Getenv("HF_TOKEN")
	target := strings.TrimSpace(p.Target)
	if target == "" {
		return nil, fmt.Errorf("missing target (HF repo, URL or .gguf path)")
	}
	var (
		tgt *gguf.Target
		err error
	)
	if st, e := os.Stat(target); e == nil && !st.IsDir() {
		tgt, err = gguf.ResolveLocal(target)
	} else if LooksLocal(target) {
		return nil, fmt.Errorf("file not found: %s", target)
	} else {
		tgt, err = gguf.ResolveHF(target, p.File, p.Quant, token)
	}
	if err != nil {
		return nil, err
	}

	var src gguf.Source
	if tgt.Local != "" {
		src, err = gguf.OpenLocal(tgt.Local)
	} else {
		src, err = gguf.OpenHTTP(tgt.URL, token)
	}
	if err != nil {
		return nil, err
	}
	defer src.Close()
	meta, err := gguf.Read(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tgt.Display, err)
	}
	if tgt.Size > 0 {
		meta.FileSize = tgt.Size
	}

	var hw hardware.Info
	if p.HW != nil {
		hw = *p.HW
	} else {
		hw = hardware.Detect()
	}
	vramTotal, vramFree := hw.TotalVRAM()
	if p.VRAMGB > 0 {
		vramFree = uint64(p.VRAMGB * (1 << 30))
		if vramTotal < vramFree {
			vramTotal = vramFree
		}
	}
	res := calculator.Calculate(calculator.Config{
		Meta:      meta,
		VRAMFree:  vramFree,
		Overhead:  p.OverheadMiB << 20,
		OverheadP: p.OverheadPct / 100,
	})
	name := tgt.Display
	if meta.Name != "" && !strings.EqualFold(meta.Name, name) {
		name = meta.Name + "  (" + tgt.Display + ")"
	}
	return &CheckResult{
		Display: tgt.Display, Name: name, Meta: meta, Shards: tgt.Shards,
		HW: hw, VRAMTotal: vramTotal, VRAMFree: vramFree, Result: res,
		Fetched: gguf.Fetched(src),
	}, nil
}
