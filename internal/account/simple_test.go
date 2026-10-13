package account_test

import (
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

func TestSimpleLogin(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetPasswordAsOperator(t.Context(), account.Operator(), "viorel@example.com", "a long enough password"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, email, pw string
		want            error
	}{
		{"right", "viorel@example.com", "a long enough password", nil},
		{"right, other case", "VIOREL@example.com", "a long enough password", nil},
		{"wrong password", "viorel@example.com", "nope", account.ErrBadLogin},
		{"unknown email", "nobody@example.com", "a long enough password", account.ErrBadLogin},
		{"bad address", "nope", "a long enough password", account.ErrInvalidEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := f.svc.SimpleLogin(t.Context(), tt.email, tt.pw)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if (err == nil) != (g.Secret != "") {
				t.Errorf("session secret set = %v with err %v", g.Secret != "", err)
			}
		})
	}
	// A user without a password gets the same answer.
	if _, err := f.svc.SetAdmin(t.Context(), account.Operator(), "nopass@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SimpleLogin(t.Context(), "nopass@example.com", ""); !errors.Is(err, account.ErrBadLogin) {
		t.Errorf("user without a password: err = %v, want ErrBadLogin", err)
	}
}

func TestAddUser(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	const pw = "a long enough password"

	if err := f.svc.AddUser(ctx, account.UserActor("someone"), "olga@example.com", pw, false); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("AddUser(non-operator) error = %v, want ErrForbidden", err)
	}
	if err := f.svc.AddUser(ctx, account.Operator(), "olga@example.com", "", false); err == nil {
		t.Error("AddUser(no password) succeeded, want an error")
	}
	if err := f.svc.AddUser(ctx, account.Operator(), "olga@example.com", pw, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SimpleLogin(ctx, "olga@example.com", pw); err != nil {
		t.Errorf("SimpleLogin() after AddUser error: %v", err)
	}
	if _, err := f.svc.SimpleLogin(ctx, "olga@example.com", "wrong"); !errors.Is(err, account.ErrBadLogin) {
		t.Errorf("SimpleLogin(wrong) error = %v, want ErrBadLogin", err)
	}
	u, _ := f.store.UserByEmail(ctx, "olga@example.com")
	if !u.IsAdmin {
		t.Error("the --admin flag was not applied")
	}
	if err := f.svc.AddUser(ctx, account.Operator(), "olga@example.com", "another long password", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SimpleLogin(ctx, "olga@example.com", pw); err != nil {
		t.Errorf("an existing account lost its password: %v", err)
	}
}
