package serverstats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/sitecert"
	"github.com/petervlad/loadout/internal/vminfo"
)

type counters struct{ err error }

func (c counters) Counts(context.Context, time.Time) (registry.Counts, error) {
	return registry.Counts{Releases: 7}, c.err
}

func (c counters) AccountCounts(context.Context, time.Time) (account.Counts, error) {
	return account.Counts{Users: 3}, nil
}

type queue struct{}

func (queue) Stats() mail.QueueStats { return mail.QueueStats{Sent: 4, Failed: 1} }

// refused is a provider error as the page shows it: a plain sentence.
const refused = "Example Cloud refused the token."

type cloud struct{ err error }

func (cloud) Name() string { return "Example Cloud" }

func (c cloud) Summary(context.Context) (CloudSummary, error) {
	if c.err != nil {
		return CloudSummary{}, c.err
	}
	return CloudSummary{Facts: []CloudFact{{Label: "Name", Value: "loadout-registry"}}}, nil
}

func write(t *testing.T, path string, n int, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReport(t *testing.T) {
	data, backups := t.TempDir(), t.TempDir()
	write(t, filepath.Join(data, "registry.db"), 4096, time.Time{})
	write(t, filepath.Join(data, "registry.db-wal"), 1000, time.Time{})
	write(t, filepath.Join(data, "memory.db"), 2048, time.Time{})
	write(t, filepath.Join(data, ".loadout", "loadout.log"), 500, time.Time{})
	old := time.Date(2026, 9, 27, 3, 15, 0, 0, time.UTC)
	newest := time.Date(2026, 9, 28, 3, 15, 0, 0, time.UTC)
	write(t, filepath.Join(backups, "loadout-20260927T031500Z.tar.gz"), 300, old)
	write(t, filepath.Join(backups, "loadout-20260928T031500Z.tar.gz"), 200, newest)
	write(t, filepath.Join(backups, "notes.txt"), 50, time.Time{})

	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	c := New(Config{
		DataDir: data, DBFiles: []string{"registry.db", "memory.db"}, BackupDir: backups, Version: "v1.2.3",
		Started: now.Add(-3 * time.Hour), Now: func() time.Time { return now },
		Registry: counters{}, Accounts: counters{}, Mail: queue{}, Cloud: cloud{},
	})
	r := c.Report(t.Context())

	if r.Process.Version != "v1.2.3" || r.Process.Uptime != 3*time.Hour || r.Process.HeapBytes == 0 || r.Process.Goroutines == 0 {
		t.Errorf("Process = %+v", r.Process)
	}
	d := r.Disk
	if d.FSErr != "" || d.FSTotal == 0 || d.FSFree > d.FSTotal || d.UsedPercent() > 100 {
		t.Errorf("filesystem = %d free of %d (%q)", d.FSFree, d.FSTotal, d.FSErr)
	}
	if len(d.DBs) != 2 {
		t.Fatalf("databases = %+v, want registry.db and memory.db", d.DBs)
	}
	if f := d.DBs[0]; f.Name != "registry.db" || f.DBBytes != 4096 || f.WALBytes != 1000 || f.SHMBytes != 0 || f.Err != "" {
		t.Errorf("registry.db = %+v", f)
	}
	if f := d.DBs[1]; f.Name != "memory.db" || f.DBBytes != 2048 || f.Err != "" {
		t.Errorf("memory.db = %+v", f)
	}
	if d.DataBytes != 7644 || d.DataFiles != 4 || d.DataErr != "" {
		t.Errorf("data folder = %d bytes in %d files (%q), want 7644 in 4", d.DataBytes, d.DataFiles, d.DataErr)
	}
	if r.Counts.Registry.Releases != 7 || r.Counts.Accounts.Users != 3 || r.Counts.Err != "" {
		t.Errorf("Counts = %+v", r.Counts)
	}
	b := r.Backups
	if b.Count != 2 || b.Bytes != 500 || b.LatestName != "loadout-20260928T031500Z.tar.gz" || !b.Latest.Equal(newest) || b.LatestBytes != 200 || b.Err != "" {
		t.Errorf("Backups = %+v", b)
	}
	if !r.Mail.On || r.Mail.Stats.Sent != 4 {
		t.Errorf("Mail = %+v", r.Mail)
	}
	if !r.Cloud.Configured || r.Cloud.Name != "Example Cloud" || r.Cloud.Summary.Facts[0].Value != "loadout-registry" || r.Cloud.Err != "" {
		t.Errorf("Cloud = %+v", r.Cloud)
	}
}

// TestReportDegrades checks that each missing source only marks its own
// part.
func TestReportDegrades(t *testing.T) {
	data := t.TempDir()
	c := New(Config{
		DataDir: data, DBFiles: []string{"registry.db", "memory.db"}, BackupDir: filepath.Join(data, "no-such-folder"),
		Started: time.Now(), Registry: counters{err: errors.New("database is locked")}, Accounts: counters{},
		Cloud: cloud{err: errors.New(refused)},
	})
	r := c.Report(t.Context())
	if len(r.Disk.DBs) != 2 || r.Disk.DBs[0].Err != "It does not exist." || r.Disk.DBs[1].Err != "It does not exist." || r.Disk.FSErr != "" {
		t.Errorf("Disk = %+v, want only the database missing", r.Disk)
	}
	if r.Counts.Err == "" || strings.Contains(r.Counts.Err, "locked") || r.Counts.Accounts.Users != 3 {
		t.Errorf("Counts = %+v, want a plain reason and the account counts kept", r.Counts)
	}
	if r.Backups.Err != "It does not exist." {
		t.Errorf("Backups = %+v", r.Backups)
	}
	if r.Mail.On {
		t.Errorf("Mail = %+v, want off", r.Mail)
	}
	if !r.Cloud.Configured || !strings.Contains(r.Cloud.Err, "refused the token") {
		t.Errorf("Cloud = %+v", r.Cloud)
	}

	r = New(Config{DataDir: data, DBFiles: []string{"registry.db", "memory.db"}}).Report(t.Context())
	if !r.Backups.Off || r.Cloud.Configured {
		t.Errorf("without a backup folder or provider: Backups %+v, Cloud %+v", r.Backups, r.Cloud)
	}
}

func TestUnreadableBackups(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.Mkdir(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: a folder needs x to be removed
	r := New(Config{DataDir: t.TempDir(), DBFiles: []string{"registry.db", "memory.db"}, BackupDir: dir}).Report(t.Context())
	if r.Backups.Err != "The service may not read it." {
		t.Errorf("Backups.Err = %q", r.Backups.Err)
	}
}

func TestHost(t *testing.T) {
	stated := HostFacts{Provider: "Example Cloud", Region: "eu-1"}
	c := New(Config{
		Host: stated, PublicURL: "https://registry.example.com",
		Hostname: func() (string, error) { return "loadout-1", nil },
	})
	h := c.Report(t.Context()).Host
	if h.Stated != stated || h.Name != "loadout-1" || h.PublicURL != "https://registry.example.com" || h.NameErr != "" {
		t.Errorf("Host = %+v", h)
	}

	c = New(Config{Hostname: func() (string, error) { return "", errors.New("no") }})
	if h = c.Report(t.Context()).Host; h.Name != "" || h.NameErr == "" {
		t.Errorf("Host without a name = %+v", h)
	}
}

type memCounter struct{ err error }

func (m memCounter) Stats(context.Context, time.Time) (memory.Stats, error) {
	return memory.Stats{ValidTokens: 2, Spaces: []memory.SpaceStats{{Namespace: "lime", Enabled: true, Active: 3}}}, m.err
}

func TestReportMemory(t *testing.T) {
	data := t.TempDir()
	tests := []struct {
		name string
		mem  MemoryCounter
		want Memory
	}{
		{"not configured", nil, Memory{}},
		{"counted", memCounter{}, Memory{On: true, Stats: memory.Stats{ValidTokens: 2, Spaces: []memory.SpaceStats{{Namespace: "lime", Enabled: true, Active: 3}}}}},
		{"failed", memCounter{err: errors.New("boom /secret/path")}, Memory{On: true, Err: "Team memory could not be counted."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(Config{DataDir: data, Memory: tt.mem}).Report(t.Context()).Memory
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Report().Memory = %+v, want %+v", got, tt.want)
			}
		})
	}
}

type machine struct{}

func (machine) Read() vminfo.Machine {
	return vminfo.Machine{OS: vminfo.OS{Name: "Ubuntu 24.04.1 LTS"}}
}

func TestReportMachine(t *testing.T) {
	data := t.TempDir()
	if r := New(Config{DataDir: data}).Report(t.Context()); r.MachineOn {
		t.Errorf("without a reader: MachineOn = true, Machine = %+v", r.Machine)
	}
	r := New(Config{DataDir: data, Machine: machine{}}).Report(t.Context())
	if !r.MachineOn || r.Machine.OS.Name != "Ubuntu 24.04.1 LTS" {
		t.Errorf("Machine = %+v, on %v", r.Machine, r.MachineOn)
	}
}

type certs struct{}

func (certs) Check(context.Context) sitecert.Report {
	return sitecert.Report{Served: sitecert.Served{Issuer: "Example Issuing CA", Trusted: true}}
}

func TestReportCerts(t *testing.T) {
	data := t.TempDir()
	if r := New(Config{DataDir: data}).Report(t.Context()); r.CertsOn {
		t.Errorf("without a checker: CertsOn = true")
	}
	r := New(Config{DataDir: data, Certs: certs{}}).Report(t.Context())
	if !r.CertsOn || r.Certs.Served.Issuer != "Example Issuing CA" {
		t.Errorf("Certs = %+v, on %v", r.Certs, r.CertsOn)
	}
}
