package vminfo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// fakeKernel is a Kernel with fixed answers.
type fakeKernel struct {
	si    Sysinfo
	siErr error
	u     Uname
	uErr  error
}

func (f fakeKernel) Sysinfo() (Sysinfo, error) { return f.si, f.siErr }
func (f fakeKernel) Uname() (Uname, error)     { return f.u, f.uErr }

// vmKernel answers like a small VM: 3.8 GiB, 1 GiB swap with 128 MiB used.
func vmKernel() fakeKernel {
	return fakeKernel{
		si: Sysinfo{
			Uptime:    36*time.Hour + 30*time.Minute + 700*time.Millisecond,
			Load:      [3]float64{0.25, 0.5, 0.75},
			TotalRAM:  3961712 << 10,
			TotalSwap: 1 << 30,
			FreeSwap:  1<<30 - 128<<20,
		},
		u: Uname{Sysname: "Linux", Release: "6.8.0-45-generic", Machine: "x86_64"},
	}
}

// tree opens a testdata tree and a clock one hour after its apt stamp, since a
// checkout does not keep modification times.
func tree(t *testing.T, name string, k Kernel) (*Reader, time.Time) {
	t.Helper()
	fsys := os.DirFS("testdata/" + name)
	now := time.Now()
	if info, err := fs.Stat(fsys, pathAptStamp); err == nil {
		now = info.ModTime().Add(time.Hour)
	}
	r := New(fsys, k, func() time.Time { return now })
	r.cpus = 2
	return r, now
}

func TestReadFullProc(t *testing.T) {
	r, now := tree(t, "ubuntu2404", vmKernel())
	m := r.Read()

	if m.OS.Name != "Ubuntu 24.04.1 LTS" || m.OS.Err != "" {
		t.Errorf("OS = %+v", m.OS)
	}
	if got := m.Kernel.String(); got != "6.8.0-45-generic x86_64" {
		t.Errorf("Kernel = %q (%+v)", got, m.Kernel)
	}
	if m.CPU.Count != 2 || m.CPU.Model != "Intel Core Processor (Broadwell, IBRS)" || m.CPU.Err != "" {
		t.Errorf("CPU = %+v", m.CPU)
	}
	want := Memory{
		Total: 3961712 << 10, Available: 2870316 << 10, AvailableOK: true,
		SwapTotal: 1 << 30, SwapUsed: 128 << 20,
	}
	if m.Memory != want {
		t.Errorf("Memory = %+v, want %+v", m.Memory, want)
	}
	if m.Cgroup != (Cgroup{On: true, Used: 157286400}) {
		t.Errorf("Cgroup = %+v", m.Cgroup)
	}
	if m.Load.One != 0.25 || m.Load.Five != 0.5 || m.Load.Fifteen != 0.75 || m.Load.CPUs != 2 || m.Load.Warn {
		t.Errorf("Load = %+v", m.Load)
	}
	if m.Uptime.For != 36*time.Hour+30*time.Minute || !m.Uptime.Since.Equal(now.Add(-m.Uptime.For)) {
		t.Errorf("Uptime = %+v", m.Uptime)
	}
	if m.Reboot.Pending || m.Reboot.AutoRebootAt != "04:30" || m.Reboot.Err != "" {
		t.Errorf("Reboot = %+v", m.Reboot)
	}
	if m.Updates.Err != "" || m.Updates.Stale || m.Updates.Last.IsZero() {
		t.Errorf("Updates = %+v", m.Updates)
	}
}

func TestReadSandboxed(t *testing.T) {
	r, _ := tree(t, "sandboxed", vmKernel())
	m := r.Read()

	if m.CPU.Model != "Intel Core Processor (Broadwell, IBRS)" || m.CPU.Err != "" {
		t.Errorf("model should come from host-facts, got %+v", m.CPU)
	}
	if m.Memory.Total != 3961712<<10 || m.Memory.SwapTotal != 1<<30 || m.Memory.SwapUsed != 128<<20 {
		t.Errorf("total and swap should come from sysinfo, got %+v", m.Memory)
	}
	if m.Memory.AvailableOK || m.Memory.Available != 0 || m.Memory.AvailableErr != reasonHidden {
		t.Errorf("available memory should say hidden, got %+v", m.Memory)
	}
	if !m.Cgroup.On {
		t.Errorf("cgroup should be readable in the sandbox, got %+v", m.Cgroup)
	}
}

