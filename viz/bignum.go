package viz

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A 3×5 pixel font drawn with half blocks: 3 columns, 3 rows per glyph.
var glyphs = map[rune][5]string{
	'0': {"###", "#.#", "#.#", "#.#", "###"},
	'1': {".#.", "##.", ".#.", ".#.", "###"},
	'2': {"###", "..#", "###", "#..", "###"},
	'3': {"###", "..#", ".##", "..#", "###"},
	'4': {"#.#", "#.#", "###", "..#", "..#"},
	'5': {"###", "#..", "###", "..#", "###"},
	'6': {"###", "#..", "###", "#.#", "###"},
	'7': {"###", "..#", "..#", ".#.", ".#."},
	'8': {"###", "#.#", "###", "#.#", "###"},
	'9': {"###", "#.#", "###", "..#", "###"},
	'.': {".", ".", ".", ".", "#"},
	',': {".", ".", ".", ".", "#"},
	'-': {"...", "...", "###", "...", "..."},
	'+': {"...", ".#.", "###", ".#.", "..."},
	'%': {"#.#", "..#", ".#.", "#..", "#.#"},
	'/': {"..#", "..#", ".#.", "#..", "#.."},
	'k': {"#..", "#.#", "##.", "#.#", "#.#"},
	'K': {"#.#", "#.#", "##.", "#.#", "#.#"},
	'M': {"#...#", "##.##", "#.#.#", "#...#", "#...#"},
	'G': {"###", "#..", "#.#", "#.#", "###"},
	'T': {"###", ".#.", ".#.", ".#.", ".#."},
	'm': {".....", "##.#.", "#.#.#", "#.#.#", "#.#.#"},
	's': {"...", "###", "##.", "..#", "###"},
	'h': {"#..", "#..", "###", "#.#", "#.#"},
	'd': {"..#", "..#", "###", "#.#", "###"},
	'B': {"##.", "#.#", "##.", "#.#", "##."},
	'i': {".", "#", ".", "#", "#"},
	'µ': {"...", "#.#", "#.#", "##.", "#.."},
	'n': {"...", "##.", "#.#", "#.#", "#.#"},
	'b': {"#..", "#..", "##.", "#.#", "##."},
	' ': {".", ".", ".", ".", "."},
}

// BigText renders text 3 rows tall with a pixel font (characters it doesn't
// know fall back to normal text on the middle row). It returns the lines and
// their width.
func BigText(text string, color lipgloss.TerminalColor) ([]string, int) {
	rows := [3]strings.Builder{}
	for i, r := range text {
		g, ok := glyphs[r]
		if !ok {
			for k := range rows {
				if k == 1 {
					rows[k].WriteRune(r)
				} else {
					rows[k].WriteByte(' ')
				}
			}
			continue
		}
		if i > 0 {
			for k := range rows {
				rows[k].WriteByte(' ')
			}
		}
		w := len(g[0])
		for k := 0; k < 3; k++ {
			top := g[2*k]
			bot := strings.Repeat(".", w)
			if 2*k+1 < 5 {
				bot = g[2*k+1]
			}
			for x := 0; x < w; x++ {
				t, b := top[x] == '#', bot[x] == '#'
				switch {
				case t && b:
					rows[k].WriteString("█")
				case t:
					rows[k].WriteString("▀")
				case b:
					rows[k].WriteString("▄")
				default:
					rows[k].WriteByte(' ')
				}
			}
		}
	}
	st := lipgloss.NewStyle().Foreground(color)
	out := make([]string, 3)
	width := 0
	for k := range rows {
		s := rows[k].String()
		width = max(width, len([]rune(s)))
		out[k] = s
	}
	for k := range out {
		pad := width - len([]rune(out[k]))
		out[k] = st.Render(out[k]) + strings.Repeat(" ", pad)
	}
	return out, width
}
