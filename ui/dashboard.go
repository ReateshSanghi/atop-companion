package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"atop-companion/storage"
	"atop-companion/telemetry"
)

// dashboard is the main window: a plot checklist on the left, the charts of the selected plots on the right
type dashboard struct {
	win      fyne.Window
	data     *telemetry.Dataset
	hostName string
	host     *hostView
	// anomalies holds every run above a limit for the current host; breach marks the samples inside them
	anomalies []telemetry.Anomaly
	breach    map[int]bool
	view      *view
	selected  map[string]bool // plot key -> shown on the right

	chartList []*chart
	plotBox   *fyne.Container
	chartsBox *fyne.Container
	status    *widget.Label
	progress  *widget.ProgressBar
	hostSel   *widget.Select
	analyze   *widget.Button
	popup     *widget.PopUp
	popupIdx  int
}

// Run opens the main window and blocks until it is closed
func Run() {
	a := app.New()
	setDarkMode(false)

	d := &dashboard{selected: map[string]bool{}, popupIdx: -1, breach: map[int]bool{}}
	d.win = a.NewWindow("ATOP Advanced Cross-Platform Companion")
	d.win.SetContent(d.build())
	d.win.Resize(fyne.NewSize(1280, 800))
	d.win.ShowAndRun()
}

// build creates the toolbar, the status line and the split view
func (d *dashboard) build() fyne.CanvasObject {
	open := widget.NewButtonWithIcon("Open folder...", theme.FolderOpenIcon(), d.openFolder)
	d.analyze = widget.NewButtonWithIcon("Analyze this !!", theme.WarningIcon(), d.analysisWindow)
	d.analyze.Disable()
	reset := widget.NewButtonWithIcon("Reset zoom", theme.ViewRefreshIcon(), func() {
		if d.view != nil {
			d.view.reset()
		}
	})
	clearCache := widget.NewButtonWithIcon("Clear log cache", theme.DeleteIcon(), d.clearCache)
	d.hostSel = widget.NewSelect(nil, d.selectHost)
	d.hostSel.PlaceHolder = "Host"
	d.hostSel.Hide()
	dark := widget.NewCheck("Dark mode", func(on bool) {
		setDarkMode(on)
		for _, c := range d.chartList {
			c.Refresh()
		}
	})
	toolbar := container.NewHBox(open, d.analyze, reset, clearCache, d.hostSel, dark)

	d.status = widget.NewLabel("No folder loaded. Open a folder that contains ATOP raw logs.")
	d.progress = widget.NewProgressBar()
	d.progress.Hide()
	hint := widget.NewLabel("Drag on a chart to zoom, double-click to reset, click or right-click a point for its details.")
	hint.TextStyle = fyne.TextStyle{Italic: true}

	d.plotBox = container.NewVBox()
	d.chartsBox = container.NewVBox()
	left := container.NewBorder(
		widget.NewLabelWithStyle("Plots", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, nil, nil,
		container.NewVScroll(d.plotBox))
	split := container.NewHSplit(left, container.NewVScroll(d.chartsBox))
	split.SetOffset(0.2)

	top := container.NewVBox(toolbar, d.status, d.progress, hint, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, split)
}

// openFolder asks for a folder and loads every atop log in it
func (d *dashboard) openFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		if uri == nil {
			return
		}
		d.loadFolder(localPath(uri.Path()))
	}, d.win)
}

// loadFolder reads the folder in the background, so the window stays responsive
func (d *dashboard) loadFolder(dir string) {
	d.status.SetText("Reading " + dir + " ...")
	d.progress.SetValue(0)
	d.progress.Show()
	d.analyze.Disable()

	go func() {
		data, err := telemetry.LoadFolder(dir, func(done, total int) {
			fyne.Do(func() { d.progress.SetValue(float64(done) / float64(total)) })
		})
		if err != nil {
			fyne.Do(func() {
				d.progress.Hide()
				d.status.SetText(err.Error())
				dialog.ShowError(err, d.win)
			})
			return
		}
		fyne.Do(func() {
			d.progress.Hide()
			d.setData(dir, data)
		})
		// Keep the database cache up to date without holding up the window
		if names := data.HostNames(); len(names) > 0 {
			_ = saveToCache(data.Hosts[names[0]])
		}
	}()
}

// setData shows a freshly loaded dataset
func (d *dashboard) setData(dir string, data *telemetry.Dataset) {
	d.data = data
	names := data.HostNames()
	d.hostSel.SetOptions(names)
	if len(names) > 1 {
		d.hostSel.Show()
	} else {
		d.hostSel.Hide()
	}
	d.hostName = ""
	d.selectHost(names[0])
	d.hostSel.SetSelected(names[0])

	if len(data.Problems) > 0 {
		dialog.ShowInformation("Some files were not read",
			strings.Join(data.Problems, "\n"), d.win)
	}
	d.status.SetText(fmt.Sprintf("%s: %d file(s) read, %d host(s), %d records skipped, %d files ignored",
		dir, data.Files, len(names), data.Skipped, len(data.Problems)))
}

