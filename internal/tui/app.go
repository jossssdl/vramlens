package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vramlens/internal/bench"
	"vramlens/internal/gguf"
	"vramlens/internal/hardware"
	"vramlens/internal/probe"
	"vramlens/internal/render"
)

type screen int

const (
	scMenu screen = iota
	scCheckForm
	scCheckBusy
	scPicker
	scCheckResult
	scBenchForm
	scBenchRun
	scError
)

type (
	tickMsg   time.Time
	hwMsg     hardware.Info
	modelsMsg struct {
		id     int
		models []string
		err    error
	}
	checkMsg struct {
		id  int
		res *probe.CheckResult
		err error
	}
)

type app struct {
	st     *HomeState
	w, h   int
	sc     screen
	tick   int
	hw     hardware.Info
	hwOK   bool
	menu   int
	form   *form
	parent context.Context

	models     []string
	modelsErr  string
	modelsBusy bool
	modelsReq  int

	reqID      int
	busyTarget string
	busySince  time.Time
	checkRes   *probe.CheckResult
	override   bool
	scroll     int

	cands  []gguf.Candidate
	pick   int
	pickOf string

	errTitle string
	errMsg   string
	errTip   string
	errBack  screen

	run        *benchRun
	startCmd   tea.Cmd
	standalone bool
}

// RunApp starts the interactive menu.
func RunApp() error {
	_, err := tea.NewProgram(app{st: NewHomeState(), parent: context.Background()}, tea.WithAltScreen()).Run()
	return err
}

// RunBench runs a benchmark in the live view and exits when the user is done.
func RunBench(ctx context.Context, cfg bench.Config) error {
	r, cmd := newBenchRun(ctx, cfg)
	a := app{st: NewHomeState(), parent: ctx, sc: scBenchRun, run: r, standalone: true}
	a.startCmd = cmd
	_, err := tea.NewProgram(a).Run()
	r.cancel()
	return err
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func detectHW() tea.Cmd { return func() tea.Msg { return hwMsg(hardware.Detect()) } }

func fetchModels(id int, provider, endpoint string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ms, err := bench.ListModels(ctx, bench.Provider(provider), endpoint, "")
		return modelsMsg{id, ms, err}
	}
}

func checkCmd(id int, p probe.CheckParams) tea.Cmd {
	return func() tea.Msg {
		res, err := probe.Check(p)
		return checkMsg{id, res, err}
	}
}

func (a app) cw() int {
	w := a.w
	if w == 0 {
		w = 100
	}
	if w > 110 {
		w = 110
	}
	if w < 60 {
		w = 60
	}
	return w
}

func (a app) ht() int {
	if a.h == 0 {
		return 30
	}
	return a.h
}

func (a app) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd(), detectHW()}
	if a.startCmd != nil {
		cmds = append(cmds, a.startCmd)
	}
	return tea.Batch(cmds...)
}

// vramFree is the VRAM budget shown in pickers: the override or detected free VRAM.
func (a app) vramFree() uint64 {
	if g, err := strconv.ParseFloat(strings.TrimSpace(a.st.CheckVRAM), 64); err == nil && g > 0 {
		return uint64(g * (1 << 30))
	}
	_, free := a.hw.TotalVRAM()
	return free
}

func (a *app) openCheckForm() {
	a.sc = scCheckForm
	a.form = &form{title: "Diagnose a model", fields: []field{
		{key: "target", label: "Model", kind: fText, s: &a.st.CheckTarget, ph: "org/repo   or   C:\\path\\model.gguf",
			hint: "Hugging Face repo, a huggingface.co URL, or a local .gguf file"},
		{key: "quant", label: "Quantization", kind: fText, s: &a.st.CheckQuant, ph: "(any)",
			hint: "optional, e.g. Q4_K_M — leave empty to pick from a list"},
		{key: "vram", label: "VRAM budget (GiB)", kind: fText, s: &a.st.CheckVRAM, ph: "(detected)",
			hint: "optional: simulate a different GPU, e.g. 6 or 24"},
	}}
}

