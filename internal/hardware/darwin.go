//go:build darwin

package hardware

import (
	"regexp"
	"strconv"
	"strings"
)

var vmStatRe = regexp.MustCompile(`Pages (free|inactive|speculative):\s+(\d+)\.`)
var pageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)

func detectRAM(info *Info) error {
	out, err := runCmd("sysctl", "-n", "hw.memsize")
	if err != nil {
		return err
	}
	info.RAMTotal, _ = strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	if vm, err := runCmd("vm_stat"); err == nil {
		page := uint64(16384)
		if m := pageSizeRe.FindStringSubmatch(vm); m != nil {
			page, _ = strconv.ParseUint(m[1], 10, 64)
		}
		var pages uint64
		for _, m := range vmStatRe.FindAllStringSubmatch(vm, -1) {
			n, _ := strconv.ParseUint(m[2], 10, 64)
			pages += n
		}
		info.RAMFree = pages * page
	}
	return nil
}

// appleUsableGPUBytes estimates the share of unified memory Metal may wire.
// iogpu.wired_limit_mb overrides the default heuristic when non-zero.
func appleUsableGPUBytes(total uint64, wiredLimitMB uint64) uint64 {
	if wiredLimitMB > 0 {
		return wiredLimitMB * mib
	}
	if total >= 64<<30 {
		return total * 75 / 100
	}
	return total * 2 / 3
}

func detectGPUs(info *Info) {
	name := "Apple GPU"
	if out, err := runCmd("sysctl", "-n", "machdep.cpu.brand_string"); err == nil {
		name = strings.TrimSpace(out)
	}
	if strings.HasPrefix(name, "Intel") {
		gpus, err := detectNvidia()
		if err == nil {
			info.GPUs = gpus
		} else {
			info.Warnings = append(info.Warnings, "Intel Mac: no supported GPU detected")
		}
		return
	}
	var wired uint64
	if out, err := runCmd("sysctl", "-n", "iogpu.wired_limit_mb"); err == nil {
		wired, _ = strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	}
	usable := appleUsableGPUBytes(info.RAMTotal, wired)
	free := usable
	if info.RAMFree < free {
		free = info.RAMFree
	}
	info.Unified = true
	info.GPUs = []GPU{{Name: name + " (unified memory)", Vendor: "apple", VRAMTotal: usable, VRAMFree: free}}
}
