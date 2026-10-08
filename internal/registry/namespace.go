package registry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/tier"
)

// MaxLayers is how many namespaces a chain may have: a token's own
// namespace plus at most three ancestors.
const MaxLayers = 4

// Reserved reports whether name is never given to a new namespace:
// loadout is the namespace of the CLI's built-in skills
// (loadout/loadout-setup), and skills is their page on the website, which
// sits above the teams in the sidebar. A namespace made before this rule
// keeps working.
func Reserved(name string) bool { return slices.Contains([]string{"loadout", "skills"}, name) }

// NamespaceKind says what a namespace is. It is stored, so it is a string.
type NamespaceKind string

// Namespace kinds: a team, or a project whose packages every team below it
// reads (the L0 above team L1s).
const (
	KindTeam    NamespaceKind = "team"
	KindProject NamespaceKind = "project"
)

// Valid reports whether k is a known kind.
func (k NamespaceKind) Valid() bool { return k == KindTeam || k == KindProject }

// Namespace is the first segment of a package id. It may have a parent,
// whose packages it can read.
type Namespace struct {
	Name string
	// Parent is empty for a root namespace.
	Parent    string
	Kind      NamespaceKind
	CreatedAt time.Time
	// RequireReview refuses direct publishes: every version goes through
	// propose and approve.
	RequireReview bool
	// Wiki shows the namespace's wiki page. Off by default; turning it off
	// hides the page and keeps its history.
	Wiki bool
	// Course is the knowledge base the wiki shows as its course; the zero
	// ID means none of its own (CourseOf may find one above).
	Course pack.ID
}

// NamespaceStore keeps namespaces. Implementations must be safe for
// concurrent use.
type NamespaceStore interface {
	// CreateNamespace stores ns, or returns ErrExists if the name is taken.
	CreateNamespace(ctx context.Context, ns Namespace) error
	// Namespace returns one namespace, or ErrNotFound.
	Namespace(ctx context.Context, name string) (Namespace, error)
	// Namespaces returns every namespace, sorted by name.
	Namespaces(ctx context.Context) ([]Namespace, error)
	// SetRequireReview turns review-only publishing on or off, or returns
	// ErrNotFound.
	SetRequireReview(ctx context.Context, name string, on bool) error
	// SetWiki turns the namespace's wiki on or off, or returns
	// ErrNotFound.
	SetWiki(ctx context.Context, name string, on bool) error
	// SetCourse sets the knowledge base the namespace's wiki shows as its
	// course; the zero ID clears it. ErrNotFound for a missing namespace.
	SetCourse(ctx context.Context, name string, course pack.ID) error
	// ModelTiers returns the namespace's own model table, empty when it
	// sets nothing, or ErrNotFound.
	ModelTiers(ctx context.Context, name string) (tier.Table, error)
	// SetModelTiers replaces the namespace's own model table in one step,
	// or returns ErrNotFound.
	SetModelTiers(ctx context.Context, name string, t tier.Table) error
	// DeleteNamespace removes an empty namespace with its tokens, model
	// tiers, memberships and wiki revisions. It returns ErrNotFound if it
	// does not exist and ErrNotEmpty if it has child namespaces, releases
	// or proposals; the check and the delete are one atomic step.
	DeleteNamespace(ctx context.Context, name string) error
}

// ErrNotEmpty means a namespace still has children, releases or proposals.
var ErrNotEmpty = errors.New("namespace is not empty")

// Layer is one namespace a principal can read.
type Layer struct {
	Namespace string
	Kind      NamespaceKind
	// Depth is 0 for the principal's own namespace, 1 for its parent, and
	// so on. When two layers ship an agent with the same name, the lower
	// depth wins.
	Depth int
}