func (a *app) openBenchForm() tea.Cmd {
	a.sc = scBenchForm
	a.form = &form{title: "Benchmark a server", fields: []field{
		{key: "preset", label: "Preset", kind: fChoice, s: &a.st.Preset, choices: []string{"Quick", "Standard", "Thorough", "Custom"},
			hint: "Quick: 3 runs, small context, no reasoning · ←/→ to change"},
		{key: "provider", label: "Server type", kind: fChoice, s: &a.st.Provider, choices: []string{"ollama", "openai"},
			hint: "ollama, or any OpenAI-compatible server (llama.cpp, LM Studio, vLLM)"},
		{key: "endpoint", label: "Endpoint", kind: fText, s: &a.st.Endpoint, hint: "server base URL"},
		{key: "model", label: "Model", kind: fText, s: &a.st.Model, ph: "model name",
			hint: "type a name, or ←/→ to pick one found on the server"},
		{key: "runs", label: "Runs", kind: fText, s: &a.st.Runs, hint: "how many requests to send"},
		{key: "conc", label: "Parallel requests", kind: fText, s: &a.st.Conc, hint: "1 = one at a time (cleanest numbers)"},
		{key: "ctx", label: "Context (Ollama)", kind: fText, s: &a.st.NumCtx, ph: "(server default)",
			hint: "num_ctx in tokens — lower it if the model fails to load"},
		{key: "maxtok", label: "Max tokens", kind: fText, s: &a.st.MaxTokens, ph: "(no limit)",
			hint: "caps each answer; too low truncates the JSON and counts as invalid"},
		{key: "nothink", label: "Disable thinking", kind: fBool, b: &a.st.NoThink,
			hint: "space to toggle — skips reasoning on thinking models (much faster)"},
	}}
	return a.refreshModels()
}

func (a *app) refreshModels() tea.Cmd {
	a.modelsReq++
	a.modelsBusy, a.modelsErr = true, ""
	return fetchModels(a.modelsReq, a.st.Provider, a.st.Endpoint)
}

func (a app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
	case tickMsg:
		a.tick++
		return a, tickCmd()
	case hwMsg:
		a.hw, a.hwOK = hardware.Info(m), true
	case modelsMsg:
		if m.id != a.modelsReq {
			return a, nil
		}
		a.modelsBusy = false
		if m.err != nil {
			a.models, a.modelsErr = nil, "no models listed ("+m.err.Error()+")"
		} else {
			a.models = m.models
			if a.st.Model == "" && len(m.models) > 0 {
				a.st.Model = m.models[0]
			}
		}
	case checkMsg:
		if m.id != a.reqID || a.sc != scCheckBusy {
			return a, nil
		}
		return a.onCheck(m)
	case benchEvt:
		if a.run == nil || m.run != a.run {
			return a, nil
		}
		a.run.apply(m)
		if m.kind == evDone {
			return a, detectHW()
		}
		return a, a.run.wait()
	case tea.KeyMsg:
		if m.String() == "ctrl+c" {
			if a.run != nil {
				a.run.cancel()
			}
			return a, tea.Quit
		}
		switch a.sc {
		case scMenu:
			return a.keyMenu(m)
		case scCheckForm, scBenchForm:
			return a.keyForm(m)
		case scCheckBusy:
			if m.String() == "esc" || m.String() == "q" {
				a.reqID++ // drop the pending result
				a.sc = scCheckForm
			}
		case scPicker:
			return a.keyPicker(m)
		case scCheckResult:
			return a.keyResult(m)
		case scBenchRun:
			return a.keyBench(m)
		case scError:
			return a.keyError(m)
		}
	}
	return a, nil
}

func (a app) onCheck(m checkMsg) (tea.Model, tea.Cmd) {
	var amb *gguf.AmbiguousError
	switch {
	case errors.As(m.err, &amb):
		a.cands, a.pick, a.pickOf, a.sc = amb.Candidates, 0, amb.Repo, scPicker
	case m.err != nil:
		a.errTitle, a.errMsg, a.errBack, a.sc = "Could not analyse the model", m.err.Error(), scCheckForm, scError
		a.errTip = tipFor(m.err.Error())
	default:
		a.checkRes, a.scroll, a.sc = m.res, 0, scCheckResult
	}
	return a, nil
}

