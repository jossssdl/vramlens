package calculator

import (
	"testing"

	"github.com/jossssdl/vramlens/internal/gguf"
)

func llama3_8B() *gguf.Metadata {
	kv := make([]uint32, 32)
	for i := range kv {
		kv[i] = 8
	}
	return &gguf.Metadata{
		NLayers: 32, NEmbd: 4096, NHeads: 32, NKVHeads: kv,
		KeyLength: 128, ValueLength: 128, ContextTrain: 131072,
		FileSize: 4_920_000_000,
	}
}

func TestKVBytesLlama3(t *testing.T) {
	m := llama3_8B()
	// 2 * 32 layers * 8 kv heads * 128 dim * 2 B = 131072 B/token (128 KiB)
	if got := KVBytesFor(m, 8192, KVF16); got != 1<<30 {
		t.Errorf("FP16 8k = %d, want 1 GiB", got)
	}
	if got := KVBytesFor(m, 8192, KVQ8); got != (1<<30)*34/64 {
		t.Errorf("Q8 8k = %d", got)
	}
	if got := KVBytesFor(m, 8192, KVQ4); got != (1<<30)*18/64 {
		t.Errorf("Q4 8k = %d", got)
	}
}

func TestOffloadThresholdMatchesFit(t *testing.T) {
	m := llama3_8B()
	res := Calculate(Config{Meta: m, VRAMFree: 7778 << 20, Overhead: 512 << 20, OverheadP: 0})
	n := res.OffloadCtx[KVF16]
	if n <= 0 {
		t.Fatal("expected a positive threshold")
	}
	budget := res.VRAMFree - m.FileSize - res.OverheadBytes
	if KVBytesFor(m, n, KVF16) > budget {
		t.Errorf("ctx %d does not fit", n)
	}
	if KVBytesFor(m, n+1, KVF16) <= budget {
		t.Errorf("ctx %d should not fit but ctx+1 does", n)
	}
	// Row consistency: 2k fits on GPU, 128k FP16 does not.
	if res.Rows[0].Fit[KVF16] != FitGPU {
		t.Errorf("2k should fit: %v", res.Rows[0].Fit[KVF16])
	}
	last := res.Rows[len(res.Rows)-1]
	if last.Fit[KVF16] == FitGPU {
		t.Errorf("128k FP16 should not fit in 7.6 GiB")
	}
	if last.Fit[KVQ4] == FitGPU && last.Total[KVQ4] > res.VRAMFree {
		t.Errorf("fit flag inconsistent")
	}
}

func TestWeightsExceedVRAM(t *testing.T) {
	res := Calculate(Config{Meta: llama3_8B(), VRAMFree: 2 << 30, Overhead: 512 << 20})
	if res.OffloadCtx[KVF16] != 0 {
		t.Errorf("threshold = %d, want 0", res.OffloadCtx[KVF16])
	}
	if res.Rows[0].Fit[KVF16] == FitGPU {
		t.Error("should not fit")
	}
}
