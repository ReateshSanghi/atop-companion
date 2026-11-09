package telemetry

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testHeader is an atop 2.8 header matching the layout in layout28
var testHeader = AtopRawHeader{
	Magic:      AtopMagic,
	Version:    0x0208,
	RawHeadLen: 480,
	RecordLen:  96,
	Hertz:      100,
	SstatLen:   layout28.SstatLen,
	TstatLen:   0,
	PageSize:   4096,
}

// sstatFields holds the values written into a test block
type sstatFields struct {
	ncpu                        uint64
	system, user, idle          uint64
	physPages, freePages        uint64
	buffers, cache, slabReclaim uint64
	swapTotal, swapFree         uint64
	load1                       float32
	disks                       []diskFields
	nets                        []netFields
}

type diskFields struct {
	name           string
	nrsect, nwsect uint64
	ioMs           uint64
}

type netFields struct {
	name   string
	rx, tx uint64
}

// buildBlock writes the given values into a block of the 2.8 layout
func buildBlock(f sstatFields) []byte {
	lay := layout28
	block := make([]byte, lay.SstatLen)
	put := func(off int, v uint64) { binary.LittleEndian.PutUint64(block[off:], v) }

	put(lay.NrcpuOff, f.ncpu)
	for i, v := range []uint64{f.system, f.user, 0, f.idle, 0, 0, 0, 0, 0} {
		put(lay.CPUOff+8*i, v)
	}
	binary.LittleEndian.PutUint32(block[lay.LoadOff:], math.Float32bits(f.load1))
	put(lay.MemOff+8*lay.MemPhys, f.physPages)
	put(lay.MemOff+8*lay.MemFree, f.freePages)
	put(lay.MemOff+8*lay.MemBuffer, f.buffers)
	put(lay.MemOff+8*lay.MemCache, f.cache)
	put(lay.MemOff+8*lay.MemSwapTotal, f.swapTotal)
	put(lay.MemOff+8*lay.MemSwapFree, f.swapFree)
	put(lay.MemOff+8*lay.MemSlabReclaim, f.slabReclaim)

	for i, d := range f.disks {
		off := lay.DskOff + i*lay.DskStride
		copy(block[off:off+lay.DskNameLen], d.name)
		put(off+lay.DskNameLen+8, d.nrsect)
		put(off+lay.DskNameLen+24, d.nwsect)
		put(off+lay.DskNameLen+32, d.ioMs)
	}

	binary.LittleEndian.PutUint64(block[lay.NetCountOff:], uint64(len(f.nets)))
	for i, n := range f.nets {
		off := lay.NetOff + i*lay.NetStride
		copy(block[off:off+lay.NetNameLen], n.name)
		put(off+lay.NetRxOff, n.rx)
		put(off+lay.NetTxOff, n.tx)
	}
	return block
}

// typicalFields: 4 CPUs over 10 s at 100 Hz (4000 ticks available); 40% busy; 1000 pages of memory, 600 used
func typicalFields() sstatFields {
	return sstatFields{
		ncpu:   4,
		system: 10, user: 30, idle: 60,
		physPages: 1000, freePages: 200, buffers: 100, cache: 100,
		load1: 1.5,
		disks: []diskFields{{name: "sda", nrsect: 1000, nwsect: 1000, ioMs: 500}},
		nets:  []netFields{{name: "eth0", rx: 10240, tx: 5120}, {name: "lo", rx: 999, tx: 999}},
	}
}

var t0 = time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)

