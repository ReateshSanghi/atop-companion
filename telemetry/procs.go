package telemetry

import (
	"encoding/binary"
	"os"
	"sort"
)

// tstatSize is the size in bytes of one per-process record (struct tstat) in atop 2.8
const tstatSize = 968

// Offsets inside one tstat record, from atop 2.8.1 (atop.h / photoproc.h), little-endian
const (
	tstatPID     = 4
	tstatPPID    = 8
	tstatNThr    = 44
	tstatName    = 48 // char[16], NUL-terminated
	tstatIsProc  = 64 // 1 for a process leader, 0 for a thread
	tstatState   = 65 // one character, e.g. 'R', 'S', 'D'
	tstatCmdline = 88 // char[256]
	tstatUTime   = 448
	tstatSTime   = 456
)

// maxProcsPerSample limits how many processes are returned. Only the busiest ones are useful in a report.
const maxProcsPerSample = 20

// ProcessStat is one process during one interval
type ProcessStat struct {
	PID     int
	PPID    int
	Name    string
	Cmdline string
	State   string
	Threads int
	CPUPct  float64 // % of one CPU core used during the interval (100 = one full core)
}

// procSource says where the process block of a record is in its file
type procSource struct {
	path     string
	offset   int64  // start of the compressed process block
	compLen  uint32 // compressed length
	interval int
	hertz    int
}

// Processes decodes the busiest processes of this sample from its raw file. The block is read on demand
// because it is large (about 50 MB uncompressed per record) and most samples are never inspected.
// It returns nil for samples without a process block.
func (s *Sample) Processes() ([]ProcessStat, error) {
	if s.proc == nil {
		return nil, nil
	}
	file, err := os.Open(s.proc.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	block, err := inflateAll(file, s.proc.offset, s.proc.compLen)
	if err != nil {
		return nil, err
	}
	return decodeProcesses(block, s.proc.hertz, s.proc.interval), nil
}

// decodeProcesses reads the per-process block of one record and keeps the busiest processes by CPU.
// Only process leaders are counted; threads share the process statistics.
func decodeProcesses(block []byte, hertz, interval int) []ProcessStat {
	if interval <= 0 || hertz <= 0 {
		return nil
	}
	capacity := float64(interval) * float64(hertz) // ticks one core can give in the interval
	var procs []ProcessStat
	for off := 0; off+tstatSize <= len(block); off += tstatSize {
		rec := block[off : off+tstatSize]
		if rec[tstatIsProc] != 1 {
			continue
		}
		ticks := binary.LittleEndian.Uint64(rec[tstatUTime:]) + binary.LittleEndian.Uint64(rec[tstatSTime:])
		if ticks == 0 {
			continue
		}
		procs = append(procs, ProcessStat{
			PID:     int(int32(binary.LittleEndian.Uint32(rec[tstatPID:]))),
			PPID:    int(int32(binary.LittleEndian.Uint32(rec[tstatPPID:]))),
			Name:    cString(rec[tstatName : tstatName+16]),
			Cmdline: cString(rec[tstatCmdline : tstatCmdline+256]),
			State:   string(rec[tstatState : tstatState+1]),
			Threads: int(int32(binary.LittleEndian.Uint32(rec[tstatNThr:]))),
			CPUPct:  100.0 * float64(ticks) / capacity,
		})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].CPUPct > procs[j].CPUPct })
	if len(procs) > maxProcsPerSample {
		procs = procs[:maxProcsPerSample]
	}
	return procs
}
