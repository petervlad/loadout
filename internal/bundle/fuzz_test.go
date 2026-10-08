package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"runtime"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
)

// maxDecodeAlloc bounds the heap Decode may allocate for one input. The
// limits allow at most MaxTotal bytes of files, which Decode reads once and
// pack.FromFiles and the kind's parser copy, plus tar and gzip buffers, so
// anything near this is a missing limit.
var maxDecodeAlloc = uint64(4 * limits.MaxTotal)

// fuzzEntry is one archive member for a hand-built seed bundle.
type fuzzEntry struct {
	hdr  tar.Header
	body string
}

// fuzzBundle builds a tar.gz from entries, for inputs Encode never produces.
func fuzzBundle(tb testing.TB, entries ...fuzzEntry) []byte {
	tb.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := e.hdr
		if err := tw.WriteHeader(&hdr); err != nil {
			tb.Fatalf("write header %q: %v", hdr.Name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			tb.Fatalf("write body %q: %v", hdr.Name, err)
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

func reg(name, body string) fuzzEntry {
	return fuzzEntry{hdr: tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}, body: body}
}

func FuzzDecode(f *testing.F) {
	p, err := pack.FromFiles(map[string][]byte{pack.ManifestFile: []byte(manifest), pack.AgentFile: []byte(agentMD)})
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := Encode(p)
	if err != nil {
		f.Fatal(err)
	}
	pax := reg(pack.AgentFile, agentMD)
	pax.hdr.Format = tar.FormatPAX
	pax.hdr.PAXRecords = map[string]string{"comment": strings.Repeat("c", 512)}
	gnu := reg(pack.AgentFile, agentMD)
	gnu.hdr.Format = tar.FormatGNU

	seeds := []struct {
		name string
		data []byte
	}{
		{"encoded", encoded},
		{"hand built valid", fuzzBundle(f, reg(pack.ManifestFile, manifest), reg(pack.AgentFile, agentMD))},
		{"reverse order", fuzzBundle(f, reg(pack.AgentFile, agentMD), reg(pack.ManifestFile, manifest))},
		{"pax records", fuzzBundle(f, reg(pack.ManifestFile, manifest), pax)},
		{"gnu format", fuzzBundle(f, reg(pack.ManifestFile, manifest), gnu)},
		{"duplicate", fuzzBundle(f, reg(pack.ManifestFile, manifest), reg(pack.ManifestFile, manifest), reg(pack.AgentFile, agentMD))},
		{"traversal", fuzzBundle(f, reg("../agent.md", agentMD))},
		{"backslash", fuzzBundle(f, reg(`a\b`, "x"))},
		{"dot", fuzzBundle(f, reg(".", "x"))},
		{"dir", fuzzBundle(f, fuzzEntry{hdr: tar.Header{Name: "d", Mode: 0o755, Typeflag: tar.TypeDir}})},
		{"symlink", fuzzBundle(f, fuzzEntry{hdr: tar.Header{Name: "agent.md", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}})},
		{"extra file", fuzzBundle(f, reg(pack.ManifestFile, manifest), reg(pack.AgentFile, agentMD), reg("README", "hi"))},
		{"empty archive", fuzzBundle(f)},
		{"not gzip", []byte("not a bundle")},
		{"empty", nil},
		{"truncated", encoded[:len(encoded)/2]},
	}
	for _, s := range seeds {
		f.Add(s.data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxBundleSize {
			t.Skip("over MaxBundleSize, rejected before decoding")
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		got, err := Decode(data)
		runtime.ReadMemStats(&after)
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > maxDecodeAlloc {
			t.Errorf("Decode(%d bytes) allocated %d bytes, want at most %d", len(data), alloc, maxDecodeAlloc)
		}
		if err != nil {
			return
		}
		re, err := Encode(got)
		if err != nil {
			t.Fatalf("Encode(Decode(data)) error: %v", err)
		}
		again, err := Decode(re)
		if err != nil {
			t.Fatalf("Decode(Encode(Decode(data))) error: %v", err)
		}
		if again.Digest() != got.Digest() {
			t.Errorf("digest after re-encoding = %s, want %s", again.Digest(), got.Digest())
		}
	})
}
