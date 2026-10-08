package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

// The files of a release, for people: the whole version as a zip and one
// file as a download. Every route carries the version, so a link always
// means one immutable release, and every route checks what the package
// page checks (openRelease).
//
//	GET /t/{ns}/p/{team}/{name}/{version}/download        the version as a zip
//	GET /t/{ns}/p/{team}/{name}/{version}/raw/{path...}   one file, as an attachment
//	GET /t/{ns}/p/{team}/{name}/{version}/files/{path...} the preview page of one file (?source=1)
//	GET /t/{ns}/p/{team}/{name}/{version}/draw/{path...}  its sandboxed diagram frame (?block=K, ?page=N)

const (
	// maxZips is how many zips are built at once. A 64 MB knowledge base
	// deflates in about a second; more at once would starve the pages.
	maxZips = 4
	// zipWait is how long a download waits for a slot before the busy
	// page.
	zipWait = 2 * time.Second
	// transferTimeout is the write deadline of a download, as the API
	// gives a bundle (server.allowTransfer): a 64 MB zip over a slow
	// line takes minutes, and web may not import server.
	transferTimeout = 10 * time.Minute
)

// rawCSP is the policy of a downloaded file: if a browser shows it inline
// after all, it runs nothing, loads nothing and has an opaque origin.
const rawCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// filePageCSP is the pages' policy plus the diagram frames, which are on
// this origin.
const filePageCSP = htmlCSP + "; frame-src 'self'"

// frameCSP is the policy of a diagram frame. The libraries need inline
// styles (Mermaid writes a style element into its SVG, the draw.io viewer
// sets style on everything), which is why they run here and not on the
// page. The sandbox directive keeps the frame sandboxed even when its URL
// is opened directly: an opaque origin, no cookies of the page, no popups,
// no forms, and default-src 'none' leaves it no network. The origin is
// written out because browsers have differed on what 'self' means in a
// sandboxed document.
func frameCSP(origin string) string {
	return "default-src 'none'; script-src " + origin + "; style-src " + origin + " 'unsafe-inline'; img-src " + origin +
		" data:; font-src " + origin + " data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'; sandbox allow-scripts"
}

// release is one released version of a package the page's user may read.
type release struct {
	rel registry.Release
	// all are every release of the package, oldest first.
	all []registry.Release
	// data is the stored bundle, not yet decoded.
	data []byte
	// base is "/t/{ns}/p/{team}/{name}/{version}", for links.
	base string
}

// newest is the package's newest release.
func (rl release) newest() registry.Release { return rl.all[len(rl.all)-1] }

// latest reports whether the release is the package's newest.
func (rl release) latest() bool { return rl.rel.Version == rl.newest().Version }

// openRelease resolves the package in the path and the given version for
// t, or answers the 404 the package page would and returns false. version
// "" is the newest. The package's team must be at the page's layer or
// above, as on the packages list; the registry checks read access again.
func (h *handler) openRelease(w http.ResponseWriter, r *http.Request, t team, version string) (release, bool) {
	id, err := pack.NewID(r.PathValue("team"), r.PathValue("name"))
	if err != nil {
		h.notFound(w, r, t.user)
		return release{}, false
	}
	depth, _ := t.pr.Depth(t.ns())
	if d, ok := t.pr.Depth(id.Team()); !ok || d < depth {
		h.notFound(w, r, t.user)
		return release{}, false
	}
	all, err := h.ui.Registry.Releases(r.Context(), t.pr, id)
	if errors.Is(err, registry.ErrNotFound) || (err == nil && len(all) == 0) {
		h.notFound(w, r, t.user)
		return release{}, false
	}
	if err != nil {
		h.fail(w, r, err)
		return release{}, false
	}
	want := all[len(all)-1].Version
	if version != "" {
		// Only the canonical form, so one release has one URL.
		v, err := semver.Parse(version)
		if err != nil || v.String() != version || !slices.ContainsFunc(all, func(rel registry.Release) bool { return rel.Version == v }) {
			h.notFound(w, r, t.user)
			return release{}, false
		}
		want = v
	}
	rel, data, err := h.ui.Registry.Fetch(r.Context(), t.pr, id, want.String())
	if errors.Is(err, registry.ErrNotFound) {
		h.notFound(w, r, t.user)
		return release{}, false
	}
	if err != nil {
		h.fail(w, r, err)
		return release{}, false
	}
	base := "/t/" + t.ns() + "/p/" + id.Team() + "/" + id.Name() + "/" + want.String()
	return release{rel: rel, all: all, data: data, base: base}, true
}

