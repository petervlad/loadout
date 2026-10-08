package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/registry"
)

// DeviceStart is what `loadout login` gets when it starts.
type DeviceStart struct {
	DeviceCode string
	UserCode   string
	// VerificationURI is the page where the person enters the code;
	// VerificationURIComplete has the code filled in.
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// DeviceRequest is a pending CLI sign-in as the signed-in person sees it.
type DeviceRequest struct {
	UserCode string
	// Namespace is the namespace the CLI asked for, or empty to choose.
	Namespace string
	// Choices are the namespaces the CLI may get a token for, with the
	// person's effective role there: Namespace, or each of their
	// memberships. Empty means none fits.
	Choices []Membership
}

// DevicePoll is the answer to one poll. Token is set when Status is
// device.StatusApproved; it is a secret shown to the CLI once.
type DevicePoll struct {
	Status    device.Status
	Token     string
	Namespace string
	Scope     registry.TokenScope
	// ExpiresAt is when the token stops working.
	ExpiresAt time.Time
}

// StartDevice starts a CLI sign-in for namespace; empty lets the person
// choose one of their memberships when approving, and
// registry.AccountNamespace asks for an account token, which covers them
// all. client is the rate-limit
// key of the caller's address; each client may have device.MaxPerClient
// sign-ins pending.
func (s *Service) StartDevice(_ context.Context, namespace, client string) (DeviceStart, error) {
	if namespace != "" && namespace != registry.AccountNamespace {
		if err := ident.Check("namespace", namespace); err != nil {
			return DeviceStart{}, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
		}
	}
	a, err := s.flow.Start(namespace, client)
	if err != nil {
		return DeviceStart{}, err
	}
	page := s.publicURL + "/auth/device"
	return DeviceStart{
		DeviceCode:              a.DeviceCode,
		UserCode:                a.UserCode,
		VerificationURI:         page,
		VerificationURIComplete: page + "?code=" + a.UserCode,
		ExpiresIn:               a.ExpiresAt.Sub(s.now()),
		Interval:                a.Interval,
	}, nil
}

// DeviceRequest returns the pending sign-in for a user code with the
// memberships u may approve it for. An unknown code is ErrInvalidCode.
func (s *Service) DeviceRequest(ctx context.Context, u User, userCode string) (DeviceRequest, error) {
	req, err := s.flow.Pending(userCode)
	if errors.Is(err, device.ErrNotFound) {
		return DeviceRequest{}, ErrInvalidCode
	}
	if err != nil {
		return DeviceRequest{}, err
	}
	out := DeviceRequest{UserCode: req.UserCode, Namespace: req.Namespace}
	if req.Namespace == registry.AccountNamespace {
		// An account sign-in covers every place; the page lists them.
		places, err := s.roles.Places(ctx, u)
		if err != nil {
			return DeviceRequest{}, err
		}
		for _, p := range places {
			if p.Via == "" {
				out.Choices = append(out.Choices, Membership{UserID: u.ID, Email: u.Email, Namespace: p.Namespace.Name, Role: p.Access.Role})
			}
		}
		return out, nil
	}
	if req.Namespace != "" {
		a, err := s.roles.accessOf(ctx, u, req.Namespace)
		if err != nil {
			return DeviceRequest{}, err
		}
		if a.CanRead() {
			out.Choices = []Membership{{UserID: u.ID, Email: u.Email, Namespace: req.Namespace, Role: a.Role}}
		}
		return out, nil
	}
	ms, err := s.Memberships(ctx, u.ID)
	if err != nil {
		return DeviceRequest{}, err
	}
	for _, m := range ms {
		a, err := s.roles.accessOf(ctx, u, m.Namespace)
		if err != nil {
			return DeviceRequest{}, err
		}
		m.Role = a.Role // an owner above raises a member to maintainer
		out.Choices = append(out.Choices, m)
	}
	return out, nil
}

// ApproveDevice approves a pending sign-in for u in namespace, where u must
// have a role (and which must be the one the CLI asked for, if it did).
func (s *Service) ApproveDevice(ctx context.Context, u User, userCode, namespace string) error {
	req, err := s.flow.Pending(userCode)
	if errors.Is(err, device.ErrNotFound) {
		return ErrInvalidCode
	}
	if err != nil {
		return err
	}
	if namespace == "" {
		namespace = req.Namespace
	}
	if req.Namespace != "" && namespace != req.Namespace {
		return fmt.Errorf("%w: the CLI asked for %s", registry.ErrInvalid, req.Namespace)
	}
	if namespace == registry.AccountNamespace {
		places, err := s.roles.Places(ctx, u)
		if err != nil {
			return fmt.Errorf("approve sign-in: %w", err)
		}
		if len(places) == 0 {
			return ErrNotMember
		}
	} else if a, err := s.roles.accessOf(ctx, u, namespace); err != nil {
		return fmt.Errorf("approve sign-in: %w", err)
	} else if !a.CanRead() {
		return ErrNotMember
	}
	if err := s.flow.Approve(userCode, device.Approval{UserID: u.ID, Namespace: namespace}); err != nil {
		if errors.Is(err, device.ErrNotFound) {
			return ErrInvalidCode
		}
		return fmt.Errorf("approve sign-in: %w", err)
	}
	s.log.InfoContext(ctx, "cli sign-in approved", "user_id", u.ID, "namespace", namespace)
	return nil
}

// DenyDevice refuses a pending sign-in.
func (s *Service) DenyDevice(_ context.Context, userCode string) error {
	if err := s.flow.Deny(userCode); errors.Is(err, device.ErrNotFound) {
		return ErrInvalidCode
	} else if err != nil {
		return err
	}
	return nil
}

// PollDevice answers the CLI. Once approved, it issues a namespace token
// for the user's role as it is now (publish for maintainers and above,
// read for members), labelled with the user's email, and bound to the user
// so it follows their role.
func (s *Service) PollDevice(ctx context.Context, deviceCode string) (DevicePoll, error) {
	r := s.flow.Poll(deviceCode)
	if r.Status != device.StatusApproved {
		return DevicePoll{Status: r.Status}, nil
	}
	ap := r.Approval
	u, err := s.store.UserByID(ctx, ap.UserID)
	if errors.Is(err, registry.ErrNotFound) {
		return DevicePoll{Status: device.StatusDenied}, nil
	}
	if err != nil {
		return DevicePoll{}, fmt.Errorf("issue login token: %w", err)
	}
	if ap.Namespace == registry.AccountNamespace {
		secret, tok, err := s.reg.CreateAccountToken(ctx, u.Email+" via loadout login", u.ID)
		if err != nil {
			return DevicePoll{}, fmt.Errorf("issue login token: %w", err)
		}
		return DevicePoll{Status: device.StatusApproved, Token: secret, Namespace: tok.Team, ExpiresAt: tok.ExpiresAt}, nil
	}
	a, err := s.roles.accessOf(ctx, u, ap.Namespace)
	if err != nil {
		return DevicePoll{}, fmt.Errorf("issue login token: %w", err)
	}
	scope, ok := a.TokenScope()
	if !ok {
		return DevicePoll{Status: device.StatusDenied}, nil // lost the role since approving
	}
	secret, tok, err := s.reg.CreateUserToken(ctx, ap.Namespace, scope, u.Email+" via loadout login", u.ID)
	if err != nil {
		return DevicePoll{}, fmt.Errorf("issue login token: %w", err)
	}
	return DevicePoll{Status: device.StatusApproved, Token: secret, Namespace: tok.Team, Scope: tok.Scope, ExpiresAt: tok.ExpiresAt}, nil
}
