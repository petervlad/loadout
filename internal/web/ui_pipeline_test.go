package web_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

const loopYAML = `start: plan
slots:
  plan:
    brief: Plan the work ` + xss + `
    writes: plan.md
    reads: [ticket]
    verdicts: [READY]
  dev:
    brief: Build it.
    writes: dev.md
    also: [notes.md]
    reads: [plan.md, ticket]
    verdicts: [DONE, REPLAN]
edges:
  - {from: plan, on: READY, to: dev, via: gate}
  - {from: dev, on: DONE, to: end, via: auto}
  - {from: dev, on: REPLAN, to: plan, via: gate, max_rounds: 2}
`

func pipelineFiles(team, name, version, yaml string) map[string][]byte {
	return map[string][]byte{
		pack.ManifestFile: manifest(team, name, "pipeline", version, "Moves work through "+xss),
		pack.PipelineFile: []byte(yaml),
	}
}

// putRaw stores a release without the registry's checks, as one made before
// a check existed would be.
func (s *uiSite) putRaw(team, name, version, yaml string) {
	s.t.Helper()
	files := pipelineFiles(team, name, version, yaml)
	p, err := pack.FromFiles(files)
	if err != nil {
		s.t.Fatal(err)
	}
	data := encode(s.t, files)
	rel := registry.Release{ID: p.ID(), Version: p.Version(), Digest: p.Digest(), Description: "raw", Kind: pack.KindPipeline, Size: len(data), PublishedAt: time.Now()}
	if _, err := s.mem.PutRelease(s.t.Context(), rel, data); err != nil {
		s.t.Fatal(err)
	}
}

// pipelineSite has the template acme/dev-loop, the binding lime/dev-loop,
// two bindings that cannot run, and a template of the sibling team koala.
func pipelineSite(t *testing.T) *uiSite {
	t.Helper()
	s := newUISite(t)
	s.publishKinds()
	s.publishBundle("acme", encode(t, pipelineFiles("acme", "dev-loop", "1.2.0", loopYAML)))
	s.publishBundle("lime", encode(t, pipelineFiles("lime", "dev-loop", "1.0.0",
		"uses: acme/dev-loop@1\nbind:\n  plan: planner\n  dev: python-standards\nwork_folder: explorations/\ngate:\n  - {from: dev, on: DONE}\n")))
	s.publishBundle("koala", encode(t, pipelineFiles("koala", "loop", "1.0.0", loopYAML)))
	s.putRaw("lime", "half-loop", "1.0.0", "uses: acme/dev-loop@1\nbind:\n  plan: ghost-agent\n  dev:\nwork_folder: explorations/\n")
	s.putRaw("lime", "lost-loop", "1.0.0", "uses: koala/loop@1\nbind:\n  plan: planner\n  dev: planner\nwork_folder: explorations/\n")
	return s
}

func TestPipelinesTab(t *testing.T) {
	s := pipelineSite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/lime")
	wantIn(t, p, "packages", `pill-kind">Pipeline<`, "Pipelines <span", `href="/t/lime?kind=pipeline"`, "acme/dev-loop", "lime/dev-loop")
	p = b.get("/t/lime?kind=pipeline")
	wantIn(t, p, "pipeline filter", `aria-current="true">Pipelines`, "acme/dev-loop", "lime/dev-loop")
	wantOut(t, p, "pipeline filter", "lime/planner", "koala/loop")
	wantIn(t, b.get("/t/lime?q=pipeline"), "search by kind", "lime/dev-loop")
}

