package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/jossssdl/vramlens/internal/bench"
	"github.com/jossssdl/vramlens/internal/render"
	"github.com/jossssdl/vramlens/internal/tui"
)

const benchUsage = `vramlens bench - latency, throughput and JSON-schema reliability of a local LLM server

Usage:
  vramlens bench --model <name> [flags]

Flags:
  --provider <ollama|openai>  wire protocol (default ollama); "openai" covers llama.cpp server, LM Studio, vLLM...
  --endpoint <url>            base URL (default http://localhost:11434 for ollama, http://localhost:8080 for openai)
  --model <name>              model name as the server knows it (required)
  --quick                     fast preset: 3 runs, 2048 context, no reasoning, 256 max tokens
  --prompt <text>             user prompt (default: a small structured-extraction task)
  --prompt-file <path>        read the prompt from a file
  --schema <path>             JSON Schema file the output must satisfy (default: built-in demo schema)
  -n <int>                    number of requests (default 10)
  -c <int>                    concurrent requests (default 1)
  --max-tokens <int>          cap generated tokens per request (too low truncates the JSON)
  --temperature <float>       sampling temperature (default 0)
  --seed <int>                fixed sampling seed
  --num-ctx <int>             Ollama only: context window in tokens (lower it if the model fails to load)
  --no-think                  Ollama only: disable reasoning output for thinking models
  --timeout <duration>        per-request timeout (default 2m)
  --json                      print machine-readable results (no TUI)
  --no-tui                    plain output instead of the live TUI

Environment:
  OPENAI_API_KEY              bearer token sent to the server, if it needs one
  NO_COLOR                    disable colors
`

func runBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	provider := fs.String("provider", "ollama", "")
	endpoint := fs.String("endpoint", "", "")
	model := fs.String("model", "", "")
	quick := fs.Bool("quick", false, "")
	prompt := fs.String("prompt", "", "")
	promptFile := fs.String("prompt-file", "", "")
	schemaFile := fs.String("schema", "", "")
	n := fs.Int("n", 10, "")
	c := fs.Int("c", 1, "")
	maxTok := fs.Int("max-tokens", 0, "")
	temp := fs.Float64("temperature", 0, "")
	seed := fs.Int("seed", 0, "")
	numCtx := fs.Int("num-ctx", 0, "")
	noThink := fs.Bool("no-think", false, "")
	timeout := fs.Duration("timeout", 2*time.Minute, "")
	asJSON := fs.Bool("json", false, "")
	noTUI := fs.Bool("no-tui", false, "")
	fs.Usage = func() { fmt.Fprint(os.Stderr, benchUsage) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *model == "" {
		return errors.New("--model is required")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// --quick supplies defaults for anything the user did not set explicitly.
	if *quick {
		if !set["n"] {
			*n = 3
		}
		if !set["num-ctx"] {
			*numCtx = 2048
		}
		if !set["max-tokens"] {
			*maxTok = 256
		}
		if !set["no-think"] {
			*noThink = true
		}
	}

	cfg := bench.Config{
		Provider:    bench.Provider(*provider),
		Endpoint:    *endpoint,
		APIKey:      os.Getenv("OPENAI_API_KEY"),
		Model:       *model,
		Prompt:      *prompt,
		N:           *n,
		Concurrency: *c,
		Temperature: *temp,
		NumCtx:      *numCtx,
		NoThink:     *noThink,
		MaxTokens:   *maxTok,
		Timeout:     *timeout,
	}
	switch cfg.Provider {
	case bench.Ollama:
		if cfg.Endpoint == "" {
			cfg.Endpoint = "http://localhost:11434"
		}
	case bench.OpenAI:
		if cfg.Endpoint == "" {
			cfg.Endpoint = "http://localhost:8080"
		}
	default:
		return fmt.Errorf("unknown provider %q (use ollama or openai)", *provider)
	}
	if set["seed"] {
		cfg.Seed = seed
	}
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		cfg.Prompt = string(b)
	}
	if cfg.Prompt == "" {
		cfg.Prompt = bench.DefaultPrompt
	}
	raw := []byte(bench.DefaultSchemaJSON)
	if *schemaFile != "" {
		var err error
		if raw, err = os.ReadFile(*schemaFile); err != nil {
			return err
		}
	}
	if err := json.Unmarshal(raw, &cfg.Schema); err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if isTerminal() && !*asJSON && !*noTUI {
		return tui.RunBench(ctx, cfg)
	}

	// Plain mode: one progress line on stderr, final report on stdout.
	start := time.Now()
	var done int
	progress := func(last bench.Sample) {
		if *asJSON {
			return
		}
		done++
		eta := time.Duration(0)
		if done < cfg.N {
			eta = time.Since(start) / time.Duration(done) * time.Duration(cfg.N-done)
		}
		fmt.Fprintf(os.Stderr, "\r%s %d/%d  elapsed %s  ETA %s  last: %s, %.1f t/s   ",
			render.PctBar(100*float64(done)/float64(cfg.N), 20), done, cfg.N,
			render.Clock(time.Since(start)), render.Clock(eta), render.Dur(last.TTFT), last.TPS)
	}
	samples, err := bench.Run(ctx, cfg, progress)
	if !*asJSON {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	stats := bench.Summarize(samples)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if e := enc.Encode(map[string]any{
			"config": map[string]any{
				"provider": cfg.Provider, "endpoint": cfg.Endpoint, "model": cfg.Model,
				"n": cfg.N, "concurrency": cfg.Concurrency, "temperature": cfg.Temperature,
				"num_ctx": cfg.NumCtx, "max_tokens": cfg.MaxTokens, "no_think": cfg.NoThink,
			},
			"stats":   stats,
			"samples": samples,
		}); e != nil {
			return e
		}
		return err
	}
	fmt.Println(render.BenchReport(cfg, samples, stats, termWidth()))
	return err
}
