package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
)

const (
	manifest = "name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n"
	agentMD  = "---\nname: planner\ndescription: Plans.\n---\nYou plan.\n"
)

func testPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{pack.ManifestFile: []byte(manifest), pack.AgentFile: []byte(agentMD)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// rawBundle builds a tar.gz by hand, for inputs Encode would never produce.
func rawBundle(t *testing.T, entries ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	// The fastest level: some tests write tens of megabytes.
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := strings.Repeat("x", int(h.Size))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRoundTrip(t *testing.T) {
	p := testPack(t)
	data, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error: %v", err)
	}
	if got.Digest() != p.Digest() {
		t.Errorf("digest after round trip = %s, want %s", got.Digest(), p.Digest())
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	a, err := Encode(testPack(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(testPack(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("encoding the same package twice gave different bytes")
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"not gzip", []byte("hello"), "decode bundle"},
		{"path traversal", rawBundle(t, &tar.Header{Name: "../agent.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "leaves the package"},
		{"absolute path", rawBundle(t, &tar.Header{Name: "/etc/agent.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "empty path segment"},
		{"backslash", rawBundle(t, &tar.Header{Name: `dir\agent.md`, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "invalid file name"},
		{"hidden", rawBundle(t, &tar.Header{Name: "refs/.env", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "hidden"},
		{"too deep", rawBundle(t, &tar.Header{Name: strings.Repeat("d/", 9) + "x.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "folders deep"},
		{"symlink", rawBundle(t, &tar.Header{Name: "agent.md", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}), "not a regular file"},
		{"huge file", rawBundle(t, &tar.Header{Name: "agent.md", Mode: 0o644, Size: int64(limits.MaxFileSize) + 1, Typeflag: tar.TypeReg}), "too large"},
		{"too big", bytes.Repeat([]byte{0}, MaxBundleSize+1), "too large"},
		{"folder entry", rawBundle(t, &tar.Header{Name: "references/", Mode: 0o755, Typeflag: tar.TypeDir}), "not a regular file"},
		{"twice", rawBundle(t, &tar.Header{Name: "a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, &tar.Header{Name: "a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "appears twice"},
		{"case only", rawBundle(t, &tar.Header{Name: "refs/a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, &tar.Header{Name: "Refs/a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "differ only in case"},
		{"file where a folder goes", rawBundle(t, &tar.Header{Name: "refs", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, &tar.Header{Name: "refs/a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "puts a folder in its place"},
		{"dot segment", rawBundle(t, &tar.Header{Name: "refs/./a.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}), "leaves the package"},
		{"invalid package", rawBundle(t, &tar.Header{Name: "agent.md", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}), "missing loadout.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Decode() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecodeLimits(t *testing.T) {
	// Each bundle is valid but for its size, so only a limit can refuse it.
	many := make([]*tar.Header, limits.MaxFiles+1)
	for i := range many {
		many[i] = &tar.Header{Name: fmt.Sprintf("docs/%d.md", i), Mode: 0o644, Typeflag: tar.TypeReg}
	}
	var big []*tar.Header
	for i := range limits.MaxTotal/limits.MaxFileSize + 1 {
		big = append(big, &tar.Header{Name: fmt.Sprintf("docs/%d.md", i), Mode: 0o644, Size: int64(limits.MaxFileSize), Typeflag: tar.TypeReg})
	}
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"over the compressed size", bytes.Repeat([]byte{0}, MaxBundleSize+1), fmt.Sprintf("limit %d", MaxBundleSize)},
		{"too many files", rawBundle(t, many...), fmt.Sprintf("more than %d files", limits.MaxFiles)},
		{"too many bytes uncompressed", rawBundle(t, big...), fmt.Sprintf("over %d bytes uncompressed", limits.MaxTotal)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.data)
			if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Decode() error = %v, want ErrTooLarge with %q", err, tc.wantErr)
			}
		})
	}
}

// TestLargeKBRoundTrip: a knowledge base the size of a real wiki mirror
// (thousands of documents, one large table) survives Encode and Decode.
func TestLargeKBRoundTrip(t *testing.T) {
	files := map[string][]byte{
		pack.ManifestFile:  []byte("name: lime/wiki\nkind: kb\nversion: 1.0.0\ndescription: The team wiki.\n"),
		"tables/hosts.tsv": []byte(strings.Repeat("host\tip\towner\n", 600<<10/15)),
	}
	for i := range 3000 {
		files[fmt.Sprintf("space-%d/page-%d.md", i%40, i)] = fmt.Appendf(nil, "# Page %d\n\n%s\n", i, strings.Repeat("Some text about the page. ", 40))
	}
	p, err := pack.FromFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error: %v", err)
	}
	if got.Digest() != p.Digest() || len(got.Files()) != len(files) {
		t.Errorf("after round trip = %d files, digest %s; want %d files, digest %s", len(got.Files()), got.Digest(), len(files), p.Digest())
	}
}

const skillMain = "---\nname: notes\ndescription: Take notes.\n---\nWrite them down.\n"

// nestedFiles is a skill with supporting files in folders.
func nestedFiles() map[string][]byte {
	return map[string][]byte{
		pack.ManifestFile:          []byte("name: lime/notes\nkind: skill\nversion: 1.0.0\ndescription: Notes.\n"),
		"SKILL.md":                 []byte(skillMain),
		"references/format.md":     []byte("# Format\n"),
		"references/deep/a/b/c.md": []byte("deep\n"),
		"scripts/run.sh":           []byte("echo hi\n"),
		"z.txt":                    []byte("last\n"),
	}
}

func TestNestedRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		files map[string][]byte
	}{
		{"skill", nestedFiles()},
		{"kb", map[string][]byte{
			pack.ManifestFile:     []byte("name: lime/ops\nkind: kb\nversion: 1.0.0\ndescription: Ops.\n"),
			"README.md":           []byte("# Overview\n"),
			"runbooks/db/fail.md": []byte("# Failover\n"),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := pack.FromFiles(tc.files)
			if err != nil {
				t.Fatal(err)
			}
			data, err := Encode(p)
			if err != nil {
				t.Fatalf("Encode() error: %v", err)
			}
			got, err := Decode(data)
			if err != nil {
				t.Fatalf("Decode() error: %v", err)
			}
			if got.Digest() != p.Digest() || got.Kind() != p.Kind() {
				t.Errorf("after round trip = %s %s, want %s %s", got.Kind(), got.Digest(), p.Kind(), p.Digest())
			}
			if diff := cmp.Diff(tc.files, got.Files()); diff != "" {
				t.Errorf("files mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNestedEncodingIsDeterministic(t *testing.T) {
	var first []byte
	for i := range 10 {
		// A new map each time, so Go's map order differs between runs.
		p, err := pack.FromFiles(nestedFiles())
		if err != nil {
			t.Fatal(err)
		}
		data, err := Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = data
			continue
		}
		if !bytes.Equal(first, data) {
			t.Fatal("encoding the same package twice gave different bytes")
		}
	}

	gz, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Mode != 0o644 || !hdr.ModTime.Equal(epoch) {
			t.Errorf("entry %s = type %c mode %o time %s, want a plain file at the epoch", hdr.Name, hdr.Typeflag, hdr.Mode, hdr.ModTime)
		}
		names = append(names, hdr.Name)
	}
	want := []string{"SKILL.md", pack.ManifestFile, "references/deep/a/b/c.md", "references/format.md", "scripts/run.sh", "z.txt"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("entries mismatch, want sorted files and no folder entries (-want +got):\n%s", diff)
	}
}
