package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var turkish bool

// L picks the English or Turkish text.
func L(en, tr string) string {
	if turkish {
		return tr
	}
	return en
}

func decimal(s string) string {
	if turkish {
		return strings.Replace(s, ".", ",", 1)
	}
	return s
}

func size(b uint64) string {
	const unit = 1024
	if b < unit*unit*unit {
		return fmt.Sprintf("%d MB", b/(unit*unit))
	}
	v := float64(b) / (unit * unit * unit)
	if v >= 1024 {
		return decimal(fmt.Sprintf("%.1f TB", v/1024))
	}
	return decimal(fmt.Sprintf("%.1f GB", v))
}

func plural(n int, one, many, tr string) string {
	if turkish {
		return fmt.Sprintf("%d %s", n, tr)
	}
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// span is a rough duration: "3 days 4 h", "2 h 5 min", "7 min".
func span(d time.Duration) string {
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	switch {
	case days > 0:
		s := plural(days, "day", "days", "gün")
		if h > 0 {
			s += fmt.Sprintf(" %d %s", h, L("h", "sa"))
		}
		return s
	case h > 0:
		return fmt.Sprintf("%d %s %d %s", h, L("h", "sa"), m, L("min", "dk"))
	}
	return fmt.Sprintf("%d %s", m, L("min", "dk"))
}

func ago(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return plural(int(d.Hours()/24+0.5), "day", "days", "gün")
	case d >= 2*time.Hour:
		return plural(int(d.Hours()+0.5), "hour", "hours", "saat")
	}
	return plural(int(d.Minutes()), "minute", "minutes", "dakika")
}

type style struct{ color bool }

