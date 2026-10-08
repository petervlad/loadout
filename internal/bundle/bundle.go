// Package bundle encodes a package as the tar.gz that travels between the
// CLI and a registry, and decodes one safely.
//
// Encoding is deterministic (sorted names, fixed timestamps and modes), so
// the same package always produces the same bytes. Identity does not rely on
// that, though: a package is identified by its content digest (pack.Digest),
// which Decode recomputes from the files.
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/pack"
)

// MaxBundleSize is the most compressed bytes Decode accepts. Big
// knowledge bases compress well, so this is half their uncompressed limit.
const MaxBundleSize = 32 << 20

// limits are what Decode accepts uncompressed: the largest of any kind,
// since the kind is known only once the manifest is parsed. A registry
// decodes untrusted uploads, so every limit is enforced while reading,
// before anything is buffered in full; pack.FromFiles then applies the
// kind's own limits.
var limits = pack.MaxLimits()

// ContentType is the media type of an encoded bundle.
const ContentType = "application/vnd.loadout.bundle+gzip"

// ErrTooLarge is returned when a bundle exceeds a size limit.
var ErrTooLarge = errors.New("bundle is too large")

// epoch is the fixed modification time written into every archive entry.
var epoch = time.Unix(0, 0).UTC()

// Encode returns the bundle bytes for p.
func Encode(p *pack.Pack) ([]byte, error) {
	files := p.Files()
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gz.ModTime = epoch
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data := files[name]
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(data)),
			ModTime:  epoch,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("encode %s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("encode %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("encode bundle: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("encode bundle: %w", err)
	}
	if buf.Len() > MaxBundleSize {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, buf.Len(), MaxBundleSize)
	}
	return buf.Bytes(), nil
}

// Decode reads bundle bytes and returns the validated package.
func Decode(data []byte) (*pack.Pack, error) {
	files := map[string][]byte{}
	err := walk(data, func(hdr *tar.Header, r io.Reader) error {
		body := make([]byte, hdr.Size)
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("decode %s: %w", hdr.Name, err)
		}
		files[hdr.Name] = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	p, err := pack.FromFiles(files)
	if err != nil {
		return nil, fmt.Errorf("decode bundle: %w", err)
	}
	return p, nil
}

// checkHeader accepts only plain files at paths pack.CheckPath allows:
// relative, inside the package, no links or devices.
func checkHeader(hdr *tar.Header) error {
	if hdr.Typeflag != tar.TypeReg {
		return fmt.Errorf("decode bundle: %q is not a regular file", hdr.Name)
	}
	if strings.Contains(hdr.Name, `\`) {
		return fmt.Errorf("decode bundle: invalid file name %q", hdr.Name)
	}
	if err := pack.CheckPath(hdr.Name); err != nil {
		return fmt.Errorf("decode bundle: %w", err)
	}
	if hdr.Size < 0 || hdr.Size > int64(limits.MaxFileSize) {
		return fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, hdr.Name, limits.MaxFileSize)
	}
	return nil
}
