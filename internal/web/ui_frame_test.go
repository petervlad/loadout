package web_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const handbook = "/t/lime/p/lime/handbook/1.1.0"

func frameSite(t *testing.T) (*uiSite, *browser) {
	t.Helper()
	s := filesSite(t)
	s.publishHandbook()
	return s, s.as("bob@example.com")
}

func TestDrawFrame(t *testing.T) {
	_, b := frameSite(t)

	p := b.get(handbook + "/draw/flow.mmd?src=%3Cscript%3Eevil()%3C/script%3E")
	if p.status != http.StatusOK {
		t.Fatalf("frame = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "mmd frame", `<body class="frame-mermaid" data-kind="mermaid"`,
		`<pre id="src" hidden>graph TD`+"\n"+`  A[Start] --&gt; B[&lt;script&gt;alert(1)&lt;/script&gt;]`,
		`<script src="/auth/static/vendor/mermaid-12.0.0/mermaid.min.js"></script>`, `<script src="/auth/static/preview.js?v=`,
		`<title>Diagram: flow.mmd</title>`)
	wantOut(t, p, "mmd frame", xss, "evil()", "/auth/static/ui.js", "style.css")
	wantCSP := "default-src 'none'; script-src http://localhost; style-src http://localhost 'unsafe-inline'; img-src http://localhost data:; " +
		"font-src http://localhost data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'; sandbox allow-scripts"
	if got := p.header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("frame CSP = %q, want %q", got, wantCSP)
	}
	if got := p.header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("frame X-Frame-Options = %q, want SAMEORIGIN", got)
	}

	p = b.get(handbook + "/draw/arch.drawio?page=2")
	wantIn(t, p, "drawio frame", `<body class="frame-drawio" data-kind="drawio" data-page="1">`, `<script src="/auth/static/vendor/drawio-31.5.3/viewer-static.min.js">`,
		`&lt;diagram name=&#34;Deploy&#34;&gt;`)
	if p := b.get(handbook + "/draw/arch.drawio?page=3"); p.status != http.StatusNotFound {
		t.Errorf("frame of a missing page = %d, want 404", p.status)
	}
	wantIn(t, b.get(handbook+"/files/arch.drawio?page=2"), "drawio page", "· draw.io diagram, 2 pages",
		`<nav class="page-tabs" aria-label="Pages"><a href="/t/lime/p/lime/handbook/1.1.0/files/arch.drawio?page=1">Overview</a><a href="/t/lime/p/lime/handbook/1.1.0/files/arch.drawio?page=2" aria-current="page">Deploy</a></nav>`,
		`data-frame="/t/lime/p/lime/handbook/1.1.0/draw/arch.drawio?page=2"`, `class="diagram diagram-file diagram-canvas"`)

	p = b.get(handbook + "/draw/README.md?block=1")
	wantIn(t, p, "fence frame", `<pre id="src" hidden>graph TD; A--&gt;B`)
	p = b.get(handbook + "/draw/README.md?drawio=1")
	wantIn(t, p, "draw.io fence frame", `<body class="frame-drawio" data-kind="drawio" data-page="0">`, `<pre id="src" hidden>&lt;mxGraphModel&gt;`)

	// The theme of the page, from its cookie.
	b.post("/account/theme", url.Values{"theme": {"dark"}})
	wantIn(t, b.get(handbook+"/draw/flow.mmd"), "dark frame", `<html lang="en" class="frame-mermaid" data-theme="dark">`)

	for _, path := range []string{
		"/draw/README.md", "/draw/README.md?block=0", "/draw/README.md?block=2", "/draw/README.md?block=x",
		"/draw/README.md?drawio=0", "/draw/README.md?drawio=2",
		"/draw/notes.txt", "/draw/missing.mmd", "/draw/../README.md",
	} {
		p := b.get(handbook + path)
		for p.status == http.StatusTemporaryRedirect {
			p = b.get(p.location)
		}
		if p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
}

