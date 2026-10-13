package web

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// packagesView is the body of packages.html.
type packagesView struct {
	Namespace string
	Query     string
	// Kind, Layer and Sort are the current filters and order ("" for all
	// and the default order); Kinds, Layers and Sorts are the choices,
	// with their links.
	Kind  pack.Kind
	Layer string
	Sort  string
	// KindName is the chosen kind in a sentence: "skills", "KBs".
	KindName string
	Kinds    []kindFilter
	Layers   []kindFilter
	Sorts    []sortChoice
	Rows     []packageRow
	// Total counts every package, before the search.
	Total int
	// Install is the command that installs them on a new machine.
	Install string
}

// packageRow is one package in the list.
type packageRow struct {
	ID          string
	Team        string
	Name        string
	Description string
	Kind        pack.Kind
	Version     string
	// Own is true for the page's own namespace, false for a layer above.
	Own       bool
	Published time.Time
	Releases  int
}

// kindFilter is one choice of a filter: all packages, or one kind or one
// layer, with how many packages it shows.
type kindFilter struct {
	Label   string
	Count   int
	URL     string
	Current bool
}

// sortChoice is one order of the list.
type sortChoice struct {
	Value, Label string
}

// packageSorts are the orders of the list; the first is the default: the
// team's own layer first, then the layers above, each by name.
var packageSorts = []sortChoice{
	{"", "Layer"},
	{"name", "Name"},
	{"kind", "Kind"},
	{"updated", "Recently updated"},
	{"releases", "Most releases"},
}

// sortRows orders rows by sort, keeping the default order among equals.
func sortRows(rows []packageRow, sort string) {
	var cmp func(a, b packageRow) int
	switch sort {
	case "name":
		cmp = func(a, b packageRow) int { return strings.Compare(a.Name, b.Name) }
	case "kind":
		order := map[pack.Kind]int{}
		for i, k := range pack.Kinds() {
			order[k] = i
		}
		cmp = func(a, b packageRow) int { return order[a.Kind] - order[b.Kind] }
	case "updated":
		cmp = func(a, b packageRow) int { return b.Published.Compare(a.Published) }
	case "releases":
		cmp = func(a, b packageRow) int { return b.Releases - a.Releases }
	default:
		return
	}
	slices.SortStableFunc(rows, cmp)
}

// listQuery is what the packages list shows: the search, the filters and
// the order.
type listQuery struct {
	Query, Layer, Sort string
	Kind               pack.Kind
}