func TestReadSandboxedWithoutHostFacts(t *testing.T) {
	r, _ := tree(t, "sandboxed-nofacts", vmKernel())
	m := r.Read()

	if m.CPU.Model != "" || m.CPU.Err != reasonHidden || m.CPU.Count != 2 {
		t.Errorf("CPU = %+v, want the count and a hidden model", m.CPU)
	}
	if m.Cgroup.On {
		t.Errorf("cgroup v1 should leave the item out, got %+v", m.Cgroup)
	}
	if m.Updates.Err != "No record of unattended upgrades." {
		t.Errorf("Updates = %+v", m.Updates)
	}
	if m.Reboot.AutoRebootAt != "" || m.Reboot.Pending {
		t.Errorf("Reboot = %+v", m.Reboot)
	}
}

func TestRebootListsTwentyOfThirty(t *testing.T) {
	r, _ := tree(t, "reboot", vmKernel())
	rb := r.Read().Reboot
	if !rb.Pending || len(rb.Packages) != MaxPackages || rb.More != 10 || rb.Err != "" {
		t.Fatalf("Reboot = pending %v, %d packages, %d more, err %q", rb.Pending, len(rb.Packages), rb.More, rb.Err)
	}
	if rb.Packages[0] != "linux-image-6.8.0-01-generic" || rb.Packages[19] != "linux-image-6.8.0-20-generic" {
		t.Errorf("Packages = %v", rb.Packages)
	}
}

func TestRebootWithoutPackageList(t *testing.T) {
	fsys := fstest.MapFS{"run/reboot-required": {}}
	rb := New(fsys, vmKernel(), nil).Read().Reboot
	if !rb.Pending || len(rb.Packages) != 0 || rb.Err != "" {
		t.Errorf("Reboot = %+v", rb)
	}
}

func TestRebootDropsOddPackageNames(t *testing.T) {
	fsys := fstest.MapFS{
		"run/reboot-required":      {},
		"run/reboot-required.pkgs": {Data: []byte("libc6\n<script>alert(1)</script>\n\nopenssl\n")},
	}
	rb := New(fsys, vmKernel(), nil).Read().Reboot
	if got := strings.Join(rb.Packages, ","); got != "libc6,openssl" || rb.More != 0 {
		t.Errorf("Packages = %q, more %d", got, rb.More)
	}
}

func TestUpdatesStamp(t *testing.T) {
	stamp := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		now   time.Time
		stale bool
	}{
		{"one day old", stamp.Add(24 * time.Hour), false},
		{"exactly three days", stamp.Add(StaleUpdates), false},
		{"four days old", stamp.Add(4 * 24 * time.Hour), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{pathAptStamp: {ModTime: stamp}}
			u := New(fsys, vmKernel(), func() time.Time { return tc.now }).Read().Updates
			if u.Stale != tc.stale || !u.Last.Equal(stamp) || u.Err != "" {
				t.Errorf("Updates = %+v, want stale %v", u, tc.stale)
			}
		})
	}
}

