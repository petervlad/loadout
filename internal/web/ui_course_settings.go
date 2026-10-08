package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// courseForm is the Course panel on Settings: a select of the knowledge
// bases the namespace reads.
type courseForm struct {
	// None is the first option, which sends the empty value: "No course",
	// or the course of a namespace above, which the wiki then shows.
	None string
	// Groups are the knowledge bases, by namespace, closest first.
	Groups []courseGroup
	// Selected is the chosen value ("team/name", "" for none); Posted
	// marks a form sent back with an error.
	Selected string
	Posted   bool
}

// courseGroup is the knowledge bases of one namespace.
type courseGroup struct {
	Namespace string
	IDs       []string
}

// courseForm builds the Course panel of t. A posted form (prev) keeps
// the value it sent.
func (h *handler) courseForm(r *http.Request, t team, prev *courseForm) (*courseForm, error) {
	f := &courseForm{None: "No course"}
	if c := t.place.Namespace.Course; !c.IsZero() {
		f.Selected = c.String()
	}
	if prev != nil {
		f.Selected, f.Posted = prev.Selected, true
	}
	if above, ok := registry.CourseOf(chainOf(t)[1:]); ok {
		f.None = above.From + "'s course (" + above.Package.String() + ")"
	}
	rels, err := h.ui.Registry.Packages(r.Context(), t.pr)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if rel.Kind != pack.KindKB {
			continue
		}
		ns := rel.ID.Team()
		if n := len(f.Groups); n == 0 || f.Groups[n-1].Namespace != ns {
			f.Groups = append(f.Groups, courseGroup{Namespace: ns})
		}
		g := &f.Groups[len(f.Groups)-1]
		g.IDs = append(g.IDs, rel.ID.String())
	}
	return f, nil
}

// saveCourse sets the course the wiki shows. A value the registry
// refuses (not a knowledge base, not in a layer every reader reads)
// shows the page again with the error at the field.
func (h *handler) saveCourse(w http.ResponseWriter, r *http.Request, t team) {
	raw := strings.TrimSpace(r.PostForm.Get("course"))
	back400 := func(msg string) {
		h.renderSettings(w, r, t, http.StatusBadRequest, settingsView{Course: &courseForm{Selected: raw}}, msg)
	}
	var id pack.ID
	if raw != "" {
		var err error
		if id, err = pack.ParseID(raw); err != nil {
			back400("Choose a knowledge base from the list.")
			return
		}
	}
	_, err := h.ui.Teams.SetCourse(r.Context(), t.actor(), t.ns(), id)
	if errors.Is(err, registry.ErrInvalid) {
		back400(sentence(err))
		return
	}
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	if id.IsZero() {
		back(w, r, "/t/"+t.ns()+"/settings", "course-none", nil)
		return
	}
	back(w, r, "/t/"+t.ns()+"/settings", "course-set", url.Values{"course": {id.String()}})
}
