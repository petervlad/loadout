package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/agent"
)

func TestTiersPage(t *testing.T) {
	s := newUISite(t)
	ctx := t.Context()
	if _, _, err := s.accounts.AddMember(ctx, account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")

	// The project sets Copilot's deep model and effort.
	p := olga.post("/t/acme/tiers", url.Values{
		"model-copilot-deep": {"  Claude Sonnet 5 "}, "effort-copilot-deep": {"high"},
		"model-claude-fast": {"claude-haiku-5"},
	})
	if p.status != http.StatusSeeOther {
		t.Fatalf("owner saves acme = %d\n%s", p.status, p.body)
	}
	wantIn(t, olga.get(p.location), "after save", "Model settings saved", `value="Claude Sonnet 5"`, `value="claude-haiku-5"`)
	st, err := s.accounts.ModelTiers(ctx, account.Operator(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Own().Get("copilot", agent.ModelDeep); c.Model != "Claude Sonnet 5" || c.Effort != "high" {
		t.Errorf("stored copilot deep = %+v", c)
	}

	// The team page shows the project's values as placeholders, the
	// built-in Claude aliases where nothing is set, and Copilot's notes.
	page := olga.get("/t/lime/tiers")
	wantIn(t, page, "lime as owner",
		`action="/t/lime/tiers"`,
		`placeholder="Claude Sonnet 5, from acme"`,
		`placeholder="claude-haiku-5, from acme"`,
		`placeholder="opus, built in"`,
		`placeholder="The person&#39;s own choice"`,
		`<option value="">high, from acme</option>`,
		"model picker is on Auto",
		"HydraFusion",
		"not allowed on your Copilot plan",
		"GitHub Copilot", "Claude Code")

	// A team value overrides one value and keeps the rest.
	if p := olga.post("/t/lime/tiers", url.Values{"effort-copilot-deep": {"max"}}); p.status != http.StatusSeeOther {
		t.Fatalf("owner saves lime = %d\n%s", p.status, p.body)
	}
	st, err = s.accounts.ModelTiers(ctx, account.Operator(), "lime")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Resolve().Get("copilot", agent.ModelDeep); c.Model != "Claude Sonnet 5" || c.Effort != "max" {
		t.Errorf("lime's copilot deep = %+v, want acme's model with lime's effort", c)
	}

	// A bad value is refused with the form kept.
	p = olga.post("/t/lime/tiers", url.Values{"model-copilot-fast": {"x\ny"}, "effort-copilot-deep": {"max"}})
	if p.status != http.StatusBadRequest {
		t.Errorf("control character = %d, want 400", p.status)
	}
	wantIn(t, p, "bad value", "not printable", `action="/t/lime/tiers"`, `id="form-error"`,
		`spellcheck="false" aria-invalid="true" aria-describedby="form-error" autofocus>`)
	if i, j := strings.Index(p.body, `id="model-copilot-fast"`), strings.Index(p.body, `aria-invalid="true"`); i < 0 || j < i || strings.Count(p.body, `aria-invalid="true"`) != 1 {
		t.Errorf("bad value: want only model-copilot-fast marked invalid")
	}

	// The page leads back to the settings page.
	wantIn(t, page, "back link", `<div class="back"><a href="/t/lime/settings">`)

	// A team maintainer changes the team's table too.
	alice := s.as("alice@example.com")
	page = alice.get("/t/lime/tiers")
	wantIn(t, page, "maintainer", `action="/t/lime/tiers"`, `placeholder="Claude Sonnet 5, from acme"`, "model picker is on Auto", "HydraFusion")
	if p := alice.post("/t/lime/tiers", url.Values{"model-copilot-fast": {"Claude Haiku 4.5"}, "effort-copilot-deep": {"max"}}); p.status != http.StatusSeeOther {
		t.Errorf("maintainer saves = %d, want 303\n%s", p.status, p.body)
	}
	wantIn(t, alice.get("/t/lime/settings"), "members link", `href="/t/lime/tiers"`, "Choose models")
	wantIn(t, olga.get("/t/lime/settings"), "members link", "Choose models")

	// A plain member neither sees the link nor opens or changes the page;
	// sync still reads the table for them.
	dan := s.as("dan@example.com")
	wantOut(t, dan.get("/t/lime/settings"), "member's settings page", `href="/t/lime/tiers"`, "Choose models")
	if p := dan.get("/t/lime/tiers"); p.status != http.StatusNotFound {
		t.Errorf("member opens the page = %d, want 404", p.status)
	}
	if p := dan.post("/t/lime/tiers", url.Values{"model-copilot-deep": {"GPT-5"}}); p.status != http.StatusForbidden {
		t.Errorf("member saves = %d, want 403", p.status)
	}
	if _, err := s.accounts.ModelTiers(ctx, account.Operator(), "lime"); err != nil {
		t.Fatal(err)
	}

	// Someone outside the team gets nothing.
	eve := s.as("eve@example.com")
	if p := eve.get("/t/lime/tiers"); p.status != http.StatusNotFound {
		t.Errorf("outsider = %d, want 404", p.status)
	}
	if p := olga.post("/t/lime/tiers", url.Values{"csrf": {"wrong"}}); p.status != http.StatusForbidden {
		t.Errorf("wrong CSRF = %d, want 403", p.status)
	}
}

// TestReviewSettingForOwners: the "Review before release" panel is shown only
// to those who may change it; maintainers and members do not see it.
func TestReviewSettingForOwners(t *testing.T) {
	s := newUISite(t)
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	wantIn(t, s.as("olga@example.com").get("/t/lime/settings"), "owner", `id="review-h"`, `name="require_review"`)
	for _, who := range []string{"alice@example.com", "dan@example.com"} {
		wantOut(t, s.as(who).get("/t/lime/settings"), who, `id="review-h"`, "Review before release", `name="require_review"`)
	}
}