func tipFor(msg string) string {
	switch {
	case strings.Contains(msg, "HF_TOKEN"), strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "The repo is gated or private. Set the HF_TOKEN environment variable with a Hugging Face token and restart."
	case strings.Contains(msg, "404"), strings.Contains(msg, "not found"):
		return "Check the spelling: Hugging Face repos look like  organisation/name  and must contain .gguf files."
	case strings.Contains(msg, "no matching"):
		return "No file matched that quantization. Clear the Quantization field to list what the repo offers."
	case strings.Contains(msg, "dial"), strings.Contains(msg, "timeout"), strings.Contains(msg, "no such host"):
		return "Network problem: check your internet connection."
	}
	return ""
}

func (a app) keyMenu(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		a.menu = (a.menu + 2) % 3
	case "down", "j":
		a.menu = (a.menu + 1) % 3
	case "1":
		a.openCheckForm()
	case "2":
		cmd := a.openBenchForm()
		return a, cmd
	case "q", "esc", "3":
		return a, tea.Quit
	case "enter":
		switch a.menu {
		case 0:
			a.openCheckForm()
		case 1:
			cmd := a.openBenchForm()
			return a, cmd
		default:
			return a, tea.Quit
		}
	}
	return a, nil
}

func (a app) keyForm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := a.form
	fd := f.cur()
	move := func(d int) tea.Cmd {
		prev := fd.key
		f.focus = (f.focus + d + len(f.fields)) % len(f.fields)
		if prev == "endpoint" && a.sc == scBenchForm {
			return a.refreshModelsPtr()
		}
		return nil
	}
	touch := func() { // editing a tunable detaches from the named preset
		if a.sc == scBenchForm {
			switch fd.key {
			case "runs", "conc", "ctx", "maxtok", "nothink":
				a.st.Preset = "Custom"
			}
		}
		f.err = ""
	}
	switch k.Type {
	case tea.KeyEsc:
		a.sc = scMenu
		return a, detectHW()
	case tea.KeyTab, tea.KeyDown:
		cmd := move(1)
		return a, cmd
	case tea.KeyShiftTab, tea.KeyUp:
		cmd := move(-1)
		return a, cmd
	case tea.KeyEnter:
		return a.submit()
	case tea.KeyCtrlU:
		if fd.kind == fText {
			*fd.s = ""
			touch()
		}
	case tea.KeyBackspace:
		if fd.kind == fText && *fd.s != "" {
			_, n := utf8.DecodeLastRuneInString(*fd.s)
			*fd.s = (*fd.s)[:len(*fd.s)-n]
			touch()
		}
	case tea.KeySpace:
		switch fd.kind {
		case fBool:
			*fd.b = !*fd.b
			touch()
		case fText:
			*fd.s += " "
			touch()
		}
	case tea.KeyRunes:
		if fd.kind == fText {
			*fd.s += string(k.Runes)
			touch()
		}
	case tea.KeyLeft, tea.KeyRight:
		d := 1
		if k.Type == tea.KeyLeft {
			d = -1
		}
		switch {
		case fd.kind == fChoice:
			cur := 0
			for i, c := range fd.choices {
				if c == *fd.s {
					cur = i
				}
			}
			*fd.s = fd.choices[(cur+d+len(fd.choices))%len(fd.choices)]
			f.err = ""
			switch fd.key {
			case "preset":
				a.st.applyPreset(*fd.s)
			case "provider":
				if a.st.Provider == "openai" && a.st.Endpoint == ollamaDefault {
					a.st.Endpoint = openaiDefault
				} else if a.st.Provider == "ollama" && a.st.Endpoint == openaiDefault {
					a.st.Endpoint = ollamaDefault
				}
				a.models = nil
				cmd := a.refreshModels()
				return a, cmd
			}
		case fd.kind == fBool:
			*fd.b = !*fd.b
			touch()
		case fd.key == "model" && len(a.models) > 0:
			cur := -1
			for i, m := range a.models {
				if m == a.st.Model {
					cur = i
				}
			}
			a.st.Model = a.models[(cur+d+len(a.models))%len(a.models)]
		}
	}
	return a, nil
}

// refreshModelsPtr lets closures that captured a value copy still schedule a refresh.
func (a *app) refreshModelsPtr() tea.Cmd { return a.refreshModels() }

