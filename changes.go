package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PkgChange is the net change of one package inside the window.
type PkgChange struct {
	Name string    `json:"name"`
	Kind string    `json:"kind"` // installed, removed, upgraded, downgraded
	Old  string    `json:"old,omitempty"`
	New  string    `json:"new,omitempty"`
	Time time.Time `json:"time"`
}

type logEntry struct {
	t        time.Time
	kind     string
	name     string
	old, new string
}

// parsePacman reads one line such as
// [2026-11-25T21:14:07+0300] [ALPM] upgraded linux (7.3.5-1 -> 7.3.6-1)
func parsePacman(line string) (logEntry, bool) {
	var e logEntry
	if !strings.HasPrefix(line, "[") {
		return e, false
	}
	stamp, rest, ok := strings.Cut(line[1:], "] [ALPM] ")
	if !ok {
		return e, false
	}
	t, err := time.Parse("2006-01-02T15:04:05-0700", stamp)
	if err != nil {
		return e, false
	}
	verb, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return e, false
	}
	name, vers, ok := strings.Cut(rest, " (")
	if !ok {
		return e, false
	}
	vers = strings.TrimSuffix(vers, ")")
	e = logEntry{t: t, name: name, kind: verb}
	switch verb {
	case "upgraded", "downgraded":
		e.old, e.new, _ = strings.Cut(vers, " -> ")
	case "installed", "reinstalled":
		e.new = vers
	case "removed":
		e.old = vers
	default:
		return e, false
	}
	return e, true
}

// parseDpkg reads one line such as
// 2026-11-25 10:00:00 upgrade curl:amd64 8.1-1 8.2-1
func parseDpkg(line string) (logEntry, bool) {
	var e logEntry
	f := strings.Fields(line)
	if len(f) < 6 {
		return e, false
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", f[0]+" "+f[1], time.Local)
	if err != nil {
		return e, false
	}
	name, _, _ := strings.Cut(f[3], ":")
	e = logEntry{t: t, name: name, old: f[4], new: f[5]}
	switch f[2] {
	case "upgrade":
		e.kind = "upgraded"
	case "install":
		e.kind, e.old = "installed", ""
	case "remove", "purge":
		e.kind, e.new = "removed", ""
	default:
		return e, false
	}
	return e, true
}

// installWindow is how long the first installation may take; entries inside it are not "changes".
const installWindow = time.Hour

// logStart is the time of the oldest entry in the package log, which on a fresh system is the installation.
func logStart(kind string) (time.Time, bool) {
	path, parse := packageLog(kind)
	f, err := os.Open(p(path))
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if e, good := parse(sc.Text()); good {
			// only an installation starts with "installed"; a rotated log starts with anything
			return e.t, e.kind == "installed"
		}
	}
	return time.Time{}, false
}

func packageLog(kind string) (string, func(string) (logEntry, bool)) {
	if kind == "dpkg" {
		return "/var/log/dpkg.log", parseDpkg
	}
	return "/var/log/pacman.log", parsePacman
}

