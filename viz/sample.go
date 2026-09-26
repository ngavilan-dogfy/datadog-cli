package viz

import (
	"math"

	"github.com/charmbracelet/lipgloss"
)

// Sample draws two made-up series so people can see which style looks
// right in their terminal.
func Sample(style Style, width, height int) []string {
	pts := func(f func(float64) float64) []Point {
		out := make([]Point, 120)
		for i := range out {
			out[i] = Point{T: int64(i) * 60_000, V: f(float64(i) / 119)}
		}
		return out
	}
	c := Chart{
		Width: width, Height: height, Style: style, NoXAxis: true, NoYAxis: true, Cursor: -1,
		Series: []Series{
			{Name: "a", Color: lipgloss.Color("4"), Points: pts(func(t float64) float64 { return 1 + math.Sin(t*2*math.Pi*1.5)*0.8 })},
			{Name: "b", Color: lipgloss.Color("5"), Points: pts(func(t float64) float64 { return 1 + math.Cos(t*2*math.Pi)*0.5 })},
		},
	}
	return c.Render()
}
