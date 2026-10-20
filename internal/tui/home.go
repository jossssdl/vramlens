package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vramlens/internal/bench"
	"vramlens/internal/hardware"
)

// ActionKind is what the user chose in the home screen.
type ActionKind int

const (
	ActionQuit ActionKind = iota
	ActionCheck
	ActionBench
)

// HomeState holds form values; it is reused between runs so a failed attempt
// can be corrected without retyping.
type HomeState struct {
	CheckTarget string
	CheckQuant  string

	Provider string
	Endpoint string
	Model    string
	Runs     string
	Conc     string
	NumCtx   string
	NoThink  bool
}

const (
	ollamaDefault = "http://localhost:11434"
	openaiDefault = "http://localhost:8080"
)

// NewHomeState returns the default form values.
func NewHomeState() *HomeState {
	return &HomeState{Provider: "ollama", Endpoint: ollamaDefault, Runs: "10", Conc: "1"}
}

// BenchConfig converts the bench form into a bench.Config.
func (s *HomeState) BenchConfig() (bench.Config, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s.Runs))
	if err != nil || n < 1 {
		return bench.Config{}, fmt.Errorf("runs must be a positive number")
	}
	c, err := strconv.Atoi(strings.TrimSpace(s.Conc))
	if err != nil || c < 1 {
		return bench.Config{}, fmt.Errorf("concurrency must be a positive number")
	}
	nctx := 0
	if t := strings.TrimSpace(s.NumCtx); t != "" {
		if nctx, err = strconv.Atoi(t); err != nil || nctx < 0 {
			return bench.Config{}, fmt.Errorf("context must be a number")
		}
	}
	if strings.TrimSpace(s.Model) == "" {
		return bench.Config{}, fmt.Errorf("choose or type a model name")
	}
	return bench.Config{
		Provider:    bench.Provider(s.Provider),
		Endpoint:    strings.TrimSpace(s.Endpoint),
		Model:       strings.TrimSpace(s.Model),
		N:           n,
		Concurrency: c,
		NumCtx:      nctx,
		NoThink:     s.NoThink,
	}, nil
}

type screen int

const (
	scMenu screen = iota
	scCheck
	scBench
)

type fieldKind int

const (
	fText fieldKind = iota
	fChoice
	fBool
)

type field struct {
	label   string
	hint    string
	kind    fieldKind
	s       *string
	b       *bool
	choices []string
	// pickModel lets ←/→ cycle through the models found on the server.
	pickModel bool
}

type (
	modelsMsg struct {
		models []string
		err    error
	}
	hwMsg string
)

type homeModel struct {
	st      *HomeState
	sc      screen
	menu    int
	fields  []field
	focus   int
	errMsg  string
	models  []string
	modelsE string
	hw      string
	action  ActionKind
}

var menuItems = []string{
	"Diagnose a model  (will it fit my GPU?)",
	"Benchmark a server  (speed + valid JSON)",
	"Quit",
}

func (m homeModel) Init() tea.Cmd {
	return func() tea.Msg {
		hw := hardware.Detect()
		_, free := hw.TotalVRAM()
		gpu := "no GPU detected"
		if len(hw.GPUs) > 0 {
			gpu = fmt.Sprintf("%s · %.1f GiB VRAM free", hw.GPUs[0].Name, float64(free)/(1<<30))
		}
		return hwMsg(fmt.Sprintf("%s · RAM %.1f GiB free", gpu, float64(hw.RAMFree)/(1<<30)))
	}
}

func fetchModels(provider, endpoint string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ms, err := bench.ListModels(ctx, bench.Provider(provider), endpoint, "")
		return modelsMsg{ms, err}
	}
}

func (m *homeModel) openCheck() {
	m.sc, m.focus, m.errMsg = scCheck, 0, ""
	m.fields = []field{
		{label: "Model", hint: "Hugging Face repo (org/name) or path to a .gguf file", kind: fText, s: &m.st.CheckTarget},
		{label: "Quantization", hint: "optional, e.g. Q4_K_M — needed when a repo has many files", kind: fText, s: &m.st.CheckQuant},
	}
}

func (m *homeModel) openBench() tea.Cmd {
	m.sc, m.focus, m.errMsg = scBench, 0, ""
	m.fields = []field{
		{label: "Server type", hint: "←/→ to change", kind: fChoice, s: &m.st.Provider, choices: []string{"ollama", "openai"}},
		{label: "Endpoint", hint: "server base URL", kind: fText, s: &m.st.Endpoint},
		{label: "Model", hint: "type a name, or ←/→ to pick one found on the server", kind: fText, s: &m.st.Model, pickModel: true},
		{label: "Runs", hint: "number of requests", kind: fText, s: &m.st.Runs},
		{label: "Concurrency", hint: "parallel requests", kind: fText, s: &m.st.Conc},
		{label: "Context (Ollama)", hint: "num_ctx in tokens; empty = server default. Lower it if the model fails to load", kind: fText, s: &m.st.NumCtx},
		{label: "Disable thinking", hint: "space to toggle (Ollama reasoning models)", kind: fBool, b: &m.st.NoThink},
	}
	return fetchModels(m.st.Provider, m.st.Endpoint)
}

func (m homeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hwMsg:
		m.hw = string(msg)
	case modelsMsg:
		m.models, m.modelsE = msg.models, ""
		if msg.err != nil {
			m.modelsE = "could not list models: " + msg.err.Error()
		} else if m.st.Model == "" && len(msg.models) > 0 {
			m.st.Model = msg.models[0]
		}
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.action = ActionQuit
			return m, tea.Quit
		}
		if m.sc == scMenu {
			return m.updateMenu(msg)
		}
		return m.updateForm(msg)
	}
	return m, nil
}