func TestAutoReboot(t *testing.T) {
	tests := []struct{ name, conf, want string }{
		{"on with a time", "Unattended-Upgrade::Automatic-Reboot \"true\";\nUnattended-Upgrade::Automatic-Reboot-Time \"03:15\";\n", "03:15"},
		{"on without a time", "Unattended-Upgrade::Automatic-Reboot \"true\";\n", "right after the upgrade"},
		{"on with a bad time", "Unattended-Upgrade::Automatic-Reboot \"true\";\nUnattended-Upgrade::Automatic-Reboot-Time \"25:99\";\n", "right after the upgrade"},
		{"off", "Unattended-Upgrade::Automatic-Reboot \"false\";\n", ""},
		{"commented out", "// Unattended-Upgrade::Automatic-Reboot \"true\";\n", ""},
		{"last line wins", "Unattended-Upgrade::Automatic-Reboot \"true\";\nUnattended-Upgrade::Automatic-Reboot \"false\";\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{pathAptLocal: {Data: []byte(tc.conf)}}
			if got := New(fsys, vmKernel(), nil).Read().Reboot.AutoRebootAt; got != tc.want {
				t.Errorf("AutoRebootAt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadWarnsAboveCPUCount(t *testing.T) {
	tests := []struct {
		five float64
		warn bool
	}{{1.9, false}, {2.0, false}, {2.01, true}, {7, true}}
	for _, tc := range tests {
		k := vmKernel()
		k.si.Load[1] = tc.five
		r := New(nil, k, nil)
		r.cpus = 2
		if got := r.Read().Load.Warn; got != tc.warn {
			t.Errorf("5 minute load %v: Warn = %v, want %v", tc.five, got, tc.warn)
		}
	}
}

func TestMeminfoWinsOverSysinfoAndClampsAvailable(t *testing.T) {
	fsys := fstest.MapFS{pathMeminfo: {Data: []byte(
		"MemTotal: 1000 kB\nMemAvailable: 5000 kB\nSwapTotal: 100 kB\nSwapFree: 400 kB\n")}}
	m := New(fsys, vmKernel(), nil).Read().Memory
	if m.Total != 1000<<10 || m.Available != 1000<<10 || !m.AvailableOK {
		t.Errorf("Memory = %+v, want available clamped to the total", m)
	}
	if m.SwapTotal != 100<<10 || m.SwapUsed != 0 {
		t.Errorf("swap = %d total %d used, want a free count above the total to read as unused", m.SwapTotal, m.SwapUsed)
	}
}

func TestMeminfoWithoutAvailable(t *testing.T) {
	fsys := fstest.MapFS{pathMeminfo: {Data: []byte("MemTotal: 2048 kB\n")}}
	m := New(fsys, vmKernel(), nil).Read().Memory
	if m.Total != 2048<<10 || m.AvailableOK || m.AvailableErr != reasonNoLine {
		t.Errorf("Memory = %+v", m)
	}
}

func TestBrokenMeminfoFallsBackToSysinfo(t *testing.T) {
	fsys := fstest.MapFS{pathMeminfo: {Data: []byte("garbage\n")}}
	m := New(fsys, vmKernel(), nil).Read().Memory
	if m.Total != 3961712<<10 || m.AvailableErr != reasonHidden {
		t.Errorf("Memory = %+v", m)
	}
}

// TestFailuresAreReasons: with nothing readable and a kernel that errors,
// every item says why and nothing panics.
func TestFailuresAreReasons(t *testing.T) {
	boom := errors.New("boom")
	for name, r := range map[string]*Reader{
		"failing kernel, no files": New(fstest.MapFS{}, fakeKernel{siErr: boom, uErr: boom}, nil),
		"no kernel, no files":      New(nil, nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			m := r.Read()
			for item, reason := range map[string]string{
				"OS": m.OS.Err, "Kernel": m.Kernel.Err, "CPU": m.CPU.Err, "Memory": m.Memory.Err,
				"Load": m.Load.Err, "Uptime": m.Uptime.Err, "Updates": m.Updates.Err,
			} {
				if reason == "" || !strings.HasSuffix(reason, ".") {
					t.Errorf("%s reason = %q, want a sentence", item, reason)
				}
			}
			if m.Cgroup.On || m.Reboot.Pending || m.CPU.Count < 1 {
				t.Errorf("Cgroup %+v, Reboot %+v, CPU %+v", m.Cgroup, m.Reboot, m.CPU)
			}
		})
	}
}

func TestKernelFailureKeepsFileItems(t *testing.T) {
	r, _ := tree(t, "ubuntu2404", fakeKernel{siErr: errors.New("x"), uErr: errors.New("y")})
	m := r.Read()
	if m.OS.Name == "" || !m.Memory.AvailableOK || m.Memory.Total == 0 {
		t.Errorf("file items should survive a failing kernel: OS %+v, Memory %+v", m.OS, m.Memory)
	}
	if m.Kernel.Err != "The uname system call failed." || m.Load.Err != "The sysinfo system call failed." {
		t.Errorf("Kernel %+v, Load %+v", m.Kernel, m.Load)
	}
}

// TestCaps: a file over its cap is refused with a reason and not parsed.
func TestCaps(t *testing.T) {
	pad := func(prefix string, n int) []byte {
		return append([]byte(prefix), make([]byte, n)...)
	}
	fsys := fstest.MapFS{
		pathOSRelease: {Data: pad("PRETTY_NAME=\"Ubuntu\"\n", capSmall)},
		pathCPUInfo:   {Data: pad("model name\t: Big CPU\n", capCPUInfo)},
		pathHostFacts: {Data: pad("cpu_model=Facts CPU\n", capSmall)},
		pathMeminfo:   {Data: pad("MemTotal: 1 kB\n", capSmall)},
	}
	m := New(fsys, vmKernel(), nil).Read()
	if m.OS.Name != "" || !strings.Contains(m.OS.Err, "larger than 16 KiB") {
		t.Errorf("OS = %+v", m.OS)
	}
	if m.CPU.Model != "" || !strings.Contains(m.CPU.Err, "larger than 256 KiB") {
		t.Errorf("CPU = %+v: an oversized cpuinfo and host-facts give no model", m.CPU)
	}
	if m.Memory.Total != 3961712<<10 {
		t.Errorf("an oversized meminfo should fall back to sysinfo, got %+v", m.Memory)
	}
}

// TestFileJustUnderTheCap: the cap is a limit, not a rejection of big files.
func TestFileAtTheCap(t *testing.T) {
	data := []byte("PRETTY_NAME=\"Ubuntu\"\n")
	data = append(data, []byte(strings.Repeat("#", capSmall-len(data)))...)
	if len(data) != capSmall {
		t.Fatal("test data has the wrong size")
	}
	m := New(fstest.MapFS{pathOSRelease: {Data: data}}, nil, nil).Read()
	if m.OS.Name != "Ubuntu" {
		t.Errorf("OS = %+v", m.OS)
	}
}

func TestHostFactsReadsOnlyTheModelKey(t *testing.T) {
	fsys := fstest.MapFS{pathHostFacts: {Data: []byte("secret=hunter2\ncpu_model=Facts CPU\nother=x\n")}}
	m := New(fsys, vmKernel(), nil).Read()
	if m.CPU.Model != "Facts CPU" {
		t.Errorf("CPU = %+v", m.CPU)
	}
	if got := fmt.Sprintf("%+v", m); strings.Contains(got, "hunter2") {
		t.Errorf("another key of host-facts reached the machine: %s", got)
	}
}

func TestOddTextIsRefused(t *testing.T) {
	long := strings.Repeat("a", maxText+1)
	tests := []struct{ name, file, content string }{
		{"control character", pathOSRelease, "PRETTY_NAME=\"Ub\x1b[31muntu\"\n"},
		{"too long", pathOSRelease, "PRETTY_NAME=\"" + long + "\"\n"},
		{"empty", pathOSRelease, "PRETTY_NAME=\"\"\n"},
		{"missing key", pathOSRelease, "NAME=Ubuntu\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(fstest.MapFS{tc.file: {Data: []byte(tc.content)}}, nil, nil).Read()
			if m.OS.Name != "" || m.OS.Err == "" {
				t.Errorf("OS = %+v, want a reason and no name", m.OS)
			}
		})
	}
}

func TestParseOSRelease(t *testing.T) {
	tests := []struct{ in, want string }{
		{"PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n", "Ubuntu 24.04.1 LTS"},
		{"PRETTY_NAME='Debian GNU/Linux 12'\n", "Debian GNU/Linux 12"},
		{"PRETTY_NAME=Arch\n", "Arch"},
		{"NAME=x\nPRETTY_NAME=\"A \\\"quoted\\\" name\"\n", `A "quoted" name`},
		{"  PRETTY_NAME = nope\n", ""},
		{"PRETTY_NAME=\"unterminated\n", "\"unterminated"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := parseOSRelease(tc.in); got != tc.want {
			t.Errorf("parseOSRelease(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseMeminfo(t *testing.T) {
	m := parseMeminfo("MemTotal:  100 kB\nMemAvailable: 40 kB\nSwapTotal: 8 kB\nSwapFree: 6 kB\nHugePages_Total: 0\nBad: x kB\n")
	want := meminfo{
		Total: 100 << 10, Available: 40 << 10, SwapTotal: 8 << 10, SwapFree: 6 << 10,
		HasTotal: true, HasAvailable: true, HasSwap: true,
	}
	if m != want {
		t.Errorf("parseMeminfo = %+v, want %+v", m, want)
	}
	if got := parseMeminfo("MemTotal: 100 MB\nMemTotal: -5 kB\n"); got.HasTotal {
		t.Errorf("a wrong unit or a negative number should be skipped, got %+v", got)
	}
}

func TestParseCPUModel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"processor\t: 0\nmodel name\t: Intel(R) Xeon(R)\n", "Intel(R) Xeon(R)"},
		{"processor\t: 0\nCPU part\t: 0xd0c\n", ""},
		{"model name: A: B\n", "A: B"},
	}
	for _, tc := range tests {
		if got := parseCPUModel(tc.in); got != tc.want {
			t.Errorf("parseCPUModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseCgroup(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0::/system.slice/loadout.service\n", "/system.slice/loadout.service", true},
		{"0::/\n", "/", true},
		{"12:memory:/a\n1:name=systemd:/a\n", "", false},
		{"0::/../../etc\n", "", false},
		{"0::relative\n", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		got, ok := parseCgroup(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseCgroup(%q) = %q, %v, want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCgroupAtTheRoot(t *testing.T) {
	fsys := fstest.MapFS{
		pathSelfCgroup:                     {Data: []byte("0::/\n")},
		pathCgroupRoot + "/memory.current": {Data: []byte("42\n")},
	}
	if cg := New(fsys, nil, nil).Read().Cgroup; cg != (Cgroup{On: true, Used: 42}) {
		t.Errorf("Cgroup = %+v", cg)
	}
}

func TestCgroupBadNumberLeavesItemOut(t *testing.T) {
	fsys := fstest.MapFS{
		pathSelfCgroup:                     {Data: []byte("0::/\n")},
		pathCgroupRoot + "/memory.current": {Data: []byte("max\n")},
	}
	if cg := New(fsys, nil, nil).Read().Cgroup; cg.On {
		t.Errorf("Cgroup = %+v", cg)
	}
}

func TestValidClockAndPackage(t *testing.T) {
	for in, want := range map[string]bool{"04:30": true, "00:00": true, "23:59": true, "24:00": false, "4:30": false, "04:60": false, "ab:cd": false, "": false} {
		if got := validClock(in); got != want {
			t.Errorf("validClock(%q) = %v", in, got)
		}
	}
	for in, want := range map[string]bool{"libc6": true, "g++-13": true, "libstdc++6:amd64": true, "a b": false, "": false, "x;y": false, strings.Repeat("a", 101): false} {
		if got := validPackage(in); got != want {
			t.Errorf("validPackage(%q) = %v", in, got)
		}
	}
}

func TestRealKernelNeverPanics(t *testing.T) {
	m := New(os.DirFS("testdata/ubuntu2404"), System(), nil).Read()
	if m.Kernel.Err == "" && m.Kernel.Release == "" {
		t.Errorf("Kernel = %+v, want a release or a reason", m.Kernel)
	}
	if m.Load.Err == "" && m.Uptime.For <= 0 {
		t.Errorf("Uptime = %+v, want a value or a reason", m.Uptime)
	}
}
