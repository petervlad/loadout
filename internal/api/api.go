// Package api defines the JSON wire types of the registry HTTP API, shared by
// the server and the client.
//
// Package bundles travel as raw bytes of type bundle.ContentType; everything
// else is JSON. Errors are always an ErrorResponse with one of the Code
// constants, so clients can react to them without parsing messages.
//
// The package depends on nothing else in loadout, so the client never pulls
// in server-side code.
package api

import (
	"errors"
	"time"
)

// Error codes carried in ErrorBody.Code.
const (
	CodeNotFound        = "not_found"
	CodeVersionConflict = "version_conflict"
	CodeUnauthorized    = "unauthorized"
	CodeForbidden       = "forbidden"
	CodeInvalid         = "invalid"
	CodeTooLarge        = "too_large"
	CodeInternal        = "internal"
)

// ErrNotFound matches, with errors.Is, a response carrying CodeNotFound, so
// callers can detect it without depending on the client's error type.
var ErrNotFound = errors.New("not found")

// Response headers sent with a bundle download.
const (
	// HeaderDigest carries the content digest of the bundle's package.
	HeaderDigest = "X-Loadout-Digest"
	// HeaderVersion carries the version of the bundle's package, which
	// resolves "latest" for the caller.
	HeaderVersion = "X-Loadout-Version"
)