func TestTemplatePage(t *testing.T) {
	s := pipelineSite(t)
	b := s.as("bob@example.com")
	base := "/t/lime/p/acme/dev-loop"
	p := b.get(base)
	if p.status != http.StatusOK {
		t.Fatalf("template = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "template", `pill-kind">Pipeline<`,
		`data-frame="/t/lime/p/acme/dev-loop/1.2.0/draw/pipeline.yaml?pipeline=1"`, `data-title="Diagram: acme/dev-loop"`,
		`class="diagram diagram-file"`, `flowchart TD`, `-.-&gt;|&#34;REPLAN, max 2 rounds&#34;|`,
		"Dashed arrows wait for a person",
		// The slots table.
		`<th scope="col">Slot</th><th scope="col">Reads</th><th scope="col">Writes</th><th scope="col">Verdicts</th><th scope="col">Brief</th>`,
		`<code>plan</code> <span class="pill">Start</span>`, `<code>plan.md</code>`, `<code>notes.md</code>`, `<span class="pill pill-kind">READY</span>`, "Build it.",
		// The edges table, with gate and auto told apart and the cap.
		`<span class="pill pill-own">Gate</span>`, `<span class="pill">Auto</span>`, "At most 2 rounds", "The diagram needs JavaScript. The tables below say the same.",
		// Use this pipeline, with the viewer's team.
		"Use this pipeline", `<code class="cmd" id="use-pipeline">loadout new pipeline lime/dev-loop --uses acme/dev-loop@1</code>`, `data-copy="use-pipeline"`)
	// A template cannot be installed, so its page has no install row; a binding keeps its row.
	wantOut(t, p, "template", xss, "Agent for each slot", "loadout install acme/dev-loop", `id="install"`)
	wantIn(t, b.get("/t/lime/p/lime/dev-loop"), "binding install row", `<code class="cmd" id="install">loadout install lime/dev-loop</code>`)
	// The frame is on this origin, so the page may frame it; a page with no diagram may not.
	if csp := p.header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "; frame-src 'self'") {
		t.Errorf("template page CSP = %q, want it to end with frame-src 'self'", csp)
	}
	if csp := b.get("/t/lime/p/lime/lost-loop").header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-src") {
		t.Errorf("a page with no diagram allows frames: %q", csp)
	}
	wantIn(t, p, "escaped", "Plan the work &lt;script&gt;alert(1)&lt;/script&gt;")

	// Read from the project, there is no team to name.
	wantIn(t, b.get("/t/acme/p/acme/dev-loop"), "from the project", "loadout new pipeline TEAM/dev-loop --uses acme/dev-loop@1")
}