func (a app) submit() (tea.Model, tea.Cmd) {
	f := a.form
	switch a.sc {
	case scCheckForm:
		if strings.TrimSpace(a.st.CheckTarget) == "" {
			f.err = "Enter a Hugging Face repo or the path of a .gguf file."
			return a, nil
		}
		vram := 0.0
		if t := strings.TrimSpace(a.st.CheckVRAM); t != "" {
			v, err := strconv.ParseFloat(t, 64)
			if err != nil || v <= 0 {
				f.err = "VRAM budget must be a positive number of GiB (or empty)."
				return a, nil
			}
			vram = v
		}
		a.override = vram > 0
		a.reqID++
		a.sc, a.busyTarget, a.busySince = scCheckBusy, strings.TrimSpace(a.st.CheckTarget), time.Now()
		hw := a.hw
		p := probe.CheckParams{Target: a.st.CheckTarget, Quant: strings.TrimSpace(a.st.CheckQuant),
			VRAMGB: vram, OverheadMiB: 512, OverheadPct: 5}
		if a.hwOK {
			p.HW = &hw
		}
		return a, checkCmd(a.reqID, p)
	case scBenchForm:
		cfg, err := a.st.BenchConfig()
		if err != nil {
			f.err = err.Error()
			return a, nil
		}
		return a.startBench(cfg)
	}
	return a, nil
}

func (a app) startBench(cfg bench.Config) (tea.Model, tea.Cmd) {
	r, cmd := newBenchRun(a.parent, cfg)
	a.run, a.sc = r, scBenchRun
	return a, cmd
}

func (a app) keyPicker(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		a.pick = (a.pick + len(a.cands) - 1) % len(a.cands)
	case "down", "j":
		a.pick = (a.pick + 1) % len(a.cands)
	case "esc", "b":
		a.sc = scCheckForm
	case "enter":
		c := a.cands[a.pick]
		a.reqID++
		a.sc, a.busyTarget, a.busySince = scCheckBusy, c.Name, time.Now()
		hw := a.hw
		vram, _ := strconv.ParseFloat(strings.TrimSpace(a.st.CheckVRAM), 64)
		p := probe.CheckParams{Target: a.pickOf, File: c.Path(), VRAMGB: vram, OverheadMiB: 512, OverheadPct: 5}
		if a.hwOK {
			p.HW = &hw
		}
		return a, checkCmd(a.reqID, p)
	case "q":
		return a, tea.Quit
	}
	return a, nil
}

func (a app) resultLines() []string {
	return strings.Split(render.CheckReport(a.checkRes, a.override, a.cw()), "\n")
}

func (a app) keyResult(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	lines := a.resultLines()
	page := a.ht() - 6
	if page < 5 {
		page = 5
	}
	maxScroll := len(lines) - page
	if maxScroll < 0 {
		maxScroll = 0
	}
	switch k.String() {
	case "up", "k":
		a.scroll--
	case "down", "j":
		a.scroll++
	case "pgup":
		a.scroll -= page
	case "pgdown", " ":
		a.scroll += page
	case "home", "g":
		a.scroll = 0
	case "end", "G":
		a.scroll = maxScroll
	case "b", "esc":
		a.sc = scCheckForm
	case "m":
		a.sc = scMenu
	case "q":
		return a, tea.Quit
	}
	if a.scroll < 0 {
		a.scroll = 0
	}
	if a.scroll > maxScroll {
		a.scroll = maxScroll
	}
	return a, nil
}

func (a app) keyBench(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := a.run
	switch k.String() {
	case "q", "esc":
		if !r.done {
			r.cancel() // the run reports done; keep the partial results on screen
			return a, nil
		}
		if a.standalone {
			return a, tea.Quit
		}
		a.sc = scBenchForm
		a.openBenchFormKeep()
	case "r":
		if r.done {
			return a.startBench(r.cfg)
		}
	case "s":
		if r.done && len(r.samples) > 0 {
			r.save()
		}
	case "b":
		if r.done {
			if a.standalone {
				return a, tea.Quit
			}
			a.openBenchFormKeep()
		}
	case "m":
		if r.done {
			if a.standalone {
				return a, tea.Quit
			}
			a.sc = scMenu
		}
	}
	return a, nil
}

