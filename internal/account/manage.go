package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
)

// CreateNamespace creates a namespace for actor: admins create projects,
// those who govern a project create teams in it. The tree has two layers:
// projects (L0) have no parent, and every team (L1) is in a project.
func (s *Service) CreateNamespace(ctx context.Context, actor Actor, name, parent string, kind registry.NamespaceKind) (registry.Namespace, error) {
	if err := s.canCreate(ctx, actor, parent, kind); err != nil {
		return registry.Namespace{}, err
	}
	if err := s.checkShape(ctx, parent, kind); err != nil {
		return registry.Namespace{}, err
	}
	ns, err := s.reg.CreateNamespace(ctx, name, parent, kind)
	if err != nil {
		return registry.Namespace{}, err
	}
	s.log.InfoContext(ctx, "namespace created on request", "namespace", name, "by", actorLog(actor))
	// The person who creates a project owns it: it shows in their sidebar
	// and stays theirs if they stop being an admin.
	if kind == registry.KindProject && actor.UserID != "" {
		u, err := s.store.UserByID(ctx, actor.UserID)
		if err != nil {
			return ns, fmt.Errorf("project %s was created, but its owner could not be set: %w", name, err)
		}
		m := Membership{UserID: u.ID, Email: u.Email, Namespace: name, Role: RoleOwner, CreatedAt: s.now().UTC()}
		if err := s.store.PutMember(ctx, m); err != nil {
			return ns, fmt.Errorf("project %s was created, but its owner could not be set: %w", name, err)
		}
		s.log.InfoContext(ctx, "member role set", "user_id", u.ID, "namespace", name, "role", RoleOwner, "by", "creator")
	}
	return ns, nil
}

func (s *Service) canCreate(ctx context.Context, actor Actor, parent string, kind registry.NamespaceKind) error {
	if parent == "" {
		a, err := s.access(ctx, actor, "")
		if err != nil {
			return err
		}
		if a.Role != RoleAdmin {
			return forbidden("only admins create namespaces without a parent")
		}
		return nil
	}
	p, err := s.namespace(ctx, parent)
	if errors.Is(err, registry.ErrNotFound) {
		return fmt.Errorf("%w: parent namespace %s does not exist", registry.ErrInvalid, parent)
	}
	if err != nil {
		return err
	}
	a, err := s.access(ctx, actor, parent)
	if err != nil {
		return err
	}
	if !a.CanCreateChild(p.Kind, kind) {
		return forbidden(fmt.Sprintf("creating a %s under %s needs an owner of the project %s (or an admin)", kind, parent, parent))
	}
	return nil
}

// checkShape keeps the tree at two layers: a project at the top, its teams
// right below it.
func (s *Service) checkShape(ctx context.Context, parent string, kind registry.NamespaceKind) error {
	switch {
	case kind == registry.KindProject && parent != "":
		return fmt.Errorf("%w: a project is the top layer (L0) and has no parent", registry.ErrInvalid)
	case kind == registry.KindTeam && parent == "":
		return fmt.Errorf("%w: a team belongs to a project; choose one with --parent", registry.ErrInvalid)
	case kind == registry.KindTeam:
		p, err := s.namespace(ctx, parent)
		if err != nil {
			return err
		}
		if p.Kind != registry.KindProject {
			return fmt.Errorf("%w: %s is a team; a team belongs to a project, not to another team", registry.ErrInvalid, parent)
		}
	}
	return nil
}

// DeleteNamespace deletes an empty namespace; only admins may.
func (s *Service) DeleteNamespace(ctx context.Context, actor Actor, name string) error {
	a, err := s.access(ctx, actor, name)
	if err != nil {
		return err
	}
	if a.Role != RoleAdmin {
		return forbidden("only admins delete namespaces, signed in on the web or on the registry host")
	}
	return s.reg.DeleteNamespace(ctx, name)
}

// SetRequireReview turns review-only publishing of a namespace on or off;
// owners (of it or a project above it) and admins may.
func (s *Service) SetRequireReview(ctx context.Context, actor Actor, name string, on bool) (registry.Namespace, error) {
	if _, err := s.namespace(ctx, name); err != nil {
		return registry.Namespace{}, err
	}
	a, err := s.access(ctx, actor, name)
	if err != nil {
		return registry.Namespace{}, err
	}
	if !a.CanSetReview() {
		return registry.Namespace{}, forbidden("changing review rules of " + name + " needs an owner (or an admin)")
	}
	return s.reg.SetRequireReview(ctx, name, on)
}

// CreateMachineToken creates a token without a user (for CI or a shared
// machine) in namespace; maintainers and above may. The token records who
// made it: it can never approve or reject a proposal, and four eyes treat
// it as its maker. The secret is shown once.
func (s *Service) CreateMachineToken(ctx context.Context, actor Actor, namespace string, scope registry.TokenScope, label string) (string, registry.Token, error) {
	if err := s.canManageTokens(ctx, actor, namespace); err != nil {
		return "", registry.Token{}, err
	}
	return s.reg.CreateMachineToken(ctx, namespace, scope, label, actor.UserID)
}

// MachineTokens lists a namespace's tokens without a user.
func (s *Service) MachineTokens(ctx context.Context, actor Actor, namespace string) ([]registry.Token, error) {
	if err := s.canManageTokens(ctx, actor, namespace); err != nil {
		return nil, err
	}
	toks, err := s.reg.Tokens(ctx, namespace)
	if err != nil {
		return nil, err
	}
	out := toks[:0]
	for _, t := range toks {
		if t.UserID == "" {
			out = append(out, t)
		}
	}
	return out, nil
}

// RevokeMachineToken revokes one of a namespace's tokens without a user.
func (s *Service) RevokeMachineToken(ctx context.Context, actor Actor, namespace, id string) error {
	toks, err := s.MachineTokens(ctx, actor, namespace)
	if err != nil {
		return err
	}
	for _, t := range toks {
		if t.ID == id {
			return s.reg.RevokeToken(ctx, id)
		}
	}
	return fmt.Errorf("no machine token %s in %s: %w", id, namespace, registry.ErrNotFound)
}

func (s *Service) canManageTokens(ctx context.Context, actor Actor, namespace string) error {
	if _, err := s.namespace(ctx, namespace); err != nil {
		return err
	}
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return err
	}
	if !a.CanManageTokens() {
		return forbidden("managing tokens of " + namespace + " needs the maintainer role")
	}
	return nil
}
