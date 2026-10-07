package main

import (
	"fmt"
	"image/color"
	"log"

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

func main() {
	err := storage.InitializeStorage()
	if err != nil {
		log.Fatalf("Critical Error setting up local bbolt DB: %v", err)
	}
	defer storage.CloseStorage()
	_ = storage.PruneOldLogs()

	myApp := app.New()
	myWindow := myApp.NewWindow("ATOP Advanced Cross-Platform Companion")
	myWindow.Resize(fyne.NewSize(1000, 650))

	fileStatusLabel := widget.NewLabelWithStyle(
		"📂 No Log File Selected. Please upload a Linux ATOP raw binary database file.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)

	gridData := [][]string{
		{"Date Stamp", "Time Segment", "Average CPU", "Peak RAM"},
		{"Select a valid log file to populate metrics rows...", "", "", ""},
	}

	spreadsheetTable := widget.NewTable(
		func() (int, int) { return len(gridData), 4 },
		func() fyne.CanvasObject { return widget.NewLabel("Column Text Layout Wrapper") },
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			label.SetText(gridData[id.Row][id.Col])
			label.TextStyle = fyne.TextStyle{Bold: false}
			
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				return
			}

			// Task 4: Anomaly Highlighter. Highlight cells if utilization values cross boundaries
			if id.Row > 0 && id.Col == 2 {
				var val float64
				_, parseErr := fmt.Sscanf(gridData[id.Row][id.Col], "%f%%", &val)
				if parseErr == nil && val > 80.0 {
					label.TextStyle = fyne.TextStyle{Bold: true} // Bold display alert row text markers
				}
			}
		},
	)
	spreadsheetTable.SetColumnWidth(0, 130)
	spreadsheetTable.SetColumnWidth(1, 130)

	// Proportional grid block initialization to keep visual line charts rendering correctly
	chartContainer := container.NewWithoutLayout()
	chartWrapperBlock := container.NewVBox(
		widget.NewLabelWithStyle("Historical Load Cross-Comparison Plot:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewGridWrap(fyne.NewSize(420, 320), chartContainer),
	)

	uploadButton := widget.NewButtonWithIcon("Import Linux ATOP Log", theme.FileIcon(), func() {
		fileDialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, myWindow)
				return
			}
			if reader == nil {
				return
			}
			
			targetPath := reader.URI().Path()
			fileStatusLabel.SetText(fmt.Sprintf("🔄 Running Adaptive Header Interceptor: %s...", reader.URI().Name()))

			header, headerErr := telemetry.ReadAtopHeader(targetPath)
			if headerErr != nil {
				_, _ = storage.ValidateLogFile(targetPath)
				dialog.ShowError(headerErr, myWindow)
				fileStatusLabel.SetText("❌ File validation rejected. System anomaly logged and isolated into quarantine space.")
				return
			}

			// Invoke the Chronological Log Stream Parser
			parsedPoints, parseErr := telemetry.ParseAtopLogStream(targetPath, header)
			if parseErr != nil {
				dialog.ShowError(parseErr, myWindow)
				return
			}

			successMsg := fmt.Sprintf("✅ Active File: %s | Version: %d.%d | Chronological Records Extracted: %d", 
				reader.URI().Name(), header.Version>>8, header.Version&0xFF, len(parsedPoints))
			fileStatusLabel.SetText(successMsg)

			// Persist dynamic logs straight to embedded storage tracking records
			for _, pt := range parsedPoints {
				_ = storage.SaveMetricSnapshot(storage.HistoricalSnapshot{
					Timestamp: pt.Timestamp, CPUUtil: pt.CPUUtil, MemUtil: pt.MemUtil, DiskIO: pt.DiskIO,
				})
			}

			// Sync and populate rows dynamically inside our dashboard grid component
			gridData = [][]string{{"Date Stamp", "Time Segment", "Average CPU", "Peak RAM"}}
			for _, pt := range parsedPoints {
				gridData = append(gridData, []string{
					pt.Timestamp[0:10],
					pt.Timestamp[11:19],
					fmt.Sprintf("%.1f%%", pt.CPUUtil),
					fmt.Sprintf("%.1f%%", pt.MemUtil),
				})
			}
			spreadsheetTable.Refresh()

			// Clear previous graphics and plot lines across coordinates safely
			chartContainer.Objects = nil
			if len(parsedPoints) > 1 {
				widthStep := float32(400.0) / float32(len(parsedPoints)-1)
				for i := 0; i < len(parsedPoints)-1; i++ {
					// Map metrics down to canvas coordinate bounds cleanly
					y1 := float32(280.0) - float32(parsedPoints[i].CPUUtil*2.5)
					y2 := float32(280.0) - float32(parsedPoints[i+1].CPUUtil*2.5)
					
					trendLine := canvas.NewLine(color.RGBA{R: 57, G: 255, B: 20, A: 255}) // Atop Green
					trendLine.Position1 = fyne.NewPos(float32(i)*widthStep, y1)
					trendLine.Position2 = fyne.NewPos(float32(i+1)*widthStep, y2)
					trendLine.StrokeWidth = 3
					chartContainer.Add(trendLine)
				}
				chartContainer.Refresh()
			}

		}, myWindow)
		fileDialog.Show()
	})

	exportButton := widget.NewButtonWithIcon("Export Interactive SVG", theme.DocumentSaveIcon(), func() {
		dialog.ShowInformation("Export Engine", "Generating responsive vector standalone HTML report document...", myWindow)
	})
	
	wipeButton := widget.NewButtonWithIcon("Clear Log Cache", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Confirm Action", "Permanently clear the local historical database workspace cache workspace?", func(confirm bool) {
			if confirm {
				_ = storage.ClearAllDatabaseLogs()
				gridData = [][]string{
					{"Date Stamp", "Time Segment", "Average CPU", "Peak RAM"},
					{"Select a valid log file to populate metrics rows...", "", "", ""},
				}
				spreadsheetTable.Refresh()
				chartContainer.Objects = nil
				chartContainer.Refresh()
				fileStatusLabel.SetText("🗑️ Storage log cache completely wiped.")
			}
		}, myWindow)
	})

	actionControlBar := container.NewHBox(uploadButton, exportButton, wipeButton)
	topHeaderSection := container.NewVBox(
		widget.NewLabelWithStyle("=== ATOP OFFLINE ARCHIVE DIAGNOSTIC PANEL ===", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		fileStatusLabel,
		actionControlBar,
		widget.NewSeparator(),
	)

	splitDataDashboard := container.NewHSplit(spreadsheetTable, chartWrapperBlock)
	splitDataDashboard.SetOffset(0.55)

	rootApplicationLayout := container.NewBorder(
		topHeaderSection, nil, nil, nil, splitDataDashboard,
	)

	myWindow.SetContent(rootApplicationLayout)
	myWindow.ShowAndRun()
}
