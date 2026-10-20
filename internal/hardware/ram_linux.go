//go:build linux

package hardware

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func detectRAM(info *Info) error {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return err
	}
	defer f.Close()
	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSuffix(fields[0], ":")] = v * 1024
	}
	info.RAMTotal = vals["MemTotal"]
	info.RAMFree = vals["MemAvailable"]
	if info.RAMFree == 0 {
		info.RAMFree = vals["MemFree"]
	}
	return sc.Err()
}
