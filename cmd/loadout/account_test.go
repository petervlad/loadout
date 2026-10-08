package main

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/mail/mailtest"
	"github.com/petervlad/loadout/internal/pack"
)

// signUp creates an account for email on the registry at base and returns
// its signed-in browser.
func signUp(t *testing.T, base string, mails *mailtest.Recorder, email string) *webBrowser {
	t.Helper()
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/signup", nil)
	if status, loc, page := web.send(http.MethodPost, "/auth/signup", url.Values{"email": {email}, "password": {"tangerine kettle river"}}); status != http.StatusSeeOther {
		t.Fatalf("signup %s = %d %s\n%s", email, status, loc, page)
	}
	code := regexp.MustCompile(`\b(\d{6})\b`).FindStringSubmatch(mails.WaitFor(t, email, 0).Text)
	if code == nil {
		t.Fatalf("no code in the mail to %s", email)
	}
	if status, loc, _ := web.send(http.MethodPost, "/auth/verify", url.Values{"code": {code[1]}}); status != http.StatusSeeOther {
		t.Fatalf("verify %s = %d %s", email, status, loc)
	}
	return web
}

// deviceSignIn runs the CLI sign-in: a CLI asks for namespace ask, and the
// person behind web approves it for namespace approve. It returns the
// approval page's status and, when approved, the token.
func deviceSignIn(t *testing.T, base string, web *webBrowser, ask, approve string) (int, api.DeviceTokenResponse) {
	t.Helper()
	c, err := client.New(base, "")
	if err != nil {
		t.Fatal(err)
	}
	start, err := c.StartDevice(t.Context(), ask)
	if err != nil {
		t.Fatalf("StartDevice(%q) error: %v", ask, err)
	}
	status, _, page := web.send(http.MethodPost, "/auth/device", url.Values{"code": {start.UserCode}, "namespace": {approve}, "action": {"approve"}})
	if status != http.StatusOK || !strings.Contains(page, "Terminal signed in") {
		return status, api.DeviceTokenResponse{}
	}
	tok, err := c.PollDevice(t.Context(), start.DeviceCode)
	if err != nil || tok.Status != api.DeviceApproved {
		t.Fatalf("PollDevice() = %+v, %v; want approved", tok, err)
	}
	return status, tok
}

// accountToken signs the person behind web in for their whole account.
func accountToken(t *testing.T, base string, web *webBrowser) string {
	t.Helper()
	status, tok := deviceSignIn(t, base, web, api.AccountNamespace, api.AccountNamespace)
	if status != http.StatusOK || tok.Namespace != api.AccountNamespace {
		t.Fatalf("account sign-in = %d, %+v; want an account token", status, tok)
	}
	return tok.Token
}