// filePath is the package file named in the path, if it is one a package
// may hold. CheckPath allows only letters, digits, '.', '_', '-' and '/',
// so a real link is never percent-encoded; one that is (an encoded slash
// or dot) is refused rather than decoded into another name.
func filePath(r *http.Request) (string, bool) {
	p := r.PathValue("path")
	if pack.CheckPath(p) != nil || strings.Contains(p, `\`) {
		return "", false
	}
	return p, strings.HasSuffix(r.URL.EscapedPath(), "/"+p)
}

// download sends the whole release as a zip.
func (h *handler) download(w http.ResponseWriter, r *http.Request, t team) {
	rl, ok := h.openRelease(w, r, t, r.PathValue("version"))
	if !ok {
		return
	}
	if !h.zipSlot(r.Context()) {
		h.busy(w, r)
		return
	}
	defer func() { <-h.zips }()
	h.allowTransfer(w, r)
	id := rl.rel.ID
	root := id.Team() + "-" + id.Name() + "-" + rl.rel.Version.String()
	hd := w.Header()
	hd.Set("Content-Type", "application/zip")
	hd.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": root + ".zip"}))
	if err := bundle.Zip(w, rl.data, root, rl.rel.PublishedAt); err != nil {
		// The headers are gone; the browser reports a broken download.
		h.log.WarnContext(r.Context(), "write zip", "package", id.String(), "err", err)
	}
}

// zipSlot waits up to zipWait for one of the maxZips slots.
func (h *handler) zipSlot(ctx context.Context) bool {
	timer := time.NewTimer(zipWait)
	defer timer.Stop()
	select {
	case h.zips <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// allowTransfer gives a download the time a large file needs, as
// server.allowTransfer does for bundles.
func (h *handler) allowTransfer(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(transferTimeout)); err != nil {
		h.log.DebugContext(r.Context(), "extend write deadline", "err", err)
	}
}

// rawFile sends one file of the release as an attachment. Every file is
// plain text to the browser, whatever its name: a skill may hold
// evil.html or an SVG with script, and neither may render here.
func (h *handler) rawFile(w http.ResponseWriter, r *http.Request, t team) {
	rl, ok := h.openRelease(w, r, t, r.PathValue("version"))
	if !ok {
		return
	}
	p, ok := filePath(r)
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	_, files, err := bundle.Scan(rl.data, func(name string) bool { return name == p })
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data, ok := files[p]
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	hd.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(p)}))
	hd.Set("Content-Security-Policy", rawCSP)
	if _, err := w.Write(data); err != nil {
		h.log.DebugContext(r.Context(), "write file", "err", err)
	}
}

// fileView is the body of file.html.
type fileView struct {
	ID      string
	Path    string
	Version string
	Size    int
	// Package is the package page at this version. Newest is the page of
	// the newest version, empty when this is it, and Latest its version.
	Package string
	Newest  string
	Latest  string
	// Raw downloads the file; Self is this page.
	Raw  string
	Self string
	Body previewBody
	// Source shows a rendered preview's text instead; Rendered offers it.
	Source   bool
	Rendered bool
	// Prev and Next are the neighbours in reading order, "" at the ends;
	// Base makes their links.
	Prev, Next string
	Base       string
}

// filePage shows one file of a release: rendered when it is Markdown, as
// text otherwise, with its neighbours in reading order. It reads the
// bundle once and keeps only this file, so paging through a large
// knowledge base does not decode all of it on every click.
func (h *handler) filePage(w http.ResponseWriter, r *http.Request, t team) {
	rl, ok := h.openRelease(w, r, t, r.PathValue("version"))
	if !ok {
		return
	}
	p, ok := filePath(r)
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	entries, files, err := bundle.Scan(rl.data, func(name string) bool { return name == p })
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data, ok := files[p]
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	f := fileRef{Path: p, Data: data, Entries: entries, Base: rl.base}
	f.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	id := rl.rel.ID
	pkg := "/t/" + t.ns() + "/p/" + id.Team() + "/" + id.Name()
	v := fileView{
		ID: id.String(), Path: p, Version: rl.rel.Version.String(), Size: len(data),
		Package: pkg, Latest: rl.newest().Version.String(),
		Raw: rl.base + "/raw/" + p, Self: f.url(p), Base: rl.base,
		Body: previewOf(f),
	}
	if !rl.latest() {
		v.Package += "?version=" + v.Version
		v.Newest = pkg
	}
	v.Rendered = v.Body.rendered()
	if v.Rendered && r.URL.Query().Get("source") == "1" {
		v.Source = true
		what := v.Body.What
		v.Body = textPreview(f)
		v.Body.What = what
	}
	order := readingOrder(entries, rl.rel.Kind.MainFile())
	if i := slices.Index(order, p); i >= 0 {
		if i > 0 {
			v.Prev = order[i-1]
		}
		if i+1 < len(order) {
			v.Next = order[i+1]
		}
	}
	// The sidebar without the team's header: the file's path is the
	// page's title, and a back link leads to the package.
	w.Header().Set("Content-Security-Policy", filePageCSP)
	h.renderUI(w, r, http.StatusOK, "file.html", t.user, t.places, t.ns(), uiView{Title: p + " · " + id.String(), NS: t.ns(), Page: v})
}

// frameView is the data of frame.html.
type frameView struct {
	Title  string
	Theme  string
	Kind   string
	Lib    string
	Source string
	Page   int
}

// drawFrame is the document inside a diagram's frame: the library of its
// kind and the diagram's source, taken from the package file on the
// server (the query only picks a fence or a page), so no link can make it
// draw text of someone else's choosing.
func (h *handler) drawFrame(w http.ResponseWriter, r *http.Request, t team) {
	rl, ok := h.openRelease(w, r, t, r.PathValue("version"))
	if !ok {
		return
	}
	p, ok := filePath(r)
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	_, files, err := bundle.Scan(rl.data, func(name string) bool { return name == p })
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data, ok := files[p]
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	src, ok := frameOf(fileRef{Path: p, Data: data, Base: rl.base}, r.URL.Query())
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	h.serveFrame(w, r, "Diagram: "+path.Base(p), src)
}

// serveFrame writes the frame document that draws src, with the frame's
// policy: the sandbox, and framing by the registry's own pages only.
func (h *handler) serveFrame(w http.ResponseWriter, r *http.Request, title string, src frameSource) {
	v := frameView{Title: title, Theme: h.theme(r), Kind: src.Kind, Lib: diagramLibs[src.Kind], Source: src.Source, Page: src.Page}
	var buf strings.Builder
	if err := h.ui.frame.Execute(&buf, v); err != nil {
		h.fail(w, r, fmt.Errorf("render frame: %w", err))
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", frameCSP(h.origin))
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	if _, err := io.WriteString(w, buf.String()); err != nil {
		h.log.DebugContext(r.Context(), "write frame", "err", err)
	}
}

// readingOrder is the order the pages go through a release's files: the
// main file first, the others by path, the manifest last.
func readingOrder(entries []bundle.Entry, main string) []string {
	var out []string
	if main != "" && slices.ContainsFunc(entries, func(e bundle.Entry) bool { return e.Path == main }) {
		out = append(out, main)
	}
	for _, e := range entries {
		if e.Path != main && e.Path != pack.ManifestFile {
			out = append(out, e.Path)
		}
	}
	return append(out, pack.ManifestFile)
}
