package serverstats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/registry"
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
	dir := t.TempDir()
	rel := filepath.Join(dir, "os-release")
	if err := os.WriteFile(rel, []byte("NAME=Ubuntu\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stated := HostFacts{Provider: "Example Cloud", Region: "eu-1"}
	c := New(Config{
		Host: stated, PublicURL: "https://registry.example.com", OSRelease: rel,
		Hostname: func() (string, error) { return "loadout-1", nil },
	})
	h := c.Report(t.Context()).Host
	if h.Stated != stated || h.Name != "loadout-1" || h.OS != "Ubuntu 24.04.1 LTS" || h.PublicURL != "https://registry.example.com" || h.CPUs < 1 || h.Arch == "" {
		t.Errorf("Host = %+v", h)
	}
	if h.NameErr != "" || h.OSErr != "" {
		t.Errorf("Host errors = %q, %q, want none", h.NameErr, h.OSErr)
	}

	// Each fact fails on its own.
	c = New(Config{
		OSRelease: filepath.Join(dir, "missing"),
		Hostname:  func() (string, error) { return "", errors.New("no") },
	})
	h = c.Report(t.Context()).Host
	if h.Name != "" || h.NameErr == "" || h.OS != "" || h.OSErr == "" || h.CPUs < 1 {
		t.Errorf("Host without facts = %+v", h)
	}

	// A file over the limit is read only in part.
	big := filepath.Join(dir, "big")
	content := strings.Repeat("# filler\n", maxOSRelease/8) + "PRETTY_NAME=late\n"
	if err := os.WriteFile(big, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h = New(Config{OSRelease: big}).Report(t.Context()).Host
	if h.OS != "" || h.OSErr == "" {
		t.Errorf("OS from a file over the limit = %q (%q), want none", h.OS, h.OSErr)
	}
}

func TestPrettyName(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"quoted", "PRETTY_NAME=\"Debian GNU/Linux 12\"\n", "Debian GNU/Linux 12"},
		{"single quoted", "PRETTY_NAME='Alpine'\n", "Alpine"},
		{"unquoted", "PRETTY_NAME=Arch\n", "Arch"},
		{"fallback", "NAME=\"Fedora\"\nVERSION_ID=40\n", "Fedora 40"},
		{"name only", "NAME=Fedora\n", "Fedora"},
		{"comment", "# PRETTY_NAME=no\nNAME=Yes\n", "Yes"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := prettyName(strings.NewReader(tc.in)); got != tc.want {
				t.Errorf("prettyName() = %q, want %q", got, tc.want)
			}
		})
	}
}
