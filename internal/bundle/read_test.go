package bundle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
)

// nestedBundle is nestedFiles encoded.
func nestedBundle(t *testing.T) []byte {
	t.Helper()
	p, err := pack.FromFiles(nestedFiles())
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestScan(t *testing.T) {
	files := nestedFiles()
	entries, got, err := Scan(nestedBundle(t), func(p string) bool { return p == "references/format.md" })
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	var want []Entry
	for _, p := range slices.Sorted(maps.Keys(files)) {
		want = append(want, Entry{Path: p, Size: len(files[p])})
	}
	if diff := cmp.Diff(want, entries); diff != "" {
		t.Errorf("entries mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string][]byte{"references/format.md": files["references/format.md"]}, got); diff != "" {
		t.Errorf("contents mismatch, want only the wanted file (-want +got):\n%s", diff)
	}
}

func TestScanSortsAnyArchive(t *testing.T) {
	data := rawBundle(t,
		&tar.Header{Name: "b.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg},
		&tar.Header{Name: "a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	entries, _, err := Scan(data, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]Entry{{"a.md", 1}, {"b.md", 2}}, entries); diff != "" {
		t.Errorf("entries mismatch (-want +got):\n%s", diff)
	}
}

func TestScanRejects(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"oversized entry", rawBundle(t, &tar.Header{Name: "a.md", Mode: 0o644, Size: int64(limits.MaxFileSize) + 1, Typeflag: tar.TypeReg}), "too large"},
		{"path traversal", rawBundle(t, &tar.Header{Name: "../a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "leaves the package"},
		{"symlink", rawBundle(t, &tar.Header{Name: "a.md", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}), "not a regular file"},
		{"twice", rawBundle(t, &tar.Header{Name: "a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, &tar.Header{Name: "a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "appears twice"},
		{"not gzip", []byte("hello"), "decode bundle"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Scan(tc.data, func(string) bool { return true })
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Scan() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestEachVisitsInPathOrder(t *testing.T) {
	files := nestedFiles()
	var paths []string
	err := Each(nestedBundle(t), func(p string, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, files[p]) {
			t.Errorf("Each(%s) = %q, want %q", p, data, files[p])
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatalf("Each() error: %v", err)
	}
	if diff := cmp.Diff(slices.Sorted(maps.Keys(files)), paths); diff != "" {
		t.Errorf("order mismatch (-want +got):\n%s", diff)
	}
}

func TestEachStopsOnError(t *testing.T) {
	stop := errors.New("stop")
	n := 0
	err := Each(nestedBundle(t), func(string, io.Reader) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Errorf("Each() = %v after %d files, want the callback's error after 1", err, n)
	}
}

func TestZip(t *testing.T) {
	files := nestedFiles()
	mod := time.Date(2026, 9, 29, 10, 30, 0, 0, time.UTC)
	var a, b bytes.Buffer
	if err := Zip(&a, nestedBundle(t), "lime-notes-1.0.0", mod); err != nil {
		t.Fatalf("Zip() error: %v", err)
	}
	if err := Zip(&b, nestedBundle(t), "lime-notes-1.0.0", mod); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two zips of the same release differ")
	}

	zr, err := zip.NewReader(bytes.NewReader(a.Bytes()), int64(a.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Mode() != 0o644 || !f.Modified.Equal(mod) || f.Method != zip.Deflate {
			t.Errorf("%s: mode %v, time %s, method %d; want 0644, %s, deflate", f.Name, f.Mode(), f.Modified, f.Method, mod)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		p, ok := strings.CutPrefix(f.Name, "lime-notes-1.0.0/")
		if !ok {
			t.Errorf("%s is not under the root folder", f.Name)
		}
		got[p] = data
	}
	if diff := cmp.Diff(files, got); diff != "" {
		t.Errorf("zip contents mismatch (-want +got):\n%s", diff)
	}
	if !slices.IsSorted(names) {
		t.Errorf("zip entries %v are not in path order", names)
	}
}
