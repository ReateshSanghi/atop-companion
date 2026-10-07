package telemetry

import (
	"encoding/binary"
	"fmt"
	"os"
)

// AtopMagic is the official hexadecimal signature for valid atop logs (0xFEEDBEEF)
const AtopMagic = 0xFEEDBEEF

// AtopRawHeader represents the raw C-struct header layout of an atop file.
// C Definition:
// struct rawheader {
//    unsigned int  magic;      // 4 bytes
//    unsigned short aversion;  // 2 bytes (Version)
//    unsigned short future;    // 2 bytes
//    unsigned short sstatlen;  // 2 bytes (System Struct Length)
//    unsigned short tstatlen;  // 2 bytes (Process Struct Length)
//    unsigned short rawheadlen;// 2 bytes (Header Length)
// };
type AtopRawHeader struct {
	Magic      uint32
	Version    uint16
	Future     uint16
	SstatLen   uint16 // Length of compressed system-level statistics
	TstatLen   uint16 // Length of compressed process-level statistics
	RawHeadLen uint16 // Length of the record header
}

// ReadAtopHeader safely opens a file and parses the binary header configuration.
// It returns the header struct if the file is valid, or an error if the magic number fails.
func ReadAtopHeader(filePath string) (*AtopRawHeader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create a structural buffer to hold the header data
	header := &AtopRawHeader{}

	// Read the exact binary fields sequentially (Little Endian standard for x86 Linux logs)
	if err := binary.Read(file, binary.LittleEndian, &header.Magic); err != nil {
		return nil, fmt.Errorf("failed to read magic signature: %w", err)
	}

	// 1. GATEKEEPER: Validate Magic Number immediately
	if header.Magic != AtopMagic {
		return nil, fmt.Errorf("invalid file signature: expected 0x%X, found 0x%X (Not an atop log)", AtopMagic, header.Magic)
	}

	// Read remaining configuration fields
	if err := binary.Read(file, binary.LittleEndian, &header.Version); err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}
	if err := binary.Read(file, binary.LittleEndian, &header.Future); err != nil {
		return nil, fmt.Errorf("failed to read future bits: %w", err)
	}
	if err := binary.Read(file, binary.LittleEndian, &header.SstatLen); err != nil {
		return nil, fmt.Errorf("failed to read sstat length: %w", err)
	}
	if err := binary.Read(file, binary.LittleEndian, &header.TstatLen); err != nil {
		return nil, fmt.Errorf("failed to read tstat length: %w", err)
	}
	if err := binary.Read(file, binary.LittleEndian, &header.RawHeadLen); err != nil {
		return nil, fmt.Errorf("failed to read header length: %w", err)
	}

	return header, nil
}
