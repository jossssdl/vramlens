//go:build !darwin

package hardware

func detectGPUs(info *Info) {
	gpus, err := detectNvidia()
	if err != nil {
		info.Warnings = append(info.Warnings, "no NVIDIA GPU detected (nvidia-smi: "+err.Error()+")")
		return
	}
	info.GPUs = gpus
}
