package ui

import (
	"fmt"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"atop-companion/telemetry"
)

// hostView carries what the detail views need about the host currently shown
type hostView struct {
	samples []telemetry.Sample
	plots   []telemetry.Plot
}

// fmtValue formats a value with its unit, e.g. "14.8 %"
func fmtValue(v float64, unit string) string {
	if math.IsNaN(v) {
		return "-"
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

// overLimit returns the limit of a series at sample i and whether the value is above it
func overLimit(s telemetry.Series, i int) (float64, bool) {
	if s.Limit == nil || i >= len(s.Values) {
		return 0, false
	}
	lim := s.Limit(i)
	v := s.Values[i]
	return lim, lim > 0 && !math.IsNaN(v) && v > lim
}

// breachedAt lists the series over their limit at sample i, as readable lines
func (h hostView) breachedAt(i int) []string {
	var lines []string
	for _, p := range h.plots {
		for _, s := range p.Series {
			if lim, over := overLimit(s, i); over {
				lines = append(lines, fmt.Sprintf("%s %s: %s (limit %.1f %s)", p.Title, s.Name,
					fmtValue(s.Values[i], p.Unit), lim, p.Unit))
			}
		}
	}
	return lines
}

// processTable returns a grid with the busiest processes of a sample
func processTable(procs []telemetry.ProcessStat, limit int) fyne.CanvasObject {
	if len(procs) == 0 {
		return widget.NewLabel("No process data for this sample.")
	}
	cells := []fyne.CanvasObject{
		boldLabel("PID"), boldLabel("Name"), boldLabel("State"), boldLabel("Threads"), boldLabel("CPU (1 core = 100%)"),
	}
	for i, p := range procs {
		if i >= limit {
			break
		}
		cells = append(cells,
			widget.NewLabel(fmt.Sprint(p.PID)),
			widget.NewLabel(p.Name),
			widget.NewLabel(p.State),
			widget.NewLabel(fmt.Sprint(p.Threads)),
			widget.NewLabel(fmt.Sprintf("%.1f %%", p.CPUPct)))
	}
	return container.NewGridWithColumns(5, cells...)
}

func boldLabel(s string) *widget.Label {
	return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// pointDetails builds the full report for one sample: every plotted value with its limit, the disks, the
// network interfaces and the busiest processes
func pointDetails(h hostView, i int) fyne.CanvasObject {
	s := h.samples[i]
	rows := []fyne.CanvasObject{
		boldLabel("Time (local)"), widget.NewLabel(s.Time.Local().Format("2006-01-02 15:04:05")),
		boldLabel("Interval"), widget.NewLabel(fmt.Sprintf("%d s", s.Interval)),
		boldLabel("CPUs"), widget.NewLabel(fmt.Sprint(s.NCPU)),
	}
	content := []fyne.CanvasObject{
		widget.NewLabelWithStyle(fmt.Sprintf("Sample %d of %d", i+1, len(h.samples)), fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
		container.NewGridWithColumns(2, rows...),
		widget.NewSeparator(),
		boldLabel("Measurements"),
	}

	cells := []fyne.CanvasObject{boldLabel("Plot"), boldLabel("Series"), boldLabel("Value"), boldLabel("Limit"), boldLabel("Status")}
	for _, p := range h.plots {
		for _, sr := range p.Series {
			v := math.NaN()
			if i < len(sr.Values) {
				v = sr.Values[i]
			}
			limit, status := "-", ""
			if lim, over := overLimit(sr, i); over {
				status = "OVER LIMIT"
				limit = fmtValue(lim, p.Unit)
			} else if sr.Limit != nil && i < len(sr.Values) {
				if lim := sr.Limit(i); lim > 0 {
					limit = fmtValue(lim, p.Unit)
				}
			}
			cells = append(cells, widget.NewLabel(p.Title), widget.NewLabel(sr.Name),
				widget.NewLabel(fmtValue(v, p.Unit)), widget.NewLabel(limit), widget.NewLabel(status))
		}
	}
	content = append(content, container.NewGridWithColumns(5, cells...))

	if len(s.Disks) > 0 {
		dc := []fyne.CanvasObject{boldLabel("Disk"), boldLabel("Read"), boldLabel("Write"), boldLabel("Busy")}
		for _, d := range s.Disks {
			dc = append(dc, widget.NewLabel(d.Name), widget.NewLabel(fmtValue(d.ReadMBps, "MB/s")),
				widget.NewLabel(fmtValue(d.WriteMBps, "MB/s")), widget.NewLabel(fmtValue(d.BusyPct, "%")))
		}
		content = append(content, widget.NewSeparator(), boldLabel("Disks"), container.NewGridWithColumns(4, dc...))
	}
	if len(s.Nets) > 0 {
		nc := []fyne.CanvasObject{boldLabel("Interface"), boldLabel("Received"), boldLabel("Transmitted")}
		for _, n := range s.Nets {
			nc = append(nc, widget.NewLabel(n.Name), widget.NewLabel(fmtValue(n.RxBps/1024, "KB/s")),
				widget.NewLabel(fmtValue(n.TxBps/1024, "KB/s")))
		}
		content = append(content, widget.NewSeparator(), boldLabel("Network"), container.NewGridWithColumns(3, nc...))
	}

	// Processes are read from the file in the background: the process block is large
	procBox := container.NewVBox(widget.NewLabel("Reading the busiest processes..."))
	go func() {
		procs, err := s.Processes()
		fyne.Do(func() {
			var obj fyne.CanvasObject = processTable(procs, 20)
			if err != nil {
				obj = widget.NewLabel("Could not read processes: " + err.Error())
			}
			procBox.Objects = []fyne.CanvasObject{obj}
			procBox.Refresh()
		})
	}()
	content = append(content, widget.NewSeparator(), boldLabel("Busiest processes"), procBox)
	return container.NewVScroll(container.NewVBox(content...))
}

// showDetails opens a window with the full report for sample i
func (d *dashboard) showDetails(i int) {
	if d.host == nil || i < 0 || i >= len(d.host.samples) {
		return
	}
	w := fyne.CurrentApp().NewWindow("Details - " + d.host.samples[i].Time.Local().Format("2006-01-02 15:04:05"))
	w.SetContent(pointDetails(*d.host, i))
	w.Resize(fyne.NewSize(900, 700))
	w.Show()
}

// hoverSummary is the compact table shown when the pointer is over a problematic sample
func hoverSummary(h hostView, i int) fyne.CanvasObject {
	s := h.samples[i]
	lines := []fyne.CanvasObject{
		boldLabel(s.Time.Local().Format("2006-01-02 15:04:05")),
	}
	for _, l := range h.breachedAt(i) {
		lines = append(lines, widget.NewLabel("! "+l))
	}
	lines = append(lines, widget.NewLabel("Right-click for the full details and busiest processes."))
	return container.NewPadded(container.NewVBox(lines...))
}

// analysisWindow lists every anomaly of the host and lets the user jump to each one
func (d *dashboard) analysisWindow() {
	if d.host == nil {
		return
	}
	h := *d.host
	anomalies := d.anomalies
	header := fmt.Sprintf("%d anomalies over %d samples on host %s", len(anomalies), len(h.samples), d.hostName)
	if len(anomalies) == 0 {
		header = fmt.Sprintf("No threshold exceeded in the %d samples of host %s.", len(h.samples), d.hostName)
	}
	perPlot := map[string]int{}
	for _, a := range anomalies {
		perPlot[a.Plot]++
	}
	rows := []fyne.CanvasObject{widget.NewLabelWithStyle(header, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})}
	for _, p := range h.plots {
		if n := perPlot[p.Title]; n > 0 {
			rows = append(rows, widget.NewLabel(fmt.Sprintf("  %s: %d", p.Title, n)))
		}
	}
	rows = append(rows, widget.NewSeparator())

	for _, a := range anomalies {
		a := a
		start := a.Samples[0].Time.Local().Format("2006-01-02 15:04")
		end := a.Samples[len(a.Samples)-1].Time.Local().Format("2006-01-02 15:04")
		text := fmt.Sprintf("%s / %s  %s - %s  peak %s (limit %.1f %s)",
			a.Plot, a.Series, start, end, fmtValue(a.Peak, a.Unit), a.Limit, a.Unit)
		show := widget.NewButton("Show", func() {
			pad := max((a.End-a.Start)/2, 2)
			if d.view != nil {
				d.view.set(a.Start-pad, a.End+pad)
			}
			d.showDetails(a.PeakIndex)
		})
		rows = append(rows, container.NewBorder(nil, nil, nil, show, widget.NewLabel(text)))
	}

	w := fyne.CurrentApp().NewWindow("Analyze this !!")
	w.SetContent(container.NewVScroll(container.NewVBox(rows...)))
	w.Resize(fyne.NewSize(900, 600))
	w.Show()
}
