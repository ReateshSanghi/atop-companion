package telemetry

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// AtopMagic is the official hexadecimal signature for valid atop logs (0xFEEDBEEF)
const AtopMagic = 0xFEEDBEEF

// AtopRawHeader is the decoded file header of an atop 2.x raw log.
// Byte offsets follow the layout decoded and checked in pam_monitoring_tool/src/atop_parser/reader.py.
type AtopRawHeader struct {
	Magic      uint32
	Version    uint16 // major<<8 | minor, e.g. 0x0208 for atop 2.8 (the file sets bit 15 as a 2.x marker)
	RawHeadLen uint16 // size of the file header; the first record starts at this offset
	RecordLen  uint16 // size of each record header (96 bytes for atop 2.8)
	Hertz      uint16 // kernel clock ticks per second
	SstatLen   uint32 // decompressed length of the system-statistics block
	TstatLen   uint32 // decompressed length of the per-process block
	PageSize   uint32 // memory page size in bytes
	Hostname   string
}

// minHeaderLen is the number of bytes needed to read every header field used here
const minHeaderLen = 480

// ReadAtopHeader safely opens a file and parses the binary header configuration.
// It returns the header struct if the file is a supported atop 2.x raw log, or an error otherwise.
func ReadAtopHeader(filePath string) (*AtopRawHeader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Read up to the largest header size atop writes; a shorter file is fine as long as it covers the header
	buf := make([]byte, 4096)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}
	buf = buf[:n]

	if n < minHeaderLen {
		return nil, fmt.Errorf("file is too short to be an atop log (%d bytes)", n)
	}

	// 1. GATEKEEPER: Validate Magic Number immediately
	magic := binary.LittleEndian.Uint32(buf[0:])
	if magic != AtopMagic {
		return nil, fmt.Errorf("invalid file signature: expected 0x%X, found 0x%X (Not an atop log)", AtopMagic, magic)
	}

	// Version word: bit 15 marks atop 2.x; the rest is major<<8 | minor (0x8208 -> 2.8)
	aversion := binary.LittleEndian.Uint16(buf[4:])
	if aversion&0x8000 == 0 {
		return nil, fmt.Errorf("atop 1.x raw logs are not supported (version word 0x%X)", aversion)
	}

	h := &AtopRawHeader{
		Magic:      magic,
		Version:    aversion &^ 0x8000,
		RawHeadLen: binary.LittleEndian.Uint16(buf[10:]),
		RecordLen:  binary.LittleEndian.Uint16(buf[12:]),
		Hertz:      binary.LittleEndian.Uint16(buf[14:]),
		SstatLen:   binary.LittleEndian.Uint32(buf[28:]),
		TstatLen:   binary.LittleEndian.Uint32(buf[32:]),
		PageSize:   binary.LittleEndian.Uint32(buf[436:]),
		// utsname nodename: 65-byte field starting after the 65-byte sysname
		Hostname: cString(buf[101:166]),
	}
	if h.PageSize == 0 {
		h.PageSize = 4096
	}
	if h.Hertz == 0 {
		h.Hertz = 100
	}
	if int(h.RawHeadLen) < minHeaderLen || int(h.RawHeadLen) > n || h.RecordLen < 64 {
		return nil, fmt.Errorf("implausible header sizes (header %d, record %d)", h.RawHeadLen, h.RecordLen)
	}

	return h, nil
}
