package gguf

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type gw struct{ bytes.Buffer }

func (w *gw) u32(v uint32) { binary.Write(&w.Buffer, binary.LittleEndian, v) }
func (w *gw) u64(v uint64) { binary.Write(&w.Buffer, binary.LittleEndian, v) }
func (w *gw) str(s string) { w.u64(uint64(len(s))); w.WriteString(s) }
func (w *gw) kvU32(k string, v uint32) {
	w.str(k)
	w.u32(tU32)
	w.u32(v)
}
func (w *gw) kvStr(k, v string) {
	w.str(k)
	w.u32(tString)
	w.str(v)
}

// buildGGUF makes a v3 header; kvHeads may be scalar (len 1) or per-layer.
func buildGGUF(kvHeads []uint32, bigTokenizer bool) []byte {
	var w gw
	w.u32(magic)
	w.u32(3)
	w.u64(0) // tensors
	w.u64(9) // kv count
	w.kvStr("general.architecture", "llama")
	w.kvStr("general.name", "Test 8B")
	w.kvU32("general.file_type", 15)
	w.kvU32("llama.block_count", 4)
	w.kvU32("llama.embedding_length", 4096)
	w.kvU32("llama.attention.head_count", 32)
	if len(kvHeads) == 1 {
		w.kvU32("llama.attention.head_count_kv", kvHeads[0])
	} else {
		w.str("llama.attention.head_count_kv")
		w.u32(tArray)
		w.u32(tU32)
		w.u64(uint64(len(kvHeads)))
		for _, h := range kvHeads {
			w.u32(h)
		}
	}
	w.kvU32("llama.context_length", 8192)
	// tokenizer array of strings, large enough to exceed the first 64 KiB chunk
	w.str("tokenizer.ggml.tokens")
	w.u32(tArray)
	w.u32(tString)
	n := 10
	if bigTokenizer {
		n = 40000
	}
	w.u64(uint64(n))
	for i := 0; i < n; i++ {
		w.str("tok" + strconv.Itoa(i))
	}
	return w.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	p := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadScalarKV(t *testing.T) {
	src, err := OpenLocal(writeTemp(t, buildGGUF([]uint32{8}, false)))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	m, err := Read(src)
	if err != nil {
		t.Fatal(err)
	}
	if m.Arch != "llama" || m.NLayers != 4 || m.NEmbd != 4096 || m.NHeads != 32 || m.Quant != "Q4_K_M" {
		t.Errorf("unexpected metadata: %+v", m)
	}
	if m.KeyLength != 128 || m.ValueLength != 128 {
		t.Errorf("head dim: %d/%d", m.KeyLength, m.ValueLength)
	}
	if len(m.NKVHeads) != 4 || m.NKVHeads[3] != 8 {
		t.Errorf("kv heads: %v", m.NKVHeads)
	}
}

func TestReadPerLayerKVHeads(t *testing.T) {
	src, _ := OpenLocal(writeTemp(t, buildGGUF([]uint32{8, 8, 4, 2}, false)))
	defer src.Close()
	m, err := Read(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{8, 8, 4, 2}
	for i, v := range want {
		if m.NKVHeads[i] != v {
			t.Fatalf("kv heads %v, want %v", m.NKVHeads, want)
		}
	}
}

func TestBadMagic(t *testing.T) {
	src, _ := OpenLocal(writeTemp(t, []byte("NOTGGUF-padding-padding")))
	defer src.Close()
	if _, err := Read(src); err == nil || !strings.Contains(err.Error(), "magic") {
		t.Errorf("expected bad magic error, got %v", err)
	}
}

func TestHTTPRangeFetchesLittle(t *testing.T) {
	data := buildGGUF([]uint32{8}, true) // ~400 KB, tokenizer past the first chunk
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "m.gguf", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	src, err := OpenHTTP(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Size() != int64(len(data)) {
		t.Fatalf("size %d != %d", src.Size(), len(data))
	}
	m, err := Read(src)
	if err != nil {
		t.Fatal(err)
	}
	if m.NLayers != 4 {
		t.Errorf("layers %d", m.NLayers)
	}
	if got := Fetched(src); got > 128*1024 {
		t.Errorf("fetched %d bytes; early exit should keep it near one chunk", got)
	}
}

func TestParseHFSpec(t *testing.T) {
	cases := []struct{ in, repo, file, quant string }{
		{"bartowski/Llama-GGUF", "bartowski/Llama-GGUF", "", ""},
		{"bartowski/Llama-GGUF:Q4_K_M", "bartowski/Llama-GGUF", "", "Q4_K_M"},
		{"https://huggingface.co/a/b", "a/b", "", ""},
		{"https://huggingface.co/a/b/resolve/main/x/y.gguf", "a/b", "x/y.gguf", ""},
	}
	for _, c := range cases {
		r, f, q := parseHFSpec(c.in, "", "")
		if r != c.repo || f != c.file || q != c.quant {
			t.Errorf("%q => %q %q %q", c.in, r, f, q)
		}
	}
}
