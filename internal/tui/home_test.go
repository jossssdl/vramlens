package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func send(m homeModel, keys ...tea.KeyMsg) homeModel {
	for _, k := range keys {
		nm, _ := m.Update(k)
		m = nm.(homeModel)
	}
	return m
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestCheckFlow(t *testing.T) {
	m := homeModel{st: NewHomeState()}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter}) // menu item 0
	if m.sc != scCheck {
		t.Fatal("expected check form")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter}) // empty target
	if m.errMsg == "" || m.action == ActionCheck {
		t.Error("empty target must be rejected")
	}
	m = send(m, runes("org/repo"), tea.KeyMsg{Type: tea.KeySpace}, tea.KeyMsg{Type: tea.KeyBackspace}, tea.KeyMsg{Type: tea.KeyBackspace}, runes("o"))
	if m.st.CheckTarget != "org/repo" {
		t.Errorf("target %q", m.st.CheckTarget)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.action != ActionCheck {
		t.Error("expected check action")
	}
}

func TestBenchFormValidationAndModelCycle(t *testing.T) {
	m := homeModel{st: NewHomeState(), models: []string{"a", "b"}}
	m = send(m, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter}) // menu -> bench
	if m.sc != scBench {
		t.Fatal("expected bench form")
	}
	m.sc, m.fields = scBench, nil
	(&m).openBench()
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.errMsg == "" {
		t.Error("missing model must be rejected")
	}
	m.focus = 2 // Model field
	m = send(m, tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyRight})
	if m.st.Model != "b" {
		t.Errorf("model %q, want b", m.st.Model)
	}
	m.focus = 0
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.st.Provider != "openai" || m.st.Endpoint != openaiDefault {
		t.Errorf("provider switch: %+v", m.st)
	}
	m.st.Provider = "ollama"
	if cfg, err := m.st.BenchConfig(); err != nil || cfg.N != 10 || cfg.Model != "b" {
		t.Errorf("cfg %+v err %v", cfg, err)
	}
}
