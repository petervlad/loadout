package account

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"

	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/registry"
)

// GrantResult says what an invitation did in one namespace.
type GrantResult string

// Grant results.
const (
	// GrantAdded: the person had no role there and now has the one asked.
	GrantAdded GrantResult = "added"
	// GrantRaised: they had a lower role, now raised to the one asked.
	GrantRaised GrantResult = "raised"
	// GrantKept: they already had the role asked or a higher one, which an
	// invitation never lowers.
	GrantKept GrantResult = "kept"
)

// Grant is what an invitation did in one namespace.
type Grant struct {
	Namespace string
	// Asked is the role the invitation asked for; Role is the role the
	// person has now, and Was the one they had before ("" for none).
	Asked  Role
	Role   Role
	Was    Role
	Result GrantResult
}

// Invitation is the result of InviteToProject.
type Invitation struct {
	Email   string
	Project string
	// Grants holds the project first, then its teams by name.
	Grants []Grant
	// Link is the registry's sign-in page with the address filled in and
	// the project as the page to open next. It carries no secret: it only
	// helps the person sign up with the right address. Empty without a
	// public URL.
	Link string
	// Mailed reports whether an invitation mail was queued. It is sent only
	// when something changed and a mailer is configured.
	Mailed bool
}

// Changed reports whether the invitation added or raised any role.
func (inv Invitation) Changed() bool {
	return slices.ContainsFunc(inv.Grants, func(g Grant) bool { return g.Result != GrantKept })
}

// InviteToProject gives the person with email a role in project and roles
// in some of its teams, in one step. Every grant follows the rules of
// AddMember (maintainers add members, those who govern add maintainers,
// admins add owners), and a role the person already has is raised, never
// lowered. It is all or nothing: every grant is checked before anything is
// written, and the memberships are written in one store transaction. An
// address without an account gets one without a password, as with
// AddMember; that account is created just before the transaction, so a
// failed write can leave it behind with no role, which gives no access and
// is what signing up would create anyway. The result never says whether
// the address had an account.
func (s *Service) InviteToProject(ctx context.Context, actor Actor, project, email string, projectRole Role, teams map[string]Role) (Invitation, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return Invitation{}, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	ns, err := s.namespace(ctx, project)
	if err != nil {
		return Invitation{}, fmt.Errorf("invite to %s: %w", project, err)
	}
	if ns.Kind != registry.KindProject {
		return Invitation{}, fmt.Errorf("%w: invitations are made from a project; %s is a %s, so add members to it directly", registry.ErrInvalid, project, ns.Kind)
	}
	asked := []Grant{{Namespace: project, Asked: projectRole}}
	for _, name := range slices.Sorted(maps.Keys(teams)) {
		asked = append(asked, Grant{Namespace: name, Asked: teams[name]})
	}

	// Check everything first: the roles, the teams, and the actor's right
	// to give each role.
	access := make([]Access, len(asked))
	for i, g := range asked {
		if i > 0 {
			if err := s.checkTeamOf(ctx, g.Namespace, project); err != nil {
				return Invitation{}, err
			}
		}
		if !g.Asked.Valid() {
			return Invitation{}, fmt.Errorf("%w: role %q in %s: want member, maintainer or owner", registry.ErrInvalid, g.Asked, g.Namespace)
		}
		if i > 0 {
			if g.Asked == RoleOwner {
				return Invitation{}, fmt.Errorf("%w: owner is a role on projects; give it in %s, which makes them an owner of every team in it", registry.ErrInvalid, project)
			}
		}
		if access[i], err = s.access(ctx, actor, g.Namespace); err != nil {
			return Invitation{}, err
		}
		if !access[i].CanAssign(g.Asked) {
			return Invitation{}, forbidden(fmt.Sprintf("you cannot give the %s role in %s, so nothing was changed", g.Asked, g.Namespace))
		}
	}

	u, err := s.store.UserByEmail(ctx, email)
	exists := err == nil
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		return Invitation{}, fmt.Errorf("invite to %s: %w", project, err)
	}
	for i := range asked {
		g := &asked[i]
		g.Role, g.Result = g.Asked, GrantAdded
		if !exists {
			continue
		}
		old, err := s.store.Membership(ctx, u.ID, g.Namespace)
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return Invitation{}, fmt.Errorf("invite to %s: %w", g.Namespace, err)
		}
		g.Was = old.Role
		switch {
		case old.Role == "":
		case old.Role.AtLeast(g.Asked):
			g.Role, g.Result = old.Role, GrantKept
		case !access[i].CanAssign(old.Role):
			return Invitation{}, forbidden(fmt.Sprintf("%s is a %s of %s, which you cannot change, so nothing was changed", email, old.Role, g.Namespace))
		default:
			g.Result = GrantRaised
		}
	}

	var writes []Membership
	for _, g := range asked {
		if g.Result != GrantKept {
			writes = append(writes, Membership{Namespace: g.Namespace, Role: g.Role, CreatedAt: s.now().UTC()})
		}
	}
	if len(writes) > 0 {
		if !exists {
			if u, err = s.userForEmail(ctx, email); err != nil {
				return Invitation{}, fmt.Errorf("invite to %s: %w", project, err)
			}
		}
		for i := range writes {
			writes[i].UserID, writes[i].Email = u.ID, u.Email
		}
		if err := s.store.PutMembers(ctx, writes); err != nil {
			return Invitation{}, fmt.Errorf("invite to %s: nothing was changed: %w", project, err)
		}
		for _, m := range writes {
			s.log.InfoContext(ctx, "member role set", "user_id", u.ID, "namespace", m.Namespace, "role", m.Role, "by", actorLog(actor), "via", "invite")
		}
	}

	inv := Invitation{Email: email, Project: project, Grants: asked, Link: s.signInLink(email, project)}
	if inv.Changed() && s.mailer != nil {
		inviter, err := s.inviterName(ctx, actor)
		if err != nil {
			return inv, fmt.Errorf("invite to %s: the roles were given, but the mail was not sent: %w", project, err)
		}
		m := invitationMail(email, inviter, inv)
		if err := s.mailer.Send(ctx, m); err != nil {
			s.log.ErrorContext(ctx, "queue invitation email", "to", mail.Redact(email), "err", err)
		} else {
			inv.Mailed = true
		}
	}
	return inv, nil
}

