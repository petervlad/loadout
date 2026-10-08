package server

import (
	"fmt"
	"net/http"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/tier"
)

// tiers answers GET /v1/tiers: the model table installs in the request's
// view render with. Any token that reads the view may ask, machine tokens
// included, since CI installs agents too.
func (s *server) tiers(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if !s.listingView(w, r, pr) {
		return
	}
	st, err := s.reg.ResolvedTiers(r.Context(), pr)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPITiers(st))
}

// setTiers answers PUT /v1/namespaces/{ns}/tiers: it replaces the
// namespace's own cells, for owners and admins.
func (s *server) setTiers(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.TiersRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := tableOf(req.Cells)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %w", registry.ErrInvalid, err))
		return
	}
	st, err := s.manage.SetModelTiers(r.Context(), a, r.PathValue("ns"), t)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPITiers(st))
}

func tableOf(cells []api.TierCell) (tier.Table, error) {
	out := make([]tier.Cell, 0, len(cells))
	for _, c := range cells {
		out = append(out, tier.Cell{
			Tool: c.Tool, Hint: agent.Model(c.Hint),
			Model: c.Model, Effort: tier.Effort(c.Effort),
		})
	}
	return tier.FromCells(out)
}

// toAPITiers is a stack's resolved table, with where each value comes from.
func toAPITiers(st tier.Stack) api.TiersResponse {
	t := st.Resolve()
	out := api.TiersResponse{Namespace: st.Namespace(), Cells: []api.TierCell{}, Digest: t.Digest()}
	for _, c := range t.Cells() {
		mf, ef := st.Origin(c.Tool, c.Hint)
		out.Cells = append(out.Cells, api.TierCell{
			Tool: c.Tool, Hint: string(c.Hint), Model: c.Model, Effort: string(c.Effort), ModelFrom: mf, EffortFrom: ef,
		})
	}
	return out
}