// CreateNamespace adds a namespace under parent (empty for a root).
func (s *Service) CreateNamespace(ctx context.Context, name, parent string, kind NamespaceKind) (Namespace, error) {
	if err := ident.Check("namespace", name); err != nil {
		return Namespace{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if Reserved(name) {
		return Namespace{}, fmt.Errorf("%w: %s is reserved for loadout's own skills; choose another name", ErrInvalid, name)
	}
	if !kind.Valid() {
		return Namespace{}, fmt.Errorf("%w: kind %q: want team or project", ErrInvalid, kind)
	}
	if parent == name {
		return Namespace{}, fmt.Errorf("%w: namespace %s cannot be its own parent", ErrInvalid, name)
	}
	if parent != "" {
		chain, err := s.chain(ctx, parent)
		if errors.Is(err, ErrNotFound) {
			return Namespace{}, fmt.Errorf("%w: parent namespace %s does not exist", ErrInvalid, parent)
		}
		if err != nil {
			return Namespace{}, fmt.Errorf("create namespace %s: %w", name, err)
		}
		for _, l := range chain {
			if l.Namespace == name {
				return Namespace{}, fmt.Errorf("%w: %s is already an ancestor of %s", ErrInvalid, name, parent)
			}
		}
		if len(chain) >= MaxLayers {
			return Namespace{}, fmt.Errorf("%w: %s would be %d layers deep, the limit is %d", ErrInvalid, name, len(chain)+1, MaxLayers)
		}
	}
	ns := Namespace{Name: name, Parent: parent, Kind: kind, CreatedAt: s.now().UTC()}
	if err := s.namespaces.CreateNamespace(ctx, ns); err != nil {
		return Namespace{}, fmt.Errorf("create namespace %s: %w", name, err)
	}
	s.log.Info("namespace created", "namespace", name, "parent", parent, "kind", kind)
	return ns, nil
}

// Namespaces lists every namespace, sorted by name.
func (s *Service) Namespaces(ctx context.Context) ([]Namespace, error) {
	nss, err := s.namespaces.Namespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	return nss, nil
}

// Namespace returns one namespace, or ErrNotFound.
func (s *Service) Namespace(ctx context.Context, name string) (Namespace, error) {
	ns, err := s.namespaces.Namespace(ctx, name)
	if err != nil {
		return Namespace{}, fmt.Errorf("namespace %s: %w", name, err)
	}
	return ns, nil
}

// SetRequireReview turns review-only publishing of a namespace on or off.
func (s *Service) SetRequireReview(ctx context.Context, name string, on bool) (Namespace, error) {
	if err := s.namespaces.SetRequireReview(ctx, name, on); err != nil {
		return Namespace{}, fmt.Errorf("namespace %s: require review: %w", name, err)
	}
	s.log.Info("namespace review setting changed", "namespace", name, "require_review", on)
	return s.Namespace(ctx, name)
}

// SetWiki turns the wiki of a namespace on or off.
func (s *Service) SetWiki(ctx context.Context, name string, on bool) (Namespace, error) {
	if err := s.namespaces.SetWiki(ctx, name, on); err != nil {
		return Namespace{}, fmt.Errorf("namespace %s: wiki: %w", name, err)
	}
	s.log.Info("namespace wiki setting changed", "namespace", name, "wiki", on)
	return s.Namespace(ctx, name)
}

// DeleteNamespace removes an empty namespace (no children, releases or
// proposals) with its tokens, memberships and wiki.
func (s *Service) DeleteNamespace(ctx context.Context, name string) error {
	if err := s.namespaces.DeleteNamespace(ctx, name); err != nil {
		if errors.Is(err, ErrNotEmpty) {
			return fmt.Errorf("%w: %w: delete its children, packages and proposals first", ErrInvalid, err)
		}
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	s.log.Info("namespace deleted", "namespace", name)
	return nil
}

// Layers returns the namespaces the principal reads, closest first.
func (s *Service) Layers(_ context.Context, pr Principal) ([]Layer, error) {
	if !pr.Narrowed() {
		return nil, errNoView
	}
	return pr.layers(), nil
}

// ensureNamespace creates name as a root team if it does not exist, which
// keeps "token create --team X" working without a namespace step.
func (s *Service) ensureNamespace(ctx context.Context, name string) error {
	_, err := s.namespaces.Namespace(ctx, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("namespace %s: %w", name, err)
	}
	ns := Namespace{Name: name, Kind: KindTeam, CreatedAt: s.now().UTC()}
	err = s.namespaces.CreateNamespace(ctx, ns)
	if errors.Is(err, ErrExists) {
		return nil // created by a concurrent call
	}
	if err != nil {
		return fmt.Errorf("create namespace %s: %w", name, err)
	}
	s.log.Info("namespace created", "namespace", name, "kind", KindTeam)
	return nil
}

// readChain is chain for a token's namespace. A namespace with no record
// (a token created before namespaces existed) reads as a root team.
func (s *Service) readChain(ctx context.Context, name string) ([]Layer, error) {
	chain, err := s.chain(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return []Layer{{Namespace: name, Kind: KindTeam}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("layers of %s: %w", name, err)
	}
	return chain, nil
}

func (s *Service) chain(ctx context.Context, name string) ([]Layer, error) {
	return Chain(ctx, s.namespaces, name)
}

// Chain walks from name up to its root and returns the namespaces, name
// first. It returns ErrNotFound if name does not exist. A missing ancestor,
// a cycle or a chain over MaxLayers can only come from a damaged store and
// is a plain error.
func Chain(ctx context.Context, namespaces NamespaceStore, name string) ([]Layer, error) {
	var out []Layer
	for cur := name; cur != ""; {
		for _, l := range out {
			if l.Namespace == cur {
				return nil, fmt.Errorf("namespace %s: parent cycle through %s", name, cur)
			}
		}
		if len(out) == MaxLayers {
			return nil, fmt.Errorf("namespace %s: more than %d layers", name, MaxLayers)
		}
		ns, err := namespaces.Namespace(ctx, cur)
		if errors.Is(err, ErrNotFound) && len(out) > 0 {
			return nil, fmt.Errorf("namespace %s: parent %s is missing", name, cur)
		}
		if err != nil {
			return nil, fmt.Errorf("namespace %s: %w", cur, err)
		}
		out = append(out, Layer{Namespace: ns.Name, Kind: ns.Kind, Depth: len(out)})
		cur = ns.Parent
	}
	return out, nil
}