// openBenchFormKeep reopens the bench form without refetching models.
func (a *app) openBenchFormKeep() {
	busy := a.modelsBusy
	a.openBenchForm()
	a.modelsReq--
	a.modelsBusy = busy
}

func (a app) keyError(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter", "esc", "b":
		a.sc = a.errBack
	case "m":
		a.sc = scMenu
	case "q":
		return a, tea.Quit
	}
	return a, nil
}

// ---------- views ----------

func chips(items ...string) string {
	var out []string
	for i := 0; i+1 < len(items); i += 2 {
		out = append(out, render.Accent.Bold(true).Render(items[i])+" "+render.Dim.Render(items[i+1]))
	}
	return strings.Join(out, render.Dim.Render("  ·  "))
}

func (a app) header() string {
	badge := lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(render.CAccent).Render(" vramlens ")
	line1 := badge + "  " + render.Dim.Render("VRAM doctor · KV-cache calculator · JSON benchmark")
	if !a.hwOK {
		return line1 + "\n" + render.Dim.Render(spinner(a.tick)+" detecting hardware…")
	}
	var parts []string
	if len(a.hw.GPUs) > 0 {
		g := a.hw.GPUs[0]
		_, free := a.hw.TotalVRAM()
		parts = append(parts, render.Bold.Render(strings.TrimPrefix(g.Name, "NVIDIA "))+" "+
			render.Bar(g.VRAMTotal-g.VRAMFree, g.VRAMTotal, 10)+" "+render.Dim.Render(fmt.Sprintf("%.1f GiB free", float64(free)/(1<<30))))
	} else {
		parts = append(parts, render.Warn.Render("no GPU detected"))
	}
	parts = append(parts, render.Dim.Render("RAM")+" "+render.Bar(a.hw.RAMTotal-a.hw.RAMFree, a.hw.RAMTotal, 10)+" "+
		render.Dim.Render(fmt.Sprintf("%.1f GiB free", float64(a.hw.RAMFree)/(1<<30))))
	return line1 + "\n" + strings.Join(parts, render.Dim.Render("   │   "))
}

func (a app) frame(body string, help string) string {
	return a.header() + "\n\n" + body + "\n\n" + help
}

func (a app) View() string {
	w := a.cw()
	switch a.sc {
	case scMenu:
		return a.frame(a.viewMenu(), chips("↑↓", "move", "enter", "select", "1 2", "shortcut", "q", "quit"))
	case scCheckForm, scBenchForm:
		return a.frame(render.Box(a.form.title, a.renderForm(a.form), w, render.CAccent),
			chips("tab/↑↓", "next field", "←→", "change", "enter", "run", "ctrl+u", "clear", "esc", "back"))
	case scCheckBusy:
		body := fmt.Sprintf("%s Reading the GGUF header of %s…\n%s", render.Accent.Render(spinner(a.tick)),
			render.Bold.Render(render.Trunc(a.busyTarget, w-24)),
			render.Dim.Render(fmt.Sprintf("Only the first few KB are fetched — the model is not downloaded.  (%s)", render.Clock(time.Since(a.busySince)))))
		return a.frame(render.Box("Analysing", body, w, render.CAccent), chips("esc", "cancel"))
	case scPicker:
		return a.frame(a.viewPicker(), chips("↑↓", "move", "enter", "analyse", "esc", "back"))
	case scCheckResult:
		lines := a.resultLines()
		page := a.ht() - 6
		if page < 5 {
			page = 5
		}
		end := a.scroll + page
		if end > len(lines) {
			end = len(lines)
		}
		start := a.scroll
		if start > end {
			start = end
		}
		body := strings.Join(lines[start:end], "\n")
		pos := ""
		if len(lines) > page {
			pos = render.Dim.Render(fmt.Sprintf("  ·  line %d–%d of %d", start+1, end, len(lines)))
		}
		return a.header() + "\n\n" + body + "\n" + chips("↑↓/pgup/pgdn", "scroll", "b", "change model", "m", "menu", "q", "quit") + pos
	case scBenchRun:
		help := chips("q", "cancel")
		if a.run.done {
			if a.standalone {
				help = chips("r", "run again", "s", "save .json", "q", "quit")
			} else {
				help = chips("r", "run again", "s", "save .json", "b", "settings", "m", "menu", "q", "quit")
			}
		}
		return a.header() + "\n\n" + a.viewBench() + "\n" + help
	case scError:
		inner := w - 4
		msg := lipgloss.NewStyle().Width(inner).Render(a.errMsg)
		if a.errTip != "" {
			msg += "\n\n" + render.Warn.Render("Tip: ") + lipgloss.NewStyle().Width(inner-5).Render(a.errTip)
		}
		return a.frame(render.Box(a.errTitle, msg, w, render.CRed), chips("enter", "go back", "m", "menu", "q", "quit"))
	}
	return ""
}

