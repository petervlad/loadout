package serverstats

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// makeArchive writes a tar.gz holding the named files, as loadout-backup.sh
// does (names start with "./").
func makeArchive(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		body := []byte("data of " + n)
		if err := tw.WriteHeader(&tar.Header{Name: "./" + n, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// backupSite is a data folder with both databases and a backup folder.
func backupSite(t *testing.T) (c *Collector, data, dest string) {
	t.Helper()
	data, dest = t.TempDir(), t.TempDir()
	write(t, filepath.Join(data, "registry.db"), 10, time.Time{})
	write(t, filepath.Join(data, "memory.db"), 10, time.Time{})
	c = New(Config{DataDir: data, DBFiles: []string{"registry.db", "memory.db"}, BackupDir: dest})
	return c, data, dest
}

func TestLastRun(t *testing.T) {
	good := "finished=2026-10-09T03:15:42Z\nresult=ok\nexit=0\narchive=loadout-20261009T031500Z.tar.gz\n"
	tests := []struct {
		name string
		file *string
		want LastRun
	}{
		{"missing", nil, LastRun{Missing: true}},
		{"ok", &good, LastRun{Finished: time.Date(2026, 10, 9, 3, 15, 42, 0, time.UTC), OK: true, Archive: "loadout-20261009T031500Z.tar.gz"}},
		{"failed", new("finished=2026-10-09T03:15:42Z\nresult=failed\nexit=1\narchive=\n"), LastRun{Finished: time.Date(2026, 10, 9, 3, 15, 42, 0, time.UTC), Exit: 1}},
		{"other keys are ignored", new(good + "token=hunter2\nnote\n"), LastRun{Finished: time.Date(2026, 10, 9, 3, 15, 42, 0, time.UTC), OK: true, Archive: "loadout-20261009T031500Z.tar.gz"}},
		{"too large", new(good + strings.Repeat("x", 5000)), LastRun{Err: "The status file is larger than 4 KiB."}},
		{"archive outside the frame", new("finished=2026-10-09T03:15:42Z\nresult=ok\nexit=0\narchive=../../etc/passwd\n"), LastRun{Err: "The status file names a file that is not one of the archives."}},
		{"archive with the wrong ending", new("finished=2026-10-09T03:15:42Z\nresult=ok\nexit=0\narchive=loadout-1.zip\n"), LastRun{Err: "The status file names a file that is not one of the archives."}},
		{"bad time", new("finished=yesterday\nresult=ok\nexit=0\n"), LastRun{Err: "The status file has a time that cannot be read."}},
		{"bad result", new("finished=2026-10-09T03:15:42Z\nresult=fine\nexit=0\n"), LastRun{Err: "The status file has a result that cannot be read."}},
		{"bad exit", new("finished=2026-10-09T03:15:42Z\nresult=ok\nexit=-3\n"), LastRun{Err: "The status file has an exit code that cannot be read."}},
		{"incomplete", new("result=ok\n"), LastRun{Err: "The status file is incomplete."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != nil {
				if err := os.WriteFile(filepath.Join(dir, lastRunFile), []byte(*tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := readLastRun(dir); got != tt.want {
				t.Errorf("readLastRun = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLastRunUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lastRunFile), []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if got := readLastRun(dir); got.Err != "The service may not read it." {
		t.Errorf("readLastRun = %+v, want a plain reason", got)
	}
}

func TestBackupsReportLastRunContentsAndCopies(t *testing.T) {
	c, _, dest := backupSite(t)
	now := time.Now()
	makeArchive(t, filepath.Join(dest, "loadout-20261008T031500Z.tar.gz"), "registry.db", "memory.db", "notes.txt")
	write(t, filepath.Join(dest, "before-delete-lime-x-20261007T000000Z.db"), 2048, now)
	write(t, filepath.Join(dest, "before-delete-lime-y-20261008T000000Z.db"), 1024, now)
	// Neither the status file nor a stray file is an archive or a copy.
	if err := os.WriteFile(filepath.Join(dest, lastRunFile), []byte("finished=2026-10-08T03:15:42Z\nresult=ok\nexit=0\narchive=loadout-20261008T031500Z.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, "notes.txt"), 5, now)

	c.Report(t.Context())
	waitFor(t, func() bool { return !c.Report(t.Context()).Backups.Contents.Checking })
	b := c.Report(t.Context()).Backups
	if b.Count != 1 || b.Copies != 2 || b.CopiesBytes != 3072 {
		t.Errorf("Count %d, Copies %d, CopiesBytes %d, want 1, 2, 3072", b.Count, b.Copies, b.CopiesBytes)
	}
	if !b.Last.OK || b.Last.Archive != "loadout-20261008T031500Z.tar.gz" {
		t.Errorf("Last = %+v", b.Last)
	}
	if !b.Contents.Checked || len(b.Contents.Missing) != 0 || b.Contents.Err != "" {
		t.Errorf("Contents = %+v, want both databases found", b.Contents)
	}
}

func TestBackupContents(t *testing.T) {
	tests := []struct {
		name  string
		names []string // the archive's files; nil writes a bad file instead
		bad   func(path string) error
		prep  func(data string) error
		want  Contents
	}{
		{name: "both", names: []string{"registry.db", "memory.db"}, want: Contents{Checked: true}},
		{name: "without memory.db", names: []string{"registry.db", "other"}, want: Contents{Checked: true, Missing: []string{"memory.db"}}},
		{name: "without either", names: []string{"other"}, want: Contents{Checked: true, Missing: []string{"registry.db", "memory.db"}}},
		{
			name: "memory.db was never created", names: []string{"registry.db"},
			prep: func(data string) error { return os.Remove(filepath.Join(data, "memory.db")) },
			want: Contents{Checked: true},
		},
		{
			name: "not gzip", bad: func(p string) error { return os.WriteFile(p, []byte("this is not gzip"), 0o600) },
			want: Contents{Err: "It is not a complete gzip file."},
		},
		{
			name: "truncated", names: []string{"registry.db", "memory.db"},
			bad: func(p string) error {
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				return os.WriteFile(p, data[:len(data)/2], 0o600) //nolint:gosec // G703: a path made by this test
			},
			want: Contents{Err: "It ends early."},
		},
		{
			name: "too large to list", bad: func(p string) error { return os.Truncate(p, 600<<20) },
			want: Contents{Err: "It is too large to list here."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, data, dest := backupSite(t)
			path := filepath.Join(dest, "loadout-20261008T031500Z.tar.gz")
			if tt.names != nil {
				makeArchive(t, path, tt.names...)
			}
			if tt.bad != nil {
				if tt.names == nil {
					write(t, path, 1, time.Time{})
				}
				if err := tt.bad(path); err != nil {
					t.Fatal(err)
				}
			}
			if tt.prep != nil {
				if err := tt.prep(data); err != nil {
					t.Fatal(err)
				}
			}
			c.Report(t.Context())
			waitFor(t, func() bool { return !c.Report(t.Context()).Backups.Contents.Checking })
			got := c.Report(t.Context()).Backups.Contents
			if got.Checked != tt.want.Checked || got.Err != tt.want.Err || !slices.Equal(got.Missing, tt.want.Missing) {
				t.Errorf("Contents = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestBackupContentsReadOncePerArchive: the listing is a full gunzip, so the
// same archive is read once, and a new one is read again.
func TestBackupContentsReadOncePerArchive(t *testing.T) {
	c, _, dest := backupSite(t)
	makeArchive(t, filepath.Join(dest, "loadout-20261008T031500Z.tar.gz"), "registry.db", "memory.db")
	for range 3 {
		c.Report(t.Context())
	}
	waitFor(t, func() bool { return c.Report(t.Context()).Backups.Contents.Checked })
	if c.list.reads != 1 {
		t.Errorf("the archive was opened %d times, want 1", c.list.reads)
	}
	makeArchive(t, filepath.Join(dest, "loadout-20261009T031500Z.tar.gz"), "registry.db")
	c.Report(t.Context())
	waitFor(t, func() bool { return c.Report(t.Context()).Backups.Contents.Checked })
	b := c.Report(t.Context()).Backups
	if c.list.reads != 2 || !slices.Equal(b.Contents.Missing, []string{"memory.db"}) {
		t.Errorf("reads %d, Contents %+v, want a second read that finds memory.db missing", c.list.reads, b.Contents)
	}
}

func TestBackupContentsManyEntries(t *testing.T) {
	c, _, dest := backupSite(t)
	names := make([]string, 0, maxTarEntries+5)
	for i := range maxTarEntries + 5 {
		names = append(names, "f"+strings.Repeat("x", 3)+"-"+time.Duration(i).String())
	}
	makeArchive(t, filepath.Join(dest, "loadout-20261008T031500Z.tar.gz"), names...)
	c.Report(t.Context())
	waitFor(t, func() bool { return !c.Report(t.Context()).Backups.Contents.Checking })
	got := c.Report(t.Context()).Backups.Contents
	if got.Checked || !strings.Contains(got.Err, "10,000") {
		t.Errorf("Contents = %+v, want the listing to stop with a reason", got)
	}
}

// waitFor polls until cond holds, since the listing runs in the background.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 500 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting")
}

func slowSite(t *testing.T, open func(ctx context.Context, path string) ([]string, string), timeout time.Duration) (*Collector, string) {
	t.Helper()
	c, _, dest := backupSite(t)
	c.list.open, c.list.timeout = open, timeout
	makeArchive(t, filepath.Join(dest, "loadout-20261008T031500Z.tar.gz"), "registry.db", "memory.db")
	return c, dest
}

func TestReportDoesNotWaitForListing(t *testing.T) {
	release := make(chan struct{})
	c, _ := slowSite(t, func(ctx context.Context, _ string) ([]string, string) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return []string{"registry.db", "memory.db"}, ""
	}, time.Minute)

	got := make(chan Contents, 1)
	go func() { got <- c.Report(t.Context()).Backups.Contents }()
	select {
	case ct := <-got:
		if !ct.Checking {
			t.Errorf("Contents = %+v, want Checking", ct)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Report waited for the listing")
	}
	// A second report while checking starts no second listing.
	if ct := c.Report(t.Context()).Backups.Contents; !ct.Checking {
		t.Errorf("second Contents = %+v, want Checking", ct)
	}
	close(release)
	waitFor(t, func() bool { return c.Report(t.Context()).Backups.Contents.Checked })
	if c.list.reads != 1 {
		t.Errorf("reads = %d, want 1", c.list.reads)
	}
}

func TestListingDeadlineGivesReason(t *testing.T) {
	c, _ := slowSite(t, func(ctx context.Context, _ string) ([]string, string) {
		<-ctx.Done()
		return nil, "It could not be listed."
	}, 20*time.Millisecond)
	c.Report(t.Context())
	waitFor(t, func() bool { return !c.Report(t.Context()).Backups.Contents.Checking })
	got := c.Report(t.Context()).Backups.Contents
	if got.Checked || !strings.Contains(got.Err, "too long") {
		t.Errorf("Contents = %+v, want a too-long reason", got)
	}
	if c.list.reads != 1 {
		t.Errorf("reads = %d, want the failure cached", c.list.reads)
	}
}

func TestListingCachedAndRedoneForNewArchive(t *testing.T) {
	c, dest := slowSite(t, func(_ context.Context, p string) ([]string, string) { return listArchive(context.Background(), p) }, time.Minute)
	c.Report(t.Context())
	waitFor(t, func() bool { return c.Report(t.Context()).Backups.Contents.Checked })
	c.Report(t.Context())
	if c.list.reads != 1 {
		t.Errorf("reads = %d, want 1 for the same archive", c.list.reads)
	}
	makeArchive(t, filepath.Join(dest, "loadout-20261009T031500Z.tar.gz"), "registry.db")
	if ct := c.Report(t.Context()).Backups.Contents; !ct.Checking {
		t.Errorf("new archive Contents = %+v, want Checking", ct)
	}
	waitFor(t, func() bool { return c.Report(t.Context()).Backups.Contents.Checked })
	if got := c.Report(t.Context()).Backups.Contents; !slices.Equal(got.Missing, []string{"memory.db"}) || c.list.reads != 2 {
		t.Errorf("Contents %+v reads %d, want memory.db missing after a second read", got, c.list.reads)
	}
}
