package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	sigElfCoreDump     = []byte{0x7F, 0x45, 0x4C, 0x46} // Linux Core Dump (.elf)
	sigWindowsMiniDump = []byte{0x4D, 0x44, 0x4D, 0x50} // Windows Minidump (MDMP)
	sigWindowsFullDump = []byte{0x50, 0x41, 0x47, 0x45} // Windows Memory Dump (PAGEDUMP)
	sigValidAtopRaw    = []byte{0x41, 0x54, 0x4F, 0x50} // Sample Magic Marker for valid log validation
)

// QuarantineAndLog isolates bad file formats and writes a security log
func QuarantineAndLog(filePath string, fileError error) {
	quarantineDir := "./quarantine"
	logFile := "security_audit.log"

	_ = os.MkdirAll(quarantineDir, 0755)

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		logEntry := fmt.Sprintf("[%s] FILTER DETECTED: Quarantined [%s] Reason: %v\n", timestamp, filepath.Base(filePath), fileError)
		_, _ = f.WriteString(logEntry)
		f.Close()
	}

	destinationPath := filepath.Join(quarantineDir, filepath.Base(filePath))
	_ = os.Rename(filePath, destinationPath)
}

// ValidateLogFile samples file headers safely to block improper formats
func ValidateLogFile(filePath string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, fmt.Errorf("unable to access file: %w", err)
	}
	defer file.Close()

	headerBuffer := make([]byte, 4)
	bytesRead, err := file.Read(headerBuffer)
	if err != nil || bytesRead < 4 {
		return false, fmt.Errorf("file payload is corrupted or empty")
	}

	// Guardrail checks
	if bytes.Equal(headerBuffer, sigElfCoreDump) {
		errContext := fmt.Errorf("detected a Linux Core Memory Dump file")
		QuarantineAndLog(filePath, errContext)
		return false, errContext
	}
	if bytes.Equal(headerBuffer, sigWindowsMiniDump) || bytes.Equal(headerBuffer, sigWindowsFullDump) {
		errContext := fmt.Errorf("detected a Windows System Memory Dump file")
		QuarantineAndLog(filePath, errContext)
		return false, errContext
	}

	return true, nil
}
