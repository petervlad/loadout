package web_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
)

const planner = "/t/lime/p/lime/planner"

// filesSite is newUISite with a skill that holds files a browser must not
// render, a package in the sibling team koala, and a second planner
// release.
func filesSite(t *testing.T) *uiSite {
	t.Helper()
	s := newUISite(t)
	s.publishKinds()
	s.publishBundle("lime", encode(t, skillFiles("1.1.0", "# Format\n\nOne line per PR.\n", map[string]string{
		"evil.html":  "<script>alert(1)</script>",
		"icon.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"docs/a.txt": "a\n",
	})))
	s.publish("koala", "secret", "1.0.0", "Only koala reads this", "Secret.\n")
	s.publish("lime", "planner", "2.3.1", "Plans work", "Keep plans shorter.\n")
	return s
}

// TestFileRoutesAccess: the download and raw routes open exactly what the
// package page opens, and every other package, version or person gets the
// same 404.
func TestFileRoutesAccess(t *testing.T) {
	s := filesSite(t)
	bob := s.as("bob@example.com")
	routes := map[string]string{"download": "/download", "raw": "/raw/agent.md", "files": "/files/agent.md"}
	for name, suffix := range routes {
		t.Run(name, func(t *testing.T) {
			if p := s.browser().get(planner + "/2.3.0" + suffix); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login?") {
				t.Errorf("signed out = %d %s, want a redirect to sign in", p.status, p.location)
			}
			for _, path := range []string{planner + "/2.3.0", planner + "/2.3.1", "/t/lime/p/acme/python-standards/4.1.0", "/t/acme/p/acme/python-standards/4.1.0"} {
				if p := bob.get(path + suffix); p.status != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", path+suffix, p.status)
				}
			}
			for _, path := range []string{
				"/t/lime/p/koala/secret/1.0.0",  // a sibling team's package
				"/t/koala/p/koala/secret/1.0.0", // a team bob is not in
				"/t/acme/p/lime/planner/2.3.0",  // a team's package seen from the project
				"/t/lime/p/lime/ghost/1.0.0",    // no such package
				planner + "/9.9.9",              // no such version
				planner + "/v2.3.0",             // not the canonical form
				planner + "/2.3",                // not a version
				planner + "/2.4.0",              // an open proposal, not a release
			} {
				if p := bob.get(path + suffix); p.status != http.StatusNotFound || !strings.Contains(p.body, "Nothing here") {
					t.Errorf("GET %s = %d, want the 404 page", path+suffix, p.status)
				}
			}
			if _, err := s.accounts.RemoveMember(t.Context(), account.Operator(), "lime", "bob@example.com"); err != nil {
				t.Fatal(err)
			}
			if p := bob.get(planner + "/2.3.0" + suffix); p.status != http.StatusNotFound {
				t.Errorf("after leaving lime = %d, want 404", p.status)
			}
			if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "lime", "bob@example.com", account.RoleMember); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDownloadZip(t *testing.T) {
	s := filesSite(t)
	p := s.as("bob@example.com").get("/t/lime/p/lime/release-notes/1.1.0/download")
	if p.status != http.StatusOK {
		t.Fatalf("download = %d\n%s", p.status, p.body)
	}
	if got := p.header.Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}
	if got := p.header.Get("Content-Disposition"); got != `attachment; filename=lime-release-notes-1.1.0.zip` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if p.header.Get("X-Content-Type-Options") != "nosniff" || p.header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v", p.header)
	}
	zr, err := zip.NewReader(strings.NewReader(p.body), int64(len(p.body)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = string(data)
	}
	want := map[string]string{}
	for path, data := range skillFiles("1.1.0", "# Format\n\nOne line per PR.\n", map[string]string{
		"evil.html":  "<script>alert(1)</script>",
		"icon.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"docs/a.txt": "a\n",
	}) {
		want["lime-release-notes-1.1.0/"+path] = string(data)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("zip mismatch (-want +got):\n%s", diff)
	}
}