func (m homeModel) updateMenu(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		m.menu = (m.menu + len(menuItems) - 1) % len(menuItems)
	case "down", "j":
		m.menu = (m.menu + 1) % len(menuItems)
	case "1":
		m.openCheck()
	case "2":
		return m, m.openBench()
	case "q", "esc", "3":
		m.action = ActionQuit
		return m, tea.Quit
	case "enter":
		switch m.menu {
		case 0:
			m.openCheck()
		case 1:
			return m, m.openBench()
		default:
			m.action = ActionQuit
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *homeModel) move(d int) tea.Cmd {
	prev := m.fields[m.focus].label
	m.focus = (m.focus + d + len(m.fields)) % len(m.fields)
	if prev == "Endpoint" && m.sc == scBench {
		return fetchModels(m.st.Provider, m.st.Endpoint)
	}
	return nil
}

func (m homeModel) updateForm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.fields[m.focus]
	switch k.Type {
	case tea.KeyEsc:
		m.sc, m.errMsg = scMenu, ""
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		return m, m.move(1)
	case tea.KeyShiftTab, tea.KeyUp:
		return m, m.move(-1)
	case tea.KeyEnter:
		return m.submit()
	case tea.KeyLeft, tea.KeyRight:
		d := 1
		if k.Type == tea.KeyLeft {
			d = -1
		}
		switch {
		case f.kind == fChoice:
			cur := 0
			for i, c := range f.choices {
				if c == *f.s {
					cur = i
				}
			}
			*f.s = f.choices[(cur+d+len(f.choices))%len(f.choices)]
			if m.st.Provider == "openai" && m.st.Endpoint == ollamaDefault {
				m.st.Endpoint = openaiDefault
			} else if m.st.Provider == "ollama" && m.st.Endpoint == openaiDefault {
				m.st.Endpoint = ollamaDefault
			}
			m.models = nil
			return m, fetchModels(m.st.Provider, m.st.Endpoint)
		case f.kind == fBool:
			*f.b = !*f.b
		case f.pickModel:
			if opts := m.models; len(opts) > 0 {
				cur := -1
				for i, o := range opts {
					if o == *f.s {
						cur = i
					}
				}
				*f.s = opts[(cur+d+len(opts))%len(opts)]
			}
		}
		return m, nil
	case tea.KeyBackspace:
		if f.kind == fText && *f.s != "" {
			_, size := utf8.DecodeLastRuneInString(*f.s)
			*f.s = (*f.s)[:len(*f.s)-size]
		}
		return m, nil
	case tea.KeySpace:
		if f.kind == fBool {
			*f.b = !*f.b
		} else if f.kind == fText {
			*f.s += " "
		}
		return m, nil
	case tea.KeyRunes:
		if f.kind == fText {
			*f.s += string(k.Runes)
		}
	}
	return m, nil
}

func (m homeModel) submit() (tea.Model, tea.Cmd) {
	switch m.sc {
	case scCheck:
		if strings.TrimSpace(m.st.CheckTarget) == "" {
			m.errMsg = "enter a Hugging Face repo or the path of a .gguf file"
			return m, nil
		}
		m.action = ActionCheck
	case scBench:
		if _, err := m.st.BenchConfig(); err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = ActionBench
	}
	return m, tea.Quit
}

var (
	titleSt = lipgloss.NewStyle().Bold(true)
	faintSt = lipgloss.NewStyle().Faint(true)
	selSt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	errSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func (m homeModel) View() string {
	var b strings.Builder
	b.WriteString(titleSt.Render("vramlens") + faintSt.Render("  VRAM doctor & JSON benchmark for local LLMs") + "\n")
	if m.hw != "" {
		b.WriteString(faintSt.Render(m.hw) + "\n")
	}
	b.WriteString("\n")

	switch m.sc {
	case scMenu:
		for i, it := range menuItems {
			if i == m.menu {
				b.WriteString(selSt.Render("› "+it) + "\n")
			} else {
				b.WriteString("  " + it + "\n")
			}
		}
		b.WriteString("\n" + faintSt.Render("↑/↓ move · enter select · q quit"))
	default:
		head := "Diagnose a model"
		if m.sc == scBench {
			head = "Benchmark a server"
		}
		b.WriteString(titleSt.Render(head) + "\n\n")
		for i, f := range m.fields {
			val := ""
			switch f.kind {
			case fBool:
				val = "[ ]"
				if *f.b {
					val = "[x]"
				}
			case fChoice:
				val = "‹ " + *f.s + " ›"
			default:
				val = *f.s
				if i == m.focus {
					val += "▏"
				}
			}
			line := fmt.Sprintf("%-18s %s", f.label, val)
			if i == m.focus {
				b.WriteString(selSt.Render("› "+line) + "\n")
				b.WriteString("  " + faintSt.Render(f.hint) + "\n")
			} else {
				b.WriteString("  " + line + "\n")
			}
		}
		if m.sc == scBench {
			switch {
			case m.modelsE != "":
				b.WriteString("\n" + faintSt.Render(m.modelsE+" (type the model name manually)") + "\n")
			case len(m.models) > 0:
				b.WriteString("\n" + faintSt.Render("found on server: "+strings.Join(m.models, ", ")) + "\n")
			}
		}
		if m.errMsg != "" {
			b.WriteString("\n" + errSt.Render(m.errMsg) + "\n")
		}
		b.WriteString("\n" + faintSt.Render("tab/↑/↓ move · enter run · esc back"))
	}
	return b.String() + "\n"
}

// Home shows the interactive start screen and returns the chosen action.
// Form values are read from and written back to st.
func Home(st *HomeState) (ActionKind, error) {
	final, err := tea.NewProgram(homeModel{st: st}).Run()
	if err != nil {
		return ActionQuit, err
	}
	return final.(homeModel).action, nil
}