// url is the list at ns with q, leaving out defaults.
func (q listQuery) url(ns string) string {
	v := url.Values{}
	if q.Query != "" {
		v.Set("q", q.Query)
	}
	if q.Kind != "" {
		v.Set("kind", string(q.Kind))
	}
	if q.Layer != "" {
		v.Set("layer", q.Layer)
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	u := "/t/" + ns
	if len(v) > 0 {
		u += "?" + v.Encode()
	}
	return u
}

func (h *handler) packagesPage(w http.ResponseWriter, r *http.Request, t team) {
	rels, err := h.ui.Registry.Packages(r.Context(), t.pr)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// A page read through a team below shows this layer and those above
	// it, not the team's own packages.
	depth, _ := t.pr.Depth(t.ns())
	lq := listQuery{
		Query: strings.TrimSpace(r.URL.Query().Get("q")),
		Kind:  pack.Kind(r.URL.Query().Get("kind")),
		Layer: r.URL.Query().Get("layer"),
		Sort:  r.URL.Query().Get("sort"),
	}
	if !lq.Kind.Valid() {
		lq.Kind = ""
	}
	if d, ok := t.pr.Depth(lq.Layer); !ok || d < depth {
		lq.Layer = ""
	}
	if !slices.ContainsFunc(packageSorts, func(s sortChoice) bool { return s.Value == lq.Sort }) {
		lq.Sort = ""
	}
	q, kind := lq.Query, lq.Kind
	v := packagesView{Namespace: t.ns(), Query: q, Kind: kind, Layer: lq.Layer, Sort: lq.Sort, Sorts: packageSorts, Install: h.installCommand(t)}
	if kind != "" {
		v.KindName = kindPlural(kind)
		if kind != pack.KindKB {
			v.KindName = strings.ToLower(v.KindName)
		}
	}
	// Each filter counts what it would show with the other one kept.
	kindCounts := map[pack.Kind]int{}
	layerCounts := map[string]int{}
	var layers []string // closest first
	inLayer, ofKind := 0, 0
	for _, rel := range rels {
		d, ok := t.pr.Depth(rel.ID.Team())
		if !ok || d < depth {
			continue
		}
		v.Total++
		if q != "" && !matches(rel, q) {
			continue
		}
		layerOK := lq.Layer == "" || rel.ID.Team() == lq.Layer
		kindOK := kind == "" || rel.Kind == kind
		if layerOK {
			inLayer++
			kindCounts[rel.Kind]++
		}
		if kindOK {
			ofKind++
			if layerCounts[rel.ID.Team()] == 0 {
				layers = append(layers, rel.ID.Team())
			}
			layerCounts[rel.ID.Team()]++
		}
		if !layerOK || !kindOK {
			continue
		}
		all, err := h.ui.Registry.Releases(r.Context(), t.pr, rel.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Rows = append(v.Rows, packageRow{
			ID:          rel.ID.String(),
			Team:        rel.ID.Team(),
			Name:        rel.ID.Name(),
			Description: rel.Description,
			Kind:        rel.Kind,
			Version:     rel.Version.String(),
			Own:         rel.ID.Team() == t.ns(),
			Published:   rel.PublishedAt,
			Releases:    len(all),
		})
	}
	sortRows(v.Rows, lq.Sort)
	// A filter shows only when there is more than one thing to choose from.
	if len(kindCounts) > 1 || kind != "" {
		all := lq
		all.Kind = ""
		v.Kinds = append(v.Kinds, kindFilter{Label: "All", Count: inLayer, URL: all.url(t.ns()), Current: kind == ""})
		for _, k := range pack.Kinds() {
			if kindCounts[k] > 0 || k == kind {
				one := lq
				one.Kind = k
				v.Kinds = append(v.Kinds, kindFilter{Label: kindPlural(k), Count: kindCounts[k], URL: one.url(t.ns()), Current: k == kind})
			}
		}
	}
	if lq.Layer != "" && layerCounts[lq.Layer] == 0 {
		layers = append(layers, lq.Layer)
	}
	if len(layers) > 1 || lq.Layer != "" {
		slices.SortStableFunc(layers, func(a, b string) int {
			da, _ := t.pr.Depth(a)
			db, _ := t.pr.Depth(b)
			return da - db
		})
		all := lq
		all.Layer = ""
		v.Layers = append(v.Layers, kindFilter{Label: "All layers", Count: ofKind, URL: all.url(t.ns()), Current: lq.Layer == ""})
		for _, l := range layers {
			one := lq
			one.Layer = l
			v.Layers = append(v.Layers, kindFilter{Label: l, Count: layerCounts[l], URL: one.url(t.ns()), Current: l == lq.Layer})
		}
	}
	h.renderTeam(w, r, http.StatusOK, "packages.html", "packages", t, uiView{Wide: true, Notice: packagesNotice(r.URL.Query()), Page: v})
}

// matches reports whether a release's id, description or kind contains
// q, ignoring case.
func matches(rel registry.Release, q string) bool {
	q = strings.ToLower(q)
	fields := []string{rel.ID.String(), rel.Description, string(rel.Kind), rel.Kind.Describe()}
	if rel.Kind == pack.KindKB {
		fields = append(fields, "knowledge base")
	}
	for _, s := range fields {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// installCommand is what a teammate runs on a new machine: sign in to this
// registry (one sign-in covers every team), then install everything.
func (h *handler) installCommand(team) string {
	return "loadout login --remote " + h.origin + " && loadout install --all"
}

// packageView is the body of package.html.
type packageView struct {
	ID          string
	Description string
	// Version is the release the page shows, Latest the newest; they
	// differ on ?version= of an older release.
	Version string
	Latest  string
	// Path is the package page, Base the shown release's file routes.
	Path string
	Base string
	// Releases are newest first.
	Releases []registry.Release
	Kind     pack.Kind
	// Main is the main file of the shown release (agent.md, SKILL.md or
	// a knowledge base's generated index), shown as text.
	MainName string
	Main     string
	// Generated is true when loadout writes the main file.
	Generated bool
	// Files are the other files of a skill or knowledge base.
	Files   []fileRow
	Install string
	// CanDelete shows the Delete section: the viewer governs the package's
	// namespace and this registry deletes on the web. DeletePath is its page.
	CanDelete  bool
	DeletePath string
	// CanAsk shows "Ask to delete": this registry reviews deletions and the
	// viewer reads the package. AskPath is its page. Open is the request
	// already open for the package, if any.
	CanAsk  bool
	AskPath string
	Open    *requestNote
}

// fileRow is one file of a package. Title is a knowledge base document's
// title.
type fileRow struct {
	Path  string
	Title string
	Size  int
}

// filesOf lists what a skill or knowledge base holds besides the manifest:
// a knowledge base's documents with their titles, a skill's files in
// reading order. An agent is its main file alone, so it lists nothing.
func filesOf(p *pack.Pack) []fileRow {
	files := p.Files()
	if b, ok := p.KB(); ok {
		var out []fileRow
		for _, d := range b.Docs() {
			out = append(out, fileRow{Path: d.Path, Title: d.Title, Size: len(files[d.Path])})
		}
		return out
	}
	if p.Kind() == pack.KindAgent {
		return nil
	}
	var out []fileRow
	for _, path := range p.Paths() {
		if path != pack.ManifestFile {
			out = append(out, fileRow{Path: path, Size: len(files[path])})
		}
	}
	return out
}

func (h *handler) packagePage(w http.ResponseWriter, r *http.Request, t team) {
	rl, ok := h.openRelease(w, r, t, r.URL.Query().Get("version"))
	if !ok {
		return
	}
	p, err := bundle.Decode(rl.data)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	name, main, err := p.Main()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	id := rl.rel.ID
	rels := slices.Clone(rl.all)
	slices.Reverse(rels)
	v := packageView{
		ID:          id.String(),
		Description: rl.rel.Description,
		Version:     rl.rel.Version.String(),
		Latest:      rl.newest().Version.String(),
		Path:        "/t/" + t.ns() + "/p/" + id.Team() + "/" + id.Name(),
		Base:        rl.base,
		Releases:    rels,
		Kind:        p.Kind(),
		MainName:    name,
		Main:        string(main),
		Generated:   p.Kind().MainFile() == "",
		Files:       filesOf(p),
		Install:     "loadout install " + id.String(),
	}
	if !rl.latest() {
		v.Install += "@" + v.Version
	}
	if v.CanDelete = h.canDelete(t, id); v.CanDelete {
		v.DeletePath = v.Path + "/delete"
	}
	if h.ui.Requests != nil {
		v.CanAsk, v.AskPath = true, v.Path+"/delete-request"
		if v.Open, err = h.openRequest(r, t, id); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.renderTeam(w, r, http.StatusOK, "package.html", "packages", t, uiView{
		Title:  id.String(),
		Notice: packageNotice(r.URL.Query().Get("done"), id),
		Page:   v,
	})
}

// kindPlural names a kind in the filter: "Agents", "KBs".
func kindPlural(k pack.Kind) string {
	return kindName(k) + "s"
}
