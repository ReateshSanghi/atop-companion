package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"atop-companion/telemetry"
)

// Plot area margins in pixels: left for Y labels, bottom for X labels, top for the unit
const (
	marginLeft   = 70
	marginTop    = 8
	marginRight  = 12
	marginBottom = 24
)

// view is the time range shared by all charts of one host, as inclusive sample indexes
type view struct {
	lo, hi    int
	n         int
	listeners []func()
}

func newView(n int) *view {
	return &view{lo: 0, hi: n - 1, n: n}
}

// set shows samples lo..hi. Ranges with fewer than two samples are ignored.
func (v *view) set(lo, hi int) {
	if lo > hi {
		lo, hi = hi, lo
	}
	lo = max(lo, 0)
	hi = min(hi, v.n-1)
	if hi-lo < 1 {
		return
	}
	v.lo, v.hi = lo, hi
	v.notify()
}

func (v *view) reset() {
	v.lo, v.hi = 0, v.n-1
	v.notify()
}

func (v *view) notify() {
	for _, f := range v.listeners {
		f()
	}
}

// tick is one label on an axis: its position in pixels and its text
type tick struct {
	pos   float32
	label string
}

// chart draws one plot: a unit, one or more series, the red zones where a series is over its limit, and
// the axes. Dragging selects a time range (shared by all charts), a double tap resets it, and a tap or a
// right-click asks for the details of the sample under the pointer.
type chart struct {
	widget.BaseWidget
	plot    telemetry.Plot
	samples []telemetry.Sample
	view    *view

	hover    int // index of the sample under the pointer, -1 if none
	dragging bool
	dragFrom float32
	dragTo   float32

	onHover   func(i int, at fyne.Position) // the pointer moved over sample i
	onLeave   func()                        // the pointer left the chart
	onInspect func(i int)                   // a point was chosen for detailed inspection

	img            *image.RGBA
	raster         *canvas.Raster
	size           fyne.Size
	plotL, plotR   float32
	plotT, plotB   float32
	yTicks, xTicks []tick
	emptyMessage   string
}

// newChart creates a chart for one plot of a host. Its view is refreshed whenever the shared view changes.
func newChart(plot telemetry.Plot, samples []telemetry.Sample, v *view) *chart {
	c := &chart{plot: plot, samples: samples, view: v, hover: -1}
	c.ExtendBaseWidget(c)
	v.listeners = append(v.listeners, c.Refresh)
	return c
}

// CreateRenderer implements fyne.Widget
func (c *chart) CreateRenderer() fyne.WidgetRenderer {
	c.raster = canvas.NewRaster(func(w, h int) image.Image {
		if c.img == nil {
			return image.NewRGBA(image.Rect(0, 0, w, h))
		}
		return c.img
	})
	return &chartRenderer{c: c}
}

// MinSize implements fyne.Widget
func (c *chart) MinSize() fyne.Size {
	return fyne.NewSize(300, 200)
}

// visibleRange returns the first and last sample index shown
func (c *chart) visibleRange() (int, int) {
	return c.view.lo, c.view.hi
}

// indexAt maps a horizontal pixel position to a sample index, or -1 outside the plot area
func (c *chart) indexAt(x float32) int {
	lo, hi := c.visibleRange()
	if hi <= lo || x < c.plotL || x > c.plotR {
		return -1
	}
	f := (x - c.plotL) / (c.plotR - c.plotL)
	return lo + int(math.Round(float64(f)*float64(hi-lo)))
}

// breachAt reports whether any series of this chart is over its limit at sample i
func (c *chart) breachAt(i int) bool {
	if i < 0 || i >= len(c.samples) {
		return false
	}
	for _, s := range c.plot.Series {
		if s.Limit == nil || i >= len(s.Values) {
			continue
		}
		if v, lim := s.Values[i], s.Limit(i); lim > 0 && !math.IsNaN(v) && v > lim {
			return true
		}
	}
	return false
}

