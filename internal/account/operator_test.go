package account_test

import (
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/password"
	"github.com/petervlad/loadout/internal/registry"
)

func TestSetPasswordAsOperator(t *testing.T) {
	const good = "a long enough password"
	tests := []struct {
		name  string
		actor account.Actor
		email string
		pw    string
		want  error
		weak  bool
	}{
		{"operator creates the account", account.Operator(), "Ada@Example.com", good, nil, false},
		{"a user is refused", account.UserActor("u1"), "ada@example.com", good, registry.ErrForbidden, false},
		{"a short password is refused", account.Operator(), "ada@example.com", "short", nil, true},
		{"a common password is refused", account.Operator(), "ada@example.com", "password1234", nil, true},
		{"the email as password is refused", account.Operator(), "ada.lovelace@example.com", "ada.lovelace@example.com", nil, true},
		{"a bad address is refused", account.Operator(), "nope", good, account.ErrInvalidEmail, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.svc.SetPasswordAsOperator(t.Context(), tt.actor, tt.email, tt.pw)
			switch {
			case tt.weak:
				if _, ok := errors.AsType[*password.WeakError](err); !ok {
					t.Fatalf("err = %v, want a weak password error", err)
				}
			case !errors.Is(err, tt.want):
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if err != nil {
				return
			}
			u, err := f.store.UserByEmail(t.Context(), "ada@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if !u.Verified() {
				t.Error("the address is not verified")
			}
			if g, err := f.svc.SimpleLogin(t.Context(), "ada@example.com", tt.pw); err != nil || g.Secret == "" {
				t.Errorf("sign in with the new password: %v", err)
			}
		})
	}
}

func TestSetPasswordAsOperatorReplacesAndEndsSessions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if _, err := f.svc.SetPasswordAsOperator(ctx, account.Operator(), "ada@example.com", "the first password"); err != nil {
		t.Fatal(err)
	}
	g, err := f.svc.SimpleLogin(ctx, "ada@example.com", "the first password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetPasswordAsOperator(ctx, account.Operator(), "ada@example.com", "the second password"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SessionUser(ctx, g.Secret); err == nil {
		t.Error("the old session still works")
	}
	if _, err := f.svc.SimpleLogin(ctx, "ada@example.com", "the first password"); !errors.Is(err, account.ErrBadLogin) {
		t.Errorf("old password: err = %v, want ErrBadLogin", err)
	}
	if _, err := f.svc.SimpleLogin(ctx, "ada@example.com", "the second password"); err != nil {
		t.Errorf("new password: %v", err)
	}
}
