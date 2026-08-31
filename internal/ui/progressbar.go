package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// The transfer bar is drawn at eighth-of-a-cell resolution: the cell straddling
// the fill boundary is rendered as a partial block. A whole-cell bar of the
// widths rtr uses (16–36 columns) advances only every ~3%, which throws away
// most of what rsync reports; at eighths even the narrowest bar resolves ~100
// steps, so the sub-percent progress computed in xfer.applyProgress shows up as
// movement instead of rounding away.
const (
	barFull  = '█'
	barEmpty = '░'
	// barLabelW is the width of the trailing " 100.0%" label, which is included
	// in the bar's overall width.
	barLabelW = 7
	// defaultBarWidth applies until the first window-size message arrives.
	defaultBarWidth = 40
)

// barEighths[n] is a cell filled n eighths from the left; index 0 is blank and
// 8 (a full cell) is barFull.
var barEighths = [8]rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// Fill gradient: the indigo-to-pink endpoints of the default bubbles progress
// gradient rtr used before it drew its own bar, with intermediate stops so the
// ramp travels through the saturated blues, violets and magentas between them
// instead of cutting straight across. Adjacent stops are blended in CIE L*u*v*,
// so the extra stops bend the path without introducing visible seams.
var barStops = mustColors(
	"#5A56E0", // indigo
	"#6C5BEA", // blue-violet
	"#8A5FF1", // violet
	"#AC63F5", // purple
	"#CB68F7", // magenta
	"#EE6FF8", // pink
)

// The dim fill for the not-yet-transferred remainder.
var barEmptyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#606060"))

func mustColors(hexes ...string) []colorful.Color {
	cs := make([]colorful.Color, len(hexes))
	for i, h := range hexes {
		c, err := colorful.Hex(h)
		if err != nil {
			panic("ui: bad gradient stop " + h + ": " + err.Error())
		}
		cs[i] = c
	}
	return cs
}

// renderBar draws pct (0..100) as a gradient progress bar occupying exactly
// width columns, trailing percentage label included.
func renderBar(width int, pct float64) string {
	pct = math.Max(0, math.Min(100, pct))
	cells := max(width-barLabelW, 1)
	// Fill measured in eighths of a cell across the whole bar.
	filled := int(math.Round(pct / 100 * float64(cells) * 8))

	var b strings.Builder
	for i := range cells {
		n := clamp(filled-i*8, 0, 8)
		if n == 0 {
			b.WriteString(barEmptyStyle.Render(string(barEmpty)))
			continue
		}
		r := barFull
		if n < 8 {
			r = barEighths[n]
		}
		b.WriteString(barCellStyle(i, cells).Render(string(r)))
	}
	fmt.Fprintf(&b, "%*.1f%%", barLabelW-1, pct)
	return b.String()
}

// barCellStyle is the gradient color of cell i of a width-cell bar. The ramp is
// spread over the full bar (not the filled part), so a cell keeps its color as
// the transfer advances.
func barCellStyle(i, width int) lipgloss.Style {
	p := 0.5
	if width > 1 {
		p = float64(i) / float64(width-1)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(barColorAt(p).Hex()))
}

// barColorAt samples the multi-stop gradient at p (0..1): it picks the pair of
// stops p falls between and blends them by p's position within that segment.
func barColorAt(p float64) colorful.Color {
	p = math.Max(0, math.Min(1, p))
	segs := len(barStops) - 1
	i := int(p * float64(segs))
	if i >= segs {
		i = segs - 1
	}
	return barStops[i].BlendLuv(barStops[i+1], p*float64(segs)-float64(i))
}