// selectHost switches the charts to one host of the loaded dataset
func (d *dashboard) selectHost(name string) {
	if d.data == nil || name == d.hostName {
		return
	}
	samples := d.data.Hosts[name]
	plots := telemetry.BuildPlots(samples)
	d.hostName = name
	d.host = &hostView{samples: samples, plots: plots}
	d.anomalies = telemetry.FindAnomalies(samples, plots)
	d.breach = map[int]bool{}
	for _, a := range d.anomalies {
		for i := a.Start; i <= a.End; i++ {
			d.breach[i] = true
		}
	}
	d.view = newView(len(samples))
	d.hidePopup()
	d.analyze.Enable()
	d.rebuildPlotList()
	d.rebuildCharts()

	first, last := samples[0].Time.Local(), samples[len(samples)-1].Time.Local()
	d.status.SetText(fmt.Sprintf("Host %s: %d samples from %s to %s, %d anomalies (use Analyze this !!)",
		name, len(samples), first.Format("2006-01-02 15:04"), last.Format("2006-01-02 15:04"), len(d.anomalies)))
}

// rebuildPlotList fills the left-hand checklist with the plots of the current host
func (d *dashboard) rebuildPlotList() {
	d.plotBox.Objects = nil
	for _, p := range d.host.plots {
		key := p.Key
		if _, known := d.selected[key]; !known {
			d.selected[key] = key != "memory_gib" // the GiB plot is off by default: it repeats the % plot
		}
		chk := widget.NewCheck(fmt.Sprintf("%s (%s)", p.Title, p.Unit), nil)
		chk.SetChecked(d.selected[key])
		chk.OnChanged = func(on bool) {
			d.selected[key] = on
			d.rebuildCharts()
		}
		d.plotBox.Add(chk)
	}
	d.plotBox.Refresh()
}

// rebuildCharts creates one chart per selected plot. Each plot keeps its own Y scale, so plots with
// different units are never drawn on the same axis.
func (d *dashboard) rebuildCharts() {
	d.chartsBox.Objects = nil
	d.chartList = nil
	if d.host == nil {
		d.chartsBox.Refresh()
		return
	}
	d.view.listeners = nil
	for _, p := range d.host.plots {
		if !d.selected[p.Key] {
			continue
		}
		c := newChart(p, d.host.samples, d.view)
		c.onHover = d.onHover
		c.onLeave = d.hidePopup
		c.onInspect = d.showDetails
		d.chartList = append(d.chartList, c)
		d.chartsBox.Add(container.NewBorder(legendRow(p), nil, nil, nil, c))
		d.chartsBox.Add(widget.NewSeparator())
	}
	d.chartsBox.Refresh()
}

// legendRow names the plot and shows which colour belongs to which series
func legendRow(p telemetry.Plot) fyne.CanvasObject {
	_, _, _, palette := chartColors()
	items := []fyne.CanvasObject{widget.NewLabelWithStyle(p.Title+" ("+p.Unit+")", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})}
	for k, s := range p.Series {
		swatch := canvas.NewRectangle(palette[k%len(palette)])
		swatch.SetMinSize(fyne.NewSize(12, 12))
		items = append(items, container.NewHBox(swatch, widget.NewLabel(s.Name)))
	}
	return container.NewHBox(items...)
}

// onHover shows the summary table when the pointer is over a sample that is over a limit
func (d *dashboard) onHover(i int, abs fyne.Position) {
	if d.host == nil || !d.breach[i] {
		d.hidePopup()
		return
	}
	offset := fyne.NewPos(16, 16)
	if d.popup != nil && d.popupIdx == i {
		d.popup.ShowAtPosition(abs.Add(offset))
		return
	}
	d.hidePopup()
	d.popupIdx = i
	d.popup = widget.NewPopUp(hoverSummary(*d.host, i), d.win.Canvas())
	d.popup.ShowAtPosition(abs.Add(offset))
}

// hidePopup closes the hover summary, if one is open
func (d *dashboard) hidePopup() {
	if d.popup != nil {
		d.popup.Hide()
		d.popup = nil
	}
	d.popupIdx = -1
}

// clearCache empties the database after confirmation. Loaded data stays on screen.
func (d *dashboard) clearCache() {
	dialog.ShowConfirm("Confirm action", "Permanently clear the local historical database cache?", func(ok bool) {
		if !ok {
			return
		}
		if err := storage.ClearAllDatabaseLogs(); err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		d.status.SetText("Cache cleared. Loaded data is still shown; open the folder again to refill it.")
	}, d.win)
}

// saveToCache stores the samples of a host in the local database, in one transaction
func saveToCache(samples []telemetry.Sample) error {
	rows := make([]storage.HistoricalSnapshot, 0, len(samples))
	for _, s := range samples {
		disk := 0.0
		for _, dsk := range s.Disks {
			disk += dsk.ReadMBps + dsk.WriteMBps
		}
		rows = append(rows, storage.HistoricalSnapshot{
			Timestamp: s.Time.UTC().Format(storage.TimestampLayout),
			CPUUtil:   s.CPUBusy,
			MemUtil:   s.MemPct,
			DiskIO:    disk,
		})
	}
	return storage.SaveMetricSnapshots(rows)
}

// localPath turns a URI path from the file dialog into a path the operating system can open
func localPath(p string) string {
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // Windows drive letters come with a leading slash
	}
	return filepath.FromSlash(p)
}
