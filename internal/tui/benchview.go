package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vramlens/internal/bench"
	"vramlens/internal/render"
)

type evtKind int

const (
	evStart evtKind = iota
	evSample
	evDone
)

// benchEvt is delivered to Update from the benchmark goroutines.
type benchEvt struct {
	run  *benchRun
	kind evtKind
	idx  int
	s    bench.Sample
	err  error
}

// benchRun is one live benchmark execution and everything the view needs.
type benchRun struct {
	cfg      bench.Config
	ch       chan benchEvt
	cancel   context.CancelFunc
	samples  []bench.Sample
	inflight map[int]time.Time
	start    time.Time
	end      time.Time
	done     bool
	canceled bool
	err      error
	saved    string
	saveErr  string
}

func newBenchRun(parent context.Context, cfg bench.Config) (*benchRun, tea.Cmd) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithCancel(parent)
	r := &benchRun{
		cfg:      cfg,
		cancel:   cancel,
		ch:       make(chan benchEvt, 2*cfg.N+8),
		inflight: map[int]time.Time{},
		start:    time.Now(),
	}
	run := cfg
	run.OnStart = func(i int) { r.ch <- benchEvt{run: r, kind: evStart, idx: i} }
	go func() {
		_, err := bench.Run(ctx, run, func(s bench.Sample) { r.ch <- benchEvt{run: r, kind: evSample, s: s} })
		r.ch <- benchEvt{run: r, kind: evDone, err: err}
	}()
	return r, r.wait()
}

// wait blocks until the next event of this run.
func (r *benchRun) wait() tea.Cmd {
	return func() tea.Msg { return <-r.ch }
}

func (r *benchRun) apply(e benchEvt) {
	switch e.kind {
	case evStart:
		r.inflight[e.idx] = time.Now()
	case evSample:
		delete(r.inflight, e.s.Index)
		r.samples = append(r.samples, e.s)
	case evDone:
		r.done, r.err, r.end = true, e.err, time.Now()
		r.canceled = e.err != nil
	}
}

func (r *benchRun) elapsed() time.Duration {
	if r.done {
		return r.end.Sub(r.start)
	}
	return time.Since(r.start)
}

// eta estimates remaining time from the mean duration of finished requests.
func (r *benchRun) eta() (time.Duration, bool) {
	var sum time.Duration
	n := 0
	for _, s := range r.samples {
		if s.Err == "" {
			sum += s.Total
			n++
		}
	}
	remaining := r.cfg.N - len(r.samples)
	if n == 0 || remaining <= 0 {
		return 0, remaining <= 0
	}
	conc := r.cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	if conc > remaining {
		conc = remaining
	}
	per := sum / time.Duration(n)
	waves := (remaining + conc - 1) / conc
	return per * time.Duration(waves), true
}

// save writes the results as JSON in the working directory.
func (r *benchRun) save() {
	stats := bench.Summarize(r.samples)
	name := strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			return c
		}
		return '_'
	}, r.cfg.Model)
	path := fmt.Sprintf("vramlens-bench-%s-%s.json", name, time.Now().Format("20060102-150405"))
	data, err := json.MarshalIndent(map[string]any{
		"config": map[string]any{
			"provider": r.cfg.Provider, "endpoint": r.cfg.Endpoint, "model": r.cfg.Model,
			"n": r.cfg.N, "concurrency": r.cfg.Concurrency, "temperature": r.cfg.Temperature,
			"num_ctx": r.cfg.NumCtx, "max_tokens": r.cfg.MaxTokens, "no_think": r.cfg.NoThink,
		},
		"stats":   stats,
		"samples": r.samples,
	}, "", "  ")
	if err == nil {
		err = os.WriteFile(path, data, 0o644)
	}
	if err != nil {
		r.saveErr = err.Error()
		return
	}
	r.saved, r.saveErr = path, ""
}

func (a app) viewBench() string {
	r := a.run
	w := a.cw()
	n := r.cfg.N
	done := len(r.samples)

	status := render.Accent.Render(spinner(a.tick)) + " running"
	switch {
	case r.done && r.canceled:
		status = render.Warn.Render("■ cancelled")
	case r.done:
		status = render.Good.Render("✓ finished")
	}
	settings := fmt.Sprintf("%d runs × %d parallel", n, r.cfg.Concurrency)
	if r.cfg.NumCtx > 0 {
		settings += fmt.Sprintf(" · ctx %d", r.cfg.NumCtx)
	}
	if r.cfg.MaxTokens > 0 {
		settings += fmt.Sprintf(" · max %d tokens", r.cfg.MaxTokens)
	}
	if r.cfg.NoThink {
		settings += " · thinking off"
	}
	pct := 100 * float64(done) / float64(n)
	etaTxt := render.Dim.Render("ETA estimating…")
	if eta, ok := r.eta(); r.done {
		etaTxt = ""
	} else if ok {
		etaTxt = render.Dim.Render("ETA ~" + render.Clock(eta))
	}
	head := []string{
		render.Bold.Render(r.cfg.Model) + render.Dim.Render("  "+string(r.cfg.Provider)+" @ "+r.cfg.Endpoint),
		render.Dim.Render(settings),
		"",
		fmt.Sprintf("%s  %d/%d  %3.0f%%   %s  %s   %s",
			render.PctBar(pct, 30), done, n, pct, render.Dim.Render("elapsed "+render.Clock(r.elapsed())), etaTxt, status),
	}
	if !r.done {
		ids := make([]int, 0, len(r.inflight))
		for i := range r.inflight {
			ids = append(ids, i)
		}
		sort.Ints(ids)
		for k, i := range ids {
			if k == 3 {
				head = append(head, render.Dim.Render(fmt.Sprintf("  … and %d more in flight", len(ids)-3)))
				break
			}
			head = append(head, fmt.Sprintf("  %s run %d · %s", render.Accent.Render(spinner(a.tick+k)), i+1, render.Dim.Render(render.Dur(time.Since(r.inflight[i]))+" waiting")))
		}
		if done == 0 && len(r.inflight) > 0 && time.Since(r.start) > 30*time.Second {
			head = append(head, "", render.Warn.Render("! Slow start: the model may still be loading or partly running on the CPU."),
				render.Dim.Render("  Press q, pick the Quick preset or a smaller model, and run Diagnose first to see if it fits."))
		}
	}
	parts := []string{render.Box("Benchmark", strings.Join(head, "\n"), w, render.CViolet)}

	if done > 0 {
		st := bench.Summarize(r.samples)
		parts = append(parts, render.BenchCards(st, w))
		rows := a.ht() - 28
		if rows < 3 {
			rows = 3
		}
		if rows > 12 {
			rows = 12
		}
		parts = append(parts, render.RunsTable(r.samples, rows, w))
		if done >= 2 && a.ht() >= 34 {
			parts = append(parts, render.Trend(r.samples, w))
		}
		if r.done {
			parts = append(parts, render.Verdict(st))
		}
	}
	if r.saved != "" {
		parts = append(parts, render.Good.Render("✓ Saved to "+r.saved))
	} else if r.saveErr != "" {
		parts = append(parts, render.Bad.Render("✗ Could not save: "+r.saveErr))
	}
	return strings.Join(parts, "\n")
}
