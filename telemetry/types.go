package telemetry

import "time"

// Sample is the system state of one host for one atop record. The statistics cover the Interval seconds
// that end at Time, so rates and percentages describe that interval.
type Sample struct {
	Time     time.Time // end of the interval, UTC
	Interval int       // seconds covered by the sample
	NCPU     int

	CPUBusy   float64 // % of CPU time spent neither idle nor waiting for I/O
	CPUIOWait float64 // % of CPU time spent waiting for I/O
	CPUSteal  float64 // % of CPU time stolen by the hypervisor

	Load1, Load5, Load15 float64 // load averages (runnable tasks)

	MemPct         float64 // % of physical memory in use (cache, buffers and reclaimable slab excluded)
	MemUsedBytes   float64
	MemTotalBytes  float64
	SwapPct        float64 // % of swap in use; 0 when the host has no swap
	SwapUsedBytes  float64
	SwapTotalBytes float64

	Disks []DiskStat // one entry per block device seen in the record
	Nets  []NetStat  // one entry per physical network interface

	// proc locates the per-process block of the record; see Processes
	proc *procSource
}

// DiskStat is the activity of one block device during the interval
type DiskStat struct {
	Name      string
	ReadMBps  float64 // MB/s read
	WriteMBps float64 // MB/s written
	BusyPct   float64 // % of the interval the device had I/O in flight
}

// NetStat is the traffic of one network interface during the interval
type NetStat struct {
	Name  string
	RxBps float64 // bytes/s received
	TxBps float64 // bytes/s transmitted
}

// ProcessDetail handles the secondary 4-pane modal tabular values
type ProcessDetail struct {
	PID      int32   `json:"pid"`
	Name     string  `json:"name"`
	User     string  `json:"user"`
	CPUUsage float64 `json:"cpu_usage"`
	MemUsage float64 `json:"mem_usage"`
	Threads  int32   `json:"threads"`
}
