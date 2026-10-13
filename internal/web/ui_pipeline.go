package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/pipeline"
	"github.com/petervlad/loadout/internal/registry"
)

// A pipeline package's page: a diagram drawn from the stored pipeline.yaml
// and tables that say the same in words, so the page works without script.
// A template shows its slots and edges and the command that binds it. A
// binding shows which agent each slot runs, linked into the team's view.
//
//	GET /t/{ns}/p/{team}/{name}/{version}/draw/pipeline.yaml?pipeline=1  its diagram frame

// pipelineQuery is the frame's parameter: the diagram of pipeline.yaml.
const pipelineQuery = "pipeline"

// pipelineView is the pipeline part of package.html.
type pipelineView struct {
	// Binding is true for a team's binding, false for a template.
	Binding bool
	Diagram *diagramView
	Slots   []pipeSlot
	Edges   []pipeEdge
	// Use is the command that binds this template, empty for a binding.
	Use string
	// Uses is the template a binding names, as it is written: acme/dev-loop@1.
	Uses string
	// UsesLink is that template's page in this view, and UsesVersion the
	// release the diagram was drawn from; both empty when it cannot be read.
	UsesLink    string
	UsesVersion string
	// Gates are the extra gates a binding adds.
	Gates []string
	// Problems are sentences about a binding that cannot run as it is.
	Problems []string
}

// pipeSlot is one slot of the table. Agent, Link and State are a
// binding's: State is "" (bound and found), "unbound" or "missing".
type pipeSlot struct {
	Name     string
	Brief    string
	Reads    []string
	Writes   string
	Also     []string
	Verdicts []string
	Start    bool
	Agent    string
	Link     string
	State    string
}

// pipeEdge is one edge of the table.
type pipeEdge struct {
	From, On, To string
	Gate         bool
	Rounds       int
}

// graph is a pipeline.yaml ready to draw.
type graph struct {
	mermaid string
	view    pipelineView
}

// pipelineGraph reads the stored pipeline.yaml pl as seen from t. A
// binding needs its template: the highest release of the major it names,
// read through the page's own principal, so a template the viewer cannot
// read is a warning and no diagram, never a way into another layer.
func (h *handler) pipelineGraph(ctx context.Context, t team, pl pipeline.Package) (graph, error) {
	switch p := pl.(type) {
	case *pipeline.Template:
		return graph{mermaid: pipeline.Mermaid(p, nil), view: pipelineView{Slots: slotRows(p, nil), Edges: edgeRows(p.Edges())}}, nil
	case *pipeline.Binding:
		return h.bindingGraph(ctx, t, p)
	}
	return graph{}, errors.New("unknown pipeline")
}

func (h *handler) bindingGraph(ctx context.Context, t team, b *pipeline.Binding) (graph, error) {
	v := pipelineView{Binding: true, Uses: b.Uses().String()}
	for _, g := range b.Gates() {
		v.Gates = append(v.Gates, g.From+" on "+g.On)
	}
	rel, tmpl, err := h.templateFor(ctx, t, b.Uses())
	if err != nil {
		return graph{}, err
	}
	if tmpl == nil {
		v.Problems = append(v.Problems, "The template "+v.Uses+" is not in this view, so its steps are not shown.")
		for _, name := range b.Slots() {
			v.Slots = append(v.Slots, pipeSlot{Name: name, Agent: b.Agent(name)})
		}
		h.markAgents(ctx, t, &v)
		return graph{view: v}, nil
	}
	v.UsesLink = "/t/" + t.ns() + "/p/" + rel.ID.Team() + "/" + rel.ID.Name()
	v.UsesVersion = rel.Version.String()

	agents := map[string]string{}
	for _, s := range tmpl.Slots() {
		agents[s.Name] = b.Agent(s.Name)
		if agents[s.Name] == "" {
			v.Problems = append(v.Problems, "The slot "+s.Name+" has no agent. Name one under bind before you publish.")
		}
	}
	for _, name := range b.Slots() {
		if _, ok := tmpl.Slot(name); !ok {
			v.Problems = append(v.Problems, "The binding names the slot "+name+", which "+v.Uses+" does not have.")
		}
	}
	// Bind applies the binding's gates; when it refuses (an unbound slot),
	// the template's own edges are still worth drawing.
	edges, mermaid := tmpl.Edges(), pipeline.Mermaid(tmpl, agents)
	if bound, err := pipeline.Bind(b, tmpl); err == nil {
		mermaid = bound.Mermaid()
		edges = nil
		for _, s := range bound.Slots() {
			edges = append(edges, s.Edges...)
		}
	}
	v.Slots = slotRows(tmpl, agents)
	v.Edges = edgeRows(edges)
	h.markAgents(ctx, t, &v)
	return graph{mermaid: mermaid, view: v}, nil
}

