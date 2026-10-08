package pack

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxDepth is how many folders deep a file may sit.
const MaxDepth = 8

// Limits bound the size of a package. They bound what a registry stores and
// what an install writes; each kind has its own (see kinds.go), and a
// registry enforces the largest again while decoding uploads.
type Limits struct {
	MaxFiles    int
	MaxFileSize int // bytes
	MaxTotal    int // bytes of all files, uncompressed
}

// ErrTooLarge is returned when a package exceeds a limit.
var ErrTooLarge = errors.New("package is too large")

// MaxLimits returns the largest limits of any kind: what a package may be
// before its kind is known.
func MaxLimits() Limits {
	var m Limits
	for _, s := range kinds {
		m.MaxFiles = max(m.MaxFiles, s.limits.MaxFiles)
		m.MaxFileSize = max(m.MaxFileSize, s.limits.MaxFileSize)
		m.MaxTotal = max(m.MaxTotal, s.limits.MaxTotal)
	}
	return m
}

// check reports every limit files exceed; what names whose limits they are
// in the message ("an agent", "any package").
func (l Limits) check(files map[string][]byte, what string) error {
	if len(files) > l.MaxFiles {
		return fmt.Errorf("%w: %d files, the limit for %s is %d", ErrTooLarge, len(files), what, l.MaxFiles)
	}
	var errs []error
	total := 0
	for _, p := range slices.Sorted(maps.Keys(files)) {
		n := len(files[p])
		total += n
		if n > l.MaxFileSize {
			errs = append(errs, fmt.Errorf("%w: %s is %d bytes, the limit per file for %s is %d", ErrTooLarge, p, n, what, l.MaxFileSize))
		}
	}
	if total > l.MaxTotal {
		errs = append(errs, fmt.Errorf("%w: %d bytes in all, the limit for %s is %d", ErrTooLarge, total, what, l.MaxTotal))
	}
	return errors.Join(errs...)
}

// CheckPath reports whether p is a safe file path inside a package:
// relative, slash-separated, no "." or ".." or hidden segment, at most
// MaxDepth folders, and only letters, digits, '.', '_' and '-' in a segment.
// It is the one check between a bundle from the network and a path on disk.
func CheckPath(p string) error {
	if p == "" {
		return errors.New("empty file path")
	}
	segs := strings.Split(p, "/")
	if len(segs) > MaxDepth+1 {
		return fmt.Errorf("%q is more than %d folders deep", p, MaxDepth)
	}
	for _, s := range segs {
		switch {
		case s == "":
			return fmt.Errorf("%q: empty path segment (a leading, trailing or double slash)", p)
		case s == "." || s == "..":
			return fmt.Errorf("%q leaves the package or points at a folder", p)
		case strings.HasPrefix(s, "."):
			return fmt.Errorf("%q: hidden files and folders are not part of a package", p)
		case len(s) > 128:
			return fmt.Errorf("%q: a name is longer than 128 characters", p)
		}
		if strings.ContainsFunc(s, func(r rune) bool { return !nameRune(r) }) {
			return fmt.Errorf("%q: only letters, digits, '.', '_' and '-' may be used in file names", p)
		}
	}
	return nil
}

// nameRune reports whether r may appear in a file or folder name.
func nameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

// checkFiles checks every path and content, but not the limits (see
// Limits.check), and that no two paths differ
// only in case: macOS and Windows would write them to the same file, and a
// folder may not also be a file.
func checkFiles(files map[string][]byte) error {
	seen := map[string]string{}
	var errs []error
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if err := CheckPath(p); err != nil {
			errs = append(errs, err)
			continue
		}
		data := files[p]
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			errs = append(errs, fmt.Errorf("%s is not a UTF-8 text file; packages hold text so every change can be reviewed", p))
		} else if err := checkDrawio(p, data); err != nil {
			errs = append(errs, err)
		}
		lower := strings.ToLower(p)
		if other, dup := seen[lower]; dup {
			errs = append(errs, fmt.Errorf("%s and %s differ only in case", other, p))
		}
		seen[lower] = p
	}
	for _, lower := range slices.Sorted(maps.Keys(seen)) {
		p := seen[lower]
		for dir := lower; strings.Contains(dir, "/"); {
			dir = dir[:strings.LastIndex(dir, "/")]
			if other, clash := seen[dir]; clash {
				errs = append(errs, fmt.Errorf("%s is a file, but %s puts a folder in its place", other, p))
			}
		}
	}
	return errors.Join(errs...)
}

// checkDrawio refuses a compressed draw.io diagram (DrawioCompressed).
func checkDrawio(p string, data []byte) error {
	if !strings.EqualFold(path.Ext(p), ".drawio") {
		return nil
	}
	if DrawioCompressed(data) {
		return fmt.Errorf("%s is a compressed draw.io file, which cannot be reviewed; in draw.io turn off File > Properties > Compressed and save again", p)
	}
	return nil
}

// DrawioCompressed reports whether a draw.io diagram is saved compressed.
// draw.io saves a page as deflated base64 by default, which no reviewer can
// diff; saved uncompressed it is plain XML (an mxGraphModel inside each
// diagram).
func DrawioCompressed(data []byte) bool {
	return bytes.Contains(data, []byte("<diagram")) && !bytes.Contains(data, []byte("<mxGraphModel"))
}
