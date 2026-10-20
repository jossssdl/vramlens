# vramlens

**English** · [Español](README.es.md)

> VRAM doctor, KV-cache calculator and JSON-determinism benchmark for local LLMs. Know whether a GGUF model fits your GPU — and at which context length it starts spilling to the CPU — **before** you download it, then measure how reliably it produces structured output.

## Table of contents

- [Why this exists](#why-this-exists)
- [What it does](#what-it-does)
- [Who is it for](#who-is-it-for)
- [How it works](#how-it-works)
- [Install](#install)
- [Usage: `check`](#usage-check)
- [Usage: `bench`](#usage-bench)
- [Accuracy and limitations](#accuracy-and-limitations)
- [Project layout](#project-layout)
- [Roadmap](#roadmap)
- [Development](#development)
- [License](#license)

## Why this exists

Running models locally (Ollama, llama.cpp, LM Studio) usually goes like this: you download 5–40 GB, load the model, and only then discover that it doesn't fit, or that it fits at 4k context but crawls at 32k because the KV cache pushed layers onto the CPU. Model cards list the file size, but not what the *context window* costs, and that cost is often as large as the weights themselves.

A second problem shows up once the model runs: applications that need **structured output** (JSON for tools, extraction, agents) break when a model occasionally returns truncated JSON, wraps it in markdown, or breaks the schema. Few people measure that reliability, or how much it varies between runs.

`vramlens` answers two questions up front:

1. **"With my hardware, how much context can I really run on the GPU?"**
2. **"How fast is this model, and how often does it return valid JSON?"**

## What it does

**`vramlens check`** — memory diagnosis
- Detects total/free RAM and VRAM (NVIDIA via `nvidia-smi`; Apple Silicon unified memory via `sysctl`; RAM via the OS).
- Reads GGUF headers **without downloading the model**, from a local file or straight from Hugging Face using HTTP `Range` requests (typically ~64 KiB transferred).
- Calculates exact weight size + KV-cache growth for FP16, Q8_0 and Q4_0 caches at 2k, 4k, 8k, 16k, 32k and 128k tokens.
- Finds the offload point: the largest context that still fits entirely in VRAM, and the percentage of layers on GPU beyond it.

**`vramlens bench`** — speed and structured-output reliability
- Concurrent client for **Ollama** and **OpenAI-compatible** servers (llama.cpp server, LM Studio, vLLM…).
- Sends the same prompt N times, demanding a JSON Schema, and measures **Time-To-First-Token (TTFT)**, **tokens/second (TPS)** and the **percentage of structurally valid JSON** (parses *and* satisfies the schema).
- Reports a **determinism score**: how many distinct outputs the model produced and the share of the most frequent one.
- Live terminal UI (bubbletea) with progress, per-run results and running statistics; plain and `--json` output for scripts and CI.

## Who is it for

- People running local models with Ollama, llama.cpp or LM Studio on consumer GPUs (6–24 GB) who want to pick the right quantization and context size.
- Developers building apps on local LLMs who need predictable memory, latency and valid structured output.
- Anyone comparing quantizations (`Q4_K_M` vs `Q6_K`…), cache types or models before downloading several gigabytes.

## How it works

### `check`

1. **Resolve** the target: a local `.gguf`, a `huggingface.co` URL, or `org/repo[:QUANT]` (the Hub API lists the repo's GGUF files; multi-part shards are grouped).
2. **Read the header** (GGUF v2/v3). Data is fetched in growing chunks starting at 64 KiB, big arrays are skipped, and reading stops as soon as the architecture hyperparameters are known (the tokenizer, which can be megabytes, is never downloaded). The total weight size comes from `Content-Range`.
3. **Extract**: layers, embedding size, attention heads, KV heads (scalar or per-layer), head dimensions, training context and quantization.
4. **Compute**:
   ```
   KV bytes = Σ over layers [ kv_heads × (key_len + value_len) ] × context × bytes_per_element
   bytes_per_element:  FP16 = 2 · Q8_0 = 34/32 · Q4_0 = 18/32
   total = weights + KV cache + runtime overhead
   max context in VRAM = (free VRAM − weights − overhead) / (KV bytes per token)
   ```
   When the total exceeds free VRAM, layers are assumed to be offloaded evenly and the report shows the share that stays on GPU.

### `bench`

1. Builds one streaming chat request per run: Ollama `/api/chat` with `format` = your schema, or OpenAI-style `/v1/chat/completions` with `response_format: json_schema`.
2. Runs N requests with a pool of C workers.
3. **TTFT** is the time from sending the request to the first streamed token (reasoning tokens of "thinking" models count). **TPS** uses the server's own token count and decode time when it reports them (Ollama `eval_count`/`eval_duration`, OpenAI `usage`), and falls back to streamed chunks otherwise.
4. The answer must be a **single JSON document** (no trailing text, no markdown fences) and is validated by a built-in JSON Schema validator (types, required, enum, const, nested objects/arrays, min/max, pattern, `anyOf`/`oneOf`/`allOf`, local `$ref`).
5. **Determinism** compares outputs after JSON normalization, so formatting-only differences are not counted. Use `--temperature 0 --seed N` and compare with `-c 1` vs. higher concurrency to see whether batching changes results.

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/jossssdl/vramlens.git
cd vramlens
go build -o vramlens ./cmd/vramlens      # vramlens.exe on Windows
```

`vramlens` is a terminal tool: run it from a shell, not by double-clicking.

## Usage: `check`

```bash
vramlens check bartowski/Meta-Llama-3.1-8B-Instruct-GGUF:Q4_K_M
vramlens check bartowski/Meta-Llama-3.1-8B-Instruct-GGUF --quant Q6_K
vramlens check ./models/llama-3.1-8b-q4_k_m.gguf
vramlens check <target> --vram-gb 6        # simulate a smaller GPU
vramlens check <target> --json             # machine-readable output
```

| Flag | Meaning |
|---|---|
| `--file <path>` | Exact `.gguf` path inside a Hugging Face repo |
| `--quant <name>` | Pick the GGUF whose name contains this string (e.g. `Q4_K_M`) |
| `--vram-gb <n>` | Override free VRAM instead of detecting it |
| `--overhead-mib <n>` | Fixed runtime/compute-buffer reserve (default `512`) |
| `--overhead-pct <n>` | Extra reserve as % of weights (default `5`) |
| `--json` | JSON output |

Set `HF_TOKEN` for gated or private repositories. If a repo contains several GGUF files and you don't pick one, `vramlens` lists them.

Example (RTX 5060 Laptop, 8 GB):

```
KV cache vs. VRAM
  context   FP16                    Q8_0                    Q4_0
  2k        5.56 GiB GPU            5.44 GiB GPU            5.38 GiB GPU
  32k       9.31 GiB 78% GPU        7.44 GiB GPU            6.44 GiB GPU
  128k      21.31 GiB 31% GPU       13.81 GiB 50% GPU       9.81 GiB 75% GPU

CPU offload threshold
  FP16  fits fully in VRAM up to 18276 tokens; beyond that layers spill to CPU
  Q8_0  fits fully in VRAM up to 34403 tokens; beyond that layers spill to CPU
  Q4_0  fits fully in VRAM up to 64983 tokens; beyond that layers spill to CPU
```

## Usage: `bench`

```bash
# Ollama (default endpoint http://localhost:11434)
vramlens bench --model llama3.1:8b -n 20

# llama.cpp server / LM Studio / vLLM (OpenAI-compatible)
vramlens bench --provider openai --endpoint http://localhost:8080 --model local -n 20 -c 4

# Your own schema and prompt, deterministic sampling
vramlens bench --model llama3.1:8b --schema invoice.schema.json --prompt-file prompt.txt \
               --temperature 0 --seed 42 -n 30

# Scripts / CI
vramlens bench --model llama3.1:8b -n 10 --json > result.json
```

| Flag | Meaning |
|---|---|
| `--provider <ollama\|openai>` | Wire protocol (default `ollama`) |
| `--endpoint <url>` | Base URL (default `:11434` for Ollama, `:8080` for OpenAI-compatible) |
| `--model <name>` | Model name as the server knows it (required) |
| `--prompt` / `--prompt-file` | Prompt text or file (default: a small extraction task) |
| `--schema <path>` | JSON Schema the output must satisfy (default: built-in demo schema) |
| `-n`, `-c` | Number of requests (default `10`) and concurrency (default `1`) |
| `--temperature`, `--seed` | Sampling controls (temperature default `0`) |
| `--num-ctx <n>` | Ollama only: context window; lower it if the model fails to load |
| `--no-think` | Ollama only: disable reasoning output for thinking models |
| `--timeout <d>` | Per-request timeout (default `2m`) |
| `--json` / `--no-tui` | Machine-readable output / plain text instead of the live TUI |

`OPENAI_API_KEY` is sent as a bearer token if set. The live TUI is used automatically on an interactive terminal; press `q` to cancel (partial results are kept).

## Accuracy and limitations

These are estimates and measurements, not guarantees:

- **`check`**: runtime overhead (compute buffers, CUDA context) varies by backend and batch size; tune it with `--overhead-mib` / `--overhead-pct`.
- Sizing assumes a standard attention KV cache. Recurrent/SSM layers and sliding-window caches (e.g. Mamba, some Gemma layers) are not modelled specifically.
- GPU detection supports NVIDIA and Apple Silicon. AMD/Intel GPUs are not detected yet; use `--vram-gb`.
- Windows with NVIDIA is the most tested platform; Linux and macOS code paths are implemented but less exercised.
- With several NVIDIA GPUs, free VRAM is summed, which assumes an even tensor split.
- **`bench`**: results depend on your server's load, quantization and settings. A "thinking" model spends most of its tokens reasoning, which inflates TTFT and token counts unless you disable it (`--no-think`). If a server cannot load the model (e.g. out of memory), runs are reported as errors and counted as invalid. `check` can tell you in advance if that will happen.
- Schema enforcement is only as strict as the server's constrained decoding; `bench` validates what actually came back, independent of that.

## Project layout

```
cmd/vramlens/          CLI entry point (check, bench)
internal/hardware/     RAM / VRAM detection
internal/gguf/         GGUF header parser, local + HTTP Range sources, Hugging Face resolver
internal/calculator/   Weights + KV-cache math and offload threshold
internal/bench/        Ollama / OpenAI-compatible streaming client, JSON Schema validator, statistics
internal/tui/          Live benchmark UI (bubbletea)
internal/render/       Terminal reports and bars (lipgloss)
```

## Roadmap

- [x] `check`: hardware detection, GGUF header reader, KV-cache calculator
- [x] `bench`: concurrent client, TTFT / TPS / JSON-schema validity, determinism score
- [x] Live benchmark TUI
- [ ] AMD / Intel GPU detection
- [ ] Interactive `check` view to compare several quantizations side by side
- [ ] Prebuilt release binaries

## Development

```bash
go vet ./... && go test ./... && go build ./...
```

## License

MIT — see [LICENSE](LICENSE).
