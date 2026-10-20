// Package tui hosts the interactive terminal interface (bubbletea).
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jossssdl/vramlens/internal/bench"
	"github.com/jossssdl/vramlens/internal/render"
)

const (
	ollamaDefault = "http://localhost:11434"
	openaiDefault = "http://localhost:8080"
)

type preset struct {
	name    string
	runs    string
	conc    string
	ctx     string
	maxTok  string
	noThink bool
}

var presets = []preset{
	{"Quick", "3", "1", "2048", "256", true},
	{"Standard", "10", "1", "", "", false},
	{"Thorough", "30", "2", "", "", false},
}

// HomeState holds every form value; it persists across screens so a failed
// attempt can be corrected without retyping.
type HomeState struct {
	CheckTarget string
	CheckQuant  string
	CheckVRAM   string

	Preset    string
	Provider  string
	Endpoint  string
	Model     string
	Runs      string
	Conc      string
	NumCtx    string
	MaxTokens string
	NoThink   bool
}

// NewHomeState returns defaults with the Quick preset applied.
func NewHomeState() *HomeState {
	s := &HomeState{Provider: "ollama", Endpoint: ollamaDefault}
	s.applyPreset("Quick")
	return s
}

func (s *HomeState) applyPreset(name string) {
	for _, p := range presets {
		if p.name == name {
			s.Preset, s.Runs, s.Conc, s.NumCtx, s.MaxTokens, s.NoThink = p.name, p.runs, p.conc, p.ctx, p.maxTok, p.noThink
			return
		}
	}
	s.Preset = "Custom"
}

func atoiMin(label, v string, min int, allowEmpty bool) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" && allowEmpty {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		return 0, fmt.Errorf("%s must be a whole number ≥ %d", label, min)
	}
	return n, nil
}

// BenchConfig validates the benchmark form and converts it to a bench.Config.
func (s *HomeState) BenchConfig() (bench.Config, error) {
	n, err := atoiMin("Runs", s.Runs, 1, false)
	if err != nil {
		return bench.Config{}, err
	}
	c, err := atoiMin("Concurrency", s.Conc, 1, false)
	if err != nil {
		return bench.Config{}, err
	}
	nctx, err := atoiMin("Context", s.NumCtx, 0, true)
	if err != nil {
		return bench.Config{}, err
	}
	mt, err := atoiMin("Max tokens", s.MaxTokens, 0, true)
	if err != nil {
		return bench.Config{}, err
	}
	if strings.TrimSpace(s.Model) == "" {
		return bench.Config{}, fmt.Errorf("choose or type a model name")
	}
	if strings.TrimSpace(s.Endpoint) == "" {
		return bench.Config{}, fmt.Errorf("endpoint is required")
	}
	return bench.Config{
		Provider:    bench.Provider(s.Provider),
		Endpoint:    strings.TrimSpace(s.Endpoint),
		Model:       strings.TrimSpace(s.Model),
		Prompt:      bench.DefaultPrompt,
		Schema:      bench.DefaultSchema(),
		N:           n,
		Concurrency: c,
		NumCtx:      nctx,
		MaxTokens:   mt,
		NoThink:     s.NoThink,
	}, nil
}

type fieldKind int

const (
	fText fieldKind = iota
	fChoice
	fBool
)

type field struct {
	key     string
	label   string
	hint    string
	ph      string // placeholder for empty text fields
	kind    fieldKind
	s       *string
	b       *bool
	choices []string
}

type form struct {
	title  string
	fields []field
	focus  int
	err    string
}

func (f *form) cur() *field { return &f.fields[f.focus] }

const inputW = 34

func (a app) renderForm(f *form) string {
	var l []string
	for i, fd := range f.fields {
		focused := i == f.focus
		label := render.Pad(fd.label, 18)
		if focused {
			label = render.Accent.Bold(true).Render(label)
		} else {
			label = render.Dim.Render(label)
		}
		marker := "  "
		if focused {
			marker = render.Accent.Render("▸ ")
		}
		var val string
		switch fd.kind {
		case fChoice:
			var parts []string
			for _, c := range fd.choices {
				if c == *fd.s {
					parts = append(parts, render.Accent.Bold(true).Render("● "+c))
				} else {
					parts = append(parts, render.Dim.Render("○ "+c))
				}
			}
			val = strings.Join(parts, "   ")
		case fBool:
			if *fd.b {
				val = render.Good.Render("[✓]") + " yes"
			} else {
				val = render.Dim.Render("[ ]") + render.Dim.Render(" no")
			}
		default:
			text := *fd.s
			br := render.Dim
			if focused {
				br = render.Accent
				text += "▏"
			}
			switch {
			case text == "" && fd.ph != "":
				text = render.Dim.Render(fd.ph)
			case focused:
				text = render.Bold.Render(text)
			}
			val = br.Render("[ ") + render.Pad(text, inputW-4) + br.Render(" ]")
		}
		l = append(l, marker+label+" "+val)
		if focused && fd.hint != "" {
			l = append(l, "    "+render.Dim.Render(fd.hint))
		}
		if fd.key == "model" && f.title == "Benchmark a server" {
			l = append(l, a.modelsLine())
		}
	}
	if f.title == "Benchmark a server" && a.st.Provider == "ollama" && !a.st.NoThink {
		if n, err := strconv.Atoi(strings.TrimSpace(a.st.MaxTokens)); err == nil && n > 0 && n < 1024 {
			l = append(l, "", render.Warn.Render("! Thinking is ON and max tokens is "+a.st.MaxTokens+": reasoning models can spend the whole"),
				render.Warn.Render("  cap thinking and never answer (0% valid). Disable thinking or raise max tokens."))
		}
	}
	if f.err != "" {
		l = append(l, "", render.Bad.Render("✗ "+f.err))
	}
	return strings.Join(l, "\n")
}

func (a app) modelsLine() string {
	switch {
	case a.modelsBusy:
		return "    " + render.Dim.Render(spinner(a.tick)+" looking for models on the server…")
	case a.modelsErr != "":
		return "    " + render.Warn.Render("! "+a.modelsErr) + render.Dim.Render(" — type the name manually")
	case len(a.models) > 0:
		var parts []string
		for _, m := range a.models {
			if m == a.st.Model {
				parts = append(parts, render.Accent.Bold(true).Render(m))
			} else {
				parts = append(parts, render.Dim.Render(m))
			}
		}
		return "    " + render.Dim.Render("found: ") + strings.Join(parts, render.Dim.Render(" · "))
	}
	return ""
}

func spinner(t int) string {
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	return string(frames[t%len(frames)])
}
