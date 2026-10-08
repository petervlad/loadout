// Package logging builds loadout's logger.
//
// Every run logs to two places at once:
//   - the terminal (stderr), human-readable, at a level the user controls
//     (warn by default, debug with -v);
//   - a JSON log file in the state directory, always at debug level, so a
//     problem can be investigated after the fact.
package logging

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// maxFileSize is the size at which the log file is rotated to <name>.1.
const maxFileSize = 5 << 20 // 5 MiB

// New returns a logger writing to stderr at level and to the JSON file at
// path at debug level, the variable controlling the terminal level (raise it
// after startup, e.g. when -v is parsed) and a closer for the file. If the
// file cannot be opened, logging continues on stderr only.
func New(stderr io.Writer, level slog.Level, path string) (*slog.Logger, *slog.LevelVar, io.Closer) {
	lv := new(slog.LevelVar)
	lv.Set(level)
	terminal := slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: lv})

	f, err := openFile(path)
	if err != nil {
		l := slog.New(terminal)
		l.Warn("logging to the terminal only", "path", path, "err", err)
		return l, lv, nopCloser{}
	}
	file := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(slog.NewMultiHandler(terminal, file)), lv, f
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// openFile opens path for appending, rotating it first if it is too large.
func openFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxFileSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("rotate log: %w", err)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	// 0600: the log may one day hold server URLs and team names.
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}
