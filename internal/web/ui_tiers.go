package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// ModelTool is an AI tool the model settings page has a section for: one
// adapter of the installer. A tool with built-in models also implements
// target.ModelDefaults, and its defaults show where nothing is set.
type ModelTool interface {
	Name() string
	target.ModelGuide
}

// tiersView is the body of tiers.html.
type tiersView struct {
	Namespace string
	// Above is the closest namespace the page inherits from; empty for a
	// project at the top.
	Above   string
	Efforts []tier.Effort
	Tools   []tierTool
}

// tierTool is one tool's section.
type tierTool struct {
	Name, Product, Help string
	Notes               []string
	Rows                []tierRow
}

// tierRow is one hint of one tool.
type tierRow struct {
	Hint, Label, About string
	// ModelField and EffortField name the form fields.
	ModelField, EffortField string
	// Model and Effort are the namespace's own values, as the form holds
	// them.
	Model, Effort string
	// Inherited is what applies when the namespace sets no model, for the
	// placeholder: "Claude Sonnet 5, from acme", "opus, built-in".
	Inherited string
	// InheritedEffort labels the effort choice that sets nothing.
	InheritedEffort string
}

var hintAbout = map[agent.Model][2]string{
	agent.ModelFast:     {"Fast", "Quick, simple tasks"},
	agent.ModelBalanced: {"Balanced", "Everyday work"},
	agent.ModelDeep:     {"Deep", "Planning and hard problems"},
}

func (h *handler) tiersPage(w http.ResponseWriter, r *http.Request, t team) {
	h.renderTiers(w, r, t, http.StatusOK, nil, "", "")
}

// renderTiers renders the page; submitted refills the form after an error.
func (h *handler) renderTiers(w http.ResponseWriter, r *http.Request, t team, status int, submitted map[string]string, errText, field string) {
	// The page is for those who change the table; members read it only
	// through sync, so for them it does not exist.
	if !t.member() || !t.place.Access.CanSetTiers() {
		h.notFound(w, r, t.user)
		return
	}
	st, err := h.ui.Teams.ModelTiers(r.Context(), t.actor(), t.ns())
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	v := tiersView{Namespace: t.ns(), Above: st.Above().Namespace(), Efforts: tier.Efforts()}
	for _, tool := range h.ui.Tools {
		v.Tools = append(v.Tools, toolSection(tool, st, submitted))
	}
	notice := ""
	if r.URL.Query().Get("done") == "saved" {
		notice = "Model settings saved. Installs pick them up on their next sync."
	}
	h.renderTeam(w, r, status, "tiers.html", "settings", t, uiView{
		Title:  "Models · " + t.ns(),
		Notice: notice,
		Error:  errText,
		Field:  field,
		Page:   v,
	})
}

func toolSection(tool ModelTool, st tier.Stack, submitted map[string]string) tierTool {
	sec := tierTool{Name: tool.Name(), Product: tool.Product(), Help: tool.ModelHelp(), Notes: tool.ModelNotes()}
	above := st.Above()
	inherited := above.Resolve()
	for _, hint := range tier.Hints() {
		own, _ := st.Own().Get(tool.Name(), hint)
		row := tierRow{
			Hint: string(hint), Label: hintAbout[hint][0], About: hintAbout[hint][1],
			ModelField: "model-" + tool.Name() + "-" + string(hint), EffortField: "effort-" + tool.Name() + "-" + string(hint),
			Model: own.Model, Effort: string(own.Effort),
		}
		if submitted != nil {
			row.Model, row.Effort = submitted[row.ModelField], submitted[row.EffortField]
		}
		up, _ := inherited.Get(tool.Name(), hint)
		mFrom, eFrom := above.Origin(tool.Name(), hint)
		row.Inherited = valueFrom(up.Model, mFrom, builtIn(tool, hint))
		row.InheritedEffort = "Tool default"
		if up.Effort != "" {
			row.InheritedEffort = string(up.Effort) + ", from " + eFrom
		}
		sec.Rows = append(sec.Rows, row)
	}
	return sec
}

// valueFrom says what model applies and why.
func valueFrom(model, from, builtin string) string {
	switch {
	case model != "":
		return model + ", from " + from
	case builtin != "":
		return builtin + ", built in"
	}
	return "The person's own choice"
}

func builtIn(tool ModelTool, h agent.Model) string {
	if d, ok := tool.(target.ModelDefaults); ok {
		return d.DefaultModel(h)
	}
	return ""
}

// saveTiers replaces the namespace's own values of the tools on the page;
// values of tools this registry does not show are kept.
func (h *handler) saveTiers(w http.ResponseWriter, r *http.Request, t team) {
	st, err := h.ui.Teams.ModelTiers(r.Context(), t.actor(), t.ns())
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/tiers")
		return
	}
	submitted := map[string]string{}
	var edits []tier.Edit
	for _, tool := range h.ui.Tools {
		for _, hint := range tier.Hints() {
			k := tier.Key{Tool: tool.Name(), Hint: hint}
			model := strings.TrimSpace(r.PostForm.Get("model-" + k.Tool + "-" + string(hint)))
			effort := r.PostForm.Get("effort-" + k.Tool + "-" + string(hint))
			submitted["model-"+k.Tool+"-"+string(hint)] = model
			submitted["effort-"+k.Tool+"-"+string(hint)] = effort
			edits = append(edits, tier.Edit{Key: k, Field: tier.FieldModel, Value: model}, tier.Edit{Key: k, Field: tier.FieldEffort, Value: effort})
		}
	}
	next, err := st.Own().Apply(edits)
	if err != nil {
		h.renderTiers(w, r, t, http.StatusBadRequest, submitted, sentence(err), badField(edits))
		return
	}
	if _, err := h.ui.Teams.SetModelTiers(r.Context(), t.actor(), t.ns(), next); err != nil {
		if errors.Is(err, registry.ErrInvalid) {
			h.renderTiers(w, r, t, http.StatusBadRequest, submitted, sentence(err), badField(edits))
			return
		}
		h.refused(w, r, t, err, "/t/"+t.ns()+"/tiers")
		return
	}
	back(w, r, "/t/"+t.ns()+"/tiers", "saved", nil)
}

// badField is the form field of the first edit that is wrong on its own,
// or "" when only the edits together are.
func badField(edits []tier.Edit) string {
	for _, e := range edits {
		if _, err := (tier.Table{}).Apply([]tier.Edit{e}); err != nil {
			return string(e.Field) + "-" + e.Key.Tool + "-" + string(e.Key.Hint)
		}
	}
	return ""
}
