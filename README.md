# vramlens

**English** · [Español](#español)

> VRAM doctor and KV-cache calculator for local LLMs. Know whether a GGUF model fits your GPU — and at which context length it starts spilling to the CPU — **before** you download it.

---

## English

### Why this exists

Running models locally (Ollama, llama.cpp, LM Studio) usually goes like this: you download 5–40 GB, load the model, and only then discover that it doesn't fit, or that it fits at 4k context but crawls at 32k because the KV cache pushed layers onto the CPU. Model cards list the file size, but not what the *context window* costs, and that cost is often as large as the weights themselves.

`vramlens` answers one question up front: **"with my hardware, how much context can I really run on the GPU?"**

### What it does

- **Detects your hardware**: total/free RAM and VRAM (NVIDIA via `nvidia-smi`; Apple Silicon unified memory via `sysctl`; RAM via the OS).
- **Reads GGUF headers without downloading the model**, from a local file or straight from Hugging Face using HTTP `Range` requests (typically ~64 KiB transferred).
- **Calculates memory**: exact weight size + KV-cache growth for FP16, Q8_0 and Q4_0 caches at 2k, 4k, 8k, 16k, 32k and 128k tokens.
- **Finds the offload point**: the largest context that still fits entirely in VRAM, and the percentage of layers on GPU beyond it.

### Who is it for

- People running local models with Ollama, llama.cpp or LM Studio on consumer GPUs (6–24 GB) who want to pick the right quantization and context size.
- Developers building apps on local LLMs who need predictable memory and latency.
- Anyone comparing quantizations (`Q4_K_M` vs `Q6_K`…) and cache types before downloading several gigabytes.

### How it works

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

### Install

Requires Go 1.22+.

```bash
git clone https://github.com/jossssdl/vramlens.git
cd vramlens
go build -o vramlens ./cmd/vramlens      # vramlens.exe on Windows
```

`vramlens` is a terminal tool: run it from a shell, not by double-clicking.

### Usage

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

### Accuracy and limitations

These are estimates, not guarantees:

- Runtime overhead (compute buffers, CUDA context) varies by backend and batch size; tune it with `--overhead-mib` / `--overhead-pct`.
- Sizing assumes a standard attention KV cache. Recurrent/SSM layers and sliding-window caches (e.g. Mamba, some Gemma layers) are not modelled specifically.
- GPU detection supports NVIDIA and Apple Silicon. AMD/Intel GPUs are not detected yet; use `--vram-gb`.
- Windows with NVIDIA is the most tested platform; Linux and macOS code paths are implemented but less exercised.
- With several NVIDIA GPUs, free VRAM is summed, which assumes an even tensor split.

### Project layout

```
cmd/vramlens/          CLI entry point
internal/hardware/     RAM / VRAM detection
internal/gguf/         GGUF header parser, local + HTTP Range sources, Hugging Face resolver
internal/calculator/   Weights + KV-cache math and offload threshold
internal/render/       Terminal report (lipgloss)
```

### Roadmap

- [x] `check`: hardware detection, GGUF header reader, KV-cache calculator
- [ ] `bench`: concurrent client for Ollama / OpenAI-compatible servers that measures Time-To-First-Token, tokens/second and **JSON-Schema validity rate** over N runs
- [ ] Interactive TUI (bubbletea) with memory bars and comparison tables

### Development

```bash
go vet ./... && go test ./... && go build ./...
```

### License

MIT — see [LICENSE](LICENSE).

---

## Español

> Doctor de VRAM y calculadora de KV-Cache para LLMs locales. Averigua si un modelo GGUF cabe en tu GPU —y a partir de qué contexto empieza a desbordarse a la CPU— **antes** de descargarlo.

### Por qué existe

Usar modelos locales (Ollama, llama.cpp, LM Studio) suele ser así: descargas de 5 a 40 GB, cargas el modelo y solo entonces descubres que no cabe, o que cabe con 4k de contexto pero se arrastra con 32k porque la KV-Cache empujó capas a la CPU. Las fichas de los modelos muestran el tamaño del archivo, pero no lo que cuesta la *ventana de contexto*, que muchas veces pesa tanto como los propios pesos.

`vramlens` responde de antemano una pregunta: **«con mi hardware, ¿cuánto contexto puedo ejecutar realmente en la GPU?»**

### Qué hace

- **Detecta tu hardware**: RAM y VRAM totales/libres (NVIDIA con `nvidia-smi`; memoria unificada de Apple Silicon con `sysctl`; RAM mediante el sistema operativo).
- **Lee cabeceras GGUF sin descargar el modelo**, de un archivo local o directamente de Hugging Face con peticiones HTTP `Range` (normalmente ~64 KiB transferidos).
- **Calcula la memoria**: peso exacto de los pesos + crecimiento de la KV-Cache en FP16, Q8_0 y Q4_0 para 2k, 4k, 8k, 16k, 32k y 128k tokens.
- **Encuentra el punto de offload**: el mayor contexto que aún cabe completo en VRAM y el porcentaje de capas en GPU a partir de ahí.

### Para quién es

- Personas que ejecutan modelos locales con Ollama, llama.cpp o LM Studio en GPU de consumo (6–24 GB) y quieren elegir bien la cuantización y el tamaño de contexto.
- Desarrolladores que construyen aplicaciones sobre LLMs locales y necesitan memoria y latencia predecibles.
- Cualquiera que compare cuantizaciones (`Q4_K_M` vs `Q6_K`…) y tipos de cache antes de bajar varios gigabytes.

### Cómo funciona

1. **Resuelve** el destino: un `.gguf` local, una URL de `huggingface.co` o `org/repo[:QUANT]` (la API del Hub lista los GGUF del repo; los archivos divididos en partes se agrupan).
2. **Lee la cabecera** (GGUF v2/v3). Los datos se piden en bloques crecientes desde 64 KiB, los arrays grandes se saltan y la lectura se detiene en cuanto se conocen los hiperparámetros de la arquitectura (el tokenizer, que puede ocupar megabytes, nunca se descarga). El tamaño total de los pesos sale de `Content-Range`.
3. **Extrae**: capas, dimensión de embeddings, cabezales de atención, cabezales KV (escalar o por capa), dimensiones de cabezal, contexto de entrenamiento y cuantización.
4. **Calcula**:
   ```
   bytes KV = Σ por capa [ kv_heads × (key_len + value_len) ] × contexto × bytes_por_elemento
   bytes_por_elemento:  FP16 = 2 · Q8_0 = 34/32 · Q4_0 = 18/32
   total = pesos + KV-Cache + overhead del runtime
   contexto máximo en VRAM = (VRAM libre − pesos − overhead) / (bytes KV por token)
   ```
   Si el total supera la VRAM libre, se asume que las capas se descargan de forma uniforme y el informe muestra qué porcentaje queda en GPU.

### Instalación

Requiere Go 1.22+.

```bash
git clone https://github.com/jossssdl/vramlens.git
cd vramlens
go build -o vramlens ./cmd/vramlens      # vramlens.exe en Windows
```

`vramlens` es una herramienta de terminal: ejecútala desde una consola, no con doble clic.

### Uso

```bash
vramlens check bartowski/Meta-Llama-3.1-8B-Instruct-GGUF:Q4_K_M
vramlens check bartowski/Meta-Llama-3.1-8B-Instruct-GGUF --quant Q6_K
vramlens check ./models/llama-3.1-8b-q4_k_m.gguf
vramlens check <destino> --vram-gb 6        # simula una GPU más pequeña
vramlens check <destino> --json             # salida legible por máquinas
```

| Flag | Significado |
|---|---|
| `--file <ruta>` | Ruta exacta del `.gguf` dentro de un repo de Hugging Face |
| `--quant <nombre>` | Elige el GGUF cuyo nombre contenga esta cadena (p. ej. `Q4_K_M`) |
| `--vram-gb <n>` | Sustituye la VRAM libre detectada |
| `--overhead-mib <n>` | Reserva fija de runtime/compute buffers (por defecto `512`) |
| `--overhead-pct <n>` | Reserva extra como % de los pesos (por defecto `5`) |
| `--json` | Salida JSON |

Define `HF_TOKEN` para repositorios privados o con acceso restringido. Si un repo contiene varios GGUF y no eliges uno, `vramlens` los lista.

### Precisión y limitaciones

Son estimaciones, no garantías:

- El overhead del runtime (compute buffers, contexto CUDA) varía según el backend y el tamaño de batch; ajústalo con `--overhead-mib` / `--overhead-pct`.
- El cálculo asume una KV-Cache de atención estándar. Las capas recurrentes/SSM y las cachés de ventana deslizante (p. ej. Mamba, algunas capas de Gemma) no se modelan de forma específica.
- La detección de GPU soporta NVIDIA y Apple Silicon. Las GPU AMD/Intel aún no se detectan; usa `--vram-gb`.
- Windows con NVIDIA es la plataforma más probada; las rutas de Linux y macOS están implementadas pero menos ejercitadas.
- Con varias GPU NVIDIA se suma la VRAM libre, lo que supone un reparto uniforme de tensores.

### Estructura del proyecto

```
cmd/vramlens/          Punto de entrada de la CLI
internal/hardware/     Detección de RAM / VRAM
internal/gguf/         Parser de cabeceras GGUF, fuentes local + HTTP Range, resolutor de Hugging Face
internal/calculator/   Cálculo de pesos + KV-Cache y umbral de offload
internal/render/       Informe en terminal (lipgloss)
```

### Hoja de ruta

- [x] `check`: detección de hardware, lector de cabeceras GGUF, calculadora de KV-Cache
- [ ] `bench`: cliente concurrente para servidores Ollama / compatibles con OpenAI que mide Time-To-First-Token, tokens/segundo y **tasa de validez según JSON Schema** en N ejecuciones
- [ ] TUI interactiva (bubbletea) con barras de memoria y tablas comparativas

### Desarrollo

```bash
go vet ./... && go test ./... && go build ./...
```

### Licencia

MIT — ver [LICENSE](LICENSE).
