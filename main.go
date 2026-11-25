// sincefetch is a system fetch that also says what changed since you last looked.
// It reads /proc, /sys and the package log, and writes only its own snapshot file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var version = "0.1.0"

// parseSince accepts 30m, 12h, 7d and 2w.
func parseSince(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	n, err := strconv.Atoi(s[:len(s)-1])
	if unit == 0 || err != nil || n <= 0 || n > 10000 {
		return 0, fmt.Errorf(L("cannot read %q: use something like 12h, 7d or 2w", "%q anlaşılmadı: 12h, 7d ya da 2w gibi yaz"), s)
	}
	return time.Duration(n) * unit, nil
}

// terminalWidth is 0 when f is not a terminal.
func terminalWidth(f *os.File) int {
	var ws struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.cols)
}

func systemLanguage() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func run(args []string, stdout, stderr io.Writer, width int, now time.Time) int {
	fs := flag.NewFlagSet("sincefetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "", "compare with this long ago: 12h, 7d, 2w (default: your last look)")
	lang := fs.String("lang", "", "en or tr (default: system language)")
	changes := fs.Bool("changes", false, "only what changed, without the system block")
	info := fs.Bool("info", false, "only the system block")
	noLogo := fs.Bool("no-logo", false, "hide the logo")
	noColor := fs.Bool("no-color", false, "plain text (NO_COLOR works too)")
	asJSON := fs.Bool("json", false, "machine readable output")
	noSave := fs.Bool("no-save", false, "do not remember this look")
	showVersion := fs.Bool("version", false, "print the version")
	configPath := fs.String("config", "", "settings file (default: ~/.config/sincefetch/config.json)")
	initConfig := fs.Bool("init", false, "write a commented settings file to edit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "sincefetch %s: %s\n\n", version, L("a system fetch that says what changed since you last looked", "son bakışından beri neyin değiştiğini de söyleyen sistem özeti"))
		fs.PrintDefaults()
	}
	l := systemLanguage()
	turkish = strings.HasPrefix(l, "tr")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	switch *lang {
	case "", "en", "tr":
	default:
		fmt.Fprintln(stderr, "--lang: en | tr")
		return 2
	}
	path := *configPath
	if path == "" {
		path = configFile()
	}
	cfg, cfgErr := loadConfig(path, *configPath != "" && !*initConfig)
	// the flag wins over the settings file, which wins over the system language
	for _, l := range []string{cfg.Language, *lang} {
		if l == "en" || l == "tr" {
			turkish = l == "tr"
		}
	}
	if *initConfig {
		return writeSample(path, stdout, stderr)
	}
	if cfgErr != nil {
		fmt.Fprintf(stderr, L("sincefetch: settings ignored: %v\n", "sincefetch: ayarlar yok sayıldı: %v\n"), cfgErr)
	}
	for _, problem := range cfg.check() {
		fmt.Fprintln(stderr, "sincefetch:", problem)
	}
	if *showVersion {
		fmt.Fprintln(stdout, "sincefetch", version)
		return 0
	}
	if *changes && *info {
		fmt.Fprintln(stderr, L("--changes and --info cannot be used together", "--changes ile --info birlikte kullanılamaz"))
		return 2
	}
	want, err := parseSince(*since)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	in := collect()
	boot := read("/proc/sys/kernel/random/boot_id")
	statePath := stateFile()
	history, err := load(statePath)
	broken := err != nil
	if broken {
		fmt.Fprintf(stderr, L("sincefetch: cannot read %s (%v); not saving over it\n", "sincefetch: %s okunamadı (%v); üzerine yazılmıyor\n"), statePath, err)
	}
	base := pick(history, now, want)
	from := now.Add(-7 * 24 * time.Hour)
	if want > 0 {
		from = now.Add(-want)
	}
	if base != nil {
		from = base.Time
	}
	report := compare(in, base, from, boot, now)
	if want > 0 {
		// an explicit --since is answered from the package log even without an old enough snapshot
		if base == nil || now.Sub(base.Time) < want {
			report = compare(in, base, now.Add(-want), boot, now)
		}
		report.First = false
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Version string `json:"version"`
			System  Info   `json:"system"`
			Changes Report `json:"changes"`
		}{version, in, report}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		color := width > 0 && !*noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
		w := width
		if w == 0 {
			w = 100
		}
		fmt.Fprint(stdout, render(in, report, now, view{
			style: style{color}, width: w, logo: !*noLogo,
			showInfo: !*changes, showChanges: !*info && (cfg.Changes || *changes), cfg: cfg,
		}))
	}

	if !*noSave && !broken {
		s := Snapshot{Time: now, BootID: boot, Kernel: in.Kernel, Packages: in.Packages, Flatpaks: in.Flatpaks, Disks: in.Disks, BatteryHealth: in.BatteryHealth}
		if err := save(statePath, history, s); err != nil {
			fmt.Fprintf(stderr, L("sincefetch: could not remember this look: %v\n", "sincefetch: bu bakış kaydedilemedi: %v\n"), err)
		}
	}
	return 0
}

func writeSample(path string, stdout, stderr io.Writer) int {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// O_EXCL: an existing file is the user's work and is never replaced
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, L("not written: %v\n", "yazılmadı: %v\n"), err)
		return 1
	}
	defer f.Close()
	if _, err := f.WriteString(sampleConfig); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, L("wrote ", "yazıldı: ")+path)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, terminalWidth(os.Stdout), time.Now()))
}
