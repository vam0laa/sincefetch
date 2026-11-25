package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// root is the filesystem root every system path is read from; tests point it at a fixture.
var root = "/"

func p(path string) string { return filepath.Join(root, path) }

func read(path string) string {
	b, err := os.ReadFile(p(path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readNum(path string) float64 {
	v, _ := strconv.ParseFloat(read(path), 64)
	return v
}

// Disk is one real filesystem, listed once even when it is mounted many times (btrfs subvolumes).
type Disk struct {
	Mount  string `json:"mount"`
	Device string `json:"device"`
	Used   uint64 `json:"used"`
	Total  uint64 `json:"total"`
}

// Info is the classic fetch block.
type Info struct {
	User      string        `json:"user"`
	Hostname  string        `json:"hostname"`
	OS        string        `json:"os"`
	OSID      string        `json:"os_id"`
	Machine   string        `json:"machine,omitempty"`
	Kernel    string        `json:"kernel"`
	Uptime    time.Duration `json:"uptime_ns"`
	Packages  int           `json:"packages"`
	PkgKind   string        `json:"package_manager,omitempty"`
	Flatpaks  int           `json:"flatpaks,omitempty"`
	Shell     string        `json:"shell,omitempty"`
	Desktop   string        `json:"desktop,omitempty"`
	Terminal  string        `json:"terminal,omitempty"`
	Displays  []string      `json:"displays,omitempty"`
	CPU       string        `json:"cpu,omitempty"`
	GPUs      []string      `json:"gpus,omitempty"`
	MemUsed   uint64        `json:"mem_used"`
	MemTotal  uint64        `json:"mem_total"`
	SwapUsed  uint64        `json:"swap_used"`
	SwapTotal uint64        `json:"swap_total"`
	Disks     []Disk        `json:"disks,omitempty"`
	// Battery is -1 when there is no battery.
	Battery       int     `json:"battery_percent"`
	BatteryState  string  `json:"battery_state,omitempty"`
	BatteryHealth float64 `json:"battery_health,omitempty"`
}

func collect() Info {
	osName, osID := osRelease()
	in := Info{
		User:     os.Getenv("USER"),
		Hostname: read("/proc/sys/kernel/hostname"),
		OS:       osName,
		OSID:     osID,
		Machine:  machine(),
		Kernel:   read("/proc/sys/kernel/osrelease"),
		Uptime:   uptime(),
		Shell:    base(os.Getenv("SHELL")),
		Desktop:  desktop(),
		Terminal: terminal(os.Getppid()),
		Displays: displays(),
		CPU:      cpu(),
		GPUs:     gpus(),
		Disks:    disks(),
		Battery:  -1,
	}
	in.Packages, in.PkgKind = packages()
	in.Flatpaks = flatpaks()
	in.MemUsed, in.MemTotal, in.SwapUsed, in.SwapTotal = memory()
	in.Battery, in.BatteryState, in.BatteryHealth = battery()
	return in
}

func base(s string) string {
	if s == "" {
		return ""
	}
	return filepath.Base(s)
}

func osRelease() (name, id string) {
	text := read("/etc/os-release")
	if text == "" {
		text = read("/usr/lib/os-release")
	}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "PRETTY_NAME":
			name = v
		case "ID":
			id = v
		}
	}
	if name == "" {
		name = "Linux"
	}
	return name, id
}

// machine is the product name from DMI, with the "MODEL_MODEL" suffix some vendors add removed.
func machine() string {
	name := read("/sys/devices/virtual/dmi/id/product_name")
	switch name {
	case "", "System Product Name", "To Be Filled By O.E.M.", "Default string":
		return ""
	}
	f := strings.Fields(name)
	last := f[len(f)-1]
	if a, b, ok := strings.Cut(last, "_"); ok && a == b {
		f[len(f)-1] = a
	}
	return strings.Join(f, " ")
}

func uptime() time.Duration {
	f := strings.Fields(read("/proc/uptime"))
	if len(f) == 0 {
		return 0
	}
	s, _ := strconv.ParseFloat(f[0], 64)
	return time.Duration(s * float64(time.Second))
}

func dirCount(path string) int {
	es, err := os.ReadDir(p(path))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func packages() (int, string) {
	if n := dirCount("/var/lib/pacman/local"); n > 0 {
		return n, "pacman"
	}
	f, err := os.Open(p("/var/lib/dpkg/status"))
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if sc.Text() == "Status: install ok installed" {
			n++
		}
	}
	return n, "dpkg"
}

func flatpaks() int {
	n := dirCount("/var/lib/flatpak/app")
	if home := os.Getenv("HOME"); home != "" {
		n += dirCount(filepath.Join(home, ".local/share/flatpak/app"))
	}
	return n
}

func desktop() string {
	d := os.Getenv("XDG_CURRENT_DESKTOP")
	if d == "" {
		return ""
	}
	if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "x11" {
		d += " (" + t + ")"
	}
	return d
}

var notTerminals = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "nu": true,
	"sincefetch": true, "go": true, "sudo": true, "su": true, "env": true, "timeout": true,
}

