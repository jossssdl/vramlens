package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vramlens/internal/bench"
	"vramlens/internal/calculator"
	"vramlens/internal/gguf"
	"vramlens/internal/hardware"
	"vramlens/internal/probe"
)

func testApp() app {
	a := app{st: NewHomeState(), parent: context.Background(), w: 100, h: 40}
	a.hw = hardware.Info{RAMTotal: 16 << 30, RAMFree: 3 << 30, GPUs: []hardware.GPU{{Name: "NVIDIA GeForce RTX 5060 Laptop GPU", VRAMTotal: 8 << 30, VRAMFree: 7500 << 20}}}
	a.hwOK = true
	return a
}

func key(a app, ks ...tea.KeyMsg) app {
	for _, k := range ks {
		m, _ := a.Update(k)
		a = m.(app)
	}
	return a
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func show(t *testing.T, name, s string) {
	if os.Getenv("SHOW") != "" {
		fmt.Printf("\n===== %s =====\n%s\n", name, s)
	}
}

func fakeCheck() *probe.CheckResult {
	kv := make([]uint32, 32)
	for i := range kv {
		kv[i] = 8
	}
	m := &gguf.Metadata{Name: "Meta Llama 3.1 8B Instruct", Arch: "llama", NLayers: 32, NEmbd: 4096, NHeads: 32, NKVHeads: kv,
		KeyLength: 128, ValueLength: 128, ContextTrain: 131072, Quant: "Q4_K_M", FileSize: 4920739232}
	free := uint64(7500 << 20)
	return &probe.CheckResult{Display: "Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf", Name: m.Name + "  (Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf)", Meta: m, Shards: 1,
		HW: testApp().hw, VRAMTotal: 8 << 30, VRAMFree: free,
		Result: calculator.Calculate(calculator.Config{Meta: m, VRAMFree: free, Overhead: 512 << 20, OverheadP: 0.05})}
}

func TestMenuView(t *testing.T) {
	a := testApp()
	v := a.View()
	show(t, "menu", v)
	if !strings.Contains(v, "Diagnose a model") || !strings.Contains(v, "RTX 5060") {
		t.Error("menu missing content")
	}
	a.h = 22 // compact
	if !strings.Contains(a.View(), "Benchmark a server") {
		t.Error("compact menu missing items")
	}
}

func TestFormsView(t *testing.T) {
	a := testApp()
	a = key(a, tea.KeyMsg{Type: tea.KeyEnter})
	show(t, "check form", a.View())
	a = key(a, tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyDown})
	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = m.(app)
	a.models = []string{"qwen3.5:9b", "llama3.2:3b"}
	a.modelsBusy = false
	show(t, "bench form", a.View())
	if a.sc != scBenchForm || a.st.Preset != "Quick" {
		t.Fatalf("screen %v preset %q", a.sc, a.st.Preset)
	}
	// editing runs detaches the preset
	a.form.focus = 4
	a = key(a, runes("5"))
	if a.st.Preset != "Custom" || a.st.Runs != "35" {
		t.Errorf("preset %q runs %q", a.st.Preset, a.st.Runs)
	}
	// switching preset resets the fields
	a.form.focus = 0
	a = key(a, tea.KeyMsg{Type: tea.KeyRight}) // Custom -> Quick (wraps)
	if a.st.Preset != "Quick" || a.st.Runs != "3" || !a.st.NoThink {
		t.Errorf("preset not applied: %+v", a.st)
	}
}

func TestResultAndPickerViews(t *testing.T) {
	a := testApp()
	a.checkRes = fakeCheck()
	a.sc = scCheckResult
	v := a.View()
	show(t, "check result (top)", v)
	if !strings.Contains(v, "Hardware") {
		t.Error("result missing hardware panel")
	}
	a.scroll = 1000
	a = key(a, tea.KeyMsg{Type: tea.KeyDown})
	show(t, "check result (bottom)", a.View())
	if !strings.Contains(a.View(), "What to do") {
		t.Error("bottom of result missing tips")
	}
	a.sc, a.pickOf = scPicker, "org/repo"
	a.cands = []gguf.Candidate{{Name: "model-Q2_K.gguf", Size: 3 << 30}, {Name: "model-Q4_K_M.gguf", Size: 7 << 30}, {Name: "model-Q8_0.gguf", Size: 9 << 30}}
	show(t, "picker", a.View())
}

func TestErrorView(t *testing.T) {
	a := testApp()
	a, _ = func() (app, tea.Cmd) {
		m, c := a.onCheck(checkMsg{err: fmt.Errorf("listing org/x: HTTP 404: not found")})
		return m.(app), c
	}()
	v := a.View()
	show(t, "error", v)
	if !strings.Contains(v, "Tip:") {
		t.Error("expected a tip")
	}
}

func TestBenchRunView(t *testing.T) {
	a := testApp()
	cfg := bench.Config{Provider: bench.Ollama, Endpoint: "http://localhost:11434", Model: "qwen3.5:9b", N: 6, Concurrency: 2, NumCtx: 2048, MaxTokens: 256, NoThink: true}
	r := &benchRun{cfg: cfg, inflight: map[int]time.Time{4: time.Now().Add(-9 * time.Second), 5: time.Now().Add(-2 * time.Second)}, start: time.Now().Add(-40 * time.Second)}
	for i := 0; i < 4; i++ {
		s := bench.Sample{Index: i, TTFT: time.Duration(900+i*50) * time.Millisecond, Total: 9 * time.Second, Tokens: 60, TPS: 38 + float64(i), ParseOK: true, SchemaOK: i != 2,
			Output: `{"a":1}`}
		if i == 2 {
			s.Violations = []string{"$.age: expected integer, got string"}
		}
		r.samples = append(r.samples, s)
	}
	a.run, a.sc = r, scBenchRun
	v := a.View()
	show(t, "bench running", v)
	if !strings.Contains(v, "ETA ~") {
		t.Error("expected ETA")
	}
	r.apply(benchEvt{run: r, kind: evDone})
	r.inflight = map[int]time.Time{}
	show(t, "bench done", a.View())
	if eta, ok := r.eta(); !ok || eta != 0 && len(r.samples) == cfg.N {
		t.Logf("eta %v ok %v", eta, ok)
	}
}

func TestEtaMath(t *testing.T) {
	r := &benchRun{cfg: bench.Config{N: 10, Concurrency: 2}}
	for i := 0; i < 4; i++ {
		r.samples = append(r.samples, bench.Sample{Index: i, Total: 10 * time.Second})
	}
	eta, ok := r.eta()
	if !ok || eta != 30*time.Second { // 6 left, 2 parallel -> 3 waves x 10s
		t.Errorf("eta %v ok %v", eta, ok)
	}
}
