package hardware

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const mib = 1024 * 1024

// parseNvidiaSMI parses the output of
// nvidia-smi --query-gpu=name,memory.total,memory.free --format=csv,noheader,nounits
func parseNvidiaSMI(out string) ([]GPU, error) {
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			return nil, fmt.Errorf("unexpected nvidia-smi line %q", line)
		}
		n := len(parts)
		total, err1 := strconv.ParseUint(strings.TrimSpace(parts[n-2]), 10, 64)
		free, err2 := strconv.ParseUint(strings.TrimSpace(parts[n-1]), 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("cannot parse memory in %q", line)
		}
		gpus = append(gpus, GPU{
			Name:      strings.TrimSpace(strings.Join(parts[:n-2], ",")),
			Vendor:    "nvidia",
			VRAMTotal: total * mib,
			VRAMFree:  free * mib,
		})
	}
	return gpus, nil
}

func detectNvidia() ([]GPU, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,memory.total,memory.free",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	return parseNvidiaSMI(string(out))
}
