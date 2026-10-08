package web

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
)

// staticVersion is a short hash of the embedded stylesheets and scripts.
// Pages link them with it as ?v=, so a browser keeps them until the next
// release instead of fetching them on every page.
var staticVersion = sync.OnceValue(func() string {
	h := sha256.New()
	_ = fs.WalkDir(assets, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == "static/vendor" {
				return fs.SkipDir // versioned by path, see vendorFiles
			}
			return nil
		}
		b, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
})

// staticURL is the address of an embedded static file, with its version.
func staticURL(name string) string {
	return "/auth/static/" + name + "?v=" + staticVersion()
}

// assetFuncs are the template functions every page template gets.
func assetFuncs() template.FuncMap {
	return template.FuncMap{"static": staticURL}
}

// staticFiles serves the embedded static files. A request with the
// current ?v= may be cached for good; any other must check back, which
// the ETag turns into a 304.
func staticFiles(static fs.FS) http.Handler {
	files := http.StripPrefix("/auth/static/", http.FileServerFS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		if r.URL.Query().Get("v") == staticVersion() {
			hd.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			hd.Set("Cache-Control", "no-cache")
		}
		hd.Set("ETag", `"`+staticVersion()+`"`)
		files.ServeHTTP(w, r)
	})
}
