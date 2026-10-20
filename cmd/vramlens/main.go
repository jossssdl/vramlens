// Command vramlens diagnoses VRAM needs of local LLMs from their GGUF headers.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vramlens/internal/calculator"
	"vramlens/internal/gguf"
	"vramlens/internal/hardware"
	"vramlens/internal/render"
)

const usage = `vramlens - VRAM doctor and KV-cache calculator for local LLMs

Usage:
  vramlens check <huggingface-repo[:QUANT] | file.gguf | URL> [flags]
  vramlens bench --model <name> [flags]        (see: vramlens bench --help)

Flags:
  --file <name>        exact .gguf path inside a Hugging Face repo
  --quant <Q4_K_M>     select the GGUF whose name contains this string
  --vram-gb <n>        override free VRAM (GiB) instead of detecting it
  --overhead-mib <n>   fixed runtime/compute-buffer reserve (default 512)
  --overhead-pct <n>   extra reserve as % of weights (default 5)
  --json               machine-readable output

Environment:
  HF_TOKEN             token for gated/private Hugging Face repos
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Print(usage)
		return
	}
	switch os.Args[1] {
	case "check":
		if err := runCheck(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "bench":
		if len(os.Args) > 2 && (os.Args[2] == "-h" || os.Args[2] == "--help") {
			fmt.Print(benchUsage)
			return
		}
		if err := runBench(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// looksLocal reports whether target is clearly a filesystem path rather than
// a Hugging Face repo or URL (drive letter, backslash, or a bare .gguf name).
func looksLocal(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	return filepath.VolumeName(target) != "" || strings.Contains(target, `\`) ||
		strings.HasSuffix(strings.ToLower(target), ".gguf")
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	file := fs.String("file", "", "")
	quant := fs.String("quant", "", "")
	vramGB := fs.Float64("vram-gb", 0, "")
	ovMiB := fs.Uint64("overhead-mib", 512, "")
	ovPct := fs.Float64("overhead-pct", 5, "")
	asJSON := fs.Bool("json", false, "")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	// Allow flags before or after the positional target.
	var target string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			if target != "" {
				return fmt.Errorf("unexpected argument %q", rest[0])
			}
			target, rest = rest[0], rest[1:]
		}
	}
	if target == "" {
		return errors.New("missing target (HF repo, URL or .gguf path)")
	}

	token := os.Getenv("HF_TOKEN")
	var (
		tgt *gguf.Target
		err error
	)
	if st, e := os.Stat(target); e == nil && !st.IsDir() {
		tgt, err = gguf.ResolveLocal(target)
	} else if looksLocal(target) {
		return fmt.Errorf("file not found: %s", target)
	} else {
		tgt, err = gguf.ResolveHF(target, *file, *quant, token)
	}
	if err != nil {
		return err
	}

	var src gguf.Source
	if tgt.Local != "" {
		src, err = gguf.OpenLocal(tgt.Local)
	} else {
		src, err = gguf.OpenHTTP(tgt.URL, token)
	}
	if err != nil {
		return err
	}
	defer src.Close()
	meta, err := gguf.Read(src)
	if err != nil {
		return fmt.Errorf("%s: %w", tgt.Display, err)
	}
	if tgt.Size > 0 {
		meta.FileSize = tgt.Size
	}

	hw := hardware.Detect()
	vramTotal, vramFree := hw.TotalVRAM()
	if *vramGB > 0 {
		vramFree = uint64(*vramGB * (1 << 30))
		if vramTotal < vramFree {
			vramTotal = vramFree
		}
	}

	res := calculator.Calculate(calculator.Config{
		Meta:      meta,
		VRAMFree:  vramFree,
		Overhead:  *ovMiB << 20,
		OverheadP: *ovPct / 100,
	})

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"target":   tgt.Display,
			"hardware": hw,
			"model":    meta,
			"result":   res,
		})
	}
	name := tgt.Display
	if meta.Name != "" && !strings.EqualFold(meta.Name, name) {
		name = meta.Name + "  (" + tgt.Display + ")"
	}
	fmt.Print(render.Report(hw, vramTotal, vramFree, name, meta, tgt.Shards, res))
	return nil
}