func (a app) viewMenu() string {
	w := a.cw()
	if w > 78 {
		w = 78
	}
	type item struct{ key, title, desc string }
	items := []item{
		{"1", "Diagnose a model", "Will it fit my GPU? Reads the GGUF header (nothing is downloaded)\nand shows memory use and the context where it spills to the CPU."},
		{"2", "Benchmark a server", "Measures speed, time-to-first-token and how often the model\nreturns valid JSON that matches a schema."},
	}
	compact := a.ht() < 26
	var out []string
	for i, it := range items {
		sel := a.menu == i
		col := render.CDim
		mark := "  "
		if sel {
			col, mark = render.CAccent, "▶ "
		}
		title := render.Bold.Render(it.title)
		if sel {
			title = render.Accent.Bold(true).Render(it.title)
		}
		head := render.Accent.Render(mark) + render.Dim.Render("["+it.key+"] ") + title
		if compact {
			out = append(out, head)
			continue
		}
		out = append(out, render.Box("", head+"\n  "+strings.ReplaceAll(render.Dim.Render(it.desc), "\n", "\n  "), w, col))
	}
	quit := "  " + render.Dim.Render("[q] Quit")
	if a.menu == 2 {
		quit = render.Accent.Render("▶ ") + render.Accent.Bold(true).Render("Quit")
	}
	out = append(out, quit)
	return strings.Join(out, "\n")
}

func (a app) viewPicker() string {
	w := a.cw()
	free := a.vramFree()
	var l []string
	l = append(l, render.Dim.Render(fmt.Sprintf("%s has %d files. Pick one (budget: %s of VRAM).", a.pickOf, len(a.cands), render.GiB(free))), "")
	max := a.ht() - 14
	if max < 5 {
		max = 5
	}
	start := 0
	if a.pick >= max {
		start = a.pick - max + 1
	}
	end := start + max
	if end > len(a.cands) {
		end = len(a.cands)
	}
	nameW := w - 4 - 2 - 12 - 12
	if nameW < 20 {
		nameW = 20
	}
	for i := start; i < end; i++ {
		c := a.cands[i]
		overhead := uint64(512<<20) + c.Size/20
		badge := render.Good.Render("✓ fits")
		switch {
		case free == 0:
			badge = render.Dim.Render("?")
		case c.Size+overhead > free && c.Size < free:
			badge = render.Warn.Render("◐ tight")
		case c.Size >= free:
			badge = render.Bad.Render("✗ too big")
		}
		name := render.Pad(strings.TrimSuffix(c.Name, ".gguf"), nameW)
		line := fmt.Sprintf("%s %s %s", name, render.PadLeft(render.GiB(c.Size), 11), badge)
		if i == a.pick {
			line = render.Accent.Render("▶ ") + render.Accent.Bold(true).Render(name) + fmt.Sprintf(" %s %s", render.PadLeft(render.GiB(c.Size), 11), badge)
		} else {
			line = "  " + line
		}
		l = append(l, line)
	}
	if len(a.cands) > end || start > 0 {
		l = append(l, render.Dim.Render(fmt.Sprintf("  showing %d–%d of %d", start+1, end, len(a.cands))))
	}
	l = append(l, "", render.Dim.Render("✓ fits with room for context · ◐ tight, little room for context · ✗ needs CPU offload"))
	return render.Box("Choose a file", strings.Join(l, "\n"), w, render.CViolet)
}