// render draws the plot into a fresh image of the given size and computes the axis labels
func (c *chart) render(size fyne.Size) {
	c.size = size
	w, h := int(size.Width), int(size.Height)
	if w < 80 || h < 60 {
		return
	}
	bg, gridColor, breachColor, palette := chartColors()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(img, 0, 0, w, h, bg)

	c.plotL = marginLeft
	c.plotT = marginTop
	c.plotR = float32(w - marginRight)
	c.plotB = float32(h - marginBottom)
	c.yTicks, c.xTicks = nil, nil
	plotW := c.plotR - c.plotL
	plotH := c.plotB - c.plotT

	lo, hi := c.visibleRange()
	if hi <= lo || len(c.samples) == 0 {
		c.img = img
		c.emptyMessage = "no data in this range"
		return
	}
	c.emptyMessage = ""

	// Y range: the visible values only, so the axis shows the relevant band
	ymin, ymax := math.Inf(1), math.Inf(-1)
	for _, s := range c.plot.Series {
		for i := lo; i <= hi && i < len(s.Values); i++ {
			if v := s.Values[i]; !math.IsNaN(v) {
				ymin, ymax = math.Min(ymin, v), math.Max(ymax, v)
			}
		}
	}
	if math.IsInf(ymin, 1) {
		ymin, ymax = 0, 1
	}
	yLo, yHi, step := niceTicks(ymin, ymax, 6)

	xOf := func(i int) float32 {
		return c.plotL + float32(float64(i-lo)/float64(hi-lo))*plotW
	}
	yOf := func(v float64) float32 {
		return c.plotT + float32((1-(v-yLo)/(yHi-yLo)))*plotH
	}

	for v := yLo; v <= yHi+step/2; v += step {
		y := yOf(v)
		fillRect(img, int(c.plotL), int(y), int(plotW), 1, gridColor)
		c.yTicks = append(c.yTicks, tick{pos: y, label: formatAxis(v, step)})
	}

	// Red zones: every sample where a series is over its limit
	half := plotW / float32(hi-lo) / 2
	for i := lo; i <= hi; i++ {
		if c.breachAt(i) {
			x := xOf(i)
			fillRect(img, int(x-half), int(c.plotT), max(int(2*half), 1), int(plotH), breachColor)
		}
	}

	// Series lines; a line is broken where samples are missing or a long gap separates them
	for k, s := range c.plot.Series {
		col := palette[k%len(palette)]
		for i := lo; i < hi && i+1 < len(s.Values); i++ {
			a, b := s.Values[i], s.Values[i+1]
			if math.IsNaN(a) || math.IsNaN(b) || c.gapBetween(i, i+1) {
				continue
			}
			drawLine(img, xOf(i), yOf(a), xOf(i+1), yOf(b), col)
		}
		// Limits drawn as dotted red segments, one per sample step
		if s.Limit != nil {
			limCol := color.RGBA{R: 255, G: 60, B: 60, A: 255}
			for i := lo; i < hi; i++ {
				l1, l2 := s.Limit(i), s.Limit(i+1)
				if l1 <= 0 || l2 <= 0 {
					continue
				}
				drawDotted(img, xOf(i), yOf(l1), xOf(i+1), yOf(l2), limCol)
			}
		}
	}

	// X axis: time ticks in local time
	t0 := c.samples[lo].Time
	t1 := c.samples[hi].Time
	span := t1.Sub(t0)
	tstep := timeStep(span, 7)
	first := t0.Truncate(tstep)
	if first.Before(t0) {
		first = first.Add(tstep)
	}
	for t := first; !t.After(t1); t = t.Add(tstep) {
		x := c.plotL + float32(float64(t.Sub(t0))/float64(span))*plotW
		fillRect(img, int(x), int(c.plotT), 1, int(plotH), gridColor)
		c.xTicks = append(c.xTicks, tick{pos: x, label: formatTime(t, span)})
	}

	c.img = img
	c.drawSelection(img)
}

// gapBetween reports whether the samples i and j are separated by more than three intervals (missing data)
func (c *chart) gapBetween(i, j int) bool {
	if j >= len(c.samples) {
		return true
	}
	d := c.samples[j].Time.Sub(c.samples[i].Time)
	return d > 3*time.Duration(c.samples[j].Interval)*time.Second
}

