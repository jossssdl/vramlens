// Package gguf reads model metadata from GGUF headers, locally or over HTTP
// Range requests, without loading tensor data.
package gguf

// Metadata is the subset of GGUF header information needed to size a model.
type Metadata struct {
	Name         string   `json:"name"`
	Arch         string   `json:"arch"`
	NLayers      uint32   `json:"n_layers"`
	NEmbd        uint32   `json:"n_embd"`
	NHeads       uint32   `json:"n_heads"`
	NKVHeads     []uint32 `json:"n_kv_heads"` // one entry per layer
	KeyLength    uint32   `json:"key_length"`
	ValueLength  uint32   `json:"value_length"`
	ContextTrain uint32   `json:"context_train"`
	FileType     uint32   `json:"file_type"`
	Quant        string   `json:"quant"`
	FileSize     uint64   `json:"file_size"` // total bytes of all shards
}

var fileTypeNames = map[uint32]string{
	0: "F32", 1: "F16", 2: "Q4_0", 3: "Q4_1", 7: "Q8_0", 8: "Q5_0", 9: "Q5_1",
	10: "Q2_K", 11: "Q3_K_S", 12: "Q3_K_M", 13: "Q3_K_L", 14: "Q4_K_S", 15: "Q4_K_M",
	16: "Q5_K_S", 17: "Q5_K_M", 18: "Q6_K", 19: "IQ2_XXS", 20: "IQ2_XS", 21: "Q2_K_S",
	22: "IQ3_XS", 23: "IQ3_XXS", 24: "IQ1_S", 25: "IQ4_NL", 26: "IQ3_S", 27: "IQ3_M",
	28: "IQ2_S", 29: "IQ2_M", 30: "IQ4_XS", 31: "IQ1_M", 32: "BF16",
}

// QuantName returns the llama.cpp name of a general.file_type value.
func QuantName(ft uint32) string {
	if n, ok := fileTypeNames[ft]; ok {
		return n
	}
	return "unknown"
}
