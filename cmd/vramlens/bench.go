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

	"vramlens/internal/bench"
	"vramlens/internal/render"
	"vramlens/internal/tui"
)

const benchUsage = `vramlens bench - latency, throughput and JSON-schema reliability of a local LLM server

Usage:
  vramlens bench --model <name> [flags]

Flags:
  --provider <ollama|openai>  wire protocol (default ollama); "openai" covers llama.cpp server, LM Studio, vLLM...
  --endpoint <url>            base URL (default http://localhost:11434 for ollama, http://localhost:8080 for openai)
  --model <name>              model name as the server knows it (required)
  --prompt <text>             user prompt (default: a small structured-extraction task)
  --prompt-file <path>        read the prompt from a file
  --schema <path>             JSON Schema file the output must satisfy (default: built-in demo schema)
  -n <int>                    number of requests (default 10)
  -c <int>                    concurrent requests (default 1)
  --temperature <float>       sampling temperature (default 0)
  --seed <int>                fixed sampling seed
  --num-ctx <int>             Ollama only: context window in tokens (lower it if the model fails to load)
  --no-think                  Ollama only: disable reasoning output for thinking models
  --timeout <duration>        per-request timeout (default 2m)
  --json                      print machine-readable results (no TUI)
  --no-tui                    plain output instead of the live TUI

Environment:
  OPENAI_API_KEY              bearer token sent to the server, if it needs one
`

const defaultPrompt = "Extract the person from this text: 'Marta Ruiz is a 34 year old cardiologist who enjoys climbing, chess and cooking.'"

const defaultSchema = `{
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "age": {"type": "integer", "minimum": 0},
    "profession": {"type": "string"},
    "hobbies": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["name", "age", "profession", "hobbies"],
  "additionalProperties": false
}`

func runBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	provider := fs.String("provider", "ollama", "")
	endpoint := fs.String("endpoint", "", "")
	model := fs.String("model", "", "")
	prompt := fs.String("prompt", "", "")
	promptFile := fs.String("prompt-file", "", "")
	schemaFile := fs.String("schema", "", "")
	n := fs.Int("n", 10, "")
	c := fs.Int("c", 1, "")
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
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "seed" {
			cfg.Seed = seed
		}
	})
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		cfg.Prompt = string(b)
	}
	if cfg.Prompt == "" {
		cfg.Prompt = defaultPrompt
	}
	raw := []byte(defaultSchema)
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

	st, _ := os.Stdout.Stat()
	interactive := st != nil && st.Mode()&os.ModeCharDevice != 0
	if interactive && !*asJSON && !*noTUI {
		_, err := tui.RunBench(ctx, cfg)
		return err
	}

	samples, err := bench.Run(ctx, cfg, func(s bench.Sample) {
		if !*asJSON {
			fmt.Fprintf(os.Stderr, "\rrun %d/%d", s.Index+1, cfg.N)
		}
	})
	if !*asJSON {
		fmt.Fprintln(os.Stderr)
	}
	stats := bench.Summarize(samples)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if e := enc.Encode(map[string]any{
			"config": map[string]any{
				"provider": cfg.Provider, "endpoint": cfg.Endpoint, "model": cfg.Model,
				"n": cfg.N, "concurrency": cfg.Concurrency, "temperature": cfg.Temperature,
			},
			"stats":   stats,
			"samples": samples,
		}); e != nil {
			return e
		}
		return err
	}
	fmt.Print(render.BenchRows(samples, cfg.N), "\n", render.BenchSummary(cfg, stats))
	return err
}
