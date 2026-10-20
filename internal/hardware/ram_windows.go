//go:build windows

package hardware

import (
	"syscall"
	"unsafe"
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func detectRAM(info *Info) error {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, err := proc.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return err
	}
	info.RAMTotal, info.RAMFree = m.TotalPhys, m.AvailPhys
	return nil
}
