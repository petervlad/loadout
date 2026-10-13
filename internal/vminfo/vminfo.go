// Package vminfo reads what an admin wants to know about the machine the
// registry runs on: operating system, kernel, CPUs, memory, load, uptime, a
// pending reboot and the last run of unattended upgrades. It reads small
// public files and two system calls, so it works inside the service's
// sandbox, where most of /proc is hidden. Each item fails on its own and
// leaves a plain reason, never a panic.
package vminfo

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Paths are relative to the root of the file system, as io/fs wants.
const (
	pathOSRelease  = "etc/os-release"
	pathMeminfo    = "proc/meminfo"
	pathCPUInfo    = "proc/cpuinfo"
	pathSelfCgroup = "proc/self/cgroup"
	pathCgroupRoot = "sys/fs/cgroup"
	pathHostFacts  = "usr/local/lib/loadout/host-facts"
	pathReboot     = "run/reboot-required"
	pathRebootPkgs = "run/reboot-required.pkgs"
	pathAptLocal   = "etc/apt/apt.conf.d/52unattended-upgrades-local"
	pathAptStamp   = "var/lib/apt/periodic/unattended-upgrades-stamp"
)

// Caps on how much of a file is read. A bigger file is refused with a reason.
const (
	capSmall   = 16 << 10
	capCPUInfo = 256 << 10
)

const (
	// MaxPackages is how many packages that want a reboot are listed.
	MaxPackages = 20
	// StaleUpdates is how long after the last run of unattended upgrades the
	// page warns.
	StaleUpdates = 3 * 24 * time.Hour
)

// Reasons the page shows when a thing is hidden or missing.
const (
	reasonHidden = "Hidden by the service's sandbox."
	reasonNoLine = "Not reported by this system."
)

// OS is item B1.
type OS struct {
	Name string // "Ubuntu 24.04.1 LTS"
	Err  string
}

// KernelInfo is item B2.
type KernelInfo struct {
	Release string // "6.8.0-45-generic"
	Machine string // "x86_64"
	Err     string
}

// String is "6.8.0-45-generic x86_64".
func (k KernelInfo) String() string { return Uname{Release: k.Release, Machine: k.Machine}.String() }

// CPU is item B3. Count is always known. Err says why Model is empty.
type CPU struct {
	Count int
	Model string
	Err   string
}

// Memory is item B4, in bytes. Available is only set when AvailableOK, and
// AvailableErr says why not. Err means the totals are not known either.
type Memory struct {
	Total        uint64
	Available    uint64
	AvailableOK  bool
	AvailableErr string
	SwapTotal    uint64
	SwapUsed     uint64
	Err          string
}

// Cgroup is item B5: the memory the service uses, its page cache included.
// It is left out of the page when On is false.
type Cgroup struct {
	On   bool
	Used uint64
}

// Load is item B6.
type Load struct {
	One, Five, Fifteen float64
	CPUs               int
	Warn               bool // the 5 minute load is above the CPU count
	Err                string
}

// Uptime is item B7.
type Uptime struct {
	Since time.Time
	For   time.Duration
	Err   string
}

// Reboot is item B8. Packages holds at most MaxPackages names and More counts
// the rest. AutoRebootAt is "04:30" when unattended upgrades reboot by
// themselves, else empty.
type Reboot struct {
	Pending      bool
	Packages     []string
	More         int
	AutoRebootAt string
	Err          string
}

// Updates is item B9: when unattended upgrades last ran. When Err is set, Err
// says why Last is unknown ("No record of unattended upgrades.").
type Updates struct {
	Last  time.Time
	Stale bool
	Err   string
}

// Machine is everything Read finds. It is a value that nobody changes after.
type Machine struct {
	OS      OS
	Kernel  KernelInfo
	CPU     CPU
	Memory  Memory
	Cgroup  Cgroup
	Load    Load
	Uptime  Uptime
	Reboot  Reboot
	Updates Updates
}

// Reader reads the machine. Use New: the zero value has no file system.
type Reader struct {
	fsys   fs.FS
	kernel Kernel
	now    func() time.Time
	cpus   int
}

