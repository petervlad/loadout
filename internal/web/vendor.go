package web

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// The diagram libraries, vendored by scripts/vendor.sh into static/vendor
// and embedded gzipped: every loadout binary carries them, so they are
// kept compressed. Only the diagram frames load them; no page does.

// diagramLibs are the scripts a frame of each kind loads, under
// /auth/static/. The version in the path makes the URL immutable.
var diagramLibs = map[string]string{
	"mermaid": "vendor/mermaid-12.0.0/mermaid.min.js",
	"drawio":  "vendor/drawio-31.5.3/viewer-static.min.js",
}

// vendorFiles serves static/vendor: name.js from name.js.gz, as it is to
// a browser that accepts gzip (all of them) and inflated to one that does
// not. Caddy leaves an encoded response alone.
func vendorFiles(static fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/auth/static/")
		if !strings.HasPrefix(name, "vendor/") || !strings.HasSuffix(name, ".js") || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		f, err := static.Open(name + ".gz")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		hd := w.Header()
		hd.Set("Content-Type", "text/javascript; charset=utf-8")
		hd.Set("Cache-Control", "public, max-age=31536000, immutable")
		hd.Set("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			hd.Set("Content-Encoding", "gzip")
			_, _ = io.Copy(w, f)
			return
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			http.Error(w, "broken vendored file", http.StatusInternalServerError)
			return
		}
		defer zr.Close()
		_, _ = io.Copy(w, zr) //nolint:gosec // G110: our own embedded file, not input
	})
}

// acceptsGzip reports whether the request's Accept-Encoding allows gzip.
func acceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		coding, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(coding), "gzip") && strings.ReplaceAll(strings.TrimSpace(q), " ", "") != "q=0" {
			return true
		}
	}
	return false
}
