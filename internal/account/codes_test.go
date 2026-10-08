package account_test

import (
	"errors"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
)

func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

// TestNewCodeCancelsTheOldOne: asking for a new code invalidates the open
// one, so each code comes with its own attempts only while it is the
// newest.
func TestNewCodeCancelsTheOldOne(t *testing.T) {
	f := newFixture(t)
	const email = "alice@example.com"
	first, err := f.svc.Signup(t.Context(), email, pw)
	if err != nil {
		t.Fatal(err)
	}
	code1, token1 := f.mailed(t, email, 0)
	second, err := f.svc.Signup(t.Context(), email, pw)
	if err != nil {
		t.Fatal(err)
	}
	code2, _ := f.mailed(t, email, 1)
	if _, err := f.svc.VerifyCode(t.Context(), first.ChallengeID, code1); !errors.Is(err, account.ErrInvalidCode) {
		t.Errorf("VerifyCode(first code after a second was sent) error = %v, want ErrInvalidCode", err)
	}
	if _, err := f.svc.VerifyLink(t.Context(), token1); !errors.Is(err, account.ErrInvalidCode) {
		t.Errorf("VerifyLink(first link after a second was sent) error = %v, want ErrInvalidCode", err)
	}
	if _, err := f.svc.VerifyCode(t.Context(), second.ChallengeID, code2); err != nil {
		t.Errorf("VerifyCode(newest code) error: %v", err)
	}
}

// TestWrongCodesLockTheUser: wrong codes count per user across codes; after
// MaxCodeFailures in a day even the right code is refused, until the
// failures age out.
func TestWrongCodesLockTheUser(t *testing.T) {
	const email = "alice@example.com"
	tests := []struct {
		name  string
		setup func(f *fixture)
		start func(f *fixture) (string, error)
	}{
		{"sign-up codes", func(*fixture) {}, func(f *fixture) (string, error) {
			p, err := f.svc.Signup(t.Context(), email, pw)
			return p.ChallengeID, err
		}},
		{"sign-in codes", func(f *fixture) { f.signup(t, email) }, func(f *fixture) (string, error) {
			p, err := f.svc.Login(t.Context(), email, pw)
			return p.ChallengeID, err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f)
			next := func() (string, string) {
				t.Helper()
				n := f.mails.Count(email)
				id, err := tt.start(f)
				if err != nil {
					t.Fatal(err)
				}
				code, _ := f.mailed(t, email, n)
				return id, code
			}
			for range account.MaxCodeFailures / account.MaxAttempts {
				id, code := next()
				for range account.MaxAttempts {
					if _, err := f.svc.VerifyCode(t.Context(), id, wrongCode(code)); !errors.Is(err, account.ErrInvalidCode) {
						t.Fatalf("VerifyCode(wrong) error = %v, want ErrInvalidCode", err)
					}
				}
			}
			id, code := next()
			_, err := f.svc.VerifyCode(t.Context(), id, code)
			if err == nil {
				t.Fatalf("VerifyCode(right code after %d wrong ones across codes) succeeded, want it locked", account.MaxCodeFailures)
			}
			if !errors.Is(err, account.ErrCodeLocked) {
				t.Errorf("VerifyCode() when locked error = %v, want ErrCodeLocked", err)
			}

			f.clock.advance(account.CodeFailureWindow + time.Second)
			id, code = next()
			if _, err := f.svc.VerifyCode(t.Context(), id, code); err != nil {
				t.Errorf("VerifyCode() a day later error: %v", err)
			}
		})
	}
}