func TestBindingPage(t *testing.T) {
	s := pipelineSite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/lime/p/lime/dev-loop")
	if p.status != http.StatusOK {
		t.Fatalf("binding = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "binding", `Uses <a href="/t/lime/p/acme/dev-loop"><code>acme/dev-loop@1</code></a>, release <span class="num">1.2.0</span>`,
		"Gates you added: <code>dev on DONE</code>",
		`data-frame="/t/lime/p/lime/dev-loop/1.0.0/draw/pipeline.yaml?pipeline=1"`,
		// The agent names are in the drawing and the extra gate is applied.
		`s0[&#34;plan&lt;br/&gt;planner&#34;]`, `s1[&#34;dev&lt;br/&gt;python-standards&#34;]`,
		"Agent for each slot",
		`<a class="name" href="/t/lime/p/lime/planner"><code>planner</code></a>`,
		`<a class="name" href="/t/lime/p/acme/python-standards"><code>python-standards</code></a>`)
	wantOut(t, p, "binding", "Use this pipeline", "has a problem", "Not in this view", xss)
	// DONE turned into a gate by the binding.
	if strings.Count(p.body, `<span class="pill">Auto</span>`) != 0 {
		t.Errorf("the extra gate left an auto edge")
	}

	p = b.get("/t/lime/p/lime/half-loop")
	wantIn(t, p, "unbound and missing", "This binding has 2 problems",
		"The slot dev has no agent. Name one under bind before you publish.",
		"The agent ghost-agent for the slot plan is not in this view.",
		`<code>ghost-agent</code> <span class="pill pill-warn">Not in this view</span>`,
		`<span class="pill pill-warn">No agent</span>`,
		`s0[&#34;plan&lt;br/&gt;ghost-agent&#34;]`)
	wantOut(t, p, "unbound and missing", `href="/t/lime/p/lime/ghost-agent"`)

	p = b.get("/t/lime/p/lime/lost-loop")
	wantIn(t, p, "template out of view", "This binding has a problem", "The template koala/loop@1 is not in this view, so its steps are not shown.",
		`<code>koala/loop@1</code>`, `<a class="name" href="/t/lime/p/lime/planner">`)
	wantOut(t, p, "template out of view", `data-frame=`, `href="/t/lime/p/koala/loop"`)
}

func TestPipelineFrame(t *testing.T) {
	s := pipelineSite(t)
	b := s.as("bob@example.com")

	// The template is drawn from the stored file.
	p := b.get("/t/lime/p/acme/dev-loop/1.2.0/draw/pipeline.yaml?pipeline=1&src=%3Cscript%3Eevil()%3C/script%3E")
	if p.status != http.StatusOK {
		t.Fatalf("frame = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "template frame", `<body class="frame-mermaid" data-kind="mermaid"`, `<pre id="src" hidden>flowchart TD`,
		`REPLAN, max 2 rounds`, `<title>Diagram: pipeline.yaml</title>`, `<script src="/auth/static/vendor/mermaid-12.0.0/mermaid.min.js"></script>`)
	wantOut(t, p, "template frame", "evil()", "planner", "/auth/static/ui.js")
	if got := p.header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if got := p.header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox allow-scripts") || !strings.Contains(got, "default-src 'none'") {
		t.Errorf("frame CSP = %q", got)
	}

	// A binding is drawn with its agents, through its template.
	p = b.get("/t/lime/p/lime/dev-loop/1.0.0/draw/pipeline.yaml?pipeline=1")
	wantIn(t, p, "binding frame", `plan&lt;br/&gt;planner`, `dev&lt;br/&gt;python-standards`)
	// A binding that cannot run is still drawn when its template can be read.
	wantIn(t, b.get("/t/lime/p/lime/half-loop/1.0.0/draw/pipeline.yaml?pipeline=1"), "half-bound frame", `plan&lt;br/&gt;ghost-agent`)

	for name, path := range map[string]string{
		"a sibling team's pipeline":     "/t/lime/p/koala/loop/1.0.0/draw/pipeline.yaml?pipeline=1",
		"a team bob is not in":          "/t/koala/p/koala/loop/1.0.0/draw/pipeline.yaml?pipeline=1",
		"a binding without a template":  "/t/lime/p/lime/lost-loop/1.0.0/draw/pipeline.yaml?pipeline=1",
		"no such version":               "/t/lime/p/acme/dev-loop/9.9.9/draw/pipeline.yaml?pipeline=1",
		"an agent's file":               "/t/lime/p/lime/planner/2.3.0/draw/agent.md?pipeline=1",
		"a skill's file":                "/t/lime/p/lime/release-notes/1.0.0/draw/SKILL.md?pipeline=1",
		"the manifest of a pipeline":    "/t/lime/p/acme/dev-loop/1.2.0/draw/loadout.yaml?pipeline=1",
		"the file without the query":    "/t/lime/p/acme/dev-loop/1.2.0/draw/pipeline.yaml",
		"another value":                 "/t/lime/p/acme/dev-loop/1.2.0/draw/pipeline.yaml?pipeline=2",
		"an empty value":                "/t/lime/p/acme/dev-loop/1.2.0/draw/pipeline.yaml?pipeline=",
		"a path that is not a file":     "/t/lime/p/acme/dev-loop/1.2.0/draw/..%2Fpipeline.yaml?pipeline=1",
		"a file of a pipeline that has": "/t/lime/p/acme/dev-loop/1.2.0/draw/other.yaml?pipeline=1",
	} {
		if p := b.get(path); p.status != http.StatusNotFound {
			t.Errorf("%s: GET %s = %d, want 404", name, path, p.status)
		}
	}
	if p := s.browser().get("/t/lime/p/acme/dev-loop/1.2.0/draw/pipeline.yaml?pipeline=1"); p.status != http.StatusSeeOther {
		t.Errorf("signed out = %d, want a redirect to sign in", p.status)
	}
}
