// Package hardware detects total/free RAM and VRAM on the local machine.
package hardware

// GPU describes one accelerator. For unified-memory systems VRAM* reflect
// the portion of system memory the GPU may use.
type GPU struct {
	Name      string `json:"name"`
	Vendor    string `json:"vendor"`
	VRAMTotal uint64 `json:"vram_total"`
	VRAMFree  uint64 `json:"vram_free"`
}

// Info is a snapshot of the machine's memory resources, in bytes.
type Info struct {
	OS       string   `json:"os"`
	RAMTotal uint64   `json:"ram_total"`
	RAMFree  uint64   `json:"ram_free"`
	GPUs     []GPU    `json:"gpus"`
	Unified  bool     `json:"unified_memory"`
	Warnings []string `json:"warnings,omitempty"`
}

// TotalVRAM sums VRAM across all detected GPUs.
func (i Info) TotalVRAM() (total, free uint64) {
	for _, g := range i.GPUs {
		total += g.VRAMTotal
		free += g.VRAMFree
	}
	return
}
