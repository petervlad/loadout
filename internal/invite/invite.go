// Package invite encodes and decodes team invites: one string that carries
// everything a teammate needs to join a team's registry.
//
// An invite is "ldi_" followed by the unpadded base64url encoding of a JSON
// object {"v":1,"url":...,"namespace":...,"token":...,"role":...}. It contains
// an access token, so it is a secret: it is never logged, never written to
// config.yaml and never passed on a command line. String and LogValue redact
// the token so an Invite printed or logged by mistake does not leak it.
package invite

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/petervlad/loadout/internal/ident"
)

// Prefix starts every invite, so it is recognisable and never mistaken for a
// bare token.
const Prefix = "ldi_"

// Version is the only invite format this build reads and writes.
const Version = 1

// tokenPrefix matches registry.TokenPrefix; it is repeated here so the
// client side does not import server-side code (a test keeps them equal).
const tokenPrefix = "ldt_"

// Limits on untrusted input.
const (
	maxEncodedLen = 4096
	maxTokenLen   = 256
)

// ErrMalformed is returned for input that is not a well-formed invite. Its
// message never contains the input, which may hold a token.
var ErrMalformed = errors.New("malformed invite")

// VersionError is returned for an invite written by a newer or older loadout.
type VersionError struct {
	Got int
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("invite format version %d is not supported (want %d); upgrade loadout or ask for a new invite", e.Got, Version)
}

// Role is what the invite's token may do. Empty means unknown.
type Role string

// Roles.
const (
	RoleRead    Role = "read"
	RolePublish Role = "publish"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleRead || r == RolePublish }

// Invite is a decoded invite.
type Invite struct {
	// URL is the registry's public base URL, http(s)://host[:port][/path].
	URL string
	// Namespace is the team the token belongs to.
	Namespace string
	// Token is the access token. It is a secret.
	Token string
	// Role is optional; it is only shown to the person joining.
	Role Role
}

// wire is the JSON shape inside an invite.
type wire struct {
	V         int    `json:"v"`
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
	Token     string `json:"token"`
	Role      Role   `json:"role,omitempty"`
}

// Validate checks every field. Errors never contain the token.
func (i Invite) Validate() error {
	if err := checkURL(i.URL); err != nil {
		return err
	}
	if err := ident.Check("team", i.Namespace); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := checkToken(i.Token); err != nil {
		return err
	}
	if i.Role != "" && !i.Role.Valid() {
		return fmt.Errorf("%w: role %q: want read or publish", ErrMalformed, i.Role)
	}
	return nil
}

// Encode returns the invite string.
func (i Invite) Encode() (string, error) {
	i.URL = strings.TrimRight(i.URL, "/")
	if err := i.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(wire{V: Version, URL: i.URL, Namespace: i.Namespace, Token: i.Token, Role: i.Role})
	if err != nil {
		return "", fmt.Errorf("encode invite: %w", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// Decode parses and validates an invite string. Surrounding whitespace is
// ignored.
func Decode(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	switch {
	case len(s) > maxEncodedLen:
		return Invite{}, fmt.Errorf("%w: longer than %d characters", ErrMalformed, maxEncodedLen)
	case strings.HasPrefix(s, tokenPrefix):
		return Invite{}, fmt.Errorf("%w: this is a bare token, not an invite; use: loadout remote add NAME URL --token-stdin", ErrMalformed)
	case !strings.HasPrefix(s, Prefix):
		return Invite{}, fmt.Errorf("%w: it must start with %s", ErrMalformed, Prefix)
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(s, Prefix))
	if err != nil {
		return Invite{}, fmt.Errorf("%w: not base64url", ErrMalformed) // the error would quote the input
	}

	// Read the version first, so a newer format with other fields is
	// reported as a version problem rather than as garbage.
	var head struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Invite{}, fmt.Errorf("%w: not JSON", ErrMalformed)
	}
	if head.V != Version {
		return Invite{}, &VersionError{Got: head.V}
	}

	var w wire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Invite{}, fmt.Errorf("%w: unexpected content", ErrMalformed)
	}
	if dec.More() {
		return Invite{}, fmt.Errorf("%w: trailing data", ErrMalformed)
	}
	inv := Invite{URL: strings.TrimRight(w.URL, "/"), Namespace: w.Namespace, Token: w.Token, Role: w.Role}
	if err := inv.Validate(); err != nil {
		return Invite{}, err
	}
	return inv, nil
}

// String describes the invite without its token.
func (i Invite) String() string {
	return fmt.Sprintf("invite(%s on %s)", i.Namespace, i.URL)
}

// LogValue keeps the token out of logs.
func (i Invite) LogValue() slog.Value {
	return slog.GroupValue(slog.String("url", i.URL), slog.String("namespace", i.Namespace), slog.String("role", string(i.Role)))
}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("%w: registry url is not a URL", ErrMalformed)
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%w: registry url: want http(s)://host[:port]", ErrMalformed)
	case u.Host == "":
		return fmt.Errorf("%w: registry url has no host", ErrMalformed)
	case u.User != nil || u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("%w: registry url must not contain credentials, a query or a fragment", ErrMalformed)
	}
	return nil
}

func checkToken(t string) error {
	switch {
	case !strings.HasPrefix(t, tokenPrefix) || len(t) == len(tokenPrefix):
		return fmt.Errorf("%w: no valid token", ErrMalformed)
	case len(t) > maxTokenLen:
		return fmt.Errorf("%w: token is too long", ErrMalformed)
	}
	for _, r := range t {
		if r <= ' ' || r > '~' {
			return fmt.Errorf("%w: token contains invalid characters", ErrMalformed)
		}
	}
	return nil
}