// terminal walks up the parents until it finds something that is not a shell.
func terminal(pid int) string {
	for i := 0; i < 8 && pid > 1; i++ {
		dir := "/proc/" + strconv.Itoa(pid)
		comm := read(dir + "/comm")
		if comm == "" {
			break
		}
		if !notTerminals[comm] {
			switch {
			case comm == "systemd" || comm == "init" || comm == "login":
				return ""
			case strings.HasPrefix(comm, "gnome-terminal"):
				return "gnome-terminal"
			case comm == "kitten":
				return "kitty"
			case strings.HasPrefix(comm, "tmux"):
				return "tmux"
			}
			return comm
		}
		stat := read(dir + "/stat")
		j := strings.LastIndexByte(stat, ')')
		if j < 0 {
			break
		}
		f := strings.Fields(stat[j+1:])
		if len(f) < 2 {
			break
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return ""
}

func displays() []string {
	paths, _ := filepath.Glob(p("/sys/class/drm/card*-*/status"))
	sort.Strings(paths)
	var out []string
	for _, st := range paths {
		dir := strings.TrimPrefix(filepath.Dir(st), strings.TrimSuffix(root, "/"))
		if read(dir+"/status") != "connected" {
			continue
		}
		mode, _, _ := strings.Cut(read(dir+"/modes"), "\n")
		if mode == "" {
			continue
		}
		_, name, _ := strings.Cut(filepath.Base(dir), "-")
		out = append(out, mode+" ("+name+")")
	}
	return out
}

func cpu() string {
	name, n := "", 0
	for _, line := range strings.Split(read("/proc/cpuinfo"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "processor":
			n++
		case "model name":
			name = strings.TrimSpace(v)
		}
	}
	if name == "" {
		return ""
	}
	if i := strings.Index(name, " w/ "); i > 0 {
		name = name[:i]
	}
	for _, junk := range []string{"(R)", "(TM)", "(tm)", " CPU", " Processor"} {
		name = strings.ReplaceAll(name, junk, "")
	}
	if i := strings.Index(name, " @ "); i > 0 {
		name = name[:i]
	}
	return strings.Join(strings.Fields(name), " ") + " (" + strconv.Itoa(n) + ")"
}

func gpus() []string {
	devs, _ := filepath.Glob(p("/sys/bus/pci/devices/*/class"))
	sort.Strings(devs)
	var out []string
	for _, c := range devs {
		dir := strings.TrimPrefix(filepath.Dir(c), strings.TrimSuffix(root, "/"))
		if !strings.HasPrefix(read(dir+"/class"), "0x03") {
			continue
		}
		vendor := strings.TrimPrefix(read(dir+"/vendor"), "0x")
		device := strings.TrimPrefix(read(dir+"/device"), "0x")
		rev := strings.TrimPrefix(read(dir+"/revision"), "0x")
		out = append(out, gpuName(vendor, device, rev))
	}
	return out
}

func gpuName(vendor, device, rev string) string {
	// AMD marketing names live in libdrm's table, keyed by device and revision.
	if vendor == "1002" {
		for _, line := range strings.Split(read("/usr/share/libdrm/amdgpu.ids"), "\n") {
			f := strings.Split(line, ",")
			if len(f) >= 3 && strings.EqualFold(strings.TrimSpace(f[0]), device) && strings.EqualFold(strings.TrimSpace(f[1]), rev) {
				return strings.TrimSpace(strings.Join(f[2:], ","))
			}
		}
	}
	short := map[string]string{"10de": "NVIDIA", "1002": "AMD", "8086": "Intel"}[vendor]
	name := pciName(vendor, device)
	if i, j := strings.Index(name, "["), strings.LastIndex(name, "]"); i >= 0 && j > i {
		name = name[i+1 : j]
	}
	if name == "" {
		name = vendor + ":" + device
	}
	if short != "" && !strings.HasPrefix(name, short) {
		name = short + " " + name
	}
	return name
}

func pciName(vendor, device string) string {
	for _, path := range []string{"/usr/share/hwdata/pci.ids", "/usr/share/misc/pci.ids"} {
		f, err := os.Open(p(path))
		if err != nil {
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		in := false
		for sc.Scan() {
			line := sc.Text()
			if line == "" || line[0] == '#' {
				continue
			}
			if line[0] != '\t' {
				in = strings.HasPrefix(line, vendor+" ")
				continue
			}
			if in && strings.HasPrefix(line, "\t"+device+" ") {
				return strings.TrimSpace(line[len(device)+1:])
			}
		}
	}
	return ""
}

func memory() (used, total, swapUsed, swapTotal uint64) {
	m := map[string]uint64{}
	for _, line := range strings.Split(read("/proc/meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			v, _ := strconv.ParseUint(f[1], 10, 64)
			m[strings.TrimSuffix(f[0], ":")] = v * 1024
		}
	}
	total = m["MemTotal"]
	if a := m["MemAvailable"]; a <= total {
		used = total - a
	}
	swapTotal = m["SwapTotal"]
	if free := m["SwapFree"]; free <= swapTotal {
		swapUsed = swapTotal - free
	}
	return
}

// statfs is replaced in tests.
var statfs = func(path string) (used, total uint64, ok bool) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil || s.Blocks == 0 {
		return 0, 0, false
	}
	bs := uint64(s.Bsize)
	return (s.Blocks - s.Bfree) * bs, s.Blocks * bs, true
}

var realFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "btrfs": true, "xfs": true, "f2fs": true, "zfs": true, "bcachefs": true, "ntfs3": true, "fuseblk": true}

func disks() []Disk {
	best := map[string]string{} // device -> shortest mount point
	for _, line := range strings.Split(read("/proc/mounts"), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !realFS[f[2]] {
			continue
		}
		dev, mnt := f[0], strings.ReplaceAll(f[1], `\040`, " ")
		if old, ok := best[dev]; !ok || len(mnt) < len(old) {
			best[dev] = mnt
		}
	}
	var out []Disk
	for dev, mnt := range best {
		if used, total, ok := statfs(mnt); ok {
			out = append(out, Disk{Mount: mnt, Device: dev, Used: used, Total: total})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

func battery() (percent int, state string, health float64) {
	percent = -1
	paths, _ := filepath.Glob(p("/sys/class/power_supply/*/type"))
	sort.Strings(paths)
	for _, t := range paths {
		dir := strings.TrimPrefix(filepath.Dir(t), strings.TrimSuffix(root, "/"))
		if read(dir+"/type") != "Battery" || read(dir+"/scope") == "Device" {
			continue
		}
		percent = int(readNum(dir + "/capacity"))
		state = read(dir + "/status")
		for _, kind := range []string{"energy", "charge"} {
			full, design := readNum(dir+"/"+kind+"_full"), readNum(dir+"/"+kind+"_full_design")
			if full > 0 && design > 0 {
				health = full / design * 100
				break
			}
		}
		return
	}
	return
}