// drawSelection draws the drag selection and the hover marker on top of the plot
func (c *chart) drawSelection(img *image.RGBA) {
	plotW := int(c.plotR - c.plotL)
	plotH := int(c.plotB - c.plotT)
	if c.dragging {
		x0, x1 := int(math.Min(float64(c.dragFrom), float64(c.dragTo))), int(math.Max(float64(c.dragFrom), float64(c.dragTo)))
		fillRect(img, x0, int(c.plotT), x1-x0, plotH, color.RGBA{R: 90, G: 150, B: 255, A: 70})
	}
	if c.hover >= 0 && !c.dragging {
		lo, hi := c.visibleRange()
		if c.hover >= lo && c.hover <= hi {
			x := c.plotL + float32(float64(c.hover-lo)/float64(hi-lo))*float32(plotW)
			fillRect(img, int(x), int(c.plotT), 1, plotH, color.RGBA{R: 200, G: 200, B: 200, A: 180})
		}
	}
}

// chartRenderer lays out the raster and the axis labels
type chartRenderer struct {
	c      *chart
	labels []*canvas.Text
}

func (r *chartRenderer) Layout(size fyne.Size) {
	r.c.raster.Resize(size)
	r.c.raster.Move(fyne.NewPos(0, 0))
	if size != r.c.size {
		r.c.render(size)
	}
	r.rebuildLabels()
}

func (r *chartRenderer) MinSize() fyne.Size {
	return r.c.MinSize()
}

func (r *chartRenderer) Refresh() {
	r.c.render(r.c.size)
	r.rebuildLabels()
	r.c.raster.Refresh()
	canvas.Refresh(r.c)
}

func (r *chartRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.c.raster}
	for _, t := range r.labels {
		objs = append(objs, t)
	}
	return objs
}

func (r *chartRenderer) Destroy() {}

// rebuildLabels creates the text objects for the current ticks and unit
func (r *chartRenderer) rebuildLabels() {
	c := r.c
	r.labels = nil
	fg := theme.ForegroundColor()
	size := theme.CaptionTextSize()
	newText := func(s string, alignRight bool) *canvas.Text {
		t := canvas.NewText(s, fg)
		t.TextSize = size
		if alignRight {
			t.Alignment = fyne.TextAlignTrailing
		}
		return t
	}
	for _, tk := range c.yTicks {
		t := newText(tk.label, true)
		min := t.MinSize()
		t.Resize(fyne.NewSize(marginLeft-8, min.Height))
		t.Move(fyne.NewPos(0, tk.pos-min.Height/2))
		r.labels = append(r.labels, t)
	}
	for _, tk := range c.xTicks {
		t := newText(tk.label, false)
		min := t.MinSize()
		t.Move(fyne.NewPos(tk.pos-min.Width/2, c.size.Height-marginBottom+4))
		r.labels = append(r.labels, t)
	}
	unit := newText(c.plot.Unit, false)
	unit.Move(fyne.NewPos(4, 0))
	r.labels = append(r.labels, unit)
	if c.emptyMessage != "" {
		msg := newText(c.emptyMessage, false)
		msg.Move(fyne.NewPos(c.plotL+8, c.plotT+8))
		r.labels = append(r.labels, msg)
	}
}

// Dragged starts or extends a time selection
func (c *chart) Dragged(e *fyne.DragEvent) {
	if !c.dragging {
		c.dragging = true
		c.dragFrom = e.Position.X - e.Dragged.DX
	}
	c.dragTo = e.Position.X
	c.Refresh()
}

// DragEnd applies the selection as the new shared time range
func (c *chart) DragEnd() {
	if !c.dragging {
		return
	}
	c.dragging = false
	if math.Abs(float64(c.dragTo-c.dragFrom)) > 5 {
		i0, i1 := c.indexAt(c.dragFrom), c.indexAt(c.dragTo)
		if i0 >= 0 && i1 >= 0 {
			c.view.set(i0, i1)
			return // view.set refreshes this chart too
		}
	}
	c.Refresh()
}

// Tapped asks for the details of the sample under the pointer
func (c *chart) Tapped(e *fyne.PointEvent) {
	if i := c.indexAt(e.Position.X); i >= 0 && c.onInspect != nil {
		c.onInspect(i)
	}
}

// TappedSecondary does the same as a tap, for right-clicks
func (c *chart) TappedSecondary(e *fyne.PointEvent) {
	c.Tapped(e)
}

// DoubleTapped returns to the full time range
func (c *chart) DoubleTapped(*fyne.PointEvent) {
	c.view.reset()
}

// MouseIn implements desktop.Hoverable
func (c *chart) MouseIn(e *desktop.MouseEvent) {
	c.MouseMoved(e)
}