// checkTeamOf checks that name is a team directly in project. Any other
// name, existing or not, gets the same answer, so the check does not tell
// which namespaces exist outside the project.
func (s *Service) checkTeamOf(ctx context.Context, name, project string) error {
	ns, err := s.namespace(ctx, name)
	if err != nil && !errors.Is(err, registry.ErrNotFound) && !errors.Is(err, registry.ErrInvalid) {
		return fmt.Errorf("invite to %s: %w", name, err)
	}
	if err != nil || ns.Kind != registry.KindTeam || ns.Parent != project {
		return fmt.Errorf("%w: %s is not a team in %s", registry.ErrInvalid, name, project)
	}
	return nil
}

// signInLink is the sign-in page with the address filled in, opening the
// project afterwards.
func (s *Service) signInLink(email, project string) string {
	if s.publicURL == "" {
		return ""
	}
	return s.publicURL + "/auth/login?" + url.Values{"email": {email}, "next": {"/t/" + project}}.Encode()
}

// inviterName names the actor in an invitation mail: their name and
// address, or the registry for the operator.
func (s *Service) inviterName(ctx context.Context, actor Actor) (string, error) {
	if actor.Operator || actor.UserID == "" {
		return "The registry's admin", nil
	}
	u, err := s.store.UserByID(ctx, actor.UserID)
	if err != nil {
		return "", err
	}
	if u.Name == "" {
		return u.Email, nil
	}
	return u.Name + " (" + u.Email + ")", nil
}
