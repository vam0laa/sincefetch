package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Section is one framed group of lines.
type Section struct {
	Title   string   `json:"title"`
	Modules []string `json:"modules"`
}

// Custom is a line the user defines: a fixed text, the first line of a file, or the output of a command.
type Custom struct {
	Label   string `json:"label"`
	Text    string `json:"text,omitempty"`
	File    string `json:"file,omitempty"`
	Command string `json:"command,omitempty"`
}

type Bar struct {
	Full  string `json:"full"`
	Empty string `json:"empty"`
	Width int    `json:"width"`
}

// Config is everything a user can change. A missing field keeps its default.
type Config struct {
	Language   string            `json:"language"`
	Logo       string            `json:"logo"`
	LogoColors []string          `json:"logo_colors"`
	Accent     string            `json:"accent"`
	Width      int               `json:"width"`
	Bar        Bar               `json:"bar"`
	ShowTitle  bool              `json:"show_title"`
	ShowDots   bool              `json:"show_dots"`
	Changes    bool              `json:"changes"`
	Sections   []Section         `json:"sections"`
	Labels     map[string]string `json:"labels"`
	Custom     map[string]Custom `json:"custom"`
}

func defaultConfig() Config {
	return Config{
		Language: "auto", Logo: "auto", Width: 60,
		Bar:       Bar{Full: "━", Empty: "─", Width: 10},
		ShowTitle: true, ShowDots: true, Changes: true,
		Sections: []Section{
			{Title: "system", Modules: []string{"os", "host", "uptime", "env"}},
			{Title: "hardware", Modules: []string{"cpu", "gpu", "screen", "memory", "disk", "battery"}},
		},
	}
}

var moduleNames = []string{"os", "kernel", "host", "uptime", "packages", "env", "shell", "desktop", "terminal", "cpu", "gpu", "screen", "memory", "swap", "disk", "battery"}

const sampleConfig = `// sincefetch settings. Lines starting with // are ignored.
// Delete anything you do not want to change; the default stays.
{
  // "auto" (system language), "en" or "tr"
  "language": "auto",

  // "auto", "none", "arch", "debian", "fedora", "linux", or the path of your own ASCII art file
  "logo": "auto",

  // colours, one per band of the logo, top to bottom. Empty = the default.
  // names (red, bright_cyan ...), 0-255, or "#rrggbb"
  "logo_colors": [],
  "accent": "",

  // widest the text block may get
  "width": 60,
  "bar": { "full": "━", "empty": "─", "width": 10 },

  "show_title": true,
  "show_dots": true,
  // the "since your last look" section
  "changes": true,

  // modules: os kernel host uptime packages env shell desktop terminal
  //          cpu gpu screen memory swap disk battery, and your own from "custom"
  "sections": [
    { "title": "system", "modules": ["os", "host", "uptime", "env"] },
    { "title": "hardware", "modules": ["cpu", "gpu", "screen", "memory", "disk", "battery"] }
  ],

  // rename a line, for example { "uptime": "awake" }
  "labels": {},

  // your own lines. Add the name to a section above to show it.
  // "command" runs through sh with a one second limit.
  "custom": {
    // "motto": { "label": "motto", "text": "measure, don't guess" },
    // "host_file": { "label": "name", "file": "/etc/hostname" },
    // "day": { "label": "today", "command": "date +%A" }
  }
}
`

func configFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "sincefetch", "config.json")
}

var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// stripComments removes whole-line // comments and the trailing commas that commenting lines out leaves.
func stripComments(b []byte) []byte {
	var out [][]byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			line = nil
		}
		out = append(out, line)
	}
	return trailingComma.ReplaceAll(bytes.Join(out, []byte("\n")), []byte("$1"))
}