func TestDrawFrameAccess(t *testing.T) {
	s, _ := frameSite(t)
	if p := s.browser().get(handbook + "/draw/flow.mmd"); p.status != http.StatusSeeOther {
		t.Errorf("signed out = %d, want a redirect to sign in", p.status)
	}
	eve := s.as("eve@example.com")
	for _, path := range []string{handbook + "/draw/flow.mmd", "/t/koala/p/lime/handbook/1.1.0/draw/flow.mmd", "/t/lime/p/lime/handbook/9.0.0/draw/flow.mmd"} {
		if p := eve.get(path); p.status != http.StatusNotFound {
			t.Errorf("eve GET %s = %d, want 404", path, p.status)
		}
	}
}

// Only the diagram frame may be framed; every other response says DENY.
func TestFrameOptions(t *testing.T) {
	_, b := frameSite(t)
	for _, path := range []string{"/t/lime", handbook + "/files/flow.mmd", handbook + "/raw/flow.mmd", handbook + "/download", "/auth/login", "/auth/static/ui.js"} {
		if got := b.get(path).header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("GET %s: X-Frame-Options = %q, want DENY", path, got)
		}
	}
	if csp := b.get("/t/lime").header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-src") {
		t.Errorf("packages page CSP = %q, want no frames", csp)
	}
}

func TestMermaidFilePage(t *testing.T) {
	_, b := frameSite(t)
	p := b.get(handbook + "/files/flow.mmd")
	wantIn(t, p, "mmd page", "· Mermaid diagram", `<figure class="diagram diagram-file" data-frame="/t/lime/p/lime/handbook/1.1.0/draw/flow.mmd" data-title="Diagram: flow.mmd">`,
		`<pre class="diagram-src"><code>graph TD`, "Diagrams are drawn with JavaScript on.")
	wantOut(t, p, "mmd page", "?source=1")
	wantOut(t, p, "mmd page", xss, "<iframe")
}

// Every vendored file is listed in VERSIONS with the hash of what is
// embedded, and every library a frame loads is served.
func TestVendoredFiles(t *testing.T) {
	dir := filepath.Join("static", "vendor")
	f, err := os.Open(filepath.Join(dir, "VERSIONS"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	listed := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			t.Fatalf("VERSIONS line %q: want library, version, file, sha256, license, source", line)
		}
		file, sum := fields[2], fields[3]
		listed[file] = true
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)+".gz"))
		if err != nil {
			t.Errorf("%s is listed but not vendored: %v", file, err)
			continue
		}
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256.Sum256(plain); hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s: sha256 %x, VERSIONS says %s", file, got, sum)
		}
		if _, err := os.Stat(filepath.Join(dir, strings.Split(file, "/")[0], "LICENSE")); err != nil {
			t.Errorf("%s has no LICENSE: %v", file, err)
		}
	}
	gz, err := filepath.Glob(filepath.Join(dir, "*", "*.gz"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gz {
		rel := filepath.ToSlash(strings.TrimSuffix(strings.TrimPrefix(g, dir+string(filepath.Separator)), ".gz"))
		if !listed[rel] {
			t.Errorf("%s is vendored but not in VERSIONS", rel)
		}
	}

	_, b := frameSite(t)
	for _, lib := range []string{"mermaid-12.0.0/mermaid.min.js", "drawio-31.5.3/viewer-static.min.js"} {
		if !listed[lib] {
			t.Errorf("frames load %s, which VERSIONS does not list", lib)
		}
		p := b.get("/auth/static/vendor/" + lib)
		if p.status != http.StatusOK || p.header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(p.header.Get("Content-Type"), "text/javascript") {
			t.Errorf("GET %s = %d %v", lib, p.status, p.header)
		}
		if len(p.body) < 100000 {
			t.Errorf("GET %s gave %d bytes, want the library", lib, len(p.body))
		}
	}
	for _, path := range []string{"/auth/static/vendor/VERSIONS", "/auth/static/vendor/mermaid-12.0.0/mermaid.min.js.gz", "/auth/static/vendor/none.js"} {
		if p := b.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
}
