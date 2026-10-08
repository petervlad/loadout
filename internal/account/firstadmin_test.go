package account_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/password"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

func TestEnsureFirstAdmin(t *testing.T) {
	const chosen = "a long enough password"
	tests := []struct {
		name        string
		existing    bool
		configured  string
		wantCreated bool
		wantErr     string
		wantShown   bool
	}{
		{"empty registry creates a generated admin", false, "", true, "", true},
		{"empty registry uses the configured password", false, chosen, true, "", false},
		{"a weak configured password is refused", false, "short", false, "LOADOUT_ADMIN_PASSWORD is refused", false},
		{"a common configured password is refused", false, "password1234", false, "LOADOUT_ADMIN_PASSWORD is refused", false},
		{"any account stops it", true, "", false, "", false},
		{"any account stops it even with a weak password set", true, "short", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			svc, store := firstAdminService(&logs)
			ctx := t.Context()
			if tt.existing {
				if _, err := svc.SetPasswordAsOperator(ctx, account.Operator(), "ada@example.com", chosen); err != nil {
					t.Fatal(err)
				}
			}
			got, err := svc.EnsureFirstAdmin(ctx, store, tt.configured)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if c, _ := store.AccountCounts(ctx, time.Now()); c.Users != 0 {
					t.Errorf("%d users after a refused password, want none", c.Users)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Created != tt.wantCreated {
				t.Fatalf("Created = %v, want %v", got.Created, tt.wantCreated)
			}
			if (got.Password != "") != tt.wantShown {
				t.Errorf("Password shown = %v, want %v", got.Password != "", tt.wantShown)
			}
			if !tt.wantCreated {
				if _, err := store.UserByEmail(ctx, account.FirstAdminEmail); err == nil {
					t.Error("the first admin exists in a registry that had accounts")
				}
				return
			}
			pw := tt.configured
			if tt.wantShown {
				pw = got.Password
			}
			g, err := svc.SimpleLogin(ctx, account.FirstAdminEmail, pw)
			if err != nil {
				t.Fatalf("sign in as the first admin: %v", err)
			}
			u, err := svc.SessionUser(ctx, g.Secret)
			if err != nil || !u.IsAdmin || !u.Verified() {
				t.Errorf("user = %+v, %v; want a verified admin", u, err)
			}
			if strings.Contains(logs.String(), pw) {
				t.Errorf("the password is in the log:\n%s", logs.String())
			}
		})
	}
}

// Deleting the admin of a registry with other accounts does not bring it
// back; only a completely empty table does.
func TestEnsureFirstAdminOnlyOnAnEmptyTable(t *testing.T) {
	svc, store := firstAdminService(&bytes.Buffer{})
	ctx := t.Context()
	first, err := svc.EnsureFirstAdmin(ctx, store, "")
	if err != nil || !first.Created {
		t.Fatalf("first run: %+v, %v", first, err)
	}
	again, err := svc.EnsureFirstAdmin(ctx, store, "")
	if err != nil || again.Created || again.Password != "" {
		t.Fatalf("second run: %+v, %v; want nothing", again, err)
	}
	if _, err := svc.SetPasswordAsOperator(ctx, account.Operator(), "ada@example.com", "a long enough password"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteUser(ctx, account.Operator(), account.FirstAdminEmail); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.EnsureFirstAdmin(ctx, store, ""); err != nil || got.Created {
		t.Fatalf("after deleting the admin: %+v, %v; want nothing", got, err)
	}
	if _, err := svc.DeleteUser(ctx, account.Operator(), "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.EnsureFirstAdmin(ctx, store, ""); err != nil || !got.Created {
		t.Fatalf("after deleting everyone: %+v, %v; want a new admin", got, err)
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw, err := account.GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 24 {
			t.Fatalf("length %d, want 24", len(pw))
		}
		for _, r := range pw {
			if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789", r) {
				t.Fatalf("%q has %q outside the alphabet", pw, r)
			}
		}
		if err := password.Check(pw, account.FirstAdminEmail); err != nil {
			t.Fatalf("Check(%q) = %v", pw, err)
		}
		if seen[pw] {
			t.Fatalf("%q generated twice", pw)
		}
		seen[pw] = true
	}
}

func firstAdminService(logs *bytes.Buffer) (*account.Service, *accounttest.MemStore) {
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, now, log, registry.WithMemberships(roles))
	svc := account.NewService(store, reg, roles, device.New(now), account.Options{PublicURL: "https://registry.example.com", Now: now, Log: log})
	return svc, store
}
