package main

import "strings"

type point struct{ x, y float64 }

// The Arch mark as polygons on a 100×100 square, rasterised with half blocks so the edges stay smooth.
var archBody = []point{
	{50, 0}, {57, 17}, {63, 30}, {75, 54}, {88, 79}, {100, 100}, {89, 93.5}, {78, 89}, {66, 86},
	{66, 74}, {63.5, 64}, {58, 57.5}, {50, 55}, {42, 57.5}, {36.5, 64}, {34, 74}, {34, 86},
	{22, 89}, {11, 93.5}, {0, 100}, {12, 77}, {25, 51}, {37, 28}, {43, 15},
}

// archCut is the swoosh taken out of the upper part.
var archCut = []point{{26, 37}, {40, 43.5}, {49, 44}, {57, 42.5}, {64, 44}, {55, 49}, {45, 50}, {23, 43}}

func inside(poly []point, x, y float64) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a.y > y) != (b.y > y) && x < (b.x-a.x)*(y-a.y)/(b.y-a.y)+a.x {
			in = !in
		}
	}
	return in
}

// raster draws the mark cols wide and rows tall; every text row holds two pixels.
func raster(cols, rows int) []string {
	lit := func(c, py int) bool {
		// four samples per pixel keep thin edges from flickering in and out
		n := 0
		for _, dx := range []float64{0.25, 0.75} {
			for _, dy := range []float64{0.25, 0.75} {
				x := (float64(c) + dx) / float64(cols) * 100
				y := (float64(py) + dy) / float64(rows*2) * 100
				if inside(archBody, x, y) && !inside(archCut, x, y) {
					n++
				}
			}
		}
		return n >= 2
	}
	out := make([]string, rows)
	for r := range out {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			switch top, bottom := lit(c, 2*r), lit(c, 2*r+1); {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		out[r] = b.String()
	}
	return out
}

// small logos for narrow terminals and other distributions; drawn for this program.
var logos = map[string]struct {
	color string
	art   []string
}{
	"debian": {"31", []string{
		`     _____      `,
		`    /  __ \     `,
		`   |  /    |    `,
		`   |  \___-     `,
		`    -_          `,
		`      --_       `,
	}},
	"fedora": {"34", []string{
		`      _____     `,
		`     /   __)\   `,
		`     |  /  \ \  `,
		`  ___|  |__/ /  `,
		` / (_    _)_/   `,
		`/ /  |  |       `,
		`\ \__/  |       `,
		` \(_____/       `,
	}},
	"linux": {"33", []string{
		`     .--.       `,
		`    |o_o |      `,
		`    |:_/ |      `,
		`   //   \ \     `,
		`  (|     | )    `,
		` /'\_   _/'\    `,
		` \___)=(___/    `,
	}},
}

// art is a logo with one colour code per row.
type art struct {
	accent string
	width  int
	lines  []string
	colors []string
}

// logoFor picks the logo for a distribution that fits next to the text in `rows` rows and `room` columns.
// The colours are the terminal's own palette, so the logo follows the user's theme.
func logoFor(id string, rows, room int) art {
	switch id {
	case "ubuntu", "linuxmint", "pop":
		id = "debian"
	case "endeavouros", "manjaro", "cachyos", "garuda", "artix":
		id = "arch"
	case "nobara", "rhel", "centos", "almalinux", "rocky":
		id = "fedora"
	}
	if id == "arch" {
		h := min(max(rows, 8), 18)
		w := h * 2
		if w > room {
			w = room &^ 1
			h = w / 2
		}
		if h < 8 {
			return art{accent: "36"}
		}
		a := art{accent: "36", width: w, lines: raster(w, h)}
		for i := range a.lines {
			a.colors = append(a.colors, []string{"96", "36", "34", "35"}[i*4/h])
		}
		return a
	}
	l, ok := logos[id]
	if !ok {
		l = logos["linux"]
	}
	a := art{accent: l.color, width: 16, lines: l.art}
	if room < a.width {
		return art{accent: l.color}
	}
	for range a.lines {
		a.colors = append(a.colors, l.color)
	}
	return a
}
