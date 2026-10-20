# vramlens

[English](README.md) · **Español**

> Doctor de VRAM, calculadora de KV-Cache y benchmark de determinismo JSON para LLMs locales. Averigua si un modelo GGUF cabe en tu GPU —y a partir de qué contexto empieza a desbordarse a la CPU— **antes** de descargarlo, y mide después con qué fiabilidad produce salida estructurada.

## Contenido

- [Por qué existe](#por-qué-existe)
- [Qué hace](#qué-hace)
- [Para quién es](#para-quién-es)
- [Cómo funciona](#cómo-funciona)
- [Instalación](#instalación)
- [Uso: `check`](#uso-check)
- [Uso: `bench`](#uso-bench)
- [Precisión y limitaciones](#precisión-y-limitaciones)
- [Estructura del proyecto](#estructura-del-proyecto)
- [Hoja de ruta](#hoja-de-ruta)
- [Desarrollo](#desarrollo)
- [Licencia](#licencia)

## Por qué existe

Usar modelos locales (Ollama, llama.cpp, LM Studio) suele ser así: descargas de 5 a 40 GB, cargas el modelo y solo entonces descubres que no cabe, o que cabe con 4k de contexto pero se arrastra con 32k porque la KV-Cache empujó capas a la CPU. Las fichas de los modelos muestran el tamaño del archivo, pero no lo que cuesta la *ventana de contexto*, que muchas veces pesa tanto como los propios pesos.

Hay un segundo problema cuando el modelo ya corre: las aplicaciones que necesitan **salida estructurada** (JSON para herramientas, extracción, agentes) fallan cuando el modelo devuelve de vez en cuando un JSON cortado, envuelto en markdown o que rompe el schema. Pocos miden esa fiabilidad ni cuánto varía entre ejecuciones.

`vramlens` responde de antemano dos preguntas:

1. **«Con mi hardware, ¿cuánto contexto puedo ejecutar realmente en la GPU?»**
2. **«¿Qué tan rápido es este modelo y con qué frecuencia devuelve JSON válido?»**

## Qué hace

**`vramlens check`** — diagnóstico de memoria
- Detecta RAM y VRAM totales/libres (NVIDIA con `nvidia-smi`; memoria unificada de Apple Silicon con `sysctl`; RAM mediante el sistema operativo).
- Lee cabeceras GGUF **sin descargar el modelo**, de un archivo local o directamente de Hugging Face con peticiones HTTP `Range` (normalmente ~64 KiB transferidos).
- Calcula el peso exacto de los pesos + el crecimiento de la KV-Cache en FP16, Q8_0 y Q4_0 para 2k, 4k, 8k, 16k, 32k y 128k tokens.
- Encuentra el punto de offload: el mayor contexto que aún cabe completo en VRAM y el porcentaje de capas en GPU a partir de ahí.

**`vramlens bench`** — velocidad y fiabilidad de salida estructurada
- Cliente concurrente para servidores **Ollama** y **compatibles con OpenAI** (servidor de llama.cpp, LM Studio, vLLM…).
- Envía el mismo prompt N veces exigiendo un JSON Schema y mide **Time-To-First-Token (TTFT)**, **tokens/segundo (TPS)** y el **porcentaje de JSON estructuralmente válido** (que se parsee *y* cumpla el schema).
- Informa una **puntuación de determinismo**: cuántas salidas distintas produjo el modelo y qué porcentaje representa la más frecuente.
- Interfaz de terminal en vivo (bubbletea) con progreso, resultado por ejecución y estadísticas acumuladas; salida de texto plano y `--json` para scripts y CI.

## Para quién es

- Personas que ejecutan modelos locales con Ollama, llama.cpp o LM Studio en GPU de consumo (6–24 GB) y quieren elegir bien la cuantización y el tamaño de contexto.
- Desarrolladores que construyen aplicaciones sobre LLMs locales y necesitan memoria y latencia predecibles y salida estructurada válida.
- Cualquiera que compare cuantizaciones (`Q4_K_M` vs `Q6_K`…), tipos de cache o modelos antes de bajar varios gigabytes.

## Cómo funciona

### `check`

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

### `bench`

1. Construye una petición de chat en streaming por ejecución: `/api/chat` de Ollama con `format` = tu schema, o `/v1/chat/completions` estilo OpenAI con `response_format: json_schema`.
2. Lanza N peticiones con un pool de C trabajadores.
3. El **TTFT** es el tiempo desde que se envía la petición hasta el primer token recibido (los tokens de razonamiento de los modelos «thinking» cuentan). El **TPS** usa el conteo de tokens y el tiempo de decodificación que reporta el propio servidor (Ollama `eval_count`/`eval_duration`, OpenAI `usage`) y, si no los da, los fragmentos recibidos.
4. La respuesta debe ser **un único documento JSON** (sin texto adicional ni bloques markdown) y se valida con un validador de JSON Schema incorporado (tipos, required, enum, const, objetos/arrays anidados, mínimos/máximos, pattern, `anyOf`/`oneOf`/`allOf`, `$ref` local).
5. El **determinismo** compara las salidas tras normalizar el JSON, de modo que las diferencias solo de formato no cuentan. Usa `--temperature 0 --seed N` y compara `-c 1` con una concurrencia mayor para ver si el batching cambia los resultados.

## Instalación

Requiere Go 1.24+.

```bash
git clone https://github.com/jossssdl/vramlens.git
cd vramlens
go build -o vramlens ./cmd/vramlens      # vramlens.exe en Windows
```

### Menú interactivo

Ejecuta `vramlens` sin argumentos —o haz doble clic en `vramlens.exe`— para abrir un menú:

- **Diagnosticar un modelo**: escribe un repo de Hugging Face o la ruta de un `.gguf`. Si el repo tiene muchos archivos, eliges de una lista que ya indica cuáles caben en tu VRAM. El informe muestra barras de memoria por tamaño de contexto, una línea de tiempo del offload a CPU y una recomendación; se desplaza con las flechas.
- **Benchmark de un servidor**: elige Ollama o un servidor compatible con OpenAI, selecciona un modelo (se detecta solo desde el servidor) y un preset —**Quick** (por defecto), **Standard** o **Thorough**— o ajusta tú las ejecuciones, concurrencia, contexto, tokens máximos y razonamiento. Verás el progreso en vivo con tiempo transcurrido y ETA, estadísticas y tendencias acumuladas, y podrás guardar los resultados como JSON.

Arriba se muestra el estado actual de tu GPU y RAM. Todo está disponible también como comandos simples (abajo) para scripts y CI.

## Uso: `check`

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

Ejemplo (RTX 5060 Laptop, 8 GB):

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

## Uso: `bench`

```bash
# Ollama (endpoint por defecto http://localhost:11434)
vramlens bench --model llama3.1:8b -n 20

# Servidor de llama.cpp / LM Studio / vLLM (compatible con OpenAI)
vramlens bench --provider openai --endpoint http://localhost:8080 --model local -n 20 -c 4

# Tu propio schema y prompt, muestreo determinista
vramlens bench --model llama3.1:8b --schema factura.schema.json --prompt-file prompt.txt \
               --temperature 0 --seed 42 -n 30

# Scripts / CI
vramlens bench --model llama3.1:8b -n 10 --json > resultado.json
```

| Flag | Significado |
|---|---|
| `--provider <ollama\|openai>` | Protocolo (por defecto `ollama`) |
| `--endpoint <url>` | URL base (por defecto `:11434` en Ollama, `:8080` en compatibles con OpenAI) |
| `--model <nombre>` | Nombre del modelo en el servidor (obligatorio) |
| `--prompt` / `--prompt-file` | Texto del prompt o archivo (por defecto, una pequeña tarea de extracción) |
| `--schema <ruta>` | JSON Schema que debe cumplir la salida (por defecto, un schema de demostración) |
| `-n`, `-c` | Número de peticiones (por defecto `10`) y concurrencia (por defecto `1`) |
| `--temperature`, `--seed` | Control del muestreo (temperatura por defecto `0`) |
| `--quick` | Preset rápido: 3 ejecuciones, contexto 2048, sin razonamiento, 256 tokens máx. (los flags explícitos tienen prioridad) |
| `--max-tokens <n>` | Tope de tokens generados por petición (muy bajo trunca el JSON y cuenta como inválido) |
| `--num-ctx <n>` | Solo Ollama: ventana de contexto; redúcela si el modelo no carga |
| `--no-think` | Solo Ollama: desactiva el razonamiento en modelos «thinking» |
| `--timeout <d>` | Timeout por petición (por defecto `2m`) |
| `--json` / `--no-tui` | Salida legible por máquinas / texto plano en lugar de la TUI en vivo |

Si está definida, `OPENAI_API_KEY` se envía como bearer token. La TUI en vivo se usa automáticamente en una terminal interactiva; pulsa `q` para cancelar (se conservan los resultados parciales).

## Precisión y limitaciones

Son estimaciones y mediciones, no garantías:

- **`check`**: el overhead del runtime (compute buffers, contexto CUDA) varía según el backend y el tamaño de batch; ajústalo con `--overhead-mib` / `--overhead-pct`.
- El cálculo asume una KV-Cache de atención estándar. Las capas recurrentes/SSM y las cachés de ventana deslizante (p. ej. Mamba, algunas capas de Gemma) no se modelan de forma específica.
- La detección de GPU soporta NVIDIA y Apple Silicon. Las GPU AMD/Intel aún no se detectan; usa `--vram-gb`.
- Windows con NVIDIA es la plataforma más probada; las rutas de Linux y macOS están implementadas pero menos ejercitadas.
- Con varias GPU NVIDIA se suma la VRAM libre, lo que supone un reparto uniforme de tensores.
- **`bench`**: los resultados dependen de la carga, la cuantización y la configuración de tu servidor. Un modelo «thinking» gasta casi todos sus tokens razonando, lo que infla el TTFT y el conteo de tokens salvo que lo desactives (`--no-think`). Si el servidor no puede cargar el modelo (p. ej. sin memoria), las ejecuciones se reportan como error y cuentan como inválidas. `check` puede avisarte de antemano.
- La garantía del schema solo es tan estricta como la decodificación restringida del servidor; `bench` valida lo que realmente llegó, con independencia de eso.

## Estructura del proyecto

```
cmd/vramlens/          Punto de entrada de la CLI (check, bench)
internal/hardware/     Detección de RAM / VRAM
internal/gguf/         Parser de cabeceras GGUF, fuentes local + HTTP Range, resolutor de Hugging Face
internal/calculator/   Cálculo de pesos + KV-Cache y umbral de offload
internal/bench/        Cliente streaming Ollama / compatible con OpenAI, validador de JSON Schema, estadísticas
internal/tui/          Menú interactivo e interfaz en vivo del benchmark (bubbletea)
internal/render/       Informes y barras en terminal (lipgloss)
```

## Hoja de ruta

- [x] `check`: detección de hardware, lector de cabeceras GGUF, calculadora de KV-Cache
- [x] `bench`: cliente concurrente, TTFT / TPS / validez según JSON Schema, puntuación de determinismo
- [x] TUI en vivo del benchmark
- [ ] Detección de GPU AMD / Intel
- [ ] Vista interactiva de `check` para comparar varias cuantizaciones lado a lado
- [ ] Binarios precompilados en releases

## Desarrollo

```bash
go vet ./... && go test ./... && go build ./...
```

## Licencia

MIT — ver [LICENSE](LICENSE).
