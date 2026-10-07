package telemetry

// HistoricalSnapshot represents an independent point-in-time metrics row
type HistoricalSnapshot struct {
	Timestamp string  `json:"timestamp"` // Format: YYYY-MM-DD HH:mm:ss
	CPUUtil   float64 `json:"cpu_util"`  // Overall CPU utilization percentage
	MemUtil   float64 `json:"mem_util"`  // Active RAM utilization percentage
	DiskIO    float64 `json:"disk_io"`   // Combined read/write rate in MB/s
}

// ProcessDetail handles the secondary 4-pane modal tabular values
type ProcessDetail struct {
	PID       int32   `json:"pid"`
	Name      string  `json:"name"`
	User      string  `json:"user"`
	CPUUsage  float64 `json:"cpu_usage"`
	MemUsage  float64 `json:"mem_usage"`
	Threads   int32   `json:"threads"`
}
