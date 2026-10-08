package bundle

import (
	"archive/zip"
	"fmt"
	"io"
	"time"
)

// Zip writes the files of a stored bundle as a zip, each under root/, in
// the bundle's order, dated mod. The bundle stays tar.gz on the wire; a zip
// is for people, since it opens with a double click on every desktop.
// It streams: one file is in memory at a time. The same bundle, root and
// mod always give the same bytes.
func Zip(w io.Writer, data []byte, root string, mod time.Time) error {
	zw := zip.NewWriter(w)
	err := Each(data, func(path string, r io.Reader) error {
		hdr := &zip.FileHeader{Name: root + "/" + path, Method: zip.Deflate, Modified: mod.UTC()}
		hdr.SetMode(0o644)
		f, err := zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("zip %s: %w", path, err)
		}
		if _, err := io.Copy(f, r); err != nil {
			return fmt.Errorf("zip %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	return nil
}
