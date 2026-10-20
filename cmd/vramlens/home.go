package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"vramlens/internal/tui"
)

// isTerminal reports whether stdout is an interactive terminal.
func isTerminal() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// pause keeps results visible (and the window open when launched by
// double-click) until the user presses Enter.
func pause() {
	fmt.Print("\nPress Enter to return to the menu… ")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// runHome drives the interactive menu until the user quits.
func runHome() {
	st := tui.NewHomeState()
	for {
		act, err := tui.Home(st)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			pause()
			return
		}
		switch act {
		case tui.ActionCheck:
			fmt.Println()
			err := checkOnce(checkParams{
				target: st.CheckTarget, quant: st.CheckQuant,
				overheadMiB: 512, overheadPct: 5,
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			pause()
		case tui.ActionBench:
			cfg, err := st.BenchConfig()
			if err == nil {
				cfg.APIKey = os.Getenv("OPENAI_API_KEY")
				cfg.Prompt = defaultPrompt
				if err = json.Unmarshal([]byte(defaultSchema), &cfg.Schema); err == nil {
					ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
					_, err = tui.RunBench(ctx, cfg)
					stop()
				}
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			pause()
		default:
			return
		}
	}
}