// New returns a Reader over fsys (the root of the file system, so tests can
// give a folder or a MapFS) and k. A nil fsys finds no files, a nil k fails
// both system calls and a nil now is time.Now, so every item then says why it
// is missing.
func New(fsys fs.FS, k Kernel, now func() time.Time) *Reader {
	if fsys == nil {
		fsys = noFS{}
	}
	if k == nil {
		k = unsupported{}
	}
	if now == nil {
		now = time.Now
	}
	return &Reader{fsys: fsys, kernel: k, now: now, cpus: runtime.NumCPU()}
}

// Read looks at the machine now. It reads a few small files and makes two
// system calls, so it is cheap enough for every page load.
func (r *Reader) Read() Machine {
	si, siErr := r.kernel.Sysinfo()
	return Machine{
		OS:      r.readOS(),
		Kernel:  r.readKernel(),
		CPU:     r.readCPU(),
		Memory:  r.readMemory(si, siErr),
		Cgroup:  r.readCgroup(),
		Load:    r.readLoad(si, siErr),
		Uptime:  r.readUptime(si, siErr),
		Reboot:  r.readReboot(),
		Updates: r.readUpdates(),
	}
}

func (r *Reader) readOS() OS {
	data, err := r.readFile(pathOSRelease, capSmall)
	if err != nil {
		return OS{Err: fileReason(pathOSRelease, err)}
	}
	name, ok := cleanText(parseOSRelease(data))
	if !ok {
		return OS{Err: "/" + pathOSRelease + " has no usable PRETTY_NAME."}
	}
	return OS{Name: name}
}

func (r *Reader) readKernel() KernelInfo {
	u, err := r.kernel.Uname()
	if err != nil {
		return KernelInfo{Err: sysReason("uname", err)}
	}
	rel, ok1 := cleanText(u.Release)
	mach, ok2 := cleanText(u.Machine)
	if !ok1 || !ok2 {
		return KernelInfo{Err: "The kernel gave a release that cannot be shown."}
	}
	return KernelInfo{Release: rel, Machine: mach}
}

func (r *Reader) readCPU() CPU {
	cpu := CPU{Count: r.cpus}
	model, why := r.cpuModel()
	cpu.Model, cpu.Err = model, why
	return cpu
}

// cpuModel prefers the live /proc/cpuinfo. When the sandbox hides it, the
// deploy's host-facts file stands in for it, since the model of a VM does not
// change while it lives.
func (r *Reader) cpuModel() (model, why string) {
	data, err := r.readFile(pathCPUInfo, capCPUInfo)
	switch {
	case err == nil:
		if m, ok := cleanText(parseCPUModel(data)); ok {
			return m, ""
		}
		why = reasonNoLine
	case errors.Is(err, fs.ErrNotExist):
		why = reasonHidden
	default:
		why = fileReason(pathCPUInfo, err)
	}
	if m, ok := r.hostFactsModel(); ok {
		return m, ""
	}
	return "", why
}

func (r *Reader) hostFactsModel() (string, bool) {
	data, err := r.readFile(pathHostFacts, capSmall)
	if err != nil {
		return "", false
	}
	return cleanText(parseHostFacts(data))
}

// readMemory uses /proc/meminfo when it is there. Without it, sysinfo gives
// the total and swap, and "available" is left out with the reason, because
// sysinfo's free memory leaves out the file cache and would look alarming.
func (r *Reader) readMemory(si Sysinfo, siErr error) Memory {
	if data, err := r.readFile(pathMeminfo, capSmall); err == nil {
		if mi := parseMeminfo(data); mi.HasTotal {
			return memoryFromProc(mi)
		}
	}
	if siErr != nil {
		return Memory{Err: sysReason("sysinfo", siErr)}
	}
	m := Memory{
		Total:        si.TotalRAM,
		AvailableErr: reasonHidden,
		SwapTotal:    si.TotalSwap,
		SwapUsed:     subFloor(si.TotalSwap, si.FreeSwap),
	}
	return m
}

func memoryFromProc(mi meminfo) Memory {
	m := Memory{Total: mi.Total}
	if mi.HasAvailable {
		m.Available, m.AvailableOK = min(mi.Available, mi.Total), true
	} else {
		m.AvailableErr = reasonNoLine
	}
	if mi.HasSwap {
		m.SwapTotal = mi.SwapTotal
		m.SwapUsed = subFloor(mi.SwapTotal, mi.SwapFree)
	}
	return m
}