// templateFor reads the highest release of ref's major, or returns nil when
// the page's view has none: the layer is not one it reads, nothing is
// released at that major, or the package is not a template.
func (h *handler) templateFor(ctx context.Context, t team, ref pipeline.Ref) (registry.Release, *pipeline.Template, error) {
	id, err := pack.NewID(ref.Layer, ref.Name)
	if err != nil {
		return registry.Release{}, nil, nil
	}
	depth, _ := t.pr.Depth(t.ns())
	if d, ok := t.pr.Depth(id.Team()); !ok || d < depth {
		return registry.Release{}, nil, nil
	}
	all, err := h.ui.Registry.Releases(ctx, t.pr, id)
	if err != nil {
		return registry.Release{}, nil, hideUnreadable(err)
	}
	for _, r := range slices.Backward(all) {
		if r.Version.Major() != ref.Major {
			continue
		}
		rel, data, err := h.ui.Registry.Fetch(ctx, t.pr, id, r.Version.String())
		if err != nil {
			return registry.Release{}, nil, hideUnreadable(err)
		}
		p, err := bundle.Decode(data)
		if err != nil {
			return registry.Release{}, nil, err
		}
		if pl, ok := p.Pipeline(); ok {
			if tmpl, ok := pl.(*pipeline.Template); ok {
				return rel, tmpl, nil
			}
		}
		return registry.Release{}, nil, nil
	}
	return registry.Release{}, nil, nil
}

// hideUnreadable turns "not there" and "not yours" into no error: the page
// says the template is not in the view. Any other error stays one.
func hideUnreadable(err error) error {
	if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrForbidden) {
		return nil
	}
	return err
}

// markAgents links each bound agent to its package page in t's view and
// flags the ones the view does not have. An agent is found when a package
// of kind agent with that name sits in the page's layer or one above it;
// the closest layer wins, as it does when the conductor is installed.
func (h *handler) markAgents(ctx context.Context, t team, v *pipelineView) {
	rels, err := h.ui.Registry.Packages(ctx, t.pr)
	if err != nil {
		// The table still names the agents; it just cannot link them.
		h.log.DebugContext(ctx, "list packages for a pipeline", "err", err)
		return
	}
	depth, _ := t.pr.Depth(t.ns())
	found := map[string]registry.Release{}
	best := map[string]int{}
	for _, rel := range rels {
		d, ok := t.pr.Depth(rel.ID.Team())
		if rel.Kind != pack.KindAgent || !ok || d < depth {
			continue
		}
		if old, seen := best[rel.ID.Name()]; !seen || d < old {
			found[rel.ID.Name()], best[rel.ID.Name()] = rel, d
		}
	}
	for i := range v.Slots {
		s := &v.Slots[i]
		switch rel, ok := found[s.Agent]; {
		case s.Agent == "":
			s.State = "unbound"
		case !ok:
			s.State = "missing"
			v.Problems = append(v.Problems, "The agent "+s.Agent+" for the slot "+s.Name+" is not in this view.")
		default:
			s.Link = "/t/" + t.ns() + "/p/" + rel.ID.Team() + "/" + rel.ID.Name()
		}
	}
}

func slotRows(t *pipeline.Template, agents map[string]string) []pipeSlot {
	var out []pipeSlot
	for _, s := range t.Slots() {
		out = append(out, pipeSlot{
			Name: s.Name, Brief: s.Brief, Reads: s.Reads, Writes: s.Writes, Also: s.Also, Verdicts: s.Verdicts,
			Start: s.Name == t.Start(), Agent: agents[s.Name],
		})
	}
	return out
}

func edgeRows(edges []pipeline.Edge) []pipeEdge {
	out := make([]pipeEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, pipeEdge{From: e.From, On: e.On, To: e.To, Gate: e.Via == pipeline.Gate, Rounds: e.MaxRounds})
	}
	return out
}

// pipelineOf fills v for the package page when the shown release is a
// pipeline: the figure points at the release's own frame route.
func (h *handler) pipelineOf(ctx context.Context, t team, p *pack.Pack, rl release) (*pipelineView, error) {
	pl, ok := p.Pipeline()
	if !ok {
		return nil, nil
	}
	g, err := h.pipelineGraph(ctx, t, pl)
	if err != nil {
		return nil, err
	}
	v := g.view
	if g.mermaid != "" {
		v.Diagram = &diagramView{
			Frame:  rl.base + "/draw/" + pack.PipelineFile + "?" + pipelineQuery + "=1",
			Title:  "Diagram: " + rl.rel.ID.String(),
			Source: g.mermaid,
		}
	}
	if !v.Binding {
		id := rl.rel.ID
		team := "TEAM"
		if t.place.Namespace.Kind == registry.KindTeam {
			team = t.ns()
		}
		v.Use = "loadout new pipeline " + team + "/" + id.Name() + " --uses " + id.String() + "@" + strconv.Itoa(rl.rel.Version.Major())
	}
	return &v, nil
}

// pipelineFrame draws a release's pipeline.yaml: the stored file is the
// only source, the query only asks for the drawing. Anything else is a 404.
func (h *handler) pipelineFrame(w http.ResponseWriter, r *http.Request, t team, rl release, p string) {
	if r.URL.Query().Get(pipelineQuery) != "1" || p != pack.PipelineFile || rl.rel.Kind != pack.KindPipeline {
		h.notFound(w, r, t.user)
		return
	}
	pk, err := bundle.Decode(rl.data)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	pl, ok := pk.Pipeline()
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	g, err := h.pipelineGraph(r.Context(), t, pl)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if g.mermaid == "" {
		h.notFound(w, r, t.user)
		return
	}
	h.serveFrame(w, r, "Diagram: "+p, frameSource{Kind: "mermaid", Source: g.mermaid})
}