// TestRawFile: every file is plain text and an attachment, whatever its
// name, with a policy that runs and loads nothing.
func TestRawFile(t *testing.T) {
	s := filesSite(t)
	b := s.as("bob@example.com")
	base := "/t/lime/p/lime/release-notes/1.1.0/raw/"
	for _, tc := range []struct{ path, name, body string }{
		{"references/format.md", "format.md", "# Format\n\nOne line per PR.\n"},
		{"evil.html", "evil.html", "<script>alert(1)</script>"},
		{"icon.svg", "icon.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
		{pack.ManifestFile, pack.ManifestFile, string(manifest("lime", "release-notes", "skill", "1.1.0", "Writes release notes"))},
	} {
		p := b.get(base + tc.path)
		if p.status != http.StatusOK || p.body != tc.body {
			t.Errorf("GET %s = %d %q, want 200 %q", tc.path, p.status, p.body, tc.body)
			continue
		}
		want := map[string]string{
			"Content-Type":            "text/plain; charset=utf-8",
			"Content-Disposition":     "attachment; filename=" + tc.name,
			"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox",
			"X-Content-Type-Options":  "nosniff",
		}
		for k, v := range want {
			if got := p.header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", tc.path, k, got, v)
			}
		}
	}
}

// TestFilePaths: nothing but a plain path to a file of the release returns
// a file or its preview. Paths the mux cleans (dot segments, double slashes) are
// redirected first, and the cleaned path goes through the same checks.
func TestFilePaths(t *testing.T) {
	s := filesSite(t)
	b := s.as("bob@example.com")
	for _, route := range []string{"raw", "files"} {
		filePaths(t, b, "/t/lime/p/lime/release-notes/1.1.0/"+route+"/")
	}
}

func filePaths(t *testing.T, b *browser, base string) {
	t.Helper()
	tests := []struct {
		path string
		// redirect is where the mux sends the path, "" for no redirect.
		redirect string
	}{
		{"../1.0.0/raw/SKILL.md", "/t/lime/p/lime/release-notes/1.1.0/1.0.0/raw/SKILL.md"},
		{"docs/../../../../../../../../etc/passwd", "/etc/passwd"},
		{"%2e%2e/%2e%2e/1.0.0/raw/SKILL.md", ""},
		{"references%2Fformat.md", ""},
		{"references%2fformat.md", ""},
		{`references\format.md`, ""},
		{"references%5Cformat.md", ""},
		{".hidden", ""},
		{"references", ""},
		{"references/", ""},
		{"missing.md", ""},
		{"REFERENCES/format.md", ""},
		{"references/format.md%00", ""},
	}
	for _, tc := range tests {
		p := b.get(base + tc.path)
		if tc.redirect != "" {
			if p.status != http.StatusTemporaryRedirect || p.location != tc.redirect {
				t.Errorf("GET %s%s = %d %s, want a redirect to %s", base, tc.path, p.status, p.location, tc.redirect)
				continue
			}
			p = b.get(p.location)
		}
		if p.status != http.StatusNotFound || strings.Contains(p.body, "Collect the merged PRs") || strings.Contains(p.body, "root:") {
			t.Errorf("GET %s%s = %d, want 404 with no file\n%s", base, tc.path, p.status, p.body)
		}
	}
	// A path the mux cleans into a file of the same release is that file,
	// checked like any other.
	for _, path := range []string{"docs/../SKILL.md", "references//format.md", "./SKILL.md"} {
		p := b.get(base + path)
		if p.status != http.StatusTemporaryRedirect || !strings.HasPrefix(p.location, base) {
			t.Errorf("GET %s%s = %d %s, want a redirect inside the release", base, path, p.status, p.location)
			continue
		}
		if p = b.get(p.location); p.status != http.StatusOK {
			t.Errorf("cleaned %s%s = %d, want 200", base, path, p.status)
		}
	}
}

