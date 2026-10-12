package account

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/registry"
)

// Place is a namespace a signed-in user can open in the web pages.
type Place struct {
	Namespace registry.Namespace
	// Level is the distance from the namespace's root: 0 for a root (the
	// L0 layer), 1 for a namespace below it (L1), and so on.
	Level int
	// Access is the user's own access to the namespace. It is empty for a
	// namespace they read only as an ancestor of one they belong to.
	Access Access
	// Via names that namespace below, for a place read only through it.
	Via string
	// Owns is true when u's memberships make them an owner here (see
	// Roles.Owns): they may approve their own proposals as owner.
	Owns bool
}

// webTokenPrefix starts the token id of a web principal. It is never the
// id of a stored token, which is hex.
const webTokenPrefix = "web:"

// Places lists the namespaces u can open, sorted by name: those where u has
// a role (their own, one raised by an owner above, or admin), and the
// ancestors those read, which u sees read-only through them.
func (r *Roles) Places(ctx context.Context, u User) ([]Place, error) {
	nss, err := r.namespaces.Namespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("places of %s: %w", u.ID, err)
	}
	ms, err := r.store.UserMemberships(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("places of %s: %w", u.ID, err)
	}
	roles := make(map[string]Role, len(ms))
	for _, m := range ms {
		roles[m.Namespace] = m.Role
	}
	byName := make(map[string]registry.Namespace, len(nss))
	for _, ns := range nss {
		byName[ns.Name] = ns
	}

	places := map[string]Place{}
	chains := map[string][]registry.Layer{}
	for _, ns := range nss {
		chain := chainOf(byName, ns.Name)
		chains[ns.Name] = chain
		if a := Resolve(u, chain, roles); a.CanRead() {
			member := Resolve(User{ID: u.ID}, chain, roles) // without the admin flag
			places[ns.Name] = Place{Namespace: ns, Level: len(chain) - 1, Access: a, Owns: member.Governs}
		}
	}
	// Namespaces are sorted by name, so the first team found reading an
	// ancestor is the one named in Via.
	for _, ns := range nss {
		if _, ok := places[ns.Name]; !ok || places[ns.Name].Via != "" {
			continue
		}
		for _, l := range chains[ns.Name][1:] {
			if _, ok := places[l.Namespace]; ok {
				continue
			}
			anc := byName[l.Namespace]
			places[l.Namespace] = Place{Namespace: anc, Level: len(chains[l.Namespace]) - 1, Via: ns.Name}
		}
	}
	out := make([]Place, 0, len(places))
	for _, p := range places {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Place) int { return strings.Compare(a.Namespace.Name, b.Namespace.Name) })
	return out, nil
}

// chainOf walks from name to its root in byName, closest first. A damaged
// tree (a missing parent, a cycle, too many layers) ends the walk early,
// which only ever gives less access.
func chainOf(byName map[string]registry.Namespace, name string) []registry.Layer {
	var out []registry.Layer
	for cur := name; cur != "" && len(out) < registry.MaxLayers; {
		ns, ok := byName[cur]
		if !ok || slices.ContainsFunc(out, func(l registry.Layer) bool { return l.Namespace == cur }) {
			break
		}
		out = append(out, registry.Layer{Namespace: ns.Name, Kind: ns.Kind, Depth: len(out)})
		cur = ns.Parent
	}
	return out
}

// Principal returns the registry principal u acts as in the web pages of
// namespace, with the place. It is u in that namespace with the scope a
// `loadout login` token would get there; for a place read through a
// namespace below, it is u in that one, which reads it as an ancestor. A
// namespace u cannot open is registry.ErrNotFound, so its existence does
// not leak.
func (r *Roles) Principal(ctx context.Context, u User, namespace string) (registry.Principal, Place, error) {
	places, err := r.Places(ctx, u)
	if err != nil {
		return registry.Principal{}, Place{}, err
	}
	i := slices.IndexFunc(places, func(p Place) bool { return p.Namespace.Name == namespace })
	if i < 0 {
		return registry.Principal{}, Place{}, fmt.Errorf("namespace %s: %w", namespace, registry.ErrNotFound)
	}
	p := places[i]
	acting, access := namespace, p.Access
	if p.Via != "" {
		acting = p.Via
		j := slices.IndexFunc(places, func(q Place) bool { return q.Namespace.Name == p.Via })
		access = places[j].Access
	}
	scope, ok := access.TokenScope()
	if !ok {
		return registry.Principal{}, Place{}, fmt.Errorf("namespace %s: %w", namespace, registry.ErrNotFound)
	}
	chain, err := registry.Chain(ctx, r.namespaces, acting)
	if err != nil {
		return registry.Principal{}, Place{}, fmt.Errorf("principal in %s: %w", acting, err)
	}
	return registry.Principal{
		TokenID: webTokenPrefix + u.ID,
		Team:    acting,
		Scope:   scope,
		Layers:  chain,
		Label:   u.Email + " on the web",
		UserID:  u.ID,
	}, p, nil
}

// Me returns the user behind userID and the places they can open, for a
// CLI that binds them all after `loadout login`.
func (s *Service) Me(ctx context.Context, userID string) (User, []Place, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return User{}, nil, fmt.Errorf("user %s: %w", userID, err)
	}
	places, err := s.roles.Places(ctx, u)
	if err != nil {
		return User{}, nil, err
	}
	return u, places, nil
}

