package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDeployWritesHostFacts: the service cannot read /proc/cpuinfo, so
// deploy.sh writes the CPU model into a root-owned file the server page
// reads. The block is run here against a fake cpuinfo and folder.
func TestDeployWritesHostFacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("deploy.sh is a POSIX shell script")
	}
	script, err := os.ReadFile("deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	var block []string
	in := false
	for line := range strings.Lines(string(script)) {
		switch {
		case strings.HasPrefix(line, "# host-facts:begin"):
			in = true
		case strings.HasPrefix(line, "# host-facts:end"):
			in = false
		case in:
			block = append(block, line)
		}
	}
	if len(block) == 0 {
		t.Fatal("deploy.sh has no host-facts block")
	}
	tests := []struct{ name, cpuinfo, want string }{
		{
			"first model only",
			"processor\t: 0\nmodel name\t: Intel Core Processor (Broadwell, IBRS)\n\nprocessor\t: 1\nmodel name\t: Other\n",
			"cpu_model=Intel Core Processor (Broadwell, IBRS)\n",
		},
		{"control characters dropped", "model name\t: Intel\x01 CPU\n", "cpu_model=Intel CPU\n"},
		{"no model line", "processor\t: 0\nCPU part\t: 0xd0c\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cpuinfo := filepath.Join(dir, "cpuinfo")
			if err := os.WriteFile(cpuinfo, []byte(tc.cpuinfo), 0o644); err != nil { //nolint:gosec // G306: a world-readable stand-in for /proc/cpuinfo
				t.Fatal(err)
			}
			lib := filepath.Join(dir, "lib")
			if err := os.Mkdir(lib, 0o755); err != nil { //nolint:gosec // G301: stands in for /usr/local/lib/loadout
				t.Fatal(err)
			}
			body := strings.NewReplacer("/proc/cpuinfo", cpuinfo, "/usr/local/lib/loadout", lib).Replace(strings.Join(block, ""))
			if out, err := exec.CommandContext(t.Context(), "sh", "-eu", "-c", body).CombinedOutput(); err != nil {
				t.Fatalf("host-facts block failed: %v\n%s", err, out)
			}
			path := filepath.Join(lib, "host-facts")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("host-facts = %q, want %q", got, tc.want)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Errorf("mode = %v, want 0644", info.Mode().Perm())
			}
			if _, err := os.Stat(path + ".new"); err == nil {
				t.Error("the temporary host-facts.new was left behind")
			}
		})
	}
}

// TestUnitSaysWhatProcSubsetHides: the comment above ProcSubset=pid names the
// file the server page reads in place of /proc/cpuinfo.
func TestUnitSaysWhatProcSubsetHides(t *testing.T) {
	unit, err := os.ReadFile("loadout.service")
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	before, _, found := strings.Cut(text, "\nProcSubset=pid")
	if !found {
		t.Fatal("loadout.service has no ProcSubset=pid")
	}
	comment := before[strings.LastIndex(before, "\nProtectProc=")+1:]
	if !strings.Contains(comment, "host-facts") {
		t.Error("the comment above ProcSubset=pid should name host-facts")
	}
}

// TestUnitNamesTheAddressTheCertificateCheckUses: the comment above the
// address families says what an IPAddressDeny would have to allow.
func TestUnitNamesTheAddressTheCertificateCheckUses(t *testing.T) {
	unit, err := os.ReadFile("loadout.service")
	if err != nil {
		t.Fatal(err)
	}
	before, _, found := strings.Cut(string(unit), "\nRestrictAddressFamilies=")
	if !found {
		t.Fatal("loadout.service has no RestrictAddressFamilies=")
	}
	comment := before[strings.LastIndex(before, "\nProcSubset=")+1:]
	if !strings.Contains(comment, "127.0.0.1") || !strings.Contains(comment, "IPAddressAllow") {
		t.Error("the comment above RestrictAddressFamilies= should name 127.0.0.1 and IPAddressAllow")
	}
}