func TestPackagePageVersions(t *testing.T) {
	s := filesSite(t)
	b := s.as("bob@example.com")
	p := b.get(planner)
	wantIn(t, p, "newest", "agent.md in 2.3.1", "Keep plans shorter.", `href="/t/lime/p/lime/planner/2.3.1/download">`,
		"Download 2.3.1<span class=\"vh\"> of lime/planner as a zip</span>", `href="/t/lime/p/lime/planner?version=2.3.0">2.3.0</a>`,
		`href="/t/lime/p/lime/planner" aria-current="page">2.3.1</a>`, `href="/t/lime/p/lime/planner/2.3.0/download"`,
		`href="/t/lime/p/lime/planner/2.3.1/raw/agent.md"`, "loadout install lime/planner</code>")
	wantOut(t, p, "newest", "You are looking at")

	p = b.get(planner + "?version=2.3.0")
	wantIn(t, p, "older", "agent.md in 2.3.0", "Keep plans short.\n", "You are looking at 2.3.0. The newest is <a href=\"/t/lime/p/lime/planner\">2.3.1</a>.",
		`href="/t/lime/p/lime/planner/2.3.0/download">`, `aria-current="page">2.3.0</a>`, "loadout install lime/planner@2.3.0")
	for _, v := range []string{"2.4.0", "9.9.9", "nope", "v2.3.0"} {
		if p := b.get(planner + "?version=" + url.QueryEscape(v)); p.status != http.StatusNotFound {
			t.Errorf("?version=%s = %d, want 404", v, p.status)
		}
	}

	p = b.get("/t/lime/p/lime/release-notes")
	wantIn(t, p, "skill", `href="/t/lime/p/lime/release-notes/1.1.0/raw/references/format.md"`, "Download references/format.md")
	p = b.get("/t/lime/p/lime/runbooks")
	wantOut(t, p, "kb", "/raw/SKILL.md")
}

// publishHandbook adds lime/handbook, a knowledge base with linked
// Markdown, plain text and a long text file, at 1.0.0 and 1.1.0.
func (s *uiSite) publishHandbook() {
	s.t.Helper()
	for _, v := range []string{"1.0.0", "1.1.0"} {
		s.publishBundle("lime", encode(s.t, map[string][]byte{
			pack.ManifestFile: manifest("lime", "handbook", "kb", v, "How the team works"),
			"README.md": []byte("# Handbook " + v + "\n\nSee [deploy](ops/deploy.md#steps), [missing](nope.md), [out](../x.md)\nand [site](https://example.com).\n" +
				"A wrapped\nline. ![a diagram](arch.svg) ![remote](https://evil.example/x.png) " + xss + "\n\n" +
				"```drawio\n<mxGraphModel><root><mxCell id=\"0\"/></root></mxGraphModel>\n```\n\n```mermaid\ngraph TD; A-->B\n```\n"),
			"ops/deploy.md": []byte("# Deploy\n\nBack to [the start](../README.md).\n"),
			"notes.txt":     []byte("plain <b>text</b> " + xss + "\n"),
			"flow.mmd":      []byte("graph TD\n  A[Start] --> B[" + xss + "]\n"),
			"arch.drawio":   []byte(`<mxfile><diagram name="Overview"><mxGraphModel><root><mxCell id="0"/></root></mxGraphModel></diagram><diagram name="Deploy"><mxGraphModel><root><mxCell id="0"/></root></mxGraphModel></diagram></mxfile>`),
			"big.txt":       []byte(strings.Repeat("0123456789abcdef\n", 600<<10/17)),
		}))
	}
}

