// Package serverstats gathers what a registry admin wants to see about the
// server at a glance: the data folder and its disk, what the stores hold,
// backups, the mail queue, the process, the machine, the host as the
// operator states it and, with a cloud provider, the server as the cloud
// sees it with its estimated cost. Every part is collected on its own, so one
// that cannot be read leaves a reason and never hides the others.
package serverstats

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/sitecert"
	"github.com/petervlad/loadout/internal/vminfo"
)

// RegistryCounter counts what the registry holds.
type RegistryCounter interface {
	Counts(ctx context.Context, now time.Time) (registry.Counts, error)
}

// AccountCounter counts accounts.
type AccountCounter interface {
	AccountCounts(ctx context.Context, now time.Time) (account.Counts, error)
}

// MemoryCounter counts what team memory holds: numbers and dates only.
type MemoryCounter interface {
	Stats(ctx context.Context, now time.Time) (memory.Stats, error)
}

// MailQueue reports what the login mail queue has done.
type MailQueue interface {
	Stats() mail.QueueStats
}

// MachineReader reads the machine the registry runs on.
type MachineReader interface {
	Read() vminfo.Machine
}

// Certificates checks the certificate the site serves.
type Certificates interface {
	Check(ctx context.Context) sitecert.Report
}

// Config says where to look.
type Config struct {
	// DataDir is the registry's --data folder; DBFiles the databases in it
	// (registry.db and memory.db), each reported on its own.
	DataDir string
	DBFiles []string
	// BackupDir is where the nightly backups go; empty when not known.
	BackupDir string
	Version   string
	// Started is when the process started.
	Started  time.Time
	Now      func() time.Time
	Registry RegistryCounter
	Accounts AccountCounter
	// Memory is nil when team memory is not configured.
	Memory MemoryCounter
	// Mail is nil when email sign-in is off.
	Mail MailQueue
	// Machine is nil when the machine is not read.
	Machine MachineReader
	// Certs is nil when the certificate is not checked.
	Certs Certificates
	// Cloud is nil when no cloud provider is set up.
	Cloud CloudProvider
	// Host is what the operator states about the machine; PublicURL is
	// the registry's address in a browser.
	Host      HostFacts
	PublicURL string
	// Hostname is a seam for tests; New defaults it to os.Hostname.
	Hostname func() (string, error)
}

// Collector builds reports.
type Collector struct {
	cfg  Config
	list lister
}

// New returns a collector.
func New(cfg Config) *Collector {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Hostname == nil {
		cfg.Hostname = os.Hostname
	}
	return &Collector{cfg: cfg}
}

// Report is everything the server page shows.
type Report struct {
	At      time.Time
	Process Process
	Disk    Disk
	Counts  Counts
	Backups Backups
	Memory  Memory
	Mail    Mail
	Cloud   Cloud
	Host    Host
	// Machine is the kernel, memory, load and the files of the system;
	// MachineOn is false when it is not read.
	Machine   vminfo.Machine
	MachineOn bool
	// Certs is the served certificate; CertsOn is false when it is not
	// checked.
	Certs   sitecert.Report
	CertsOn bool
}

// HostFacts is what the operator states about the machine, in plain text.
type HostFacts struct {
	Provider, Region, Size, Contact, Console string
}

// Host is the machine the registry runs on. The name is read from the
// machine; the reason says why it is missing. The system, kernel and CPUs
// are in Report.Machine.
type Host struct {
	Stated    HostFacts
	Name      string
	NameErr   string
	PublicURL string
}

// Memory is the numbers of team memory. On is false when memory is not
// configured; Err says why they could not be counted.
type Memory struct {
	On    bool
	Stats memory.Stats
	Err   string
}

// Process is the running registry.
type Process struct {
	Version    string
	GoVersion  string
	Started    time.Time
	Uptime     time.Duration
	HeapBytes  uint64
	SysBytes   uint64
	Goroutines int
}

// Disk is the data folder and the filesystem it is on.
type Disk struct {
	DataDir string
	// The filesystem; FSErr says why it could not be read.
	FSTotal, FSFree uint64
	FSErr           string
	// DBs are the databases, in the order of Config.DBFiles.
	DBs []DBFile
	// The whole data folder.
	DataBytes int64
	DataFiles int
	DataErr   string
}