// MouseMoved moves the hover marker and reports the sample under the pointer
func (c *chart) MouseMoved(e *desktop.MouseEvent) {
	i := c.indexAt(e.Position.X)
	if i != c.hover {
		c.hover = i
		c.Refresh()
	}
	if i >= 0 && c.onHover != nil {
		c.onHover(i, e.AbsolutePosition)
	}
}

// MouseOut implements desktop.Hoverable
func (c *chart) MouseOut() {
	c.hover = -1
	c.Refresh()
	if c.onLeave != nil {
		c.onLeave()
	}
}

// niceTicks returns a rounded axis range and step that cover [lo, hi] with about n ticks
func niceTicks(lo, hi float64, n int) (float64, float64, float64) {
	if hi <= lo {
		pad := math.Max(math.Abs(lo)*0.05, 1)
		lo, hi = lo-pad, hi+pad
	}
	raw := (hi - lo) / float64(n-1)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * mag
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	nlo := math.Floor(lo/step) * step
	nhi := math.Ceil(hi/step) * step
	if nhi <= nlo {
		nhi = nlo + step
	}
	return nlo, nhi, step
}

// timeSteps are the tick spacings the X axis may use
var timeSteps = []time.Duration{
	time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 48 * time.Hour, 7 * 24 * time.Hour,
}

// timeStep returns the smallest tick spacing that gives at most n ticks over span
func timeStep(span time.Duration, n int) time.Duration {
	for _, s := range timeSteps {
		if span/s <= time.Duration(n) {
			return s
		}
	}
	return timeSteps[len(timeSteps)-1]
}

// formatAxis formats a Y tick with as many decimals as its step needs
func formatAxis(v, step float64) string {
	switch {
	case step >= 1:
		return fmt.Sprintf("%.0f", v)
	case step >= 0.1:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// formatTime formats an X tick in local time: clock only for short spans, with the date otherwise
func formatTime(t time.Time, span time.Duration) string {
	if span <= 36*time.Hour {
		return t.Local().Format("15:04")
	}
	return t.Local().Format("02 Jan 15:04")
}

// fillRect paints a rectangle, blending translucent colours over what is already there
func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	b := img.Bounds()
	for py := max(y, b.Min.Y); py < min(y+h, b.Max.Y); py++ {
		for px := max(x, b.Min.X); px < min(x+w, b.Max.X); px++ {
			blend(img, px, py, c)
		}
	}
}

// blend draws c over the pixel at (x, y)
func blend(img *image.RGBA, x, y int, c color.RGBA) {
	if !(image.Point{X: x, Y: y}).In(img.Bounds()) {
		return
	}
	off := img.PixOffset(x, y)
	a := uint32(c.A)
	inv := 255 - a
	img.Pix[off+0] = uint8((uint32(c.R)*a + uint32(img.Pix[off+0])*inv) / 255)
	img.Pix[off+1] = uint8((uint32(c.G)*a + uint32(img.Pix[off+1])*inv) / 255)
	img.Pix[off+2] = uint8((uint32(c.B)*a + uint32(img.Pix[off+2])*inv) / 255)
	img.Pix[off+3] = 255
}

// drawLine draws a 2-pixel-thick line between two points
func drawLine(img *image.RGBA, x0, y0, x1, y1 float32, c color.RGBA) {
	steps := int(math.Max(math.Abs(float64(x1-x0)), math.Abs(float64(y1-y0)))) + 1
	for s := 0; s <= steps; s++ {
		f := float32(s) / float32(steps)
		x := int(math.Round(float64(x0 + (x1-x0)*f)))
		y := int(math.Round(float64(y0 + (y1-y0)*f)))
		blend(img, x, y, c)
		blend(img, x, y+1, c)
	}
}

// drawDotted draws a dotted line, used for limits
func drawDotted(img *image.RGBA, x0, y0, x1, y1 float32, c color.RGBA) {
	steps := int(math.Max(math.Abs(float64(x1-x0)), math.Abs(float64(y1-y0)))) + 1
	for s := 0; s <= steps; s += 3 {
		f := float32(s) / float32(steps)
		blend(img, int(math.Round(float64(x0+(x1-x0)*f))), int(math.Round(float64(y0+(y1-y0)*f))), c)
	}
}
