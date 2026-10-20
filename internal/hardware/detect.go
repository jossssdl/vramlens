package hardware

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

// Detect gathers RAM and GPU memory information. Failures to detect a GPU
// are reported in Info.Warnings rather than as errors: CPU-only is valid.
func Detect() Info {
	info := Info{OS: runtime.GOOS}
	if err := detectRAM(&info); err != nil {
		info.Warnings = append(info.Warnings, "RAM: "+err.Error())
	}
	detectGPUs(&info)
	return info
}

func runCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}