func TestFilePage(t *testing.T) {
	s := filesSite(t)
	s.publishHandbook()
	b := s.as("bob@example.com")
	base := "/t/lime/p/lime/handbook/1.1.0/files/"

	p := b.get(base + "README.md")
	if p.status != http.StatusOK {
		t.Fatalf("README preview = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "markdown",
		"<h1><code>README.md</code></h1>", "· Markdown", `<a href="/t/lime/p/lime/handbook">`,
		"<h1>Handbook 1.1.0</h1>",
		`<a href="/t/lime/p/lime/handbook/1.1.0/files/ops/deploy.md">deploy</a>`, // relative link to its preview
		`<a href="nope.md">missing</a>`, `<a href="../x.md">out</a>`, `<a href="https://example.com">site</a>`,
		"A wrapped\nline. a diagram remote &lt;script&gt;", // no hard wraps, images as alt text
		`<figure class="diagram diagram-canvas" data-frame="/t/lime/p/lime/handbook/1.1.0/draw/README.md?drawio=1" data-title="Diagram 1 in README.md">`,
		`<figure class="diagram" data-frame="/t/lime/p/lime/handbook/1.1.0/draw/README.md?block=1" data-title="Diagram 2 in README.md">`,
		`<pre class="diagram-src"><code>graph TD; A--&gt;B`,
		`href="/t/lime/p/lime/handbook/1.1.0/raw/README.md"`, `href="/t/lime/p/lime/handbook/1.1.0/files/README.md?source=1">Show source`,
		`rel="next" href="/t/lime/p/lime/handbook/1.1.0/files/arch.drawio"`)
	if csp := p.header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "; frame-src 'self'") {
		t.Errorf("preview page CSP = %q, want frame-src 'self'", csp)
	}
	wantOut(t, p, "markdown", xss, "<img", "evil.example", "<br>", `rel="prev"`, "The newest is", `aria-label="Team sections"`)

	p = b.get(base + "README.md?source=1")
	wantIn(t, p, "source", "# Handbook 1.1.0", "&lt;script&gt;", "Show preview", ">Source</h2>", "· Markdown")
	wantOut(t, p, "source", xss, "<h1>Handbook")

	p = b.get(base + "ops/deploy.md")
	wantIn(t, p, "nested", `<a href="/t/lime/p/lime/handbook/1.1.0/files/README.md">the start</a>`,
		`rel="prev" href="/t/lime/p/lime/handbook/1.1.0/files/notes.txt"`, `rel="next" href="/t/lime/p/lime/handbook/1.1.0/files/loadout.yaml"`)

	p = b.get(base + "notes.txt")
	wantIn(t, p, "text", "· Text", `<pre class="markdown file-text">plain &lt;b&gt;text&lt;/b&gt; &lt;script&gt;`)
	wantOut(t, p, "text", xss, "<b>text", "Show source")

	p = b.get(base + "big.txt")
	wantIn(t, p, "long text", "Showing the first 512 KB of 600 KB. Download the file to read all of it.")
	if len(p.body) > 560<<10 {
		t.Errorf("long text page is %d bytes, want about 512 KB of text", len(p.body))
	}

	p = b.get(base + pack.ManifestFile)
	wantIn(t, p, "manifest", "name: lime/handbook", `rel="prev" href="/t/lime/p/lime/handbook/1.1.0/files/ops/deploy.md"`)
	wantOut(t, p, "manifest", `rel="next"`)

	// An SVG is its source, never an image.
	p = b.get("/t/lime/p/lime/release-notes/1.1.0/files/icon.svg")
	wantIn(t, p, "svg", "· Text", "&lt;svg xmlns=")
	wantOut(t, p, "svg", "<img", "<svg xmlns=")

	// The main file comes first in a skill.
	p = b.get("/t/lime/p/lime/release-notes/1.1.0/files/SKILL.md")
	wantIn(t, p, "skill main", `rel="next" href="/t/lime/p/lime/release-notes/1.1.0/files/docs/a.txt"`)
	wantOut(t, p, "skill main", `rel="prev"`)

	p = b.get("/t/lime/p/lime/handbook/1.0.0/files/README.md")
	wantIn(t, p, "older", "<h1>Handbook 1.0.0</h1>", `<a href="/t/lime/p/lime/handbook?version=1.0.0">`, `<a href="/t/lime/p/lime/handbook">The newest is 1.1.0</a>`,
		`<a href="/t/lime/p/lime/handbook/1.0.0/files/ops/deploy.md">deploy</a>`)

	pkg := b.get("/t/lime/p/lime/handbook")
	wantIn(t, pkg, "package", `<a class="name" href="/t/lime/p/lime/handbook/1.1.0/files/README.md">Handbook 1.1.0</a>`)
	pkg = b.get(planner)
	wantIn(t, pkg, "agent", `href="/t/lime/p/lime/planner/2.3.1/files/agent.md">Preview<span class="vh"> agent.md</span>`)
}

// The zip of the same release is the same bytes every time.
func TestDownloadIsStable(t *testing.T) {
	s := filesSite(t)
	b := s.as("bob@example.com")
	a, c := b.get(planner+"/2.3.0/download"), b.get(planner+"/2.3.0/download")
	if a.status != http.StatusOK || !bytes.Equal([]byte(a.body), []byte(c.body)) {
		t.Errorf("two downloads differ (status %d)", a.status)
	}
}
