package account_test

import (
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/account"
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