func agentPack(t *testing.T, id, version string) *pack.Pack {
	t.Helper()
	name := id[strings.Index(id, "/")+1:]
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id + "\nkind: agent\nversion: " + version + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: " + name + "\ndescription: d\n---\nprompt\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustPackID(t *testing.T, s string) pack.ID {
	t.Helper()
	id, err := pack.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// statusOf is the HTTP status of a client error: 200 for none.
func statusOf(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if apiErr, ok := errors.AsType[*client.APIError](err); ok {
		return apiErr.Status
	}
	return -1
}

// TestAccountTokenPermissions drives the whole registry over HTTP with
// account tokens: one token per person, whose rights in each namespace come
// from their roles there at the time of each request.
//
//	acme (project, olga owns it)      other (project)
//	  lime: alice, bob maintainers      zeta: eve maintainer
//	  web:  carol member
//	dave is an admin with no membership.
func TestAccountTokenPermissions(t *testing.T) {
	host := &person{session: newSession(t)}
	mails := &mailtest.Recorder{}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	base, _ := serveInBackground(t, host, env, externals{mailer: mails})
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	host.must("", "namespace", "create", "web", "--parent", "acme")
	host.must("", "namespace", "create", "other", "--kind", "project")
	host.must("", "namespace", "create", "zeta", "--parent", "other")
	for _, m := range [][3]string{
		{"acme", "olga@example.com", "owner"},
		{"lime", "alice@example.com", "maintainer"},
		{"lime", "bob@example.com", "maintainer"},
		{"web", "carol@example.com", "member"},
		{"zeta", "eve@example.com", "maintainer"},
	} {
		host.must("", "member", "add", "--namespace", m[0], "--email", m[1], "--role", m[2])
	}
	tokens := map[string]string{}
	for _, name := range []string{"olga", "alice", "bob", "carol", "eve", "dave"} {
		web := signUp(t, base, mails, name+"@example.com")
		if name == "dave" {
			host.must("", "admin", "grant", "--email", "dave@example.com")
		}
		tokens[name] = accountToken(t, base, web)
	}
	ctx := t.Context()
	as := func(name, view string) *client.Client {
		t.Helper()
		c, err := client.New(base, tokens[name])
		if err != nil {
			t.Fatal(err)
		}
		return c.In(view)
	}

	// Seed a package in each team, published by its maintainers.
	if _, err := as("alice", "").Publish(ctx, agentPack(t, "lime/planner", "0.1.0")); err != nil {
		t.Fatalf("alice publishes lime/planner: %v", err)
	}
	if _, err := as("olga", "").Publish(ctx, agentPack(t, "acme/rules", "0.1.0")); err != nil {
		t.Fatalf("olga publishes acme/rules: %v", err)
	}
	if _, err := as("eve", "").Publish(ctx, agentPack(t, "zeta/secret", "0.1.0")); err != nil {
		t.Fatalf("eve publishes zeta/secret: %v", err)
	}

	publish := []struct {
		who, view, id string
		want          int
	}{
		{"alice", "", "lime/a", http.StatusOK},
		{"alice", "web", "lime/b", http.StatusOK},     // the view is only a hint: a package goes by its own namespace
		{"alice", "acme", "lime/c", http.StatusOK},    // the package's namespace decides
		{"alice", "", "acme/a", http.StatusForbidden}, // a team maintainer is no project maintainer
		{"alice", "lime", "acme/b", http.StatusForbidden},
		{"alice", "", "web/a", http.StatusForbidden}, // a sibling team
		{"alice", "", "zeta/a", http.StatusForbidden},
		{"olga", "", "lime/o", http.StatusOK}, // the project's owner owns the teams below
		{"olga", "", "web/o", http.StatusOK},
		{"olga", "", "zeta/o", http.StatusForbidden},
		{"carol", "", "web/c", http.StatusForbidden}, // a member proposes, never publishes
		{"carol", "", "acme/c", http.StatusForbidden},
		{"eve", "", "lime/e", http.StatusForbidden},
		{"dave", "", "lime/d", http.StatusOK}, // DESIGN.md: an admin's token publishes everywhere; managing is what it never does
		{"dave", "", "zeta/d", http.StatusOK},
	}
	for _, tt := range publish {
		_, err := as(tt.who, tt.view).Publish(ctx, agentPack(t, tt.id, "1.0.0"))
		if got := statusOf(err); got != tt.want {
			t.Errorf("%s publishes %s (view %q) = %d, %v; want %d", tt.who, tt.id, tt.view, got, err, tt.want)
		}
	}

	read := []struct {
		who, view, id string
		want          int
	}{
		{"alice", "", "lime/planner", http.StatusOK},
		{"alice", "", "acme/rules", http.StatusOK},
		{"carol", "", "acme/rules", http.StatusOK}, // a team's member reads its project
		{"carol", "", "lime/planner", http.StatusForbidden},
		{"carol", "web", "lime/planner", http.StatusForbidden},
		{"eve", "", "lime/planner", http.StatusForbidden},
		{"alice", "", "zeta/secret", http.StatusForbidden},
		{"alice", "zeta", "zeta/secret", http.StatusForbidden},
	}
	for _, tt := range read {
		_, err := as(tt.who, tt.view).Fetch(ctx, mustPackID(t, tt.id), "latest")
		if got := statusOf(err); got != tt.want {
			t.Errorf("%s fetches %s (view %q) = %d, %v; want %d", tt.who, tt.id, tt.view, got, err, tt.want)
		}
		_, err = as(tt.who, tt.view).Releases(ctx, mustPackID(t, tt.id))
		if got := statusOf(err); got != tt.want {
			t.Errorf("%s lists versions of %s (view %q) = %d, %v; want %d", tt.who, tt.id, tt.view, got, err, tt.want)
		}
	}

	// Listings need a view the person is in, and show only that view.
	list := []struct {
		who, view string
		want      int
		has, not  []string
	}{
		{"alice", "", http.StatusBadRequest, nil, nil},
		{"alice", "lime", http.StatusOK, []string{"lime/planner", "acme/rules"}, []string{"zeta/secret", "web/o"}},
		{"alice", "web", http.StatusForbidden, nil, nil},
		{"alice", "zeta", http.StatusForbidden, nil, nil},
		{"alice", "../lime", http.StatusBadRequest, nil, nil},
		{"carol", "web", http.StatusOK, []string{"web/o", "acme/rules"}, []string{"lime/planner"}},
		{"carol", "acme", http.StatusOK, []string{"acme/rules"}, []string{"lime/planner", "web/o"}},
		{"carol", "lime", http.StatusForbidden, nil, nil},
		{"eve", "acme", http.StatusForbidden, nil, nil},
	}
	for _, tt := range list {
		rels, err := as(tt.who, tt.view).Packages(ctx)
		if got := statusOf(err); got != tt.want {
			t.Errorf("%s lists view %q = %d, %v; want %d", tt.who, tt.view, got, err, tt.want)
			continue
		}
		var ids []string
		for _, r := range rels {
			ids = append(ids, r.ID)
		}
		got := strings.Join(ids, " ")
		for _, id := range tt.has {
			if !strings.Contains(got, id) {
				t.Errorf("%s lists view %q = %s, want %s", tt.who, tt.view, got, id)
			}
		}
		for _, id := range tt.not {
			if strings.Contains(got, id) {
				t.Errorf("%s lists view %q = %s, must not show %s", tt.who, tt.view, got, id)
			}
		}
		if tt.want == http.StatusOK {
			if _, err := as(tt.who, tt.view).Layers(ctx); err != nil {
				t.Errorf("%s layers of view %q: %v", tt.who, tt.view, err)
			}
			if _, err := as(tt.who, tt.view).Changes(ctx, 0); err != nil {
				t.Errorf("%s changes of view %q: %v", tt.who, tt.view, err)
			}
		}
	}
	for _, fn := range []func(*client.Client) error{
		func(c *client.Client) error { _, err := c.Layers(ctx); return err },
		func(c *client.Client) error { _, err := c.Changes(ctx, 0); return err },
		func(c *client.Client) error { _, err := c.Proposals(ctx, "", ""); return err },
	} {
		if got := statusOf(fn(as("alice", ""))); got != http.StatusBadRequest {
			t.Errorf("a listing without a view = %d, want 400", got)
		}
	}

	// Four eyes with account tokens: the author never decides, another
	// maintainer of the package's namespace does.
	prop, err := as("alice", "").Propose(ctx, agentPack(t, "lime/planner", "0.2.0"), api.ProposalRequest{Prompts: []string{"plan"}})
	if err != nil {
		t.Fatalf("alice proposes to lime: %v", err)
	}
	for _, who := range []string{"alice", "carol", "eve"} {
		if _, err := as(who, "").Approve(ctx, prop.ID, api.DecisionRequest{}); statusOf(err) == http.StatusOK {
			t.Errorf("%s approved alice's lime proposal; want refused", who)
		}
	}
	if _, err := as("eve", "").Proposal(ctx, prop.ID); statusOf(err) != http.StatusNotFound {
		t.Errorf("eve reads a lime proposal = %v, want 404", err)
	}
	if _, err := as("carol", "").Proposal(ctx, prop.ID); statusOf(err) != http.StatusNotFound {
		t.Errorf("carol reads a lime proposal = %v, want 404", err)
	}
	// A view he is not in is only a hint: the proposal's namespace decides.
	if _, err := as("bob", "web").Approve(ctx, prop.ID, api.DecisionRequest{Note: "ok"}); err != nil {
		t.Errorf("bob approves alice's proposal: %v", err)
	}

	// A team member proposes to the project; a team maintainer cannot
	// approve there, the project's owner can.
	prop, err = as("carol", "").Propose(ctx, agentPack(t, "acme/rules", "0.2.0"), api.ProposalRequest{Prompts: []string{"rules"}})
	if err != nil {
		t.Fatalf("carol proposes to acme: %v", err)
	}
	if _, err := as("alice", "").Reject(ctx, prop.ID, ""); statusOf(err) != http.StatusForbidden {
		t.Errorf("alice (lime maintainer) rejects an acme proposal = %v, want 403", err)
	}
	if _, err := as("alice", "").Approve(ctx, prop.ID, api.DecisionRequest{}); statusOf(err) != http.StatusForbidden {
		t.Errorf("alice (lime maintainer) approves an acme proposal = %v, want 403", err)
	}
	if _, err := as("alice", "").AddReview(ctx, prop.ID, []byte("looks fine"), nil); err != nil {
		t.Errorf("alice reviews an acme proposal she can read: %v", err)
	}
	if _, err := as("eve", "").AddReview(ctx, prop.ID, []byte("spam"), nil); statusOf(err) != http.StatusNotFound {
		t.Errorf("eve reviews an acme proposal = %v, want 404", err)
	}
	if _, err := as("olga", "").Approve(ctx, prop.ID, api.DecisionRequest{}); err != nil {
		t.Errorf("olga approves carol's acme proposal: %v", err)
	}

	// Managing over HTTP: the user's role per namespace, never admin rights.
	manage := []struct {
		name string
		do   func() error
		want int
	}{
		{"alice adds a lime member", func() error {
			_, err := as("alice", "").AddMember(ctx, "lime", "new@example.com", "member")
			return err
		}, http.StatusOK},
		{"alice adds an acme member", func() error {
			_, err := as("alice", "").AddMember(ctx, "acme", "new@example.com", "member")
			return err
		}, http.StatusForbidden},
		{"alice adds a web member", func() error {
			_, err := as("alice", "").AddMember(ctx, "web", "new@example.com", "member")
			return err
		}, http.StatusForbidden},
		{"alice makes a machine token for acme", func() error {
			_, err := as("alice", "").CreateMachineToken(ctx, "acme", "publish", "ci")
			return err
		}, http.StatusForbidden},
		{"alice creates a team in acme", func() error {
			_, err := as("alice", "").CreateNamespace(ctx, "mobile", "acme", "team")
			return err
		}, http.StatusForbidden},
		{"olga creates a team in acme", func() error {
			_, err := as("olga", "").CreateNamespace(ctx, "mobile", "acme", "team")
			return err
		}, http.StatusOK},
		{"dave (admin) creates a project", func() error {
			_, err := as("dave", "").CreateNamespace(ctx, "rogue", "", "project")
			return err
		}, http.StatusForbidden},
		{"dave (admin) adds a lime member", func() error {
			_, err := as("dave", "").AddMember(ctx, "lime", "rogue@example.com", "maintainer")
			return err
		}, http.StatusForbidden},
		{"dave (admin) lists lime's machine tokens", func() error {
			_, err := as("dave", "").MachineTokens(ctx, "lime")
			return err
		}, http.StatusForbidden},
		{"eve lists lime's members", func() error {
			_, err := as("eve", "").Members(ctx, "lime")
			return err
		}, http.StatusForbidden},
	}
	for _, tt := range manage {
		if got := statusOf(tt.do()); got != tt.want {
			t.Errorf("%s = %d, want %d", tt.name, got, tt.want)
		}
	}

	// A removed membership takes the access away at the next request; the
	// token keeps working in the teams that are left.
	host.must("", "member", "remove", "--namespace", "lime", "--email", "bob@example.com")
	if _, err := as("bob", "").Publish(ctx, agentPack(t, "lime/bob", "1.0.0")); statusOf(err) != http.StatusForbidden {
		t.Errorf("bob publishes to lime after removal = %v, want 403", err)
	}
	if _, err := as("bob", "lime").Packages(ctx); statusOf(err) != http.StatusForbidden {
		t.Errorf("bob lists lime after removal = %v, want 403", err)
	}
	if _, err := as("bob", "").Fetch(ctx, mustPackID(t, "acme/rules"), "latest"); statusOf(err) != http.StatusForbidden {
		t.Errorf("bob reads acme after leaving its only team = %v, want 403", err)
	}
	host.must("", "member", "remove", "--namespace", "web", "--email", "carol@example.com")
	if _, err := as("carol", "acme").Packages(ctx); statusOf(err) != http.StatusForbidden {
		t.Errorf("carol lists acme after leaving web = %v, want 403", err)
	}
	host.must("", "member", "add", "--namespace", "web", "--email", "alice@example.com")
	if _, err := as("alice", "web").Packages(ctx); err != nil {
		t.Errorf("alice lists web right after being added: %v", err)
	}
	host.must("", "member", "remove", "--namespace", "web", "--email", "alice@example.com")
	if _, err := as("alice", "lime").Packages(ctx); err != nil {
		t.Errorf("alice lists lime after leaving web: %v", err)
	}

	// A machine token stays in its namespace whatever view it names.
	machine := tokenRE.FindString(host.must("", "token", "create", "--team", "lime", "--publish", "--label", "ci"))
	mc, err := client.New(base, machine)
	if err != nil {
		t.Fatal(err)
	}
	rels, err := mc.In("zeta").Packages(ctx)
	if err != nil {
		t.Fatalf("machine token with a zeta view lists: %v", err)
	}
	for _, r := range rels {
		if strings.HasPrefix(r.ID, "zeta/") || strings.HasPrefix(r.ID, "web/") {
			t.Errorf("machine token of lime listed %s", r.ID)
		}
	}
	if _, err := mc.In("zeta").Publish(ctx, agentPack(t, "zeta/ci", "1.0.0")); statusOf(err) != http.StatusForbidden {
		t.Errorf("machine token of lime publishes to zeta = %v, want 403", err)
	}
	if _, err := mc.In("lime").Publish(ctx, agentPack(t, "lime/ci", "1.0.0")); err != nil {
		t.Errorf("machine token of lime publishes to lime: %v", err)
	}
	if _, err := mc.Me(ctx); statusOf(err) != http.StatusForbidden {
		t.Errorf("GET /v1/me with a machine token = %v, want 403", err)
	}
}

// TestAccountTokenFourEyesSameUser: two account tokens of one person are
// still one person when approving.
func TestAccountTokenFourEyesSameUser(t *testing.T) {
	host := &person{session: newSession(t)}
	mails := &mailtest.Recorder{}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	base, _ := serveInBackground(t, host, env, externals{mailer: mails})
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	host.must("", "member", "add", "--namespace", "lime", "--email", "alice@example.com", "--role", "maintainer")
	web := signUp(t, base, mails, "alice@example.com")
	laptop, err := client.New(base, accountToken(t, base, web))
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := client.New(base, accountToken(t, base, web))
	if err != nil {
		t.Fatal(err)
	}
	// An older per-team login token of hers counts as her too.
	_, old := deviceSignIn(t, base, web, "lime", "lime")
	if old.Namespace != "lime" {
		t.Fatalf("per-team sign-in gave %+v, want a token of lime", old)
	}
	oldClient, err := client.New(base, old.Token)
	if err != nil {
		t.Fatal(err)
	}
	prop, err := laptop.Propose(t.Context(), agentPack(t, "lime/planner", "0.1.0"), api.ProposalRequest{Prompts: []string{"plan"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*client.Client{"her other account token": desktop, "her per-team token": oldClient} {
		if _, err := c.Approve(t.Context(), prop.ID, api.DecisionRequest{}); statusOf(err) != http.StatusForbidden {
			t.Errorf("approve with %s = %v, want 403", name, err)
		}
	}
	// The per-team token still works as before, and ignores a view.
	if _, err := oldClient.In("elsewhere").Publish(t.Context(), agentPack(t, "lime/old", "0.1.0")); err != nil {
		t.Errorf("publish with a per-team login token: %v", err)
	}
}

// TestAccountSignInPage covers the approval page of an account sign-in:
// the teams it lists are escaped, a person with no team is told so plainly,
// a CLI that asked for the account cannot be given one team, and every
// approval needs the CSRF token.
func TestAccountSignInPage(t *testing.T) {
	host := &person{session: newSession(t)}
	mails := &mailtest.Recorder{}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	base, _ := serveInBackground(t, host, env, externals{mailer: mails})
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	host.must("", "member", "add", "--namespace", "lime", "--email", "alice@example.com", "--role", "maintainer")

	c, err := client.New(base, "")
	if err != nil {
		t.Fatal(err)
	}
	// Someone with no team at all.
	nobody := signUp(t, base, mails, "nobody@example.com")
	start, err := c.StartDevice(t.Context(), api.AccountNamespace)
	if err != nil {
		t.Fatal(err)
	}
	_, _, page := nobody.send(http.MethodGet, "/auth/device?code="+start.UserCode, nil)
	if strings.Contains(page, "member of *") || !strings.Contains(page, "not in any team yet") {
		t.Errorf("device page for someone with no team:\n%s", page)
	}
	if status, _, _ := nobody.send(http.MethodPost, "/auth/device", url.Values{"code": {start.UserCode}, "namespace": {"*"}, "action": {"approve"}}); status != http.StatusForbidden {
		t.Errorf("someone with no team approves an account sign-in = %d, want 403", status)
	}

	alice := signUp(t, base, mails, "alice@example.com")
	start, err = c.StartDevice(t.Context(), api.AccountNamespace)
	if err != nil {
		t.Fatal(err)
	}
	status, _, page := alice.send(http.MethodGet, "/auth/device?code="+start.UserCode, nil)
	if status != http.StatusOK || !strings.Contains(page, "Every team you belong to") || !strings.Contains(page, "lime (maintainer)") || !strings.Contains(page, `name="namespace" value="*"`) {
		t.Errorf("account sign-in page = %d\n%s", status, page)
	}
	// Approving it for one team is refused: the CLI asked for the account.
	if status, _, _ := alice.send(http.MethodPost, "/auth/device", url.Values{"code": {start.UserCode}, "namespace": {"lime"}, "action": {"approve"}}); status != http.StatusForbidden {
		t.Errorf("approve an account sign-in for lime = %d, want refused", status)
	}
	// Without the CSRF token nothing is approved.
	form := url.Values{"code": {start.UserCode}, "namespace": {"*"}, "action": {"approve"}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/auth/device", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := alice.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("approve without the CSRF token = %d, want refused", resp.StatusCode)
	}
	if poll, err := c.PollDevice(t.Context(), start.DeviceCode); err != nil || poll.Status == api.DeviceApproved {
		t.Errorf("poll after a refused approval = %+v, %v; want no token", poll, err)
	}

	// A team name is escaped on the page. Names are identifiers, so this
	// only guards the template: a name with markup cannot be created.
	if _, errOut, code := host.runIn("", "namespace", "create", "<b>x</b>", "--parent", "acme"); code == 0 {
		t.Errorf("a namespace with markup was created: %s", errOut)
	}
}