// DBFile is one database, its write-ahead log and shared memory file.
type DBFile struct {
	Name                        string
	DBBytes, WALBytes, SHMBytes int64
	Err                         string // why the database could not be read
}

// FSUsed is the used part of the filesystem.
func (d Disk) FSUsed() uint64 { return d.FSTotal - d.FSFree }

// UsedPercent is the used share of the filesystem, 0 to 100.
func (d Disk) UsedPercent() int {
	if d.FSTotal == 0 {
		return 0
	}
	return int(d.FSUsed() * 100 / d.FSTotal) //nolint:gosec // G115: at most 100
}

// Counts is what the stores hold; Err says why they could not be counted.
type Counts struct {
	Registry registry.Counts
	Accounts account.Counts
	Err      string
}

// Backups are the archives of loadout-backup.sh.
type Backups struct {
	Dir string
	// Count archives, Bytes in all; Latest is the newest.
	Count       int
	Bytes       int64
	Latest      time.Time
	LatestName  string
	LatestBytes int64
	// Err says why the folder could not be read; Off that no folder is
	// configured.
	Err string
	Off bool
	// Last is the result of the last run, Contents what the newest archive
	// holds, and Copies the copies of the database the server saved before
	// it deleted a package.
	Last        LastRun
	Contents    Contents
	Copies      int
	CopiesBytes int64
}

// Mail is the login mail queue.
type Mail struct {
	On    bool
	Stats mail.QueueStats
}

// Report collects a report. The cloud and the certificate are asked at the
// same time as the machine, and each part fails on its own.
func (c *Collector) Report(ctx context.Context) Report {
	now := c.cfg.Now()
	var (
		cloud Cloud
		certs sitecert.Report
		wait  sync.WaitGroup
	)
	wait.Go(func() { cloud = c.cloud(ctx) })
	if c.cfg.Certs != nil {
		wait.Go(func() { certs = c.cfg.Certs.Check(ctx) })
	}
	r := Report{
		At:      now,
		Process: c.process(now),
		Disk:    c.disk(),
		Counts:  c.counts(ctx, now),
		Backups: c.backups(),
		Memory:  c.memory(ctx, now),
		Mail:    c.mail(),
		Host:    c.host(),
	}
	if c.cfg.Machine != nil {
		r.Machine, r.MachineOn = c.cfg.Machine.Read(), true
	}
	wait.Wait()
	r.Cloud = cloud
	r.Certs, r.CertsOn = certs, c.cfg.Certs != nil
	return r
}

func (c *Collector) process(now time.Time) Process {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return Process{
		Version:    c.cfg.Version,
		GoVersion:  runtime.Version(),
		Started:    c.cfg.Started,
		Uptime:     now.Sub(c.cfg.Started),
		HeapBytes:  ms.HeapInuse,
		SysBytes:   ms.Sys,
		Goroutines: runtime.NumGoroutine(),
	}
}

// maxWalk stops counting the data folder after this many entries, so a
// folder that grew wild cannot slow the page.
const maxWalk = 200_000

func (c *Collector) dbFile(name string) DBFile {
	f := DBFile{Name: name}
	db := filepath.Join(c.cfg.DataDir, name)
	if fi, err := os.Stat(db); err != nil {
		f.Err = reason(err)
	} else {
		f.DBBytes = fi.Size()
	}
	// The WAL and shared memory files come and go with checkpoints.
	if fi, err := os.Stat(db + "-wal"); err == nil {
		f.WALBytes = fi.Size()
	}
	if fi, err := os.Stat(db + "-shm"); err == nil {
		f.SHMBytes = fi.Size()
	}
	return f
}

