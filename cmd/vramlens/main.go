// Command vramlens diagnoses VRAM needs of local LLMs from their GGUF headers
// and benchmarks their speed and structured-output reliability.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	xterm "github.com/charmbracelet/x/term"

	"github.com/jossssdl/vramlens/internal/probe"
	"github.com/jossssdl/vramlens/internal/render"
	"github.com/jossssdl/vramlens/internal/tui"
)

const usage = `vramlens - VRAM doctor, KV-cache calculator and JSON benchmark for local LLMs

Usage:
  vramlens                                     interactive menu
  vramlens check <huggingface-repo[:QUANT] | file.gguf | URL> [flags]
  vramlens bench --model <name> [flags]        (see: vramlens bench --help)
  vramlens --version

Flags for check:
  --file <name>        exact .gguf path inside a Hugging Face repo
  --quant <Q4_K_M>     select the GGUF whose name contains this string
  --vram-gb <n>        override free VRAM (GiB) instead of detecting it
  --overhead-mib <n>   fixed runtime/compute-buffer reserve (default 512)
  --overhead-pct <n>   extra reserve as % of weights (default 5)
  --json               machine-readable output

Environment:
  HF_TOKEN             token for gated/private Hugging Face repos
  NO_COLOR             disable colors
`

func main() {
	if len(os.Args) < 2 {
		// Launched without arguments (e.g. double-click): open the interactive menu.
		if isTerminal() {
			if err := tui.RunApp(); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			return
		}
		fmt.Print(usage)
		return
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "-v", "--version", "version":
		fmt.Println(versionString())
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

// isTerminal reports whether stdout is an interactive terminal.
func isTerminal() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// termWidth returns the terminal width, or 100 when unknown (pipes).
func termWidth() int {
	if w, _, err := xterm.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 100
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

	res, err := probe.Check(probe.CheckParams{
		Target: target, File: *file, Quant: *quant, VRAMGB: *vramGB,
		OverheadMiB: *ovMiB, OverheadPct: *ovPct,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	fmt.Println(render.CheckReport(res, *vramGB > 0, termWidth()))
	return nil
}