// MemberDetail is a membership with what the team page shows about the
// person: their name and how they sign in.
type MemberDetail struct {
	Membership Membership
	Name       string
	// Google and Password say how the member signs in. A member added by
	// email who has not signed in yet has neither and is not Verified.
	Google   bool
	Password bool
	Verified bool
	// Teams are the other teams of the same project the person belongs
	// to, sorted, limited to those the viewer may open.
	Teams []string
}

// MemberDetails lists a namespace's members like Members, with each
// member's name and sign-in methods.
func (s *Service) MemberDetails(ctx context.Context, actor Actor, namespace string) ([]MemberDetail, error) {
	ms, err := s.Members(ctx, actor, namespace)
	if err != nil {
		return nil, err
	}
	tf, err := s.newTeamFinder(ctx, actor, namespace)
	if err != nil {
		return nil, err
	}
	out := make([]MemberDetail, 0, len(ms))
	for _, m := range ms {
		d, err := s.memberDetail(ctx, m)
		if err == nil {
			d.Teams, err = tf.teams(ctx, m.UserID)
		}
		if err != nil {
			return nil, fmt.Errorf("members of %s: %w", namespace, err)
		}
		out = append(out, d)
	}
	return out, nil
}

// InheritedMembers lists the people whose role in namespace comes from a
// namespace above it: the owners of each ancestor project, closest first,
// then by email. Each Membership is the one stored on the ancestor, and a
// person who owns several ancestors is listed once, for the closest.
// Members and maintainers of a project get no rights below it, so they
// are not listed. Whoever may list the namespace's members may list these.
func (s *Service) InheritedMembers(ctx context.Context, actor Actor, namespace string) ([]MemberDetail, error) {
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return nil, err
	}
	if !a.CanListMembers() {
		return nil, forbidden("listing members of " + namespace + " needs a role in it")
	}
	chain, err := registry.Chain(ctx, s.roles.namespaces, namespace)
	if err != nil {
		return nil, fmt.Errorf("members of %s: %w", namespace, err)
	}
	tf, err := s.newTeamFinder(ctx, actor, namespace)
	if err != nil {
		return nil, err
	}
	var out []MemberDetail
	seen := map[string]bool{}
	for _, l := range chain[1:] {
		ms, err := s.store.Members(ctx, l.Namespace)
		if err != nil {
			return nil, fmt.Errorf("members of %s: %w", l.Namespace, err)
		}
		for _, m := range ms {
			if m.Role != RoleOwner || seen[m.UserID] {
				continue
			}
			seen[m.UserID] = true
			d, err := s.memberDetail(ctx, m)
			if err == nil {
				d.Teams, err = tf.teams(ctx, m.UserID)
			}
			if err != nil {
				return nil, fmt.Errorf("members of %s: %w", l.Namespace, err)
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// teamFinder finds the teams a person belongs to in the project of one
// namespace, for the member lists. It remembers each namespace it looked
// at, since the people on one list share most of their teams.
type teamFinder struct {
	s     *Service
	actor Actor
	ns    string
	root  string
	shown map[string]bool // namespace -> listed for this viewer
}

func (s *Service) newTeamFinder(ctx context.Context, actor Actor, namespace string) (*teamFinder, error) {
	chain, err := registry.Chain(ctx, s.roles.namespaces, namespace)
	if err != nil {
		return nil, fmt.Errorf("members of %s: %w", namespace, err)
	}
	return &teamFinder{s: s, actor: actor, ns: namespace, root: chain[len(chain)-1].Namespace, shown: map[string]bool{}}, nil
}

// teams lists the teams userID belongs to besides f.ns, under the same
// top project, that the viewer may open.
func (f *teamFinder) teams(ctx context.Context, userID string) ([]string, error) {
	ms, err := f.s.store.UserMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range ms {
		if m.Namespace == f.ns {
			continue
		}
		show, ok := f.shown[m.Namespace]
		if !ok {
			if show, err = f.show(ctx, m.Namespace); err != nil {
				return nil, err
			}
			f.shown[m.Namespace] = show
		}
		if show {
			out = append(out, m.Namespace)
		}
	}
	slices.Sort(out)
	return out, nil
}

// show reports whether namespace is a team under f.root that the viewer
// may open.
func (f *teamFinder) show(ctx context.Context, namespace string) (bool, error) {
	chain, err := registry.Chain(ctx, f.s.roles.namespaces, namespace)
	if errors.Is(err, registry.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if chain[0].Kind != registry.KindTeam || chain[len(chain)-1].Namespace != f.root {
		return false, nil
	}
	a, err := f.s.access(ctx, f.actor, namespace)
	if errors.Is(err, registry.ErrForbidden) || errors.Is(err, registry.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.CanListMembers(), nil
}

// memberDetail adds the person's name and sign-in methods to m.
func (s *Service) memberDetail(ctx context.Context, m Membership) (MemberDetail, error) {
	u, err := s.store.UserByID(ctx, m.UserID)
	if err != nil {
		return MemberDetail{}, err
	}
	return MemberDetail{
		Membership: m,
		Name:       u.Name,
		Google:     u.GoogleSubject != "",
		Password:   u.PasswordHash != "",
		Verified:   u.Verified(),
	}, nil
}

// DisplayNames returns a name to show for each of the user ids that
// belongs to a user: their name, else their email. Unknown ids are left
// out.
func (s *Service) DisplayNames(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if _, done := out[id]; done || id == "" {
			continue
		}
		u, err := s.store.UserByID(ctx, id)
		if errors.Is(err, registry.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("user %s: %w", id, err)
		}
		out[id] = u.Email
		if u.Name != "" {
			out[id] = u.Name
		}
	}
	return out, nil
}