func (c *Collector) disk() Disk {
	d := Disk{DataDir: c.cfg.DataDir}
	if total, free, err := statfs(c.cfg.DataDir); err != nil {
		d.FSErr = reason(err)
	} else {
		d.FSTotal, d.FSFree = total, free
	}
	for _, name := range c.cfg.DBFiles {
		d.DBs = append(d.DBs, c.dbFile(name))
	}
	seen, partial := 0, false
	err := filepath.WalkDir(c.cfg.DataDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if path == c.cfg.DataDir {
				return err
			}
			partial = true
			return nil
		}
		if seen++; seen > maxWalk {
			return fs.SkipAll
		}
		if e.Type().IsRegular() {
			// A file removed while we walk has no info; it is skipped.
			if fi, err := e.Info(); err == nil {
				d.DataBytes += fi.Size()
				d.DataFiles++
			}
		}
		return nil
	})
	switch {
	case err != nil:
		d.DataErr = reason(err)
	case partial:
		d.DataErr = "Some files could not be read; the size leaves them out."
	}
	return d
}

func (c *Collector) counts(ctx context.Context, now time.Time) Counts {
	var out Counts
	var errs []error
	if c.cfg.Registry != nil {
		rc, err := c.cfg.Registry.Counts(ctx, now)
		out.Registry = rc
		errs = append(errs, err)
	}
	if c.cfg.Accounts != nil {
		ac, err := c.cfg.Accounts.AccountCounts(ctx, now)
		out.Accounts = ac
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		out.Err = "The database could not be counted."
	}
	return out
}

// backupPrefix and backupSuffix frame the archives loadout-backup.sh
// writes: loadout-20260929T031500Z.tar.gz.
const (
	backupPrefix = "loadout-"
	backupSuffix = ".tar.gz"
)

func (c *Collector) backups() Backups {
	b := Backups{Dir: c.cfg.BackupDir}
	if b.Dir == "" {
		b.Off = true
		return b
	}
	entries, err := os.ReadDir(b.Dir)
	if err != nil {
		b.Err = reason(err)
		return b
	}
	// ReadDir sorts by name and archives are named by their UTC time, so
	// the last one is the newest.
	var latest archiveKey
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		isCopy := strings.HasPrefix(n, deleteCopyPrefix) && strings.HasSuffix(n, deleteCopySuffix)
		isArchive := strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix)
		if !isCopy && !isArchive {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if isCopy {
			b.Copies++
			b.CopiesBytes += fi.Size()
			continue
		}
		b.Count++
		b.Bytes += fi.Size()
		b.Latest, b.LatestName, b.LatestBytes = fi.ModTime(), n, fi.Size()
		latest = archiveKey{name: n, size: fi.Size(), mod: fi.ModTime()}
	}
	b.Last = readLastRun(b.Dir)
	if b.Count > 0 {
		b.Contents = c.list.contents(filepath.Join(b.Dir, latest.name), latest, c.databases())
	}
	return b
}

// databases are the database files that exist in the data folder now, which
// the newest archive should hold. A database that was never created (memory
// that was never switched on) is not expected.
func (c *Collector) databases() []string {
	var names []string
	for _, n := range c.cfg.DBFiles {
		if _, err := os.Stat(filepath.Join(c.cfg.DataDir, n)); err == nil {
			names = append(names, n)
		}
	}
	return names
}

func (c *Collector) memory(ctx context.Context, now time.Time) Memory {
	if c.cfg.Memory == nil {
		return Memory{}
	}
	st, err := c.cfg.Memory.Stats(ctx, now)
	if err != nil {
		return Memory{On: true, Err: "Team memory could not be counted."}
	}
	return Memory{On: true, Stats: st}
}

func (c *Collector) mail() Mail {
	if c.cfg.Mail == nil {
		return Mail{}
	}
	return Mail{On: true, Stats: c.cfg.Mail.Stats()}
}

func (c *Collector) host() Host {
	h := Host{Stated: c.cfg.Host, PublicURL: c.cfg.PublicURL}
	if name, err := c.cfg.Hostname(); err != nil {
		h.NameErr = reason(err)
	} else {
		h.Name = name
	}
	return h
}

// reason turns a file error into a short sentence without the path, which
// the page shows next to it.
func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "It does not exist."
	case errors.Is(err, fs.ErrPermission):
		return "The service may not read it."
	case errors.Is(err, errUnsupported):
		return "Not available on this system."
	}
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return fmt.Sprintf("It could not be read (%v).", pe.Err)
	}
	return "It could not be read."
}