func (s style) paint(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func pct(n int) string {
	if turkish {
		return fmt.Sprintf("%%%d", n)
	}
	return fmt.Sprintf("%d%%", n)
}

// shortCPU and shortGPU drop the words that say nothing; --json keeps the full names.
func shortCPU(s string) string {
	for _, junk := range []string{"AMD ", "Intel Core ", "Intel ", "Gen "} {
		s = strings.Replace(s, junk, "", 1)
	}
	return s
}

func shortGPU(s string) string {
	for _, junk := range []string{"NVIDIA GeForce ", "NVIDIA ", "AMD ", " Max-Q / Mobile", " Laptop GPU", " Mobile", " Graphics", " Series"} {
		s = strings.Replace(s, junk, "", 1)
	}
	return s
}

// gb is a size in gigabytes without the unit, for "used / total" pairs.
func gb(b uint64) string {
	v := float64(b) / (1 << 30)
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return decimal(fmt.Sprintf("%.1f", v))
}

// bar draws a level; warnHigh turns it yellow and red when high (memory, disk) instead of low (battery).
func (s style) bar(b Bar, p int, warnHigh bool) string {
	n := (p*b.Width + 50) / 100
	n = min(max(n, 0), b.Width)
	if p > 0 && n == 0 && b.Width > 0 {
		n = 1
	}
	bad := p
	if !warnHigh {
		bad = 100 - p
	}
	color := "32"
	switch {
	case bad >= 90:
		color = "31"
	case bad >= 75:
		color = "33"
	}
	return s.paint(color, strings.Repeat(b.Full, n)) + s.paint("90", strings.Repeat(b.Empty, b.Width-n))
}

// cell is one line of the block: its painted text and the columns it takes.
type cell struct {
	text string
	w    int
}

func join(parts ...string) string {
	var keep []string
	for _, x := range parts {
		if x != "" {
			keep = append(keep, x)
		}
	}
	return strings.Join(keep, " · ")
}

// field is one line before it is laid out: a text, or a level with a note.
type field struct {
	key, text string
	meter     bool
	level     int
	warnHigh  bool
}

// fields returns the lines of one module.
func fields(name string, in Info, cfg Config) []field {
	text := func(key, val string) []field {
		if val == "" {
			return nil
		}
		return []field{{key: key, text: val}}
	}
	desk, _, _ := strings.Cut(in.Desktop, " (")
	pk := ""
	if in.Packages > 0 {
		pk = plural(in.Packages+in.Flatpaks, "package", "packages", "paket")
	}
	switch name {
	case "os":
		return text("os", join(in.OS, shortVersion(in.Kernel)))
	case "kernel":
		return text(L("kernel", "kernel"), in.Kernel)
	case "host":
		return text(L("host", "makine"), in.Machine)
	case "uptime":
		return text(L("up", "açık"), join(span(in.Uptime), pk))
	case "packages":
		if in.Packages == 0 {
			return nil
		}
		v := fmt.Sprintf("%d %s", in.Packages, in.PkgKind)
		if in.Flatpaks > 0 {
			v = join(v, fmt.Sprintf("%d flatpak", in.Flatpaks))
		}
		return text(L("pkgs", "paket"), v)
	case "env":
		return text(L("env", "ortam"), join(desk, in.Shell, in.Terminal))
	case "shell":
		return text(L("shell", "kabuk"), in.Shell)
	case "desktop":
		return text(L("desktop", "masa"), in.Desktop)
	case "terminal":
		return text("term", in.Terminal)
	case "cpu":
		return text("cpu", shortCPU(in.CPU))
	case "gpu":
		var gpu []string
		for _, g := range in.GPUs {
			gpu = append(gpu, shortGPU(g))
		}
		return text("gpu", join(gpu...))
	case "screen":
		var screens []string
		for _, d := range in.Displays {
			res, _, _ := strings.Cut(d, " ")
			screens = append(screens, res)
		}
		return text(L("screen", "ekran"), join(screens...))
	case "memory":
		if in.MemTotal == 0 {
			return nil
		}
		note := gb(in.MemUsed) + " / " + gb(in.MemTotal) + " GB"
		if in.SwapUsed >= 1<<28 {
			note += L(" · swap ", " · takas ") + gb(in.SwapUsed)
		}
		return []field{{L("memory", "bellek"), note, true, percent(in.MemUsed, in.MemTotal), true}}
	case "swap":
		if in.SwapTotal == 0 {
			return nil
		}
		return []field{{L("swap", "takas"), gb(in.SwapUsed) + " / " + gb(in.SwapTotal) + " GB", true, percent(in.SwapUsed, in.SwapTotal), true}}
	case "disk":
		var out []field
		for _, d := range in.Disks {
			note := gb(d.Used) + " / " + gb(d.Total) + " GB"
			if len(in.Disks) > 1 {
				note += " " + d.Mount
			}
			out = append(out, field{"disk", note, true, percent(d.Used, d.Total), true})
		}
		return out
	case "battery":
		if in.Battery < 0 {
			return nil
		}
		note := pct(in.Battery)
		if in.BatteryState == "Charging" {
			note += L(" charging", " şarjda")
		}
		if in.BatteryHealth > 0 {
			note = join(note, L("health ", "sağlık ")+pct(round(in.BatteryHealth)))
		}
		return []field{{L("power", "pil"), note, true, in.Battery, false}}
	}
	if c, ok := cfg.Custom[name]; ok {
		label := c.Label
		if label == "" {
			label = name
		}
		return text(label, c.value())
	}
	return nil
}

func sectionTitle(t string) string {
	switch t {
	case "system":
		return L("system", "sistem")
	case "hardware":
		return L("hardware", "donanım")
	}
	return clean(t)
}

// panel builds the framed system block, w columns wide.
func (s style) panel(in Info, cfg Config, accent string, w int) []cell {
	var out []cell
	frame := func(t string) string { return s.paint("90", t) }
	if cfg.ShowTitle {
		title := clip(in.User+"@"+in.Hostname, w)
		out = append(out, cell{s.paint("1;"+accent, title), utf8.RuneCountInString(title)})
	}

	type group struct {
		title string
		list  []field
	}
	var groups []group
	keyW := 6
	for _, sec := range cfg.Sections {
		g := group{title: sectionTitle(sec.Title)}
		for _, m := range sec.Modules {
			for _, f := range fields(m, in, cfg) {
				if label, ok := cfg.Labels[m]; ok {
					f.key = label
				}
				f.key = clip(clean(f.key), 14)
				keyW = max(keyW, utf8.RuneCountInString(f.key))
				g.list = append(g.list, f)
			}
		}
		if len(g.list) > 0 {
			groups = append(groups, g)
		}
	}
	keyW += 2
	room := w - 2 - keyW
	for i, g := range groups {
		corner := "├"
		if i == 0 {
			corner = "╭"
		}
		head := frame(corner + strings.Repeat("─", w-1))
		if title := clip(g.title, w-5); title != "" {
			head = frame(corner+"─ ") + s.paint("1", title) + " " + frame(strings.Repeat("─", max(0, w-4-utf8.RuneCountInString(title))))
		}
		out = append(out, cell{head, w})
		for _, f := range g.list {
			pad := strings.Repeat(" ", max(1, keyW-utf8.RuneCountInString(f.key)))
			value, n := "", 0
			bw := cfg.Bar.Width
			if f.meter && bw > 0 && room > bw+8 {
				note := clip(f.text, room-bw-2)
				value, n = s.bar(cfg.Bar, f.level, f.warnHigh)+"  "+note, bw+2+utf8.RuneCountInString(note)
			} else {
				note := clip(f.text, room)
				value, n = note, utf8.RuneCountInString(note)
			}
			out = append(out, cell{frame("│ ") + s.paint(accent, f.key) + pad + value, 2 + keyW + n})
		}
	}
	return out
}

// news builds the "since" block with the same frame.
func (s style) news(r Report, now time.Time, w int, joined bool) []cell {
	head := L("since "+ago(now.Sub(r.Since))+" ago", ago(now.Sub(r.Since))+" öncesinden beri")
	if r.First {
		head = L("last 7 days", "son 7 gün")
	}
	corner := "╭"
	if joined {
		corner = "├"
	}
	rule := max(0, w-4-utf8.RuneCountInString(head))
	out := []cell{{s.paint("90", corner+"─ ") + s.paint("1", head) + " " + s.paint("90", strings.Repeat("─", rule)), w}}
	colors := map[string]string{"+": "32", "-": "31", "!": "33", "~": "34", "=": "90"}
	for _, l := range r.Lines {
		for i, part := range wrap(l.Text, w-4) {
			part = clip(part, w-4)
			mark := " "
			if i == 0 {
				mark = l.Mark
			}
			n := 4 + utf8.RuneCountInString(part)
			if l.Mark == "=" {
				part = s.paint("90", part)
			}
			out = append(out, cell{s.paint("90", "│ ") + s.paint("1;"+colors[l.Mark], mark) + " " + part, n})
		}
	}
	return out
}

func (s style) closing(w int, framed, showDots bool) []cell {
	var out []cell
	if framed {
		out = append(out, cell{s.paint("90", "╰"+strings.Repeat("─", w-1)), w})
	}
	if s.color && showDots {
		var dots strings.Builder
		dots.WriteString("  ")
		for _, c := range []int{31, 33, 32, 36, 34, 35, 37, 90} {
			dots.WriteString(s.paint(fmt.Sprint(c), "●") + " ")
		}
		out = append(out, cell{dots.String(), 18})
	}
	return out
}

// clip shortens plain text to n columns.
func clip(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// wrap breaks plain text into lines of at most n columns, at spaces.
func wrap(s string, n int) []string {
	if n < 20 {
		n = 20
	}
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > n {
			out = append(out, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(out, line)
}

type view struct {
	style
	width       int
	logo        bool
	showInfo    bool
	showChanges bool
	cfg         Config
}

const (
	gap      = 3
	panelMin = 46 // narrower than this and the logo goes
)

func render(in Info, r Report, now time.Time, v view) string {
	cfg := v.cfg
	id := in.OSID
	switch cfg.Logo {
	case "arch", "debian", "fedora", "linux":
		id = cfg.Logo
	}
	accent := logoFor(id, 0, 0).accent
	if c := colorCode(cfg.Accent); c != "" {
		accent = c
	}
	w := max(30, min(v.width, max(cfg.Width, 30)))

	var cells []cell
	if v.showInfo {
		cells = v.panel(in, cfg, accent, w)
	}
	framed := len(cells) > 1 || (len(cells) == 1 && !cfg.ShowTitle)
	if v.showChanges {
		cells = append(cells, v.news(r, now, w, framed)...)
		framed = true
	}
	cells = append(cells, v.closing(w, framed, cfg.ShowDots)...)

	var logo art
	if v.logo && v.showInfo && cfg.Logo != "none" && v.width-gap-panelMin >= 16 {
		// as tall as the text beside it, as wide as the terminal allows
		room := v.width - gap - panelMin
		switch cfg.Logo {
		case "", "auto", "arch", "debian", "fedora", "linux":
			logo = logoFor(id, len(cells), room)
		default:
			if lines, width, err := customArt(cfg.Logo); err == nil && width <= room {
				logo = art{width: width, lines: lines}
				for i := range lines {
					colors := []string{accent}
					if len(cfg.LogoColors) > 0 {
						colors = nil
						for _, c := range cfg.LogoColors {
							colors = append(colors, colorCode(c))
						}
					}
					logo.colors = append(logo.colors, colors[i*len(colors)/len(lines)])
				}
			}
		}
		if n := len(cfg.LogoColors); n > 0 {
			for i := range logo.colors {
				if c := colorCode(cfg.LogoColors[i*n/len(logo.colors)]); c != "" {
					logo.colors[i] = c
				}
			}
		}
		if logo.width > 0 && v.width-logo.width-gap < w {
			// rebuild the text for the narrower space left of the logo
			narrow := v
			narrow.logo, narrow.width = false, v.width-logo.width-gap
			text := strings.Split(strings.Trim(render(in, r, now, narrow), "\n"), "\n")
			return beside(v.style, logo, text)
		}
	}
	text := make([]string, len(cells))
	for i, c := range cells {
		text[i] = c.text
	}
	return beside(v.style, logo, text)
}

// beside puts the logo to the left of the text, centred on whichever is shorter.
func beside(s style, logo art, text []string) string {
	var b strings.Builder
	b.WriteByte('\n')
	rows := max(len(text), len(logo.lines))
	logoTop := (rows - len(logo.lines)) / 2
	textTop := (rows - len(text)) / 2
	for i := 0; i < rows; i++ {
		if logo.width > 0 {
			if j := i - logoTop; j >= 0 && j < len(logo.lines) {
				b.WriteString(s.paint("1;"+logo.colors[j], logo.lines[j]))
			} else {
				b.WriteString(strings.Repeat(" ", logo.width))
			}
			b.WriteString(strings.Repeat(" ", gap))
		}
		if j := i - textTop; j >= 0 && j < len(text) {
			b.WriteString(text[j])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