// loadConfig reads path. A missing file is fine unless the user named it.
func loadConfig(path string, required bool) (Config, error) {
	c := defaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(stripComments(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return defaultConfig(), fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// check reports settings that would be silently ignored.
func (c Config) check() []string {
	known := map[string]bool{}
	for _, m := range moduleNames {
		known[m] = true
	}
	for m := range c.Custom {
		known[m] = true
	}
	var bad []string
	for _, s := range c.Sections {
		for _, m := range s.Modules {
			if !known[m] {
				bad = append(bad, fmt.Sprintf(L("unknown module %q (known: %s)", "bilinmeyen modül %q (bilinenler: %s)"), m, strings.Join(moduleNames, " ")))
			}
		}
	}
	for _, v := range append([]string{c.Accent}, c.LogoColors...) {
		if v != "" && colorCode(v) == "" {
			bad = append(bad, fmt.Sprintf(L("unknown colour %q", "bilinmeyen renk %q"), v))
		}
	}
	sort.Strings(bad)
	return bad
}

// colorCode turns a colour name, a 0-255 number or #rrggbb into an SGR code.
func colorCode(v string) string {
	names := map[string]int{"black": 30, "red": 31, "green": 32, "yellow": 33, "blue": 34, "magenta": 35, "cyan": 36, "white": 37, "gray": 90, "grey": 90}
	v = strings.ToLower(strings.TrimSpace(v))
	if n, ok := names[v]; ok {
		return strconv.Itoa(n)
	}
	if rest, ok := strings.CutPrefix(v, "bright_"); ok {
		if n, ok := names[rest]; ok && n < 90 {
			return strconv.Itoa(n + 60)
		}
		return ""
	}
	if len(v) == 7 && v[0] == '#' {
		if n, err := strconv.ParseUint(v[1:], 16, 32); err == nil {
			return fmt.Sprintf("38;2;%d;%d;%d", n>>16, n>>8&255, n&255)
		}
		return ""
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 255 {
		return fmt.Sprintf("38;5;%d", n)
	}
	return ""
}

var escapes = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|.)`)

// clean makes text from files and commands safe for one terminal line: no escape sequences or control characters.
func clean(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(escapes.ReplaceAllString(s, "")), "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 || (r >= 127 && r < 160) {
			return -1
		}
		return r
	}, s))
}

// value produces the text of a custom line.
func (c Custom) value() string {
	switch {
	case c.Text != "":
		return clean(c.Text)
	case c.File != "":
		path := c.File
		if rest, ok := strings.CutPrefix(path, "~/"); ok {
			path = filepath.Join(os.Getenv("HOME"), rest)
		}
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		defer f.Close()
		b, _ := io.ReadAll(io.LimitReader(f, 4096))
		return clean(string(b))
	case c.Command != "":
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", c.Command)
		cmd.WaitDelay = 200 * time.Millisecond
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return L("(took too long)", "(çok uzun sürdü)")
		}
		if err != nil {
			return ""
		}
		return clean(string(out))
	}
	return ""
}

// customArt reads the user's own logo, at most 40 rows by 60 columns.
func customArt(path string) ([]string, int, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		path = filepath.Join(os.Getenv("HOME"), rest)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, 0, err
	}
	var lines []string
	width := 0
	for _, line := range strings.Split(strings.TrimRight(escapes.ReplaceAllString(string(b), ""), "\n"), "\n") {
		line = strings.Map(func(r rune) rune {
			if r < 32 || (r >= 127 && r < 160) {
				return -1
			}
			return r
		}, strings.ReplaceAll(line, "\t", "    "))
		line = strings.TrimRight(line, " ")
		if utf8.RuneCountInString(line) > 60 {
			line = string([]rune(line)[:60])
		}
		width = max(width, utf8.RuneCountInString(line))
		lines = append(lines, line)
		if len(lines) == 40 {
			break
		}
	}
	for i := range lines {
		lines[i] += strings.Repeat(" ", width-utf8.RuneCountInString(lines[i]))
	}
	return lines, width, nil
}
