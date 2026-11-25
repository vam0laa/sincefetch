package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var now = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func put(t *testing.T, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newFixture is a small fake Linux system.
func newFixture(t *testing.T) {
	t.Helper()
	oldRoot, oldStat := root, statfs
	root = t.TempDir()
	statfs = func(string) (uint64, uint64, bool) { return 40 << 30, 100 << 30, true }
	turkish = false
	t.Cleanup(func() { root, statfs, turkish = oldRoot, oldStat, false })
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("USER", "ada")
	put(t, "/etc/os-release", "PRETTY_NAME=\"Arch Linux\"\nID=arch\n")
	put(t, "/proc/sys/kernel/hostname", "box\n")
	put(t, "/proc/sys/kernel/osrelease", "7.3.6-arch1-1\n")
	put(t, "/proc/sys/kernel/random/boot_id", "boot-b\n")
	put(t, "/proc/uptime", "7500.12 100.0\n")
	put(t, "/proc/stat", "btime 1795600000\n")
	put(t, "/proc/meminfo", "MemTotal: 16000000 kB\nMemAvailable: 8000000 kB\nSwapTotal: 4000000 kB\nSwapFree: 3000000 kB\n")
	put(t, "/proc/cpuinfo", "processor\t: 0\nmodel name\t: AMD Ryzen 9 270 w/ Radeon 780M Graphics\nprocessor\t: 1\n")
	put(t, "/proc/mounts", "/dev/nvme0n1p2 /home btrfs rw 0 0\n/dev/nvme0n1p2 / btrfs rw 0 0\ntmpfs /tmp tmpfs rw 0 0\n")
	put(t, "/sys/class/dmi/placeholder", "")
	put(t, "/sys/devices/virtual/dmi/id/product_name", "ROG Zephyrus G14 GA403UH_GA403UH\n")
	put(t, "/sys/bus/pci/devices/0000:01:00.0/class", "0x030000\n")
	put(t, "/sys/bus/pci/devices/0000:01:00.0/vendor", "0x10de\n")
	put(t, "/sys/bus/pci/devices/0000:01:00.0/device", "0x2d98\n")
	put(t, "/usr/share/hwdata/pci.ids", "10de  NVIDIA Corporation\n\t2d98  GN21 [GeForce RTX 5050 Laptop GPU]\n8086  Intel\n")
	put(t, "/sys/class/drm/card1-eDP-1/status", "connected\n")
	put(t, "/sys/class/drm/card1-eDP-1/modes", "2880x1800\n1920x1200\n")
	put(t, "/sys/class/power_supply/BAT0/type", "Battery\n")
	put(t, "/sys/class/power_supply/BAT0/capacity", "80\n")
	put(t, "/sys/class/power_supply/BAT0/status", "Discharging\n")
	put(t, "/sys/class/power_supply/BAT0/energy_full", "68000000\n")
	put(t, "/sys/class/power_supply/BAT0/energy_full_design", "73000000\n")
	put(t, "/var/lib/pacman/local/linux-7.3.6/desc", "")
	put(t, "/var/lib/pacman/local/mesa-26.2/desc", "")
	put(t, "/usr/lib/modules/7.3.6-arch1-1/vmlinuz", "")
}

func logLine(when, rest string) string { return "[" + when + "] [ALPM] " + rest + "\n" }

const sample = "" +
	"[2026-11-20T10:00:00+0000] [PACMAN] Running 'pacman -Syu'\n" +
	"[2026-11-20T10:00:01+0000] [ALPM] upgraded old (1-1 -> 2-1)\n" +
	"[2026-11-25T10:00:00+0000] [ALPM] upgraded linux (7.3.4.arch1-1 -> 7.3.5.arch1-1)\n" +
	"[2026-11-25T11:00:00+0000] [ALPM] upgraded linux (7.3.5.arch1-1 -> 7.3.6.arch1-1)\n" +
	"[2026-11-25T11:00:01+0000] [ALPM] installed go (1.27-1)\n" +
	"[2026-11-25T11:00:02+0000] [ALPM] installed temp (1-1)\n" +
	"[2026-11-25T11:05:00+0000] [ALPM] removed temp (1-1)\n" +
	"[2026-11-25T11:06:00+0000] [ALPM] removed quickshell (0.2-1)\n" +
	"[2026-11-25T11:07:00+0000] [ALPM] upgraded same (1-1 -> 2-1)\n" +
	"[2026-11-25T11:08:00+0000] [ALPM] downgraded same (2-1 -> 1-1)\n" +
	"[2026-11-25T11:09:00+0000] [ALPM] reinstalled bash (5-1)\n" +
	"garbage line\n"

func TestPackageChanges(t *testing.T) {
	newFixture(t)
	put(t, "/var/log/pacman.log", sample)
	got, ok := packageChanges("pacman", now.Add(-48*time.Hour))
	if !ok {
		t.Fatal("log not read")
	}
	m := map[string]PkgChange{}
	for _, c := range got {
		m[c.Name] = c
	}
	if len(m) != 3 {
		t.Fatalf("want linux, go, quickshell; got %+v", got)
	}
	if c := m["linux"]; c.Kind != "upgraded" || c.Old != "7.3.4.arch1-1" || c.New != "7.3.6.arch1-1" {
		t.Errorf("linux chain: %+v", c)
	}
	if m["go"].Kind != "installed" || m["quickshell"].Kind != "removed" {
		t.Errorf("kinds: %+v", m)
	}
	if got[0].Name != "linux" {
		t.Errorf("kernel should come first, got %s", got[0].Name)
	}
}

func TestDpkgLine(t *testing.T) {
	e, ok := parseDpkg("2026-11-25 10:00:00 install curl:amd64 <none> 8.2-1")
	if !ok || e.kind != "installed" || e.name != "curl" || e.old != "" {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := parseDpkg("2026-11-25 10:00:00 status installed curl:amd64 8.2-1"); ok {
		t.Error("status lines are not changes")
	}
}

func TestVersions(t *testing.T) {
	if v := shortVersion("7.3.6.arch1-1"); v != "7.3.6" {
		t.Error(v)
	}
	if v := shortVersion("1:26.2.5-1"); v != "26.2.5" {
		t.Error(v)
	}
}

func TestShortNames(t *testing.T) {
	if g := shortGPU("NVIDIA GeForce RTX 5050 Max-Q / Mobile"); g != "RTX 5050" {
		t.Error(g)
	}
	if g := shortGPU("AMD Radeon 780M Graphics"); g != "Radeon 780M" {
		t.Error(g)
	}
	if c := shortCPU("AMD Ryzen 9 270 (16)"); c != "Ryzen 9 270 (16)" {
		t.Error(c)
	}
}

func TestLogo(t *testing.T) {
	a := logoFor("arch", 16, 40)
	if a.width != 32 || len(a.lines) != 16 || len(a.colors) != 16 {
		t.Fatal(a.width, len(a.lines))
	}
	for _, l := range a.lines {
		if utf8.RuneCountInString(l) != a.width {
			t.Fatalf("ragged row %q", l)
		}
	}
	if a := logoFor("arch", 16, 10); a.width != 0 {
		t.Error("no room, no logo")
	}
}

func TestFreshInstallIsNotAThousandNewPackages(t *testing.T) {
	newFixture(t)
	var b strings.Builder
	for i := 0; i < 150; i++ {
		b.WriteString(logLine("2026-11-25T08:00:00+0000", "installed pkg"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+" (1-1)"))
	}
	b.WriteString(logLine("2026-11-25T15:00:00+0000", "installed go (1.27-1)"))
	put(t, "/var/log/pacman.log", b.String())
	r := compare(collect(), nil, now.Add(-7*24*time.Hour), "boot-b", now)
	text := lines(r)
	if !strings.Contains(text, "installed 28 hours ago") {
		t.Error(text)
	}
	if !strings.Contains(text, "1 new package: go") || len(r.Packages) != 1 {
		t.Error(text)
	}
}

func lines(r Report) string {
	var s []string
	for _, l := range r.Lines {
		s = append(s, l.Mark+" "+l.Text)
	}
	return strings.Join(s, "\n")
}

func TestCompare(t *testing.T) {
	newFixture(t)
	in := collect()
	base := &Snapshot{Time: now.Add(-48 * time.Hour), BootID: "boot-a", Kernel: "7.3.5-arch1-1", BatteryHealth: 95,
		Disks: []Disk{{Mount: "/", Device: "/dev/nvme0n1p2", Used: 34 << 30, Total: 100 << 30}}}
	text := lines(compare(in, base, base.Time, "boot-b", now))
	for _, want := range []string{"~ kernel 7.3.5-arch1-1 → 7.3.6-arch1-1", "~ disk / +6.0 GB (40% full)", "! battery health 95% → 93%", "~ restarted since then, up 2 h 5 min"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	same := &Snapshot{Time: base.Time, BootID: "boot-b", Kernel: in.Kernel, BatteryHealth: in.BatteryHealth, Disks: in.Disks}
	if text := lines(compare(in, same, base.Time, "boot-b", now)); text != "= nothing worth mentioning changed" {
		t.Error(text)
	}
}

func TestReboot(t *testing.T) {
	newFixture(t)
	if why := needsReboot("pacman", "7.3.6-arch1-1", time.Time{}); len(why) != 0 {
		t.Error(why)
	}
	if why := needsReboot("pacman", "7.3.5-arch1-1", time.Time{}); len(why) != 1 {
		t.Error("a missing modules directory means a restart:", why)
	}
}

func TestCollect(t *testing.T) {
	newFixture(t)
	in := collect()
	if in.Machine != "ROG Zephyrus G14 GA403UH" || in.CPU != "AMD Ryzen 9 270 (2)" || in.Packages != 2 {
		t.Errorf("%+v", in)
	}
	if len(in.GPUs) != 1 || in.GPUs[0] != "NVIDIA GeForce RTX 5050 Laptop GPU" {
		t.Error(in.GPUs)
	}
	if len(in.Disks) != 1 || in.Disks[0].Mount != "/" {
		t.Error("subvolumes must be listed once:", in.Disks)
	}
	if len(in.Displays) != 1 || in.Displays[0] != "2880x1800 (eDP-1)" {
		t.Error(in.Displays)
	}
	if in.Battery != 80 || round(in.BatteryHealth) != 93 {
		t.Error(in.Battery, in.BatteryHealth)
	}
}

func TestPickAndSave(t *testing.T) {
	at := func(d time.Duration) Snapshot { return Snapshot{Time: now.Add(-d)} }
	h := []Snapshot{at(72 * time.Hour), at(30 * time.Hour), at(2 * time.Hour)}
	if s := pick(h, now, 0); s == nil || now.Sub(s.Time) != 30*time.Hour {
		t.Error("the last look is the newest one older than 12 h:", s)
	}
	if s := pick(h, now, 48*time.Hour); s == nil || now.Sub(s.Time) != 72*time.Hour {
		t.Error(s)
	}
	if s := pick([]Snapshot{at(time.Minute)}, now, 0); s != nil {
		t.Error("too fresh to compare with")
	}
	path := filepath.Join(t.TempDir(), "h.json")
	if err := save(path, nil, at(100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h, _ = load(path)
	if err := save(path, h, at(0)); err != nil {
		t.Fatal(err)
	}
	if h, _ = load(path); len(h) != 1 {
		t.Error("old snapshots are pruned:", len(h))
	}
	if err := save(path, h, Snapshot{Time: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if h, _ = load(path); len(h) != 1 {
		t.Error("at most one an hour")
	}
}

func TestSince(t *testing.T) {
	if d, err := parseSince("2w"); err != nil || d != 14*24*time.Hour {
		t.Error(d, err)
	}
	for _, bad := range []string{"d", "7", "-1d", "3y"} {
		if _, err := parseSince(bad); err == nil {
			t.Error("accepted", bad)
		}
	}
}

func TestRun(t *testing.T) {
	newFixture(t)
	put(t, "/var/log/pacman.log", sample)
	var out, errs bytes.Buffer
	if code := run([]string{"--json", "--lang", "en"}, &out, &errs, 0, now); code != 0 {
		t.Fatal(code, errs.String())
	}
	var doc struct {
		Changes Report `json:"changes"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || !doc.Changes.First {
		t.Fatal(err, out.String())
	}
	if h, _ := load(stateFile()); len(h) != 1 {
		t.Fatal("the look was not remembered")
	}
	for _, width := range []int{34, 50, 66, 80, 90, 120, 200} {
		out.Reset()
		if code := run([]string{"--lang", "tr", "--no-color", "--no-save"}, &out, &errs, width, now); code != 0 {
			t.Fatal(code, errs.String())
		}
		for _, line := range strings.Split(out.String(), "\n") {
			if utf8.RuneCountInString(line) > width {
				t.Errorf("width %d: %q", width, line)
			}
		}
		if !strings.Contains(out.String(), "donanım") || strings.Contains(out.String(), "\x1b[") {
			t.Error(out.String())
		}
	}
	if code := run([]string{"--changes", "--info"}, &out, &errs, 80, now); code != 2 {
		t.Error("conflicting flags must fail")
	}
}

func TestBrokenStateIsNotOverwritten(t *testing.T) {
	newFixture(t)
	path := stateFile()
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("{broken"), 0o600)
	var out, errs bytes.Buffer
	run(nil, &out, &errs, 80, now)
	if b, _ := os.ReadFile(path); string(b) != "{broken" || errs.Len() == 0 {
		t.Error("overwrote a file it could not read")
	}
}

func TestSampleConfigIsTheDefault(t *testing.T) {
	newFixture(t)
	var out, errs bytes.Buffer
	if code := run([]string{"--init", "--lang", "en"}, &out, &errs, 80, now); code != 0 {
		t.Fatal(code, errs.String())
	}
	c, err := loadConfig(configFile(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.check()) != 0 || len(c.Sections) != 2 || c.Width != 60 || c.Bar.Width != 10 {
		t.Errorf("%+v %v", c, c.check())
	}
	if code := run([]string{"--init"}, &out, &errs, 80, now); code != 1 {
		t.Error("--init must not replace an existing file")
	}
}

func TestConfigShapesTheOutput(t *testing.T) {
	newFixture(t)
	logo := filepath.Join(t.TempDir(), "logo.txt")
	os.WriteFile(logo, []byte("\x1b[31mXX\x1b[0m\n YY \x07\n"), 0o644)
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, []byte(`{
  // a comment
  "language": "en", "logo": "`+logo+`", "changes": false, "show_title": false,
  "accent": "#ff8800", "bar": {"full": "#", "empty": ".", "width": 5},
  "sections": [{"title": "mine", "modules": ["motto", "memory", "from_cmd", "nope"]}],
  "labels": {"memory": "ram"},
  "custom": {
    "motto": {"label": "motto", "text": "measure"},
    "from_cmd": {"label": "cmd", "command": "printf 'a\\033[2Jb\\nsecond'"},
  }
}`), 0o644)
	var out, errs bytes.Buffer
	if code := run([]string{"--config", path, "--no-color", "--no-save"}, &out, &errs, 100, now); code != 0 {
		t.Fatal(code, errs.String())
	}
	text := out.String()
	for _, want := range []string{"mine", "motto", "measure", "ram", "###..", "XX", "YY", "ab"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"second", "\x1b", "\x07", "ada@box", "last 7 days", "cpu"} {
		if strings.Contains(text, bad) {
			t.Errorf("should not contain %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(errs.String(), `unknown module "nope"`) {
		t.Error(errs.String())
	}
}

func TestBadConfigFallsBack(t *testing.T) {
	newFixture(t)
	os.MkdirAll(filepath.Dir(configFile()), 0o755)
	os.WriteFile(configFile(), []byte(`{"widht": 3}`), 0o644)
	var out, errs bytes.Buffer
	if code := run([]string{"--no-save", "--lang", "en"}, &out, &errs, 100, now); code != 0 || !strings.Contains(out.String(), "hardware") || !strings.Contains(errs.String(), "widht") {
		t.Error(code, errs.String(), out.String())
	}
}

func TestSlowCommandIsCutOff(t *testing.T) {
	start := time.Now()
	v := Custom{Command: "sleep 5"}.value()
	if time.Since(start) > 3*time.Second || v == "" {
		t.Error(time.Since(start), v)
	}
}

func TestColors(t *testing.T) {
	for in, want := range map[string]string{"cyan": "36", "bright_cyan": "96", "208": "38;5;208", "#ff8800": "38;2;255;136;0", "pinkish": "", "999": ""} {
		if got := colorCode(in); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}
