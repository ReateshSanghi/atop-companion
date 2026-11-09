package telemetry

import (
	"errors"
	"fmt"
)

// ErrUnsupportedVersion is returned when a raw log's atop version and statistics size have no registered layout
var ErrUnsupportedVersion = errors.New("unsupported atop version")

// RawLayout gives byte offsets inside the decompressed system-statistics (sstat) block for one atop release.
// Offsets are the ones used by pam_monitoring_tool/src/atop_parser/reader.py (LAYOUT_2_8), which were checked
// against real atop 2.8 logs. Memory fields are 8-byte words counted from MemOff.
type RawLayout struct {
	Version  uint16 // major<<8 | minor
	SstatLen uint32 // decompressed block length from the file header; part of the lookup key

	NrcpuOff int // u64: number of CPUs
	CPUOff   int // 9 x u64: system, user, nice, idle, wait, irq, softirq, steal, guest
	LoadOff  int // 3 x f32: load average over 1, 5 and 15 minutes

	MemOff         int // start of the memory section
	MemPhys        int // word index of physical memory (pages)
	MemFree        int // free memory (pages)
	MemBuffer      int // buffer memory (pages)
	MemCache       int // page cache (pages)
	MemSwapTotal   int // total swap (pages)
	MemSwapFree    int // free swap (pages)
	MemSlabReclaim int // reclaimable slab (pages); counted as available, like `free` does

	DskOff     int // first per-disk entry
	DskStride  int // bytes between consecutive disk entries
	DskNameLen int // NUL-padded device name at the start of each entry; counters follow it
	DskMax     int // capacity of the disk array; the list ends at the first empty name

	NetCountOff int // u32: number of valid interface entries
	NetOff      int // first interface entry
	NetStride   int // bytes between consecutive interface entries
	NetNameLen  int // NUL-padded interface name at the start of each entry
	NetRxOff    int // offset of the received-bytes counter inside an entry
	NetTxOff    int // offset of the transmitted-bytes counter inside an entry
	NetMax      int // capacity of the interface array
}

// Indexes into the 9 CPU tick counters
const (
	tickSystem = iota
	tickUser
	tickNice
	tickIdle
	tickWait
	tickIrq
	tickSoftirq
	tickSteal
	tickGuest
	tickCount
)

// layout28 is atop 2.8 (header version word 0x8208), used by Ubuntu 24.04, RHEL 9 and similar
var layout28 = RawLayout{
	Version:  0x0208,
	SstatLen: 1021960,
	NrcpuOff: 0,
	CPUOff:   88,
	LoadOff:  32,

	MemOff:         344312,
	MemPhys:        0,
	MemFree:        1,
	MemBuffer:      2,
	MemCache:       4,
	MemSwapTotal:   6,
	MemSwapFree:    7,
	MemSlabReclaim: 19,

	// Disk entries are 128 bytes: a 32-byte name, then nread, nrsect, nwrite, nwsect, io_ms as u64
	DskOff:     560672,
	DskStride:  128,
	DskNameLen: 32,
	DskMax:     256,

	// Interface entries are 272 bytes: a 16-byte name, rx bytes at +16 and tx bytes 96 bytes later
	NetCountOff: 345592,
	NetOff:      345600,
	NetStride:   272,
	NetNameLen:  16,
	NetRxOff:    16,
	NetTxOff:    16 + 96,
	NetMax:      256,
}

// rawLayouts lists every release the native decoder understands
var rawLayouts = []RawLayout{layout28}

// findLayout returns the registered layout for the file's version and statistics block size
func findLayout(h *AtopRawHeader) (RawLayout, error) {
	for _, l := range rawLayouts {
		if l.Version == h.Version && l.SstatLen == h.SstatLen {
			return l, nil
		}
	}
	return RawLayout{}, fmt.Errorf("%w: atop %d.%d with statistics block of %d bytes",
		ErrUnsupportedVersion, h.Version>>8, h.Version&0xFF, h.SstatLen)
}