// Release is one published version of a package.
type Release struct {
	// ID is the package id as "team/name".
	ID          string `json:"id"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	Description string `json:"description"`
	// Kind is agent, skill or kb; empty from a registry older than skills,
	// which means agent.
	Kind        string    `json:"kind,omitempty"`
	Size        int       `json:"size"`
	PublishedAt time.Time `json:"published_at"`
	Seq         int64     `json:"seq"`
	// Proposal is the id of the proposal it was approved from, empty for
	// a direct publish or from an older registry.
	Proposal string `json:"proposal,omitempty"`
	// SelfApproved is true when its author approved it as an owner,
	// without a second person's review.
	SelfApproved bool `json:"self_approved,omitempty"`
	// PublishedBy is who released it: the publisher, or the approver of
	// a proposal. It is left out when nobody is known (a direct publish
	// from before the registry kept it) and by older registries.
	PublishedBy *Actor `json:"published_by,omitempty"`
}

// PackagesResponse is the body of GET /v1/packages: the latest release of
// each package of the caller's team.
type PackagesResponse struct {
	Packages []Release `json:"packages"`
}

// ReleasesResponse is the body of GET /v1/packages/{team}/{name}: every
// release of one package, lowest version first.
type ReleasesResponse struct {
	Releases []Release `json:"releases"`
}

// PublishResponse is the body of a successful POST /v1/packages.
type PublishResponse struct {
	Release Release `json:"release"`
}

// ChangesResponse is the body of GET /v1/changes. Next is the value to pass
// as since on the following call.
type ChangesResponse struct {
	Changes []Release `json:"changes"`
	Next    int64     `json:"next"`
}

// HealthResponse is the body of GET /v1/health.
type HealthResponse struct {
	Status string `json:"status"`
	// Version is only set for callers that send a valid token.
	Version string `json:"version,omitempty"`
}

// ErrorResponse is the body of every error response.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody describes an error: a stable Code and a human-readable Message.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Newest is the wiki's newest revision, sent with CodeStale.
	Newest *WikiRevision `json:"newest,omitempty"`
}

// LayerKind says what a namespace is: a team, or a project whose layer every
// member team reads (the L0 above team L1s).
type LayerKind string

// Layer kinds.
const (
	LayerTeam    LayerKind = "team"
	LayerProject LayerKind = "project"
)

// Layer is one namespace a token can read, closest first.
type Layer struct {
	Namespace string    `json:"namespace"`
	Kind      LayerKind `json:"kind"`
	// Depth is 0 for the token's own namespace, 1 for its parent, and so on.
	// When two layers ship an agent with the same name, the lower depth wins.
	Depth int `json:"depth"`
}

// LayersResponse is the body of GET /v1/layers.
type LayersResponse struct {
	Layers []Layer `json:"layers"`
}

// HeaderNamespace names the namespace a request with an account token is
// about: the team whose view of the registry (its packages and the layers
// above it) the client works in. A namespace token ignores it.
const HeaderNamespace = "X-Loadout-Namespace"

// What a registry says about loadout CLI releases, on every response to a
// request with a valid token. Each holds MAJOR.MINOR.PATCH and nothing
// else; a CLI that does not know them ignores them.
const (
	// HeaderCLILatest is the newest loadout release the registry
	// recommends. Absent: it says nothing.
	HeaderCLILatest = "X-Loadout-CLI-Latest"
	// HeaderCLIMinimum is the oldest loadout release the registry still
	// supports. Absent: any.
	HeaderCLIMinimum = "X-Loadout-CLI-Minimum"
)

// AccountNamespace is the namespace a `loadout login` asks for to get an
// account token, which acts in every namespace its user belongs to.
const AccountNamespace = "*"

// MeResponse is the body of GET /v1/me: who an account token belongs to
// and every namespace they can open.
type MeResponse struct {
	Email      string        `json:"email"`
	Namespaces []MeNamespace `json:"namespaces"`
}

// MeNamespace is one namespace the user can open. Role is their role there
// (member, maintainer, owner or admin); Via names the namespace below
// through which they read it, for a layer they are not a member of.
type MeNamespace struct {
	Name   string    `json:"name"`
	Kind   LayerKind `json:"kind"`
	Parent string    `json:"parent,omitempty"`
	Role   string    `json:"role,omitempty"`
	Via    string    `json:"via,omitempty"`
}

// CodeTooManyRequests is returned when a client is rate limited.
const CodeTooManyRequests = "too_many_requests"

// DeviceStartRequest is the body of POST /v1/auth/device, which starts a
// `loadout login`. An empty Namespace lets the person choose one of their
// memberships when approving.
type DeviceStartRequest struct {
	Namespace string `json:"namespace,omitempty"`
}

// DeviceStartResponse tells the CLI which code the person approves where,
// and how often to poll. Durations are in seconds.
type DeviceStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenRequest is the body of POST /v1/auth/device/token.
type DeviceTokenRequest struct {
	DeviceCode string `json:"device_code"`
}

// Device authorization statuses in DeviceTokenResponse.
const (
	DevicePending  = "authorization_pending"
	DeviceSlowDown = "slow_down"
	DeviceExpired  = "expired"
	DeviceDenied   = "denied"
	DeviceApproved = "approved"
)

// DeviceTokenResponse answers one poll. Token, Namespace, Role and
// ExpiresAt are set once, when Status is DeviceApproved; Token is a normal
// namespace token that stops working at ExpiresAt.
type DeviceTokenResponse struct {
	Status    string    `json:"status"`
	Token     string    `json:"token,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
	Role      string    `json:"role,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// NamespaceRequest is the body of POST /v1/namespaces.
type NamespaceRequest struct {
	Name   string    `json:"name"`
	Parent string    `json:"parent,omitempty"`
	Kind   LayerKind `json:"kind"`
}

// Namespace describes one namespace.
type Namespace struct {
	Name          string    `json:"name"`
	Parent        string    `json:"parent,omitempty"`
	Kind          LayerKind `json:"kind"`
	RequireReview bool      `json:"require_review"`
	Wiki          bool      `json:"wiki"`
	// Course is the knowledge base the wiki shows as its course, as
	// "team/name"; empty when the namespace sets none of its own.
	Course string `json:"course,omitempty"`
	// CourseAbove and CourseFrom name the course the wiki shows while
	// Course is empty: that of the closest namespace above that sets one.
	// Only the answer to a PATCH that clears the course has them.
	CourseAbove string    `json:"course_above,omitempty"`
	CourseFrom  string    `json:"course_from,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// NamespaceSettings is the body of PATCH /v1/namespaces/{ns}; unset fields
// stay as they are.
type NamespaceSettings struct {
	RequireReview *bool `json:"require_review,omitempty"`
	Wiki          *bool `json:"wiki,omitempty"`
	// Course is a knowledge base as "team/name"; "" clears it.
	Course *string `json:"course,omitempty"`
}

// Member is one member of a namespace. Role is member, maintainer or
// owner.
type Member struct {
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// MembersResponse is the body of GET /v1/namespaces/{ns}/members.
type MembersResponse struct {
	Members []Member `json:"members"`
}

// MemberRequest is the body of POST /v1/namespaces/{ns}/members (Email and
// Role) and PATCH /v1/namespaces/{ns}/members/{email} (Role).
type MemberRequest struct {
	Email string `json:"email,omitempty"`
	Role  string `json:"role"`
}

// MemberResponse answers a member change. Created is true when the person
// had no account yet.
type MemberResponse struct {
	Member  Member `json:"member"`
	Created bool   `json:"created,omitempty"`
}

// InviteRequest is the body of POST /v1/namespaces/{project}/invite: a
// role in the project and, by team name, roles in some of its teams.
type InviteRequest struct {
	Email string            `json:"email"`
	Role  string            `json:"role"`
	Teams map[string]string `json:"teams,omitempty"`
}

// InviteResponse says what an invitation did in each namespace, the
// sign-in link to pass on, and whether the registry mailed it.
type InviteResponse struct {
	Email   string  `json:"email"`
	Project string  `json:"project"`
	Grants  []Grant `json:"grants"`
	Link    string  `json:"link,omitempty"`
	Mailed  bool    `json:"mailed"`
}

// Grant is one namespace of an invitation. Result is "added", "raised" or
// "kept" (the person already had Role, the asked role or a higher one).
type Grant struct {
	Namespace string `json:"namespace"`
	Asked     string `json:"asked"`
	Role      string `json:"role"`
	Was       string `json:"was,omitempty"`
	Result    string `json:"result"`
}

// RevokedResponse says how many tokens a removal revoked.
type RevokedResponse struct {
	Revoked int `json:"revoked"`
}

// Token describes a machine token, without its secret.
type Token struct {
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	Scope     string    `json:"scope"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// TokensResponse is the body of GET /v1/namespaces/{ns}/tokens.
type TokensResponse struct {
	Tokens []Token `json:"tokens"`
}

// TokenRequest is the body of POST /v1/namespaces/{ns}/tokens.
type TokenRequest struct {
	Scope string `json:"scope"`
	Label string `json:"label,omitempty"`
}

// TokenCreated carries a new machine token's secret, shown once.
type TokenCreated struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

// TierCell is one cell of a model table: what an AI tool runs for an
// agent's hint. Tool is a target name (claude, copilot), Hint fast,
// balanced or deep; Model and Effort may each be empty.
type TierCell struct {
	Tool   string `json:"tool"`
	Hint   string `json:"hint"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// ModelFrom and EffortFrom name the namespace each value comes from,
	// in a resolved table.
	ModelFrom  string `json:"model_from,omitempty"`
	EffortFrom string `json:"effort_from,omitempty"`
}

// TiersResponse is the body of GET /v1/tiers (the request's view) and of
// PUT /v1/namespaces/{ns}/tiers: the resolved model table of Namespace,
// and Digest, which changes whenever the table does (empty for an empty
// table).
type TiersResponse struct {
	Namespace string     `json:"namespace"`
	Cells     []TierCell `json:"cells"`
	Digest    string     `json:"digest,omitempty"`
}

// TiersRequest is the body of PUT /v1/namespaces/{ns}/tiers: every cell the
// namespace sets itself, replacing the ones it had.
type TiersRequest struct {
	Cells []TierCell `json:"cells"`
}
