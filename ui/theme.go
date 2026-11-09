package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// darkMode is the current appearance. It is read by the charts, which draw with their own colours.
var darkMode bool

// atopTheme renders Fyne's default theme in either the light or the dark variant
type atopTheme struct {
	dark bool
}

func (t atopTheme) variant() fyne.ThemeVariant {
	if t.dark {
		return theme.VariantDark
	}
	return theme.VariantLight
}

func (t atopTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, t.variant())
}

func (t atopTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t atopTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t atopTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

// setDarkMode switches the whole application between light and dark appearance
func setDarkMode(dark bool) {
	darkMode = dark
	fyne.CurrentApp().Settings().SetTheme(atopTheme{dark: dark})
}

// chartColors returns the background, grid and palette used by the charts for the current appearance
func chartColors() (bg, grid, breach color.RGBA, palette []color.RGBA) {
	if darkMode {
		return color.RGBA{R: 24, G: 26, B: 30, A: 255},
			color.RGBA{R: 60, G: 64, B: 70, A: 255},
			color.RGBA{R: 255, G: 50, B: 50, A: 85},
			[]color.RGBA{
				{R: 57, G: 255, B: 20, A: 255}, {R: 0, G: 170, B: 255, A: 255}, {R: 255, G: 180, B: 0, A: 255},
				{R: 255, G: 90, B: 255, A: 255}, {R: 0, G: 230, B: 230, A: 255}, {R: 255, G: 255, B: 90, A: 255},
				{R: 255, G: 120, B: 120, A: 255}, {R: 170, G: 170, B: 255, A: 255},
			}
	}
	return color.RGBA{R: 255, G: 255, B: 255, A: 255},
		color.RGBA{R: 220, G: 220, B: 220, A: 255},
		color.RGBA{R: 255, G: 0, B: 0, A: 70},
		[]color.RGBA{
			{R: 0, G: 130, B: 0, A: 255}, {R: 0, G: 90, B: 220, A: 255}, {R: 220, G: 120, B: 0, A: 255},
			{R: 180, G: 0, B: 180, A: 255}, {R: 0, G: 150, B: 150, A: 255}, {R: 150, G: 130, B: 0, A: 255},
			{R: 200, G: 40, B: 40, A: 255}, {R: 90, G: 90, B: 200, A: 255},
		}
}
