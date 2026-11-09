package telemetry

import (
	"fmt"
	"math"
	"sort"
)

// Default alarm limits. A value strictly above its limit is an anomaly and is highlighted on the charts.
const (
	LimitCPUBusy   = 80.0 // % of CPU time busy
	LimitCPUIOWait = 20.0 // % of CPU time waiting for I/O
	LimitCPUSteal  = 10.0 // % of CPU time stolen by the hypervisor
	LimitMemPct    = 90.0 // % of physical memory in use
	LimitSwapPct   = 20.0 // % of swap in use
	LimitDiskBusy  = 80.0 // % of the interval a disk had I/O in flight
)

// Series is one line on a chart. Values are aligned with the samples of the host: Values[i] belongs to
// Samples[i]. A NaN value means the sample has no value for this series, and the line is broken there.
type Series struct {
	Name   string
	Values []float64
	// Limit returns the alarm level for sample i, or 0 when the series has no limit
	Limit func(i int) float64
}

// Plot groups series that share one unit, so they can be drawn on one chart with one Y scale
type Plot struct {
	Key    string // stable identifier, e.g. "cpu"
	Title  string // e.g. "CPU"
	Unit   string // e.g. "%", "MB/s"
	Series []Series
}

// constLimit returns a Limit function with the same value for every sample
func constLimit(v float64) func(int) float64 {
	return func(int) float64 { return v }
}

// BuildPlots turns the samples of one host into plots. Samples must be in chronological order.
func BuildPlots(samples []Sample) []Plot {
	n := len(samples)
	column := func(f func(Sample) float64) []float64 {
		v := make([]float64, n)
		for i, s := range samples {
			v[i] = f(s)
		}
		return v
	}

	plots := []Plot{
		{Key: "cpu", Title: "CPU", Unit: "%", Series: []Series{
			{Name: "busy", Values: column(func(s Sample) float64 { return s.CPUBusy }), Limit: constLimit(LimitCPUBusy)},
			{Name: "iowait", Values: column(func(s Sample) float64 { return s.CPUIOWait }), Limit: constLimit(LimitCPUIOWait)},
			{Name: "steal", Values: column(func(s Sample) float64 { return s.CPUSteal }), Limit: constLimit(LimitCPUSteal)},
		}},
		{Key: "memory", Title: "Memory", Unit: "%", Series: []Series{
			{Name: "used", Values: column(func(s Sample) float64 { return s.MemPct }), Limit: constLimit(LimitMemPct)},
			{Name: "swap", Values: column(func(s Sample) float64 { return s.SwapPct }), Limit: constLimit(LimitSwapPct)},
		}},
		{Key: "memory_gib", Title: "Memory used", Unit: "GiB", Series: []Series{
			{Name: "used", Values: column(func(s Sample) float64 { return s.MemUsedBytes / (1 << 30) })},
			{Name: "swap used", Values: column(func(s Sample) float64 { return s.SwapUsedBytes / (1 << 30) })},
		}},
		{Key: "load", Title: "Load average", Unit: "load", Series: []Series{
			{Name: "1 min", Values: column(func(s Sample) float64 { return s.Load1 }), Limit: perCPULimit(samples)},
			{Name: "5 min", Values: column(func(s Sample) float64 { return s.Load5 })},
			{Name: "15 min", Values: column(func(s Sample) float64 { return s.Load15 })},
		}},
	}

	// Disk plots have one series per device, so the device list comes from the whole dataset
	disks := sortedNames(samples, func(s Sample) []string {
		names := make([]string, 0, len(s.Disks))
		for _, d := range s.Disks {
			names = append(names, d.Name)
		}
		return names
	})
	busy := make([]Series, 0, len(disks))
	throughput := make([]Series, 0, len(disks))
	for _, name := range disks {
		b := make([]float64, n)
		t := make([]float64, n)
		active := false
		for i, s := range samples {
			b[i], t[i] = math.NaN(), math.NaN()
			for _, d := range s.Disks {
				if d.Name == name {
					b[i] = d.BusyPct
					t[i] = d.ReadMBps + d.WriteMBps
					if t[i] > 0 || d.BusyPct > 0 {
						active = true
					}
				}
			}
		}
		if !active {
			continue // idle devices (loop, ram, ...) would only clutter the chart
		}
		busy = append(busy, Series{Name: name, Values: b, Limit: constLimit(LimitDiskBusy)})
		throughput = append(throughput, Series{Name: name, Values: t})
	}
	if len(busy) > 0 {
		plots = append(plots,
			Plot{Key: "disk_busy", Title: "Disk busy", Unit: "%", Series: busy},
			Plot{Key: "disk_io", Title: "Disk throughput", Unit: "MB/s", Series: throughput})
	}

	// Network: receive and transmit per physical interface, in KB/s
	ifaces := sortedNames(samples, func(s Sample) []string {
		names := make([]string, 0, len(s.Nets))
		for _, nt := range s.Nets {
			names = append(names, nt.Name)
		}
		return names
	})
	var netSeries []Series
	for _, name := range ifaces {
		rx := make([]float64, n)
		tx := make([]float64, n)
		for i, s := range samples {
			rx[i], tx[i] = math.NaN(), math.NaN()
			for _, nt := range s.Nets {
				if nt.Name == name {
					rx[i] = nt.RxBps / 1024
					tx[i] = nt.TxBps / 1024
				}
			}
		}
		netSeries = append(netSeries,
			Series{Name: name + " rx", Values: rx},
			Series{Name: name + " tx", Values: tx})
	}
	if len(netSeries) > 0 {
		plots = append(plots, Plot{Key: "network", Title: "Network", Unit: "KB/s", Series: netSeries})
	}

	return plots
}

