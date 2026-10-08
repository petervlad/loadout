package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/ratelimit"
	"github.com/petervlad/loadout/internal/registry"
)

// Auth routes:
//
//	DELETE /v1/auth/token          revoke the calling token, if loadout login issued it
//	DELETE /v1/auth/tokens         sign the caller's user out everywhere (RevokedResponse)
//	GET    /v1/me                  the caller's user and namespaces (MeResponse)
//	POST   /v1/auth/device         DeviceStartRequest in, DeviceStartResponse out
//	POST   /v1/auth/device/token   DeviceTokenRequest in, DeviceTokenResponse out
//
// The device routes exist only with WithDeviceFlow. WithPages mounts the
// web pages (/auth/..., /account, the team pages under /t/ and /, and
// /skills) on the same handler.

// Device runs the device flow of `loadout login`.
type Device interface {
	StartDevice(ctx context.Context, namespace, client string) (account.DeviceStart, error)
	PollDevice(ctx context.Context, deviceCode string) (account.DevicePoll, error)
	SignOutEverywhere(ctx context.Context, userID string) (sessions, tokens int, err error)
	Me(ctx context.Context, userID string) (account.User, []account.Place, error)
}

var _ Device = (*account.Service)(nil)

// Limiter says whether one more request for key is allowed now.
type Limiter interface {
	Allow(key string) bool
}

// errRateLimited is returned when a client asks too often.
var errRateLimited = errors.New("too many requests; try again in a minute")

// maxAuthBody caps the JSON bodies of the auth routes.
const maxAuthBody = 4 << 10

// WithDeviceFlow serves the device routes of `loadout login`, limiting how
// often one address may start or poll.
func WithDeviceFlow(d Device, limit Limiter) Option {
	return func(s *server) {
		s.device = d
		s.deviceLimit = limit
	}
}

// WithPages serves pages, the web handler, under /auth/, /account, /t/,
// /new and /skills, and at /. The pages set their own
// Content-Security-Policy.
func WithPages(pages http.Handler) Option { return func(s *server) { s.pages = pages } }

func (s *server) routeAuth(mux *http.ServeMux) {
	mux.Handle("DELETE /v1/auth/token", s.authed(s.revokeOwnToken))
	if s.device != nil {
		mux.HandleFunc("POST /v1/auth/device", s.startDevice)
		mux.HandleFunc("POST /v1/auth/device/token", s.pollDevice)
		mux.Handle("DELETE /v1/auth/tokens", s.authed(s.signOutEverywhere))
		mux.Handle("GET /v1/me", s.authed(s.me))
	}
	if s.pages != nil {
		mux.Handle("/auth/", s.pages)
		mux.Handle("/account", s.pages)
		mux.Handle("/account/", s.pages)
		mux.Handle("/t/", s.pages)
		mux.Handle("/new", s.pages)
		mux.Handle("/skills", s.pages)
		mux.Handle("/skills/", s.pages)
		mux.Handle("GET /{$}", s.pages)
	}
}

// revokeOwnToken ends the calling token, as `loadout logout` does. Only a
// token issued by `loadout login` can end itself: a token an admin made may
// be shared (CI, a team invite), so it is revoked on the registry host.
func (s *server) revokeOwnToken(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if pr.UserID == "" {
		s.writeError(w, r, fmt.Errorf("%w: only tokens from loadout login can be revoked this way; ask the registry admin (loadout token revoke %s)", registry.ErrForbidden, pr.TokenID))
		return
	}
	if err := s.reg.RevokeToken(r.Context(), pr.TokenID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// me tells a login token's owner who they are and where they belong.
func (s *server) me(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if pr.UserID == "" {
		s.writeError(w, r, fmt.Errorf("%w: a machine token belongs to no one", registry.ErrForbidden))
		return
	}
	u, places, err := s.device.Me(r.Context(), pr.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := api.MeResponse{Email: u.Email, Namespaces: make([]api.MeNamespace, 0, len(places))}
	for _, p := range places {
		role := string(p.Access.Role)
		if p.Via != "" {
			role = string(account.RoleMember) // a team's member reads and proposes to the layers above
		}
		out.Namespaces = append(out.Namespaces, api.MeNamespace{Name: p.Namespace.Name, Kind: api.LayerKind(p.Namespace.Kind), Parent: p.Namespace.Parent, Role: role, Via: p.Via})
	}
	s.writeJSON(w, r, http.StatusOK, out)
}

// signOutEverywhere ends every web session and login token of the user
// behind a login token, as `loadout logout --all` does.
func (s *server) signOutEverywhere(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if pr.UserID == "" {
		s.writeError(w, r, fmt.Errorf("%w: only tokens from loadout login belong to a person to sign out", registry.ErrForbidden))
		return
	}
	_, n, err := s.device.SignOutEverywhere(r.Context(), pr.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.RevokedResponse{Revoked: n})
}

func (s *server) startDevice(w http.ResponseWriter, r *http.Request) {
	client := ratelimit.ClientKey(r)
	if !s.deviceLimit.Allow("start:" + client) {
		s.writeError(w, r, errRateLimited)
		return
	}
	var req api.DeviceStartRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	start, err := s.device.StartDevice(r.Context(), req.Namespace, client)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.DeviceStartResponse{
		DeviceCode:              start.DeviceCode,
		UserCode:                start.UserCode,
		VerificationURI:         start.VerificationURI,
		VerificationURIComplete: start.VerificationURIComplete,
		ExpiresIn:               int(start.ExpiresIn.Seconds()),
		Interval:                int(start.Interval.Seconds()),
	})
}

func (s *server) pollDevice(w http.ResponseWriter, r *http.Request) {
	if !s.deviceLimit.Allow("poll:" + ratelimit.ClientKey(r)) {
		s.writeError(w, r, errRateLimited)
		return
	}
	var req api.DeviceTokenRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	poll, err := s.device.PollDevice(r.Context(), req.DeviceCode)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	// Never cache a response that may carry a token.
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusOK, api.DeviceTokenResponse{
		Status:    string(poll.Status),
		Token:     poll.Token,
		Namespace: poll.Namespace,
		Role:      string(poll.Scope),
		ExpiresAt: poll.ExpiresAt,
	})
}

// decodeBody reads a small JSON body into v.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeBodyLimit(w, r, v, maxAuthBody)
}

// decodeBodyLimit reads a JSON body of at most limit bytes into v.
func decodeBodyLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return err
		}
		return fmt.Errorf("%w: request body: %w", registry.ErrInvalid, err)
	}
	return nil
}
