package telemetry

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

// ErrNoMetricRecords is returned when a file has a valid header but no record could be decoded
var ErrNoMetricRecords = errors.New("no metric records could be decoded (corrupt file or unexpected layout)")

// maxSaneIntervalSeconds: a record covering more than a day is treated as since-boot totals, not a delta
const maxSaneIntervalSeconds = 86400

// sectorBytes is the size of one kernel disk sector, as used by the sector counters in atop's disk entries
const sectorBytes = 512

// virtualIfacePrefixes are interfaces left out of network totals: their traffic is already counted on the
// physical interfaces underneath them (same list as reader.py)
var virtualIfacePrefixes = []string{"lo", "bond", "docker", "veth", "br-", "virbr", "tun", "tap"}

// FileData is everything decoded from one raw log
type FileData struct {
	Path    string
	Header  *AtopRawHeader
	Samples []Sample // chronological; the since-boot record is not included
	Skipped int      // records that could not be decoded
}

// u64At reads a little-endian uint64 at the given offset, failing if it would run past the block
func u64At(block []byte, offset int) (uint64, error) {
	if offset < 0 || offset+8 > len(block) {
		return 0, fmt.Errorf("field at offset %d is outside the %d-byte block", offset, len(block))
	}
	return binary.LittleEndian.Uint64(block[offset:]), nil
}

// decodeSstat turns one decompressed system-statistics block into a Sample for an interval of `interval`
// seconds ending at t. CPU, disk and network counters are per-interval deltas in atop raw logs.
// It returns an error if the block does not look like the registered layout.
func decodeSstat(block []byte, lay RawLayout, hdr *AtopRawHeader, t time.Time, interval int) (Sample, error) {
	s := Sample{Time: t, Interval: interval}

	// CPU
	ncpu, err := u64At(block, lay.NrcpuOff)
	if err != nil {
		return s, err
	}
	if ncpu == 0 || ncpu > 4096 {
		return s, fmt.Errorf("layout check failed: implausible CPU count %d", ncpu)
	}
	s.NCPU = int(ncpu)

	var ticks [tickCount]uint64
	for i := range ticks {
		if ticks[i], err = u64At(block, lay.CPUOff+8*i); err != nil {
			return s, err
		}
	}
	// Guest time is already counted inside user time, so it is left out of the total (as in reader.py)
	total := ticks[tickSystem] + ticks[tickUser] + ticks[tickNice] + ticks[tickIdle] +
		ticks[tickWait] + ticks[tickIrq] + ticks[tickSoftirq] + ticks[tickSteal]

	// A busy machine accumulates at most ncpu * interval * hertz ticks in one interval. Much more than that
	// means the counters are totals since boot, which cannot give a percentage for this interval.
	expected := float64(ncpu) * float64(interval) * float64(hdr.Hertz)
	if total == 0 || float64(total) > 1.5*expected {
		return s, fmt.Errorf("layout check failed: CPU ticks %d do not match the interval", total)
	}
	pct := func(v uint64) float64 { return 100.0 * float64(v) / float64(total) }
	s.CPUBusy = pct(total - ticks[tickIdle] - ticks[tickWait])
	s.CPUIOWait = pct(ticks[tickWait])
	s.CPUSteal = pct(ticks[tickSteal])

	// Load averages are stored as float32
	for i, dst := range []*float64{&s.Load1, &s.Load5, &s.Load15} {
		off := lay.LoadOff + 4*i
		if off+4 > len(block) {
			return s, fmt.Errorf("load average outside the block")
		}
		*dst = float64(math.Float32frombits(binary.LittleEndian.Uint32(block[off:])))
	}

	// Memory and swap are counted in pages
	word := func(i int) (uint64, error) { return u64At(block, lay.MemOff+8*i) }
	var mem [8]uint64
	for i, idx := range []int{lay.MemPhys, lay.MemFree, lay.MemBuffer, lay.MemCache,
		lay.MemSwapTotal, lay.MemSwapFree, lay.MemSlabReclaim} {
		if mem[i], err = word(idx); err != nil {
			return s, err
		}
	}
	phys, free, buffers, cache := mem[0], mem[1], mem[2], mem[3]
	swapTotal, swapFree, reclaim := mem[4], mem[5], mem[6]
	if phys == 0 || free > phys {
		return s, fmt.Errorf("layout check failed: implausible memory figures")
	}
	pageBytes := float64(hdr.PageSize)
	reclaimable := free + cache + buffers + reclaim
	var used uint64
	if phys > reclaimable {
		used = phys - reclaimable
	}
	s.MemPct = 100.0 * float64(used) / float64(phys)
	s.MemUsedBytes = float64(used) * pageBytes
	s.MemTotalBytes = float64(phys) * pageBytes
	if swapTotal > 0 && swapFree <= swapTotal {
		s.SwapUsedBytes = float64(swapTotal-swapFree) * pageBytes
		s.SwapTotalBytes = float64(swapTotal) * pageBytes
		s.SwapPct = 100.0 * float64(swapTotal-swapFree) / float64(swapTotal)
	}

	// Disks: read, write and busy time per device
	for i := 0; i < lay.DskMax; i++ {
		off := lay.DskOff + i*lay.DskStride
		if off+lay.DskNameLen+40 > len(block) {
			return s, fmt.Errorf("disk table runs past the end of the block")
		}
		name := block[off : off+lay.DskNameLen]
		if !isPrintableName(name) {
			break // the list ends at the first empty name
		}
		// Counters after the name: nread, nrsect, nwrite, nwsect, io_ms
		c := off + lay.DskNameLen
		nrsect, _ := u64At(block, c+8)
		nwsect, _ := u64At(block, c+24)
		ioMs, _ := u64At(block, c+32)
		busy := 100.0 * float64(ioMs) / (float64(interval) * 1000.0)
		s.Disks = append(s.Disks, DiskStat{
			Name:      cString(name),
			ReadMBps:  float64(nrsect) * sectorBytes / 1048576.0 / float64(interval),
			WriteMBps: float64(nwsect) * sectorBytes / 1048576.0 / float64(interval),
			BusyPct:   math.Min(busy, 100),
		})
	}

	// Network: bytes per interface; virtual interfaces are skipped
	nIfaces, _ := u64At(block, lay.NetCountOff)
	if nIfaces > uint64(lay.NetMax) {
		nIfaces = uint64(lay.NetMax)
	}
	for i := 0; i < int(nIfaces); i++ {
		off := lay.NetOff + i*lay.NetStride
		if off+lay.NetTxOff+8 > len(block) {
			break
		}
		name := cString(block[off : off+lay.NetNameLen])
		if name == "" || hasAnyPrefix(name, virtualIfacePrefixes) {
			continue
		}
		rx, _ := u64At(block, off+lay.NetRxOff)
		tx, _ := u64At(block, off+lay.NetTxOff)
		s.Nets = append(s.Nets, NetStat{
			Name:  name,
			RxBps: float64(rx) / float64(interval),
			TxBps: float64(tx) / float64(interval),
		})
	}

	return s, nil
}