func TestDecodeSstatComputesSystemFigures(t *testing.T) {
	s, err := decodeSstat(buildBlock(typicalFields()), layout28, &testHeader, t0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertClose(t, "cpu busy", s.CPUBusy, 40.0)
	assertClose(t, "mem", s.MemPct, 60.0)
	assertClose(t, "load1", s.Load1, 1.5)
	// 2000 sectors of 512 bytes over 10 s
	assertClose(t, "disk MB/s", s.Disks[0].WriteMBps, 1000*512/1048576.0/10)
	assertClose(t, "disk busy", s.Disks[0].BusyPct, 5.0) // 500 ms of 10 s
	if len(s.Nets) != 1 || s.Nets[0].Name != "eth0" {
		t.Fatalf("virtual interfaces must be skipped, got %+v", s.Nets)
	}
	assertClose(t, "rx B/s", s.Nets[0].RxBps, 1024)
}

func TestDecodeSstatRejectsCumulativeCounters(t *testing.T) {
	f := typicalFields()
	f.user = 10_000_000
	if _, err := decodeSstat(buildBlock(f), layout28, &testHeader, t0, 10); err == nil {
		t.Fatal("expected an error for since-boot counters")
	}
}

func TestDecodeSstatRejectsImplausibleMemory(t *testing.T) {
	f := typicalFields()
	f.freePages = 5000
	if _, err := decodeSstat(buildBlock(f), layout28, &testHeader, t0, 10); err == nil {
		t.Fatal("expected an error for free memory larger than physical memory")
	}
}

func TestDecodeSstatMemoryNeverWraps(t *testing.T) {
	f := typicalFields()
	f.buffers, f.cache, f.slabReclaim = 400, 400, 400
	s, err := decodeSstat(buildBlock(f), layout28, &testHeader, t0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.MemPct != 0 {
		t.Fatalf("expected memory utilisation clamped to 0, got %v", s.MemPct)
	}
}

func TestDecodeSstatSwap(t *testing.T) {
	f := typicalFields()
	f.swapTotal, f.swapFree = 1000, 750
	s, err := decodeSstat(buildBlock(f), layout28, &testHeader, t0, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertClose(t, "swap %", s.SwapPct, 25.0)
}

func TestReadAtopHeaderDecodesAtop28(t *testing.T) {
	path := writeAtopFile(t, nil)
	h, err := ReadAtopHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Version != 0x0208 || h.RawHeadLen != 480 || h.RecordLen != 96 || h.SstatLen != 1021960 {
		t.Fatalf("unexpected header %+v", h)
	}
	if h.Hostname != "testhost" {
		t.Fatalf("hostname = %q", h.Hostname)
	}
}

func TestReadAtopHeaderRejectsBadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bogus")
	if err := os.WriteFile(path, make([]byte, 600), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAtopHeader(path); err == nil {
		t.Fatal("expected an error for a file without the atop magic")
	}
}

func TestReadFileUnsupportedVersion(t *testing.T) {
	path := writeAtopFile(t, nil)
	h := testHeader
	h.Version = 0x0C00
	if _, err := findLayout(&h); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
	_ = path
}

func TestReadFileWithNoRecordsReturnsError(t *testing.T) {
	path := writeAtopFile(t, nil)
	if _, err := ReadFile(path); !errors.Is(err, ErrNoMetricRecords) {
		t.Fatalf("expected ErrNoMetricRecords, got %v", err)
	}
}

func TestReadFileDecodesRecords(t *testing.T) {
	path := writeAtopFile(t, []atopRecord{
		{time: t0.Unix() - 600, interval: 0, block: buildBlock(sstatFields{ncpu: 4})}, // since-boot: skipped
		{time: t0.Unix(), interval: 10, block: buildBlock(typicalFields())},
		{time: t0.Unix() + 600, interval: 10, block: buildBlock(typicalFields())},
	})

	fd, err := ReadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fd.Samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(fd.Samples))
	}
	if !fd.Samples[0].Time.Equal(t0) || !fd.Samples[1].Time.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("unexpected timestamps %v, %v", fd.Samples[0].Time, fd.Samples[1].Time)
	}
	assertClose(t, "cpu", fd.Samples[0].CPUBusy, 40.0)
}

func TestReadFileSkipsCorruptRecordAndKeepsGoing(t *testing.T) {
	bad := buildBlock(typicalFields())
	bad[layout28.NrcpuOff] = 0 // zero CPUs: fails the layout check

	path := writeAtopFile(t, []atopRecord{
		{time: t0.Unix(), interval: 10, block: bad},
		{time: t0.Unix() + 600, interval: 10, block: buildBlock(typicalFields())},
	})
	fd, err := ReadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fd.Samples) != 1 || fd.Skipped != 1 {
		t.Fatalf("expected one sample and one skipped record, got %d and %d", len(fd.Samples), fd.Skipped)
	}
}

