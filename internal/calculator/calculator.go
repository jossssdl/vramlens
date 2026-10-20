// Package calculator estimates model weight and KV-cache memory and finds the
// context length at which inference stops fitting in VRAM.
package calculator

import (
	"math"

	"vramlens/internal/gguf"
)

// KVType is the storage precision of the KV cache.
type KVType string

const (
	KVF16 KVType = "FP16"
	KVQ8  KVType = "Q8_0"
	KVQ4  KVType = "Q4_0"
)

// KVTypes lists the supported cache precisions in display order.
var KVTypes = []KVType{KVF16, KVQ8, KVQ4}

// BytesPerElem is the storage cost of one cache element.
func BytesPerElem(t KVType) float64 {
	switch t {
	case KVQ8:
		return 34.0 / 32.0 // ggml Q8_0 block: 32 values in 34 bytes
	case KVQ4:
		return 18.0 / 32.0 // ggml Q4_0 block: 32 values in 18 bytes
	}
	return 2
}

// Contexts is the default set of context windows to evaluate.
var Contexts = []int{2048, 4096, 8192, 16384, 32768, 131072}

// Fit describes where a configuration runs.
type Fit string

const (
	FitGPU     Fit = "gpu"     // everything in VRAM
	FitPartial Fit = "partial" // some layers/cache offloaded to CPU
	FitCPU     Fit = "cpu"     // does not benefit from the GPU
)

// Config are the inputs of a calculation.
type Config struct {
	Meta      *gguf.Metadata
	VRAMFree  uint64 // bytes of GPU memory available for the model
	Overhead  uint64 // fixed runtime/compute-buffer reserve, bytes
	OverheadP float64
}

// Row is the outcome for one context length.
type Row struct {
	Ctx          int                `json:"ctx"`
	KVBytes      map[KVType]uint64  `json:"kv_bytes"`
	Total        map[KVType]uint64  `json:"total_bytes"`
	Fit          map[KVType]Fit     `json:"fit"`
	GPULayerFrac map[KVType]float64 `json:"gpu_layer_fraction"`
	OverTrain    bool               `json:"exceeds_training_ctx"`
}

// Result is the full report.
type Result struct {
	WeightsBytes  uint64 `json:"weights_bytes"`
	OverheadBytes uint64 `json:"overhead_bytes"`
	VRAMFree      uint64 `json:"vram_free"`
	Rows          []Row  `json:"rows"`
	// OffloadCtx is the largest context that still fits entirely in VRAM,
	// per cache type; 0 means even a 1-token context does not fit.
	OffloadCtx map[KVType]int `json:"offload_ctx"`
}

// KVBytesFor returns the KV-cache size for ctx tokens, summing over layers
// because the number of KV heads may vary per layer.
func KVBytesFor(m *gguf.Metadata, ctx int, t KVType) uint64 {
	return uint64(math.Ceil(float64(kvElemsPerToken(m)) * float64(ctx) * BytesPerElem(t)))
}

func kvElemsPerToken(m *gguf.Metadata) uint64 {
	var n uint64
	for _, h := range m.NKVHeads {
		n += uint64(h) * uint64(m.KeyLength+m.ValueLength)
	}
	return n
}

// Calculate evaluates every default context for every cache type.
func Calculate(c Config) Result {
	m := c.Meta
	overhead := c.Overhead + uint64(float64(m.FileSize)*c.OverheadP)
	res := Result{
		WeightsBytes:  m.FileSize,
		OverheadBytes: overhead,
		VRAMFree:      c.VRAMFree,
		OffloadCtx:    map[KVType]int{},
	}
	perTok := float64(kvElemsPerToken(m))
	for _, t := range KVTypes {
		budget := int64(c.VRAMFree) - int64(m.FileSize) - int64(overhead)
		if budget <= 0 || perTok == 0 {
			res.OffloadCtx[t] = 0
			continue
		}
		res.OffloadCtx[t] = int(math.Floor(float64(budget) / (perTok * BytesPerElem(t))))
	}
	for _, ctx := range Contexts {
		row := Row{
			Ctx:          ctx,
			KVBytes:      map[KVType]uint64{},
			Total:        map[KVType]uint64{},
			Fit:          map[KVType]Fit{},
			GPULayerFrac: map[KVType]float64{},
			OverTrain:    m.ContextTrain > 0 && uint32(ctx) > m.ContextTrain,
		}
		for _, t := range KVTypes {
			kv := KVBytesFor(m, ctx, t)
			total := m.FileSize + kv + overhead
			row.KVBytes[t], row.Total[t] = kv, total
			switch {
			case total <= c.VRAMFree:
				row.Fit[t], row.GPULayerFrac[t] = FitGPU, 1
			default:
				// Layers are offloaded evenly: each layer costs its share of
				// weights and of KV cache. Reserve the overhead first.
				avail := int64(c.VRAMFree) - int64(overhead)
				perLayer := float64(m.FileSize+kv) / float64(m.NLayers)
				frac := 0.0
				if avail > 0 && perLayer > 0 {
					frac = math.Min(1, math.Floor(float64(avail)/perLayer)/float64(m.NLayers))
				}
				row.GPULayerFrac[t] = frac
				if frac > 0 {
					row.Fit[t] = FitPartial
				} else {
					row.Fit[t] = FitCPU
				}
			}
		}
		res.Rows = append(res.Rows, row)
	}
	return res
}