// hasAnyPrefix reports whether s starts with any of the prefixes
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// isPrintableName reports whether a NUL-padded name field starts with a printable character
func isPrintableName(field []byte) bool {
	if field[0] == 0 {
		return false
	}
	for _, c := range field {
		if c == 0 {
			return true
		}
		if c < 0x20 || c > 0x7E {
			return false
		}
	}
	return true
}

// cString returns the NUL-terminated string held in a fixed-size field
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// inflateBlock reads a zlib-compressed block and returns exactly `size` decompressed bytes
func inflateBlock(file *os.File, offset int64, compressedLen uint32, size uint32) ([]byte, error) {
	compressed := make([]byte, compressedLen)
	if _, err := io.ReadFull(io.NewSectionReader(file, offset, int64(compressedLen)), compressed); err != nil {
		return nil, err
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	out := make([]byte, size)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, err
	}
	// The block must end exactly at the declared size
	if n, _ := zr.Read(make([]byte, 1)); n != 0 {
		return nil, fmt.Errorf("statistics block is longer than %d bytes", size)
	}
	return out, nil
}

// inflateAll reads a zlib-compressed block and returns all of its decompressed bytes
func inflateAll(file *os.File, offset int64, compressedLen uint32) ([]byte, error) {
	compressed := make([]byte, compressedLen)
	if _, err := io.ReadFull(io.NewSectionReader(file, offset, int64(compressedLen)), compressed); err != nil {
		return nil, err
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// ReadFile decodes every usable record of one atop raw log. Records that cannot be decoded are counted in
// Skipped. It returns ErrUnsupportedVersion for an unknown layout and ErrNoMetricRecords if nothing decodes.
func ReadFile(path string) (*FileData, error) {
	header, err := ReadAtopHeader(path)
	if err != nil {
		return nil, err
	}
	lay, err := findLayout(header)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := fileInfo.Size()

	data := &FileData{Path: path, Header: header}
	recLen := int64(header.RecordLen)
	offset := int64(header.RawHeadLen)
	recHeader := make([]byte, recLen)

	for offset+recLen <= fileSize {
		if _, err := file.ReadAt(recHeader, offset); err != nil {
			break
		}
		// Record header fields: curtime u64 @0, compressed system block length u32 @16,
		// compressed process block length u32 @20, interval in seconds u32 @24
		curTime := int64(binary.LittleEndian.Uint64(recHeader[0:]))
		scompLen := binary.LittleEndian.Uint32(recHeader[16:])
		pcompLen := binary.LittleEndian.Uint32(recHeader[20:])
		interval := binary.LittleEndian.Uint32(recHeader[24:])

		blockStart := offset + recLen
		next := blockStart + int64(scompLen) + int64(pcompLen)
		if next > fileSize {
			break // truncated final record, e.g. the log is still being written
		}
		// The process block is not needed, so jump over it
		offset = next

		if interval == 0 || interval > maxSaneIntervalSeconds {
			continue // first record after a boot: its counters are totals since boot
		}

		block, err := inflateBlock(file, blockStart, scompLen, header.SstatLen)
		if err != nil {
			data.Skipped++
			continue
		}
		sample, err := decodeSstat(block, lay, header, time.Unix(curTime, 0).UTC(), int(interval))
		if err != nil {
			data.Skipped++
			continue
		}
		// Remember where the process block is; it is decoded only when a sample is inspected
		if pcompLen > 0 && header.TstatLen == tstatSize {
			sample.proc = &procSource{
				path:     path,
				offset:   blockStart + int64(scompLen),
				compLen:  pcompLen,
				interval: int(interval),
				hertz:    int(header.Hertz),
			}
		}
		data.Samples = append(data.Samples, sample)
	}

	if len(data.Samples) == 0 {
		return nil, fmt.Errorf("%w: %d record(s) could not be decoded", ErrNoMetricRecords, data.Skipped)
	}
	return data, nil
}
