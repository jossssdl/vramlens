// Package tui hosts the interactive terminal interfaces (bubbletea).
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vramlens/internal/bench"
	"vramlens/internal/render"
)

type sampleMsg bench.Sample
type doneMsg struct{ err error }
type tickMsg time.Time

type benchModel struct {
	cfg     bench.Config
	samples []bench.Sample
	start   time.Time
	elapsed time.Duration
	done    bool
	err     error
	cancel  context.CancelFunc
}

func (m benchModel) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m benchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.cancel()
			if m.done {
				return m, tea.Quit
			}
			// Let the runner drain; doneMsg quits.
			return m, nil
		case "enter":
			if m.done {
				return m, tea.Quit
			}
		}
	case sampleMsg:
		m.samples = append(m.samples, bench.Sample(msg))
	case tickMsg:
		if !m.done {
			m.elapsed = time.Since(m.start)
			return m, tick()
		}
	case doneMsg:
		m.done, m.err = true, msg.err
		m.elapsed = time.Since(m.start)
		return m, tea.Quit
	}
	return m, nil
}

func (m benchModel) View() string {
	var b strings.Builder
	n := len(m.samples)
	status := "running"
	if m.done {
		status = "finished"
		if m.err != nil {
			status = "cancelled"
		}
	}
	fmt.Fprintf(&b, "%s  %s\n\n", lipgloss.NewStyle().Bold(true).Render("vramlens bench"),
		lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("%s · %s · %s", m.cfg.Model, status, m.elapsed.Round(100*time.Millisecond))))
	fmt.Fprintf(&b, "  progress  %s  %d/%d\n\n", render.PctBar(100*float64(n)/float64(m.cfg.N), 30), n, m.cfg.N)
	if n > 0 {
		b.WriteString(render.BenchRows(m.samples, 10))
		b.WriteString("\n")
		b.WriteString(render.BenchSummary(m.cfg, bench.Summarize(m.samples)))
	}
	if !m.done {
		b.WriteString("\n  " + lipgloss.NewStyle().Faint(true).Render("q: cancel"))
	}
	return b.String() + "\n"
}

// RunBench runs the benchmark inside a live TUI and returns the samples that
// completed (all of them, unless the user cancelled).
func RunBench(ctx context.Context, cfg bench.Config) ([]bench.Sample, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := benchModel{cfg: cfg, start: time.Now(), cancel: cancel}
	p := tea.NewProgram(m)

	resCh := make(chan []bench.Sample, 1)
	go func() {
		samples, err := bench.Run(ctx, cfg, func(s bench.Sample) { p.Send(sampleMsg(s)) })
		resCh <- samples
		p.Send(doneMsg{err: err})
	}()
	if _, err := p.Run(); err != nil {
		cancel()
		return nil, err
	}
	cancel()
	return <-resCh, nil
}
