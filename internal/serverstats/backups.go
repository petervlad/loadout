package serverstats

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limits on what the backup parts read, so a folder that grew wild or an odd
// archive cannot slow the page or fill memory.
const (
	// maxLastRun is the longest .last-run file read.
	maxLastRun = 4 << 10
	// maxListedArchive is the largest archive whose contents are listed. The
	// listing is a full gunzip, so a bigger one is not read.
	maxListedArchive = 512 << 20
	// maxUnpacked stops reading an archive that unpacks to more than this.
	// The registry and memory databases are a few MiB to a few hundred MiB,
	// so 2 GiB is far above a real archive, and on the 2 vCPU VM it is about
	// what gunzip finishes inside listTimeout.
	maxUnpacked = 2 << 30
	// listTimeout is how long one background listing may run.
	listTimeout = 60 * time.Second
	// maxTarEntries is how many headers are read before the listing stops.
	maxTarEntries = 10_000
)

const (
	// lastRunFile is what loadout-backup.sh writes beside the archives. Its
	// leading dot keeps it out of the archive list.
	lastRunFile = ".last-run"
	// deleteCopyPrefix and deleteCopySuffix frame the copy the server saves
	// before it deletes a package.
	deleteCopyPrefix = "before-delete-"
	deleteCopySuffix = ".db"
)

// LastRun is the result of the last run of loadout-backup.sh (item D1), from
// the status file it writes on exit.
type LastRun struct {
	// Missing is true when there is no status file yet.
	Missing  bool
	Finished time.Time
	// OK is true when the run ended well; Exit is its exit code.
	OK      bool
	Exit    int
	Archive string
	// Err says why the file could not be read.
	Err string
}

// Contents is what the newest archive holds (item D2): whether each database
// that exists in the data folder is in it.
type Contents struct {
	// Checking is true while the archive is being listed in the background.
	Checking bool
	// Checked is false when the archive was not listed, and Err says why.
	Checked bool
	// Missing names the databases the archive lacks.
	Missing []string
	Err     string
}

// readLastRun reads the status file. Only four keys count and anything else in
// the file is ignored.
func readLastRun(dir string) LastRun {
	f, err := os.Open(filepath.Join(dir, lastRunFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LastRun{Missing: true}
		}
		return LastRun{Err: reason(err)}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLastRun+1))
	if err != nil {
		return LastRun{Err: reason(err)}
	}
	if len(data) > maxLastRun {
		return LastRun{Err: "The status file is larger than 4 KiB."}
	}
	var (
		lr                             LastRun
		haveTime, haveResult, haveExit bool
	)
	for line := range strings.Lines(string(data)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "finished":
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return LastRun{Err: "The status file has a time that cannot be read."}
			}
			lr.Finished, haveTime = t.UTC(), true
		case "result":
			if v != "ok" && v != "failed" {
				return LastRun{Err: "The status file has a result that cannot be read."}
			}
			lr.OK, haveResult = v == "ok", true
		case "exit":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 255 {
				return LastRun{Err: "The status file has an exit code that cannot be read."}
			}
			lr.Exit, haveExit = n, true
		case "archive":
			if v != "" && !archiveName(v) {
				return LastRun{Err: "The status file names a file that is not one of the archives."}
			}
			lr.Archive = v
		}
	}
	if !haveTime || !haveResult || !haveExit {
		return LastRun{Err: "The status file is incomplete."}
	}
	return lr
}

// archiveName reports whether n is a name loadout-backup.sh gives an archive.
func archiveName(n string) bool {
	return strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix) && n == filepath.Base(n) && !strings.ContainsAny(n, "\x00\\")
}

// archiveKey says which archive was listed, so the listing is read once.
type archiveKey struct {
	name string
	size int64
	mod  time.Time
}

// lister reads the contents of the newest archive and remembers the answer for
// the archive's name, size and modification time. The read is a full gunzip, so
// it never runs inside a page request: one background goroutine does it, with
// a deadline, and the page shows "checking" until the answer is ready.
type lister struct {
	mu      sync.Mutex
	key     archiveKey
	names   []string
	err     string
	started bool
	done    bool
	// open lists an archive; nil means listArchive. timeout is the deadline
	// of one listing; zero means listTimeout.
	open    func(ctx context.Context, path string) ([]string, string)
	timeout time.Duration
	// reads counts the archives really opened, for tests.
	reads int
}

// contents says which of want the archive at path lacks. It never blocks on
// the archive: for a new key it starts the listing and returns Checking.
func (l *lister) contents(path string, key archiveKey, want []string) Contents {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.key != key {
		l.key, l.started, l.done = key, true, false
		l.names, l.err = nil, ""
		if key.size > maxListedArchive {
			l.err, l.done = "It is too large to list here.", true
		} else {
			l.reads++
			go l.run(key, path)
		}
	}
	if !l.done {
		return Contents{Checking: true}
	}
	if l.err != "" {
		return Contents{Err: l.err}
	}
	have := make(map[string]bool, len(l.names))
	for _, n := range l.names {
		have[n] = true
	}
	c := Contents{Checked: true}
	for _, w := range want {
		if !have[w] {
			c.Missing = append(c.Missing, w)
		}
	}
	return c
}

// run lists one archive on a context of its own, which ends at the deadline,
// and stores the answer unless a newer archive has taken the slot since.
func (l *lister) run(key archiveKey, path string) {
	timeout := l.timeout
	if timeout <= 0 {
		timeout = listTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	open := l.open
	if open == nil {
		open = listArchive
	}
	names, why := open(ctx, path)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		names, why = nil, "It took too long to list, so it was not read."
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.key == key {
		l.names, l.err, l.done = names, why, true
	}
}

// ctxReader stops a read once ctx is done, so a long gunzip ends at the deadline.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// listArchive returns the names of the files in a tar.gz, read from the
// headers only. The names are cleaned of a leading "./".
func listArchive(ctx context.Context, path string) (names []string, why string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, reason(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(ctxReader{ctx, f})
	if err != nil {
		return nil, archiveReason(err)
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maxUnpacked}
	tr := tar.NewReader(limited)
	for len(names) < maxTarEntries {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names, ""
		}
		if err != nil {
			if limited.N <= 0 {
				return nil, "It is too large to list here."
			}
			return nil, archiveReason(err)
		}
		if h.Typeflag == tar.TypeReg {
			names = append(names, strings.TrimPrefix(filepath.ToSlash(h.Name), "./"))
		}
	}
	return nil, "It has more than 10,000 files, so it was not listed in full."
}

// archiveReason says what is wrong with an archive in a few plain words.
func archiveReason(err error) string {
	switch {
	case errors.Is(err, gzip.ErrHeader), errors.Is(err, gzip.ErrChecksum):
		return "It is not a complete gzip file."
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "It ends early."
	case errors.Is(err, tar.ErrHeader):
		return "Its tar headers are broken."
	}
	return "It could not be listed."
}