// perCPULimit makes the load-average limit equal to the number of CPUs of each sample
func perCPULimit(samples []Sample) func(int) float64 {
	return func(i int) float64 {
		if i < 0 || i >= len(samples) {
			return 0
		}
		return float64(samples[i].NCPU)
	}
}

// sortedNames collects the distinct names returned by f over all samples, sorted
func sortedNames(samples []Sample, f func(Sample) []string) []string {
	seen := map[string]bool{}
	var names []string
	for _, s := range samples {
		for _, name := range f(s) {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// Anomaly is a run of consecutive samples where one series is above its limit
type Anomaly struct {
	Plot      string // plot title
	Series    string
	Unit      string
	Start     int // index of the first sample in the run
	End       int // index of the last sample in the run
	PeakIndex int
	Peak      float64
	Limit     float64  // limit at the peak
	Samples   []Sample // the samples of the run, Start..End inclusive
}

// FindAnomalies returns every run above a limit, ordered by start time
func FindAnomalies(samples []Sample, plots []Plot) []Anomaly {
	var out []Anomaly
	for _, p := range plots {
		for _, s := range p.Series {
			if s.Limit == nil {
				continue
			}
			inRun := false
			var cur *Anomaly
			for i, v := range s.Values {
				limit := s.Limit(i)
				over := limit > 0 && !math.IsNaN(v) && v > limit
				if over {
					if !inRun {
						out = append(out, Anomaly{Plot: p.Title, Series: s.Name, Unit: p.Unit, Start: i, Peak: v, PeakIndex: i, Limit: limit})
						cur = &out[len(out)-1]
						inRun = true
					}
					cur.End = i
					if v > cur.Peak {
						cur.Peak, cur.PeakIndex, cur.Limit = v, i, limit
					}
				} else {
					inRun = false
				}
			}
		}
	}
	for i := range out {
		a := &out[i]
		a.Samples = samples[a.Start : a.End+1]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// Describe returns a one-line summary of an anomaly for lists
func (a Anomaly) Describe() string {
	return fmt.Sprintf("%s / %s peak %.1f %s (limit %.1f)", a.Plot, a.Series, a.Peak, a.Unit, a.Limit)
}