func TestBuildPlotsSplitsUnitsAndDropsIdleDisks(t *testing.T) {
	f := typicalFields()
	f.disks = append(f.disks, diskFields{name: "loop0"}) // idle: must not appear
	s1, _ := decodeSstat(buildBlock(f), layout28, &testHeader, t0, 10)
	s2, _ := decodeSstat(buildBlock(f), layout28, &testHeader, t0.Add(10*time.Minute), 10)
	plots := BuildPlots([]Sample{s1, s2})

	units := map[string]bool{}
	for _, p := range plots {
		units[p.Unit] = true
		if p.Key == "disk_busy" && len(p.Series) != 1 {
			t.Fatalf("expected one disk series, got %d", len(p.Series))
		}
	}
	for _, u := range []string{"%", "load", "MB/s", "KB/s"} {
		if !units[u] {
			t.Fatalf("missing plot with unit %q", u)
		}
	}
}

func TestFindAnomaliesGroupsRunsAboveLimit(t *testing.T) {
	samples := make([]Sample, 6)
	for i := range samples {
		samples[i] = Sample{Time: t0.Add(time.Duration(i) * 10 * time.Minute), Interval: 600, NCPU: 4}
		samples[i].CPUBusy = 10
	}
	samples[1].CPUBusy = 85
	samples[2].CPUBusy = 95 // peak of the first run
	samples[4].CPUBusy = 82 // second run

	plots := BuildPlots(samples)
	found := FindAnomalies(samples, plots)
	var busy []Anomaly
	for _, a := range found {
		if a.Series == "busy" {
			busy = append(busy, a)
		}
	}
	if len(busy) != 2 {
		t.Fatalf("expected 2 runs for busy CPU, got %d", len(busy))
	}
	if busy[0].Start != 1 || busy[0].End != 2 || busy[0].PeakIndex != 2 || busy[0].Peak != 95 {
		t.Fatalf("first run wrong: %+v", busy[0])
	}
}

func TestDecodeProcessesKeepsBusiestLeaders(t *testing.T) {
	block := make([]byte, 3*tstatSize)
	put := func(rec int, off int, v uint64, size int) {
		p := block[rec*tstatSize+off:]
		if size == 4 {
			binary.LittleEndian.PutUint32(p, uint32(v))
		} else {
			binary.LittleEndian.PutUint64(p, v)
		}
	}
	// process 0: busy leader; process 1: a thread (ignored); process 2: idle leader
	copy(block[0*tstatSize+tstatName:], "busy")
	block[0*tstatSize+tstatIsProc] = 1
	put(0, tstatUTime, 3000, 8)
	copy(block[1*tstatSize+tstatName:], "thread")
	put(1, tstatUTime, 9000, 8)
	copy(block[2*tstatSize+tstatName:], "idle")
	block[2*tstatSize+tstatIsProc] = 1
	put(2, tstatUTime, 0, 8)

	procs := decodeProcesses(block, 100, 10)
	if len(procs) != 1 || procs[0].Name != "busy" {
		t.Fatalf("expected only the busy leader, got %+v", procs)
	}
	assertClose(t, "cpu %", procs[0].CPUPct, 300.0) // 3000 ticks over 10 s at 100 Hz = three full cores
}

// atopRecord is one system-statistics record to write into a test fixture
type atopRecord struct {
	time     int64
	interval uint32
	block    []byte
}

// writeAtopFile writes an atop 2.8 style file (480-byte header, 96-byte record headers, zlib blocks)
func writeAtopFile(t *testing.T, records []atopRecord) string {
	t.Helper()

	header := make([]byte, 480)
	binary.LittleEndian.PutUint32(header[0:], AtopMagic)
	binary.LittleEndian.PutUint16(header[4:], 0x8208)
	binary.LittleEndian.PutUint16(header[10:], 480)
	binary.LittleEndian.PutUint16(header[12:], 96)
	binary.LittleEndian.PutUint16(header[14:], 100)
	binary.LittleEndian.PutUint32(header[28:], layout28.SstatLen)
	binary.LittleEndian.PutUint32(header[436:], 4096)
	copy(header[101:], "testhost")

	var buf bytes.Buffer
	buf.Write(header)

	for _, rec := range records {
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(rec.block); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		recHeader := make([]byte, 96)
		binary.LittleEndian.PutUint64(recHeader[0:], uint64(rec.time))
		binary.LittleEndian.PutUint32(recHeader[16:], uint32(compressed.Len()))
		binary.LittleEndian.PutUint32(recHeader[20:], 0) // no process block in the fixture
		binary.LittleEndian.PutUint32(recHeader[24:], rec.interval)
		buf.Write(recHeader)
		buf.Write(compressed.Bytes())
	}

	path := filepath.Join(t.TempDir(), "test.raw")
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
}
