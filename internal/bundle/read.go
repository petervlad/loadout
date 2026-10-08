package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Entry is one file of a stored bundle.
type Entry struct {
	Path string
	Size int
}

// walk calls fn with every file of a bundle in the order of the archive,
// with the checks Decode makes while reading: plain files at safe paths,
// no path twice, and the largest limits of any kind. fn must read the
// body from r before it returns, or leave it; walk skips what is left.
func walk(data []byte, fn func(hdr *tar.Header, r io.Reader) error) error {
	if len(data) > MaxBundleSize {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(data), MaxBundleSize)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode bundle: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	total := int64(0)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decode bundle: %w", err)
		}
		if err := checkHeader(hdr); err != nil {
			return err
		}
		if seen[hdr.Name] {
			return fmt.Errorf("decode bundle: %q appears twice", hdr.Name)
		}
		if len(seen) == limits.MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrTooLarge, limits.MaxFiles)
		}
		seen[hdr.Name] = true
		// The header's size is checked against what is left before the
		// body is read, so a zip bomb stops at the limit.
		if total += hdr.Size; total > int64(limits.MaxTotal) {
			return fmt.Errorf("%w: over %d bytes uncompressed", ErrTooLarge, limits.MaxTotal)
		}
		if err := fn(hdr, io.LimitReader(tr, hdr.Size)); err != nil {
			return err
		}
	}
}

// Scan lists the files of a stored bundle, in path order, and returns the
// content of the ones want accepts. Only those are held in memory, so a
// page that shows one file of a large knowledge base reads the archive
// once and keeps one file.
func Scan(data []byte, want func(path string) bool) ([]Entry, map[string][]byte, error) {
	var entries []Entry
	files := map[string][]byte{}
	err := walk(data, func(hdr *tar.Header, r io.Reader) error {
		entries = append(entries, Entry{Path: hdr.Name, Size: int(hdr.Size)})
		if !want(hdr.Name) {
			return nil
		}
		body := make([]byte, hdr.Size)
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("decode %s: %w", hdr.Name, err)
		}
		files[hdr.Name] = body
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return entries, files, nil
}

// Each calls fn with every file of a stored bundle, one at a time, in the
// order of the archive. Encode writes path order; a bundle from another
// writer is visited as it was stored, which is still the same order every
// time.
func Each(data []byte, fn func(path string, r io.Reader) error) error {
	return walk(data, func(hdr *tar.Header, r io.Reader) error {
		return fn(hdr.Name, r)
	})
}