func subFloor(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

// readCgroup finds the service's own cgroup and its memory.current. Anything
// missing, version 1 included, leaves the item out.
func (r *Reader) readCgroup() Cgroup {
	data, err := r.readFile(pathSelfCgroup, capSmall)
	if err != nil {
		return Cgroup{}
	}
	cg, ok := parseCgroup(data)
	if !ok {
		return Cgroup{}
	}
	cur, err := r.readFile(path.Join(pathCgroupRoot, cg, "memory.current"), capSmall)
	if err != nil {
		return Cgroup{}
	}
	used, err := strconv.ParseUint(strings.TrimSpace(cur), 10, 64)
	if err != nil {
		return Cgroup{}
	}
	return Cgroup{On: true, Used: used}
}

func (r *Reader) readLoad(si Sysinfo, siErr error) Load {
	if siErr != nil {
		return Load{CPUs: r.cpus, Err: sysReason("sysinfo", siErr)}
	}
	return Load{
		One: si.Load[0], Five: si.Load[1], Fifteen: si.Load[2],
		CPUs: r.cpus,
		Warn: si.Load[1] > float64(r.cpus),
	}
}

func (r *Reader) readUptime(si Sysinfo, siErr error) Uptime {
	if siErr != nil {
		return Uptime{Err: sysReason("sysinfo", siErr)}
	}
	up := si.Uptime.Truncate(time.Second)
	return Uptime{Since: r.now().Add(-up).UTC(), For: up}
}

func (r *Reader) readReboot() Reboot {
	var rb Reboot
	rb.AutoRebootAt = r.autoReboot()
	if _, err := fs.Stat(r.fsys, pathReboot); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			rb.Err = fileReason(pathReboot, err)
		}
		return rb
	}
	rb.Pending = true
	data, err := r.readFile(pathRebootPkgs, capSmall)
	if err != nil {
		// A reboot without a package list is common, so only a real
		// failure is a reason.
		if !errors.Is(err, fs.ErrNotExist) {
			rb.Err = fileReason(pathRebootPkgs, err)
		}
		return rb
	}
	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		if !validPackage(line) {
			continue
		}
		if len(rb.Packages) < MaxPackages {
			rb.Packages = append(rb.Packages, line)
		} else {
			rb.More++
		}
	}
	return rb
}

// autoReboot is the time unattended upgrades reboot, or "" when they do not.
func (r *Reader) autoReboot() string {
	data, err := r.readFile(pathAptLocal, capSmall)
	if err != nil {
		return ""
	}
	conf := parseAptConf(data)
	if conf["Unattended-Upgrade::Automatic-Reboot"] != "true" {
		return ""
	}
	if at := conf["Unattended-Upgrade::Automatic-Reboot-Time"]; validClock(at) {
		return at
	}
	// Without a time apt reboots right after the upgrade.
	return "right after the upgrade"
}

func (r *Reader) readUpdates() Updates {
	info, err := fs.Stat(r.fsys, pathAptStamp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Updates{Err: "No record of unattended upgrades."}
		}
		return Updates{Err: fileReason(pathAptStamp, err)}
	}
	last := info.ModTime().UTC()
	return Updates{Last: last, Stale: r.now().Sub(last) > StaleUpdates}
}

// readFile reads the named file, refusing one longer than limit bytes.
func (r *Reader) readFile(name string, limit int64) (string, error) {
	f, err := r.fsys.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > limit {
		return "", tooLargeError{limit: limit}
	}
	return string(b), nil
}

// tooLargeError says a file is over its cap.
type tooLargeError struct{ limit int64 }

func (e tooLargeError) Error() string { return fmt.Sprintf("larger than %d KiB", e.limit>>10) }

// fileReason is the sentence the page shows for a file that cannot be used.
func fileReason(name string, err error) string {
	if tl, ok := errors.AsType[tooLargeError](err); ok {
		return fmt.Sprintf("/%s is larger than %d KiB, so it was not read.", name, tl.limit>>10)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "/" + name + " was not found."
	}
	if errors.Is(err, fs.ErrPermission) {
		return "/" + name + " is not readable by the service."
	}
	return "/" + name + " could not be read."
}

// sysReason is the sentence for a failed system call.
func sysReason(call string, err error) string {
	if errors.Is(err, errUnsupported) {
		return "Not available on this system."
	}
	return "The " + call + " system call failed."
}

// noFS is the file system of a Reader built without one.
type noFS struct{}

func (noFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