// packageChanges folds the package log after `since` into one net change per package.
func packageChanges(kind string, since time.Time) (changes []PkgChange, ok bool) {
	path, parse := packageLog(kind)
	f, err := os.Open(p(path))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	type acc struct {
		existedBefore, existsNow bool
		old, new                 string
		last                     time.Time
		downgraded               bool
	}
	seen := map[string]*acc{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		e, good := parse(sc.Text())
		if !good || !e.t.After(since) || e.kind == "reinstalled" {
			continue
		}
		a := seen[e.name]
		if a == nil {
			a = &acc{existedBefore: e.kind != "installed", old: e.old}
			seen[e.name] = a
		}
		a.existsNow = e.kind != "removed"
		if e.new != "" {
			a.new = e.new
		}
		a.downgraded = e.kind == "downgraded"
		a.last = e.t
	}
	for name, a := range seen {
		c := PkgChange{Name: name, Old: a.old, New: a.new, Time: a.last}
		switch {
		case !a.existedBefore && a.existsNow:
			c.Kind, c.Old = "installed", ""
		case a.existedBefore && !a.existsNow:
			c.Kind, c.New = "removed", ""
		case a.existedBefore && a.existsNow:
			if a.old == a.new {
				continue
			}
			c.Kind = "upgraded"
			if a.downgraded {
				c.Kind = "downgraded"
			}
		default:
			continue // installed and removed again: nothing changed
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool {
		if r := rank(changes[i].Name) - rank(changes[j].Name); r != 0 {
			return r < 0
		}
		return changes[i].Name < changes[j].Name
	})
	return changes, true
}

// rank puts the packages people care about first.
func rank(name string) int {
	order := []string{"linux", "linux-lts", "linux-zen", "linux-hardened", "nvidia", "nvidia-open", "nvidia-utils", "mesa", "systemd", "glibc", "hyprland", "plasma-desktop", "gnome-shell", "pipewire", "firefox", "chromium", "pacman", "go", "gcc", "python"}
	for i, n := range order {
		if name == n || (strings.HasPrefix(name, "linux-image") && n == "linux") {
			return i
		}
	}
	if strings.HasPrefix(name, "lib") {
		return len(order) + 1
	}
	return len(order)
}

// needsReboot lists what is installed on disk but not what is running.
func needsReboot(kind, kernel string, boot time.Time) []string {
	var why []string
	modules := false
	for _, dir := range []string{"/usr/lib/modules", "/lib/modules"} {
		if _, err := os.Stat(p(dir)); err != nil {
			continue
		}
		modules = true
		if _, err := os.Stat(p(dir + "/" + kernel)); err == nil {
			modules = false
			break
		}
	}
	if modules && kernel != "" {
		why = append(why, L("the running kernel "+kernel+" is no longer on disk", "çalışan çekirdek "+kernel+" artık diskte yok"))
	}
	if _, err := os.Stat(p("/var/run/reboot-required")); err == nil {
		why = append(why, L("the system asks for a restart", "sistem yeniden başlatma istiyor"))
	}
	if !boot.IsZero() {
		if changes, ok := packageChanges(kind, boot); ok {
			var names []string
			for _, c := range changes {
				n := c.Name
				if c.Kind != "removed" && (n == "systemd" || n == "glibc" || n == "nvidia-utils" || n == "nvidia" || n == "nvidia-open" || n == "amd-ucode" || n == "intel-ucode") {
					names = append(names, n)
				}
			}
			if len(names) > 0 {
				why = append(why, L("updated after the last boot: ", "son açılıştan sonra güncellendi: ")+strings.Join(names, ", "))
			}
		}
	}
	return why
}

func bootTime() time.Time {
	for _, line := range strings.Split(read("/proc/stat"), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if n > 0 {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

// Line is one row of the "since" section. Mark is + - ! ~ or =.
type Line struct {
	Mark string `json:"mark"`
	Text string `json:"text"`
}

// Report is everything that changed in the window.
type Report struct {
	Since    time.Time   `json:"since"`
	First    bool        `json:"first_look"`
	Lines    []Line      `json:"lines"`
	Packages []PkgChange `json:"packages,omitempty"`
	at       time.Time
}

func (r Report) now() time.Time { return r.at }

const noteworthy = 100 << 20 // disk changes below 100 MiB are noise

func compare(now Info, base *Snapshot, since time.Time, boot string, at time.Time) Report {
	r := Report{Since: since, First: base == nil, at: at}
	add := func(mark, text string) { r.Lines = append(r.Lines, Line{mark, text}) }

	for _, why := range needsReboot(now.PkgKind, now.Kernel, bootTime()) {
		add("!", L("restart needed: ", "yeniden başlat: ")+why)
	}
	if base != nil && base.Kernel != "" && base.Kernel != now.Kernel {
		add("~", L("kernel ", "çekirdek ")+base.Kernel+" → "+now.Kernel)
	}

	from := since
	// A system installed inside the window would list every package as new. Say so instead,
	// and count only what happened after the installation. (Many installs in the first hour
	// tell a real installation apart from a log that merely begins with one.)
	if start, install := logStart(now.PkgKind); install && start.After(since) {
		if all, ok := packageChanges(now.PkgKind, since); ok && len(all) >= 100 {
			from = start.Add(installWindow)
			add("=", L("installed "+ago(r.now().Sub(start))+" ago", ago(r.now().Sub(start))+" önce kuruldu"))
		}
	}

	changes, ok := packageChanges(now.PkgKind, from)
	r.Packages = changes
	if ok {
		group := map[string][]PkgChange{}
		for _, c := range changes {
			group[c.Kind] = append(group[c.Kind], c)
		}
		if g := group["upgraded"]; len(g) > 0 {
			add("+", plural(len(g), "update", "updates", "güncelleme")+": "+names(g, 3, true))
		}
		if g := group["installed"]; len(g) > 0 {
			add("+", plural(len(g), "new package", "new packages", "yeni paket")+": "+names(g, 3, false))
		}
		if g := group["removed"]; len(g) > 0 {
			add("-", plural(len(g), "removed", "removed", "kaldırıldı")+": "+names(g, 3, false))
		}
		if g := group["downgraded"]; len(g) > 0 {
			add("!", plural(len(g), "package downgraded", "packages downgraded", "paket eski sürüme döndü")+": "+names(g, 4, false))
		}
	} else if base != nil && base.Packages > 0 && now.Packages != base.Packages {
		add("~", fmt.Sprintf(L("packages %d → %d", "paket sayısı %d → %d"), base.Packages, now.Packages))
	}

	for _, d := range now.Disks {
		full := percent(d.Used, d.Total)
		if base != nil {
			for _, old := range base.Disks {
				if old.Device != d.Device {
					continue
				}
				delta := int64(d.Used) - int64(old.Used)
				if delta >= noteworthy || delta <= -noteworthy {
					sign, mag := "+", uint64(delta)
					if delta < 0 {
						sign, mag = "−", uint64(-delta)
					}
					add("~", fmt.Sprintf(L("disk %s %s%s (%d%% full)", "disk %s %s%s (%%%d dolu)"), d.Mount, sign, size(mag), full))
				}
			}
		}
		if full >= 90 {
			add("!", fmt.Sprintf(L("disk %s is %d%% full", "disk %s %%%d dolu"), d.Mount, full))
		}
	}

	if now.BatteryHealth > 0 && base != nil && base.BatteryHealth > 0 {
		a, b := round(base.BatteryHealth), round(now.BatteryHealth)
		if a != b {
			mark := "~"
			if b < a {
				mark = "!"
			}
			add(mark, fmt.Sprintf(L("battery health %d%% → %d%%", "pil sağlığı %%%d → %%%d"), a, b))
		}
	}
	if base != nil && base.BootID != "" && boot != "" && base.BootID != boot {
		add("~", L("restarted since then, up ", "o zamandan beri yeniden başlatıldı, açık kalma ")+span(now.Uptime))
	}
	if base != nil && base.Flatpaks != now.Flatpaks {
		add("~", fmt.Sprintf(L("flatpak apps %d → %d", "flatpak uygulaması %d → %d"), base.Flatpaks, now.Flatpaks))
	}

	if len(r.Lines) == 0 || (len(r.Lines) == 1 && from != since) {
		add("=", L("nothing worth mentioning changed", "kayda değer bir şey değişmedi"))
	}
	return r
}

func names(g []PkgChange, max int, versions bool) string {
	var out []string
	for i, c := range g {
		if i == max {
			break
		}
		s := c.Name
		// show the new version only for the kernel, where the number matters
		if versions && c.Name == "linux" && c.New != "" {
			s += " " + shortVersion(c.New)
		}
		out = append(out, s)
	}
	text := strings.Join(out, ", ")
	if len(g) > max {
		text += fmt.Sprintf(" +%d", len(g)-max)
	}
	return text
}

// shortVersion drops the epoch and the packaging revision unless only those changed.
func shortVersion(v string) string {
	if _, rest, ok := strings.Cut(v, ":"); ok {
		v = rest
	}
	if i := strings.IndexAny(v, "-+"); i > 0 {
		v = v[:i]
	}
	if i := strings.Index(v, ".arch"); i > 0 {
		v = v[:i]
	}
	return v
}

func percent(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return int(float64(used)/float64(total)*100 + 0.5)
}

func round(f float64) int { return int(f + 0.5) }
