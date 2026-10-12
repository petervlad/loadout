package web_test

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/builtin"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/mail/mailtest"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
	"github.com/petervlad/loadout/internal/web"
)

// uiSite is a registry with the team pages: project acme with teams lime
// and koala below it, packages in lime and acme, and one proposal.
type uiSite struct {
	*site
	props    *registry.ProposalService
	roles    *account.Roles
	store    *accounttest.MemStore
	mem      *registrytest.MemStore
	proposal string
}

const xss = "<script>alert(1)</script>"

func newUISite(t *testing.T) *uiSite {
	t.Helper()
	return newUISiteWith(t, nil)
}

// newUISiteWith is newUISite with a change to the pages' services, such
// as a server report.
func newUISiteWith(t *testing.T, change func(*web.UI)) *uiSite {
	t.Helper()
	return newUISiteDocs(t, change, "")
}

// newUISiteDocs is newUISiteWith with the registry's docs address set.
func newUISiteDocs(t *testing.T, change func(*web.UI), docsURL string) *uiSite {
	t.Helper()
	ctx, op := t.Context(), account.Operator()
	log := slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, time.Now, log, registry.WithMemberships(roles))
	props := registry.NewProposalService(regMem, regMem, time.Now, log, registry.WithOwners(roles))
	mails := &mailtest.Recorder{}
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{
		PublicURL: "http://localhost", Mailer: mails, Wiki: registry.NewWikiService(regMem, regMem, time.Now, log), Now: time.Now, Log: log,
	})
	for _, ns := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{{"acme", "", registry.KindProject}, {"lime", "acme", registry.KindTeam}, {"koala", "acme", registry.KindTeam}} {
		if _, err := reg.CreateNamespace(ctx, ns.name, ns.parent, ns.kind); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []struct {
		email, ns string
		role      account.Role
	}{
		{"alice@example.com", "lime", account.RoleMaintainer},
		{"bob@example.com", "lime", account.RoleMember},
		{"dan@example.com", "lime", account.RoleMember},
		{"eve@example.com", "koala", account.RoleMaintainer},
	} {
		if _, _, err := accounts.AddMember(ctx, op, m.ns, m.email, m.role); err != nil {
			t.Fatal(err)
		}
	}
	ui := &web.UI{Registry: reg, Proposals: props, Places: roles, Teams: accounts, Tools: []web.ModelTool{claudecode.New(), copilot.New()}, Now: time.Now, Wiki: accounts}
	if change != nil {
		change(ui)
	}
	h, err := web.New(web.Config{
		PublicURL: "http://localhost", PerIP: allowAll{}, PerEmail: allowAll{}, Log: log,
		UI: ui, DocsURL: docsURL,
	}, accounts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := &uiSite{site: &site{t: t, srv: srv, accounts: accounts, reg: reg, mails: mails}, props: props, roles: roles, store: store, mem: regMem}

	s.publish("lime", "planner", "2.3.0", "Plans work "+xss, "Keep plans short.\n")
	s.publish("acme", "python-standards", "4.1.0", "House rules for Python", "Follow the rules.\n")
	bob := s.principal("bob@example.com")
	p, err := props.Propose(ctx, bob, bundleOf(t, "lime", "planner", "2.4.0", "Plans work", "Name every file.\n"),
		registry.ProposalInput{Note: "Names the files " + xss, Prompts: []string{"Plan INFOTM-1", "Plan a migration"}})
	if err != nil {
		t.Fatal(err)
	}
	s.proposal = p.ID
	return s
}

func bundleOf(t *testing.T, team, name, version, desc, body string) []byte {
	t.Helper()
	return encode(t, map[string][]byte{
		pack.ManifestFile: manifest(team, name, "agent", version, desc),
		pack.AgentFile:    []byte("---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body),
	})
}

func manifest(team, name, kind, version, desc string) []byte {
	return []byte("name: " + team + "/" + name + "\nkind: " + kind + "\nversion: " + version + "\ndescription: " + desc + "\n")
}

// encode bundles a package from its files.
func encode(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	p, err := pack.FromFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	data, err := bundle.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// publish releases an agent with a machine token of its namespace.
func (s *uiSite) publish(team, name, version, desc, body string) {
	s.t.Helper()
	s.publishBundle(team, bundleOf(s.t, team, name, version, desc, body))
}

// publishBundle releases a bundle with a machine token of team.
func (s *uiSite) publishBundle(team string, data []byte) {
	s.t.Helper()
	secret, _, err := s.reg.CreateToken(s.t.Context(), team, registry.ScopePublish, "ci")
	if err != nil {
		s.t.Fatal(err)
	}
	pr, err := s.reg.Authenticate(s.t.Context(), secret)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.reg.Publish(s.t.Context(), pr, data); err != nil {
		s.t.Fatal(err)
	}
}

// principal is what the web pages act as for the user in lime.
func (s *uiSite) principal(email string) registry.Principal {
	s.t.Helper()
	u, err := s.store.UserByEmail(s.t.Context(), email)
	if err != nil {
		s.t.Fatal(err)
	}
	pr, _, err := s.roles.Principal(s.t.Context(), u, "lime")
	if err != nil {
		s.t.Fatal(err)
	}
	return pr
}

// as signs a new browser in as email (signing up with a password first).
func (s *uiSite) as(email string) *browser {
	s.t.Helper()
	b := s.browser()
	b.get("/auth/signup")
	n := s.mails.Count(email)
	if p := b.post("/auth/signup", url.Values{"email": {email}, "password": {pw}}); p.status != http.StatusSeeOther {
		s.t.Fatalf("signup %s = %d\n%s", email, p.status, p.body)
	}
	code := codeRE.FindStringSubmatch(s.mails.WaitFor(s.t, email, n).Text)
	if p := b.post("/auth/verify", url.Values{"code": {code[1]}}); p.status != http.StatusSeeOther {
		s.t.Fatalf("verify %s = %d\n%s", email, p.status, p.body)
	}
	return b
}

func (s *uiSite) addReport(withData bool) {
	s.t.Helper()
	rep := review.Report{
		Tool: "claude", Package: "lime/planner", Runs: 1,
		Current:   &review.Version{Version: "2.3.0"},
		Candidate: review.Version{Version: "2.4.0"},
		Prompts: []review.PromptResult{{Prompt: "Plan INFOTM-1", Runs: []review.RunPair{{
			Current:   &review.Outcome{Answer: review.Answer{Text: "old answer", Duration: 2 * time.Second, InputTokens: 3900, OutputTokens: 1100}},
			Candidate: review.Outcome{Answer: review.Answer{Text: "new answer " + xss, Duration: time.Second}},
		}}}, {Prompt: "Plan a migration", Runs: []review.RunPair{{
			Current:   &review.Outcome{Answer: review.Answer{Text: longAnswer, Duration: 11800 * time.Millisecond, InputTokens: 3700, OutputTokens: 900}},
			Candidate: review.Outcome{Answer: review.Answer{Text: strings.Repeat("step ", 160) + "done.", Duration: time.Second}},
		}, {
			Current:   &review.Outcome{Answer: review.Answer{Text: "short", Duration: time.Second}},
			Candidate: review.Outcome{Err: "rate limited"},
		}}}},
	}
	var data []byte
	if withData {
		var err error
		if data, err = json.Marshal(rep); err != nil {
			s.t.Fatal(err)
		}
	}
	if _, err := s.props.AddReview(s.t.Context(), s.principal("alice@example.com"), s.proposal, []byte("# Report\n\n"+xss+"\n"), data); err != nil {
		s.t.Fatal(err)
	}
}

// longAnswer is 411 words: a list, a table, a code block, then prose to
// its last sentence.
var longAnswer = "1. First " + strings.Repeat("word ", 60) + "\n2. Second " + strings.Repeat("word ", 60) + "\n\n" +
	"| file | change |\n|---|---|\n| a.go | retry |\n\n```\nretry(3)\n```\n\n" +
	strings.Repeat("more ", 280) + "The last sentence " + xss + "."

func wantIn(t *testing.T, p page, what string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(p.body, w) {
			t.Errorf("%s: missing %q\n%s", what, w, p.body)
		}
	}
}

func wantOut(t *testing.T, p page, what string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(p.body, w) {
			t.Errorf("%s: has %q, want it absent", what, w)
		}
	}
}

func TestTeamPagesNeedASession(t *testing.T) {
	s := newUISite(t)
	b := s.browser()
	for _, path := range []string{"/", "/t/lime", "/t/lime?q=a", "/account", "/t/lime/reviews/" + s.proposal} {
		p := b.get(path)
		want := "/auth/login?" + url.Values{"next": {path}}.Encode()
		if p.status != http.StatusSeeOther || p.location != want {
			t.Errorf("GET %s = %d %s, want a redirect to sign in and back", path, p.status, p.location)
		}
	}
}

func TestHomePage(t *testing.T) {
	s := newUISite(t)
	p := s.as("alice@example.com").get("/")
	if p.status != http.StatusOK {
		t.Fatalf("GET / = %d %s, want the home page", p.status, p.location)
	}
	wantIn(t, p, "home", "Publish once", "How it works", `<span class="flow-layer">acme<span>`, `flow-team">lime<span>`,
		"loadout login --remote http://localhost &amp;&amp; loadout install --all", `class="brand" href="/" aria-current="page"`)
}

func TestPackagesPage(t *testing.T) {
	s := newUISite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/lime")
	if p.status != http.StatusOK {
		t.Fatalf("GET /t/lime = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "packages", "lime/planner", "acme/python-standards", "2.3.0", "&lt;script&gt;alert(1)&lt;/script&gt;",
		"loadout login --remote http://localhost &amp;&amp; loadout install --all", `aria-current="page">Packages`)
	wantOut(t, p, "packages", xss)

	p = b.get("/t/lime?q=PYTHON")
	wantIn(t, p, "search", "acme/python-standards")
	wantOut(t, p, "search", "lime/planner")
	wantIn(t, b.get("/t/lime?q=nothing-like-this"), "empty search", "No packages match")

	p = b.get("/t/lime/p/lime/planner")
	wantIn(t, p, "package", "Keep plans short.", "loadout install lime/planner")
	wantOut(t, p, "package", xss)
}

// TestWideListPages checks that list pages grow with the window and
// reading pages keep their measure.
func TestWideListPages(t *testing.T) {
	s := newUISite(t)
	b := s.as("alice@example.com")
	tests := []struct {
		path string
		wide bool
	}{
		{"/t/lime", true},
		{"/t/lime/reviews", true},
		{"/t/lime/settings", true},
		{"/t/lime/p/lime/planner", false},
		{"/t/lime/reviews/" + s.proposal, false},
		{"/account", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			p := b.get(tc.path)
			if p.status != http.StatusOK {
				t.Fatalf("GET %s = %d", tc.path, p.status)
			}
			if got := strings.Contains(p.body, `<main class="main main-wide"`); got != tc.wide {
				t.Errorf("GET %s wide = %v, want %v", tc.path, got, tc.wide)
			}
		})
	}
}

// skillFiles is lime/release-notes, a skill with a reference file.
func skillFiles(version, reference string, more map[string]string) map[string][]byte {
	files := map[string][]byte{
		pack.ManifestFile:          manifest("lime", "release-notes", "skill", version, "Writes release notes"),
		"SKILL.md":                 []byte("---\nname: release-notes\ndescription: Writes release notes. Use when asked for them.\n---\nCollect the merged PRs first.\n"),
		"references/format.md":     []byte(reference),
		"references/checklist.txt": []byte("tag\nnotes\n"),
	}
	for k, v := range more {
		files[k] = []byte(v)
	}
	return files
}

// publishKinds adds a skill and a knowledge base to lime.
func (s *uiSite) publishKinds() {
	s.t.Helper()
	s.publishBundle("lime", encode(s.t, skillFiles("1.0.0", "# Format\n\nOne line per PR.\n", nil)))
	s.publishBundle("lime", encode(s.t, map[string][]byte{
		pack.ManifestFile:      manifest("lime", "runbooks", "kb", "0.3.0", "How the team runs its services"),
		"ops/deploy.md":        []byte("# Deploy " + xss + "\n\nRun make deploy.\n"),
		"ops/oncall/paging.md": []byte("# Paging\n\nWho gets paged.\n"),
	}))
}

func TestPackageKinds(t *testing.T) {
	s := newUISite(t)
	s.publishKinds()
	b := s.as("bob@example.com")
	p := b.get("/t/lime")
	wantIn(t, p, "packages", `pill-kind">Agent<`, `pill-kind">Skill<`, `pill-kind">KB<`, "lime/release-notes", "lime/runbooks")
	wantIn(t, b.get("/t/lime?q=kb"), "search by short kind", "lime/runbooks")
	p = b.get("/t/lime?q=knowledge")
	wantIn(t, p, "kind search", "lime/runbooks")
	wantOut(t, p, "kind search", "lime/planner", "lime/release-notes")

	p = b.get("/t/lime/p/lime/release-notes")
	if p.status != http.StatusOK {
		t.Fatalf("skill page = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "skill", "SKILL.md in 1.0.0", "Collect the merged PRs first.", "Files in 1.0.0", "3 files",
		`<a class="name" href="/t/lime/p/lime/release-notes/1.0.0/files/references/checklist.txt"><code>references/checklist.txt</code></a>`, "references/format.md", "10 bytes", "loadout install lime/release-notes")
	wantOut(t, p, "skill", "loadout.yaml")
	// The main file first, then the rest sorted.
	if i, j := strings.Index(p.body, ">SKILL.md<"), strings.Index(p.body, ">references/checklist.txt<"); i < 0 || j < i {
		t.Errorf("skill files out of order: SKILL.md at %d, checklist at %d", i, j)
	}

	p = b.get("/t/lime/p/lime/runbooks")
	if p.status != http.StatusOK {
		t.Fatalf("kb page = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "kb", "Generated SKILL.md in 0.3.0", "Documents in 0.3.0", "2 documents", "Deploy &lt;script&gt;alert(1)&lt;/script&gt;",
		"ops/deploy.md", "ops/oncall/paging.md", "Paging", "This is a knowledge base.")
	wantOut(t, p, "kb", xss)

	p = b.get("/t/lime/p/lime/planner")
	wantIn(t, p, "agent", "agent.md in 2.3.0", `pill-kind">Agent<`)
	wantOut(t, p, "agent", "Files in")
}

// A skill proposal shows every changed file: the main file unchanged, a
// changed reference, an added and a removed file.
func TestSkillProposalDiff(t *testing.T) {
	s := newUISite(t)
	s.publishKinds()
	files := skillFiles("1.1.0", "# Format\n\nOne line per PR, "+xss+".\n", map[string]string{"references/style.md": "Short sentences.\n"})
	delete(files, "references/checklist.txt")
	p, err := s.props.Propose(t.Context(), s.principal("bob@example.com"), encode(t, files), registry.ProposalInput{Prompts: []string{"Notes for 1.2"}})
	if err != nil {
		t.Fatal(err)
	}
	b := s.as("alice@example.com")
	page := b.get("/t/lime/reviews/" + p.ID)
	if page.status != http.StatusOK {
		t.Fatalf("proposal = %d\n%s", page.status, page.body)
	}
	wantIn(t, page, "preview", `<h3 class="file">references/checklist.txt <span class="change">(removed)</span>`,
		"One line per PR, &lt;script&gt;", `<h3 class="file">references/style.md <span class="change">(added)</span>`)
	wantOut(t, page, "preview", xss, "loadout.yaml", "The skill itself is unchanged")

	diff := b.get("/t/lime/reviews/" + p.ID + "/diff")
	wantIn(t, diff, "diff", "references/format.md", "One line per PR.", "Short sentences.", "tag", `<h3 class="file">loadout.yaml</h3>`, `<div class="diff wide">`)
	wantOut(t, diff, "diff", xss, `<h3 class="file">SKILL.md`)
	// Reading order: supporting files sorted, the manifest last.
	order := []string{">references/checklist.txt", ">references/format.md", ">references/style.md", ">loadout.yaml"}
	last := -1
	for _, name := range order {
		i := strings.Index(diff.body, name)
		if i < last {
			t.Errorf("diff: %s at %d, want after %d", name, i, last)
		}
		last = i
	}
}

// A team member reads the project above through the team: its packages,
// without reviews or members.
func TestProjectReadThroughTheTeam(t *testing.T) {
	s := newUISite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/acme")
	if p.status != http.StatusOK {
		t.Fatalf("GET /t/acme = %d", p.status)
	}
	wantIn(t, p, "acme", "acme/python-standards", "You read acme through")
	wantOut(t, p, "acme", "lime/planner", `aria-label="Team sections"`)
	for _, path := range []string{"/t/acme/reviews", "/t/acme/settings"} {
		if p := b.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
}

// A namespace the user cannot open looks exactly like one that does not
// exist.
func TestNoExistenceLeak(t *testing.T) {
	s := newUISite(t)
	b := s.as("eve@example.com")
	missing := b.get("/t/ghost")
	for _, path := range []string{"/t/lime", "/t/lime/reviews", "/t/lime/settings", "/t/lime/reviews/" + s.proposal, "/t/lime/p/lime/planner", "/t/ghost", "/t/BAD..name"} {
		p := b.get(path)
		if p.status != http.StatusNotFound || !strings.Contains(p.body, "Nothing here") {
			t.Errorf("GET %s = %d, want the 404 page", path, p.status)
		}
		wantOut(t, p, path, "lime/planner", "python-standards")
	}
	if missing.status != http.StatusNotFound {
		t.Errorf("GET /t/ghost = %d, want 404", missing.status)
	}
	// Eve's own team's page must not list another team's proposal either.
	if p := b.get("/t/koala/reviews/" + s.proposal); p.status != http.StatusNotFound {
		t.Errorf("GET lime's proposal under koala = %d, want 404", p.status)
	}
}

func TestProposalPageByRole(t *testing.T) {
	s := newUISite(t)
	s.addReport(true)
	path := "/t/lime/reviews/" + s.proposal
	approve := `action="/t/lime/reviews/` + s.proposal + `/approve"`
	tests := []struct {
		email    string
		want     []string
		unwanted []string
	}{
		{"alice@example.com", []string{approve, "Reject…", "You can approve"}, []string{"disabled"}},
		{"bob@example.com", []string{"Withdraw…", "disabled", "You proposed this version"}, []string{approve}},
		{"dan@example.com", []string{"disabled", "Only maintainers of lime approve"}, []string{approve, "Withdraw…"}},
	}
	for _, tc := range tests {
		t.Run(tc.email, func(t *testing.T) {
			p := s.as(tc.email).get(path)
			if p.status != http.StatusOK {
				t.Fatalf("GET %s = %d\n%s", path, p.status, p.body)
			}
			wantIn(t, p, tc.email, tc.want...)
			wantOut(t, p, tc.email, tc.unwanted...)
			// The report's answers side by side, escaped.
			wantIn(t, p, tc.email, "old answer", "new answer &lt;script&gt;", "Current · 2.3.0", "Candidate · 2.4.0",
				"3.9k in / 1.1k out", "Names the files &lt;script&gt;", "loadout review lime/planner</code>", "Name every file.")
			wantOut(t, p, tc.email, xss)
		})
	}
}

func TestProposalCutsLongAnswers(t *testing.T) {
	s := newUISite(t)
	s.addReport(true)
	alice := s.as("alice@example.com")
	base := "/t/lime/reviews/" + s.proposal

	short := alice.get(base + "?report=1&prompt=2")
	wantIn(t, short, "short view", "11.8 s · 3.7k in / 900 out · 411 words", "1.0 s · 161 words",
		"Long answers show their first 150 words.",
		`href="?report=1&amp;prompt=2&amp;run=1&amp;full=1#answers">Show whole answers</a>`,
		`<p class="answer-more">261 more words</p>`, `id="answers" tabindex="-1"`, "step done.")
	wantOut(t, short, "short view", "The last sentence", `name="full"`, "Show the first", xss)

	full := alice.get(base + "?report=1&prompt=2&full=1")
	wantIn(t, full, "full view", "The last sentence &lt;script&gt;", "<table>",
		`href="?report=1&amp;prompt=2&amp;run=1#answers">Show the first 150 words</a>`, `<input type="hidden" name="full" value="1">`)
	wantOut(t, full, "full view", "Show whole answers", "answer-more", xss)

	// The medium answer is shown whole, a failed run has no count, and
	// with nothing cut there is no bar.
	failed := alice.get(base + "?report=1&prompt=2&run=2")
	wantIn(t, failed, "failed run", "failed", "This run failed: rate limited", "1.0 s · 1 word")
	wantOut(t, failed, "failed run", "answer-bar", "answer-more", "failed ·")
	first := alice.get(base)
	wantOut(t, first, "short answers", "answer-bar", "Show whole answers")
}

func TestProposalShowsATextOnlyReport(t *testing.T) {
	s := newUISite(t)
	s.addReport(false)
	p := s.as("alice@example.com").get("/t/lime/reviews/" + s.proposal)
	wantIn(t, p, "text report", `<pre class="markdown"># Report`, "&lt;script&gt;")
	wantOut(t, p, "text report", xss)
}

func TestDecisions(t *testing.T) {
	s := newUISite(t)
	base := "/t/lime/reviews/" + s.proposal

	dan := s.as("dan@example.com")
	dan.get(base)
	if p := dan.post(base+"/approve", url.Values{}); p.status != http.StatusForbidden {
		t.Errorf("approve by a member = %d, want 403", p.status)
	}
	if p := dan.post(base+"/reject", url.Values{}); p.status != http.StatusForbidden {
		t.Errorf("reject by a member = %d, want 403", p.status)
	}
	bob := s.as("bob@example.com")
	if p := bob.post(base+"/approve", url.Values{}); p.status != http.StatusForbidden {
		t.Errorf("approve by the author = %d, want 403", p.status)
	}

	alice := s.as("alice@example.com")
	if p := alice.post(base+"/approve", url.Values{"csrf": {"forged"}}); p.status != http.StatusForbidden {
		t.Errorf("approve with a bad CSRF token = %d, want 403", p.status)
	}
	if p := alice.post(base+"/nonsense", url.Values{}); p.status != http.StatusNotFound {
		t.Errorf("unknown action = %d, want 404", p.status)
	}
	p := alice.post(base+"/approve", url.Values{})
	if p.status != http.StatusSeeOther || p.location != base+"?done=approved" {
		t.Fatalf("approve = %d %s\n%s", p.status, p.location, p.body)
	}
	id, err := pack.ParseID("lime/planner")
	if err != nil {
		t.Fatal(err)
	}
	rels, err := s.reg.Releases(t.Context(), s.principal("alice@example.com"), id)
	if err != nil || len(rels) != 2 || rels[1].Version.String() != "2.4.0" {
		t.Errorf("releases after approval = %+v, %v; want 2.4.0 released", rels, err)
	}
	page := alice.get(p.location)
	wantIn(t, page, "approved", "Released", "Approved. Everyone gets the new version", "approved and released it")
	wantOut(t, page, "approved", `/approve"`)
	if p := alice.post(base+"/reject", url.Values{}); p.status != http.StatusConflict {
		t.Errorf("reject after approval = %d, want 409", p.status)
	}
}

// ownProposal makes olga an owner of acme and has her propose lime/planner
// 2.5.0 in lime, the team below. It returns the proposal's page.
func (s *uiSite) ownProposal() string {
	s.t.Helper()
	ctx := s.t.Context()
	if _, _, err := s.accounts.AddMember(ctx, account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		s.t.Fatal(err)
	}
	p, err := s.props.Propose(ctx, s.principal("olga@example.com"), bundleOf(s.t, "lime", "planner", "2.5.0", "Plans work", "Name every file twice.\n"),
		registry.ProposalInput{Note: "Urgent fix", Prompts: []string{"Plan INFOTM-1"}})
	if err != nil {
		s.t.Fatal(err)
	}
	return "/t/lime/reviews/" + p.ID
}

func TestOwnerApprovesOwnProposal(t *testing.T) {
	s := newUISite(t)
	base := s.ownProposal()
	olga := s.as("olga@example.com")

	p := olga.get(base)
	wantIn(t, p, "owner's own proposal", "Withdraw…", "Approve as owner…", `name="as_owner" value="on"`,
		`name="note" class="field" maxlength="4096" required`, "without a second person's review", "No comparison is attached.",
		"As an owner of lime you may release it yourself", "Approve without review")
	wantOut(t, p, "owner's own proposal", "Reject…", `disabled aria-describedby="why"`)

	// A maintainer's own proposal still waits for a teammate.
	wantIn(t, s.as("bob@example.com").get("/t/lime/reviews/"+s.proposal), "member's own proposal", "disabled", "a teammate who maintains lime approves it")

	if p := olga.post(base+"/approve", url.Values{"as_owner": {"on"}, "csrf": {"forged"}}); p.status != http.StatusForbidden {
		t.Errorf("approve as owner with a bad CSRF token = %d, want 403", p.status)
	}
	if p := olga.post(base+"/approve", url.Values{"note": {"why"}}); p.status != http.StatusForbidden {
		t.Errorf("approve own without as_owner = %d, want 403", p.status)
	}
	p = olga.post(base+"/approve", url.Values{"as_owner": {"on"}, "note": {"  "}})
	if p.status != http.StatusBadRequest {
		t.Fatalf("approve as owner without a note = %d, want 400\n%s", p.status, p.body)
	}
	wantIn(t, p, "no note", "Say why lime/planner@2.5.0 is released without review")

	p = olga.post(base+"/approve", url.Values{"as_owner": {"on"}, "note": {"Hotfix " + xss}})
	if p.status != http.StatusSeeOther || p.location != base+"?done=approved-as-owner" {
		t.Fatalf("approve as owner = %d %s\n%s", p.status, p.location, p.body)
	}
	page := olga.get(p.location)
	wantIn(t, page, "released", "Released without review", "approved own proposal as owner, without review: “Hotfix &lt;script&gt;",
		"dot-warn", "Released without review. Everyone gets the new version")
	wantOut(t, page, "released", `/approve"`, xss)
	wantIn(t, olga.get("/t/lime/reviews?status=all"), "reviews list", "Released without review", "Waiting for review")
	wantIn(t, olga.get("/t/lime/p/lime/planner"), "package page", "Without review")
}

func TestWithdraw(t *testing.T) {
	s := newUISite(t)
	base := "/t/lime/reviews/" + s.proposal
	bob := s.as("bob@example.com")
	if p := bob.post(base+"/withdraw", url.Values{"note": {"not yet"}}); p.status != http.StatusSeeOther {
		t.Fatalf("withdraw by the author = %d\n%s", p.status, p.body)
	}
	p := bob.get("/t/lime/reviews?status=all")
	wantIn(t, p, "reviews", "Withdrawn", "lime/planner")
	wantIn(t, bob.get("/t/lime/reviews"), "open reviews", "Nothing waiting for review")
}

func TestMembersPage(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	p := alice.get("/t/lime/settings")
	wantIn(t, p, "maintainer", `action="/t/lime/members"`, "bob@example.com", "dan@example.com", "Tokens for machines", "(you)", "Added, has not signed in yet")
	wantOut(t, p, "maintainer", `value="owner"`, `name="require_review"`)

	bob := s.as("bob@example.com")
	p = bob.get("/t/lime/settings")
	wantIn(t, p, "member", "dan@example.com", "alice@example.com")
	wantOut(t, p, "member", `action="/t/lime/members"`, `action="/t/lime/members/remove"`, "Tokens for machines")
	if p := bob.post("/t/lime/members", url.Values{"email": {"x@example.com"}, "role": {"member"}}); p.status != http.StatusForbidden {
		t.Errorf("add by a member = %d, want 403", p.status)
	}
	if p := bob.post("/t/lime/members/remove", url.Values{"email": {"dan@example.com"}}); p.status != http.StatusForbidden {
		t.Errorf("remove by a member = %d, want 403", p.status)
	}

	p = alice.post("/t/lime/members", url.Values{"email": {"New@Example.com"}, "role": {"member"}})
	if p.status != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", p.status, p.body)
	}
	wantIn(t, alice.get(p.location), "after add", "New@Example.com was added", "new@example.com")
	if p := alice.post("/t/lime/members", url.Values{"email": {"not an email"}, "role": {"member"}}); p.status != http.StatusBadRequest || !strings.Contains(p.body, "valid email") {
		t.Errorf("add a bad email = %d, want 400 with the reason\n%s", p.status, p.body)
	} else {
		wantIn(t, p, "bad email", `value="not an email" aria-invalid="true" aria-describedby="form-error" autofocus>`)
	}
	if p := alice.post("/t/lime/members", url.Values{"email": {"x@example.com"}, "role": {"maintainer"}}); p.status != http.StatusForbidden {
		t.Errorf("maintainer adds a maintainer = %d, want 403 (only owners do)", p.status)
	}
	if p := alice.post("/t/lime/members/role", url.Values{"email": {"dan@example.com"}, "role": {"member"}}); p.status != http.StatusSeeOther {
		t.Errorf("set role = %d", p.status)
	}
	if p := alice.post("/t/lime/members/remove", url.Values{"email": {"dan@example.com"}}); p.status != http.StatusSeeOther {
		t.Errorf("remove = %d", p.status)
	}
	wantOut(t, alice.get("/t/lime/settings"), "after remove", "dan@example.com")
}

// TestInheritedMembers checks that a team's page lists the owners of the
// project above, whose role there is computed, not stored.
func TestInheritedMembers(t *testing.T) {
	s := newUISite(t)
	ctx, op := t.Context(), account.Operator()
	if _, err := s.reg.CreateNamespace(ctx, "wombat", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	// Olga owns acme; a project maintainer gets no rights below, so Pat is
	// not listed on the teams.
	for _, m := range []struct {
		email string
		role  account.Role
	}{{"olga@example.com", account.RoleOwner}, {"pat@example.com", account.RoleMaintainer}} {
		if _, _, err := s.accounts.AddMember(ctx, op, "acme", m.email, m.role); err != nil {
			t.Fatal(err)
		}
	}
	olgaUser, err := s.store.UserByEmail(ctx, "olga@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.LinkGoogle(ctx, olgaUser.ID, "g-olga", "Olga "+xss); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")

	// The owner on a team with no rows of its own: no claim that it is
	// empty of people, and a link to where her role is set.
	p := olga.get("/t/wombat/settings")
	wantIn(t, p, "empty team", "Nobody is added to wombat directly", "Owners of the project above already manage it; they are listed below.",
		"Through the project", "Owner, from acme", "(you)", `href="/t/acme/settings"`, "Manage in acme", "Olga &lt;script&gt;")
	wantOut(t, p, "empty team", xss, "pat@example.com", `action="/t/wombat/members/remove"`, `action="/t/wombat/members/role"`)

	// Inherited owners get no role controls or remove button on the team.
	if p := olga.post("/t/wombat/members/remove", url.Values{"email": {"olga@example.com"}}); p.status == http.StatusSeeOther {
		t.Errorf("remove an inherited owner from the team = %d, want a refusal", p.status)
	}

	// A team member reads acme only through lime, so there is no link.
	bob := s.as("bob@example.com")
	p = bob.get("/t/lime/settings")
	wantIn(t, p, "member of lime", "Through the project", "Owner, from acme", "olga@example.com", "alice@example.com")
	wantOut(t, p, "member of lime", `href="/t/acme/settings"`, "Nobody is added", "pat@example.com")

	// A member of acme itself may open its page, but not manage it.
	if _, _, err := s.accounts.AddMember(ctx, op, "acme", "bob@example.com", account.RoleMember); err != nil {
		t.Fatal(err)
	}
	wantIn(t, bob.get("/t/lime/settings"), "member of acme", `href="/t/acme/settings"`, "View in acme")

	// The project's own page lists its owners as its members, with no
	// section for a project above.
	p = olga.get("/t/acme/settings")
	wantIn(t, p, "project", "olga@example.com", "pat@example.com")
	wantOut(t, p, "project", "Through the project")
}

// TestNoInheritedMembers checks that the section and the empty state's
// mention of it stay away when nobody above owns the team.
func TestNoInheritedMembers(t *testing.T) {
	s := newUISite(t)
	p := s.as("alice@example.com").get("/t/lime/settings")
	wantOut(t, p, "no owners", "Through the project", "Nobody is added")
	if _, err := s.accounts.SetAdmin(t.Context(), account.Operator(), "root@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reg.CreateNamespace(t.Context(), "solo", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	p = s.as("root@example.com").get("/t/solo/settings")
	wantIn(t, p, "admin on an empty team", "Nobody is added to solo directly", "Add people here to give them a role in solo.")
	wantOut(t, p, "admin on an empty team", "Through the project", "already manage it")
}

func TestMachineTokens(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	p := alice.post("/t/lime/tokens", url.Values{"label": {"zuul " + xss}, "scope": {"read"}})
	if p.status != http.StatusOK || !strings.Contains(p.body, registry.TokenPrefix) || !strings.Contains(p.body, "not shown again") {
		t.Fatalf("create token = %d, want the secret once\n%s", p.status, p.body)
	}
	wantOut(t, p, "token", xss)
	if cc := p.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	list := alice.get("/t/lime/settings")
	wantOut(t, list, "token list", registry.TokenPrefix)
	toks, err := s.accounts.MachineTokens(t.Context(), account.Operator(), "lime")
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, tok := range toks {
		if strings.HasPrefix(tok.Label, "zuul") {
			id = tok.ID
		}
	}
	if p := s.as("bob@example.com").post("/t/lime/tokens/revoke", url.Values{"id": {id}}); p.status != http.StatusForbidden {
		t.Errorf("revoke by a member = %d, want 403", p.status)
	}
	if p := alice.post("/t/lime/tokens/revoke", url.Values{"id": {id}}); p.status != http.StatusSeeOther {
		t.Errorf("revoke = %d", p.status)
	}
}

// Every POST needs the CSRF token, whoever sends it.
func TestTeamPostsNeedCSRF(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	base := "/t/lime/reviews/" + s.proposal
	for _, path := range []string{
		base + "/approve", base + "/reject", base + "/withdraw", base + "/prompts", base + "/prompts/1", base + "/prompts/1/remove",
		"/t/lime/members", "/t/lime/members/role", "/t/lime/members/remove", "/t/acme/invite",
		"/t/lime/tokens", "/t/lime/tokens/revoke", "/t/lime/settings", "/t/lime/settings/review", "/t/lime/settings/wiki", "/account/theme", "/new",
	} {
		if p := alice.post(path, url.Values{"csrf": {"wrong"}, "email": {"dan@example.com"}}); p.status != http.StatusForbidden {
			t.Errorf("POST %s with a wrong CSRF token = %d, want 403", path, p.status)
		}
	}
	if p, err := s.props.Proposal(t.Context(), s.principal("alice@example.com"), s.proposal); err != nil || p.Status != registry.ProposalOpen {
		t.Errorf("proposal after forged posts = %v, %v; want still open", p.Status, err)
	}
}

func TestRequireReviewSetting(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	if p := alice.post("/t/lime/settings", url.Values{"require_review": {"on"}}); p.status != http.StatusForbidden {
		t.Errorf("maintainer turns review on = %d, want 403 (owners do)", p.status)
	}
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	wantIn(t, olga.get("/t/lime/settings"), "owner", `name="require_review"`, `value="maintainer"`)
	wantIn(t, olga.get("/t/lime/reviews"), "reviews, owner", "Review before release is <strong>", `href="/t/lime/settings#review-h"`)
	wantIn(t, alice.get("/t/lime/reviews"), "reviews, member", "Review before release is <strong>")
	wantOut(t, alice.get("/t/lime/reviews"), "reviews, member", `href="/t/lime/settings#review-h"`)
	if p := olga.post("/t/lime/settings", url.Values{"require_review": {"on"}}); p.status != http.StatusSeeOther {
		t.Fatalf("owner turns review on = %d\n%s", p.status, p.body)
	}
	wantIn(t, olga.get("/t/lime/settings?done=review-on"), "review on", "Review is now required", "On: nobody publishes to lime directly")
}

func TestCreateNamespaces(t *testing.T) {
	s := newUISite(t)
	ctx := t.Context()
	exists := func(name string) bool {
		nss, err := s.reg.Namespaces(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, ns := range nss {
			if ns.Name == name {
				return true
			}
		}
		return false
	}

	alice := s.as("alice@example.com")
	wantOut(t, alice.get("/t/lime"), "maintainer sidebar", `href="/new"`)
	if p := alice.get("/new"); p.status != http.StatusNotFound {
		t.Errorf("new page for a maintainer = %d, want 404", p.status)
	}
	if p := alice.post("/new", url.Values{"kind": {"team"}, "name": {"sneaky"}, "parent": {"acme"}}); p.status == http.StatusSeeOther || exists("sneaky") {
		t.Errorf("maintainer creates a team = %d, want refused", p.status)
	}

	if _, _, err := s.accounts.AddMember(ctx, account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	wantIn(t, olga.get("/t/acme"), "project page of an owner", `href="/new?parent=acme"`, `href="/new"`)
	p := olga.get("/new?parent=acme")
	wantIn(t, p, "owner new page", "New team", `<option value="acme" selected>`, `aria-describedby="new-parent-hint"`, `id="new-parent-hint"`)
	wantOut(t, p, "owner new page", `value="project"`)
	p = olga.post("/new", url.Values{"kind": {"team"}, "name": {"web"}, "parent": {"acme"}})
	if p.status != http.StatusSeeOther {
		t.Fatalf("owner creates a team = %d\n%s", p.status, p.body)
	}
	wantIn(t, olga.get(p.location), "after create", "Team created", `<h1 class="title">web</h1>`)
	if p := olga.post("/new", url.Values{"kind": {"project"}, "name": {"globex"}}); p.status != http.StatusBadRequest || exists("globex") {
		t.Errorf("owner creates a project = %d, want 400", p.status)
	}
	if p := olga.post("/new", url.Values{"kind": {"team"}, "name": {"lime"}, "parent": {"acme"}}); p.status != http.StatusConflict || !strings.Contains(p.body, "is taken") {
		t.Errorf("taken name = %d, want 409\n%s", p.status, p.body)
	}
	if p := olga.post("/new", url.Values{"kind": {"team"}, "name": {"Bad Name"}, "parent": {"acme"}}); p.status != http.StatusBadRequest || !strings.Contains(p.body, `value="Bad Name" aria-invalid="true" aria-describedby="form-error new-name-hint"`) {
		t.Errorf("bad name = %d, want 400 with the form refilled\n%s", p.status, p.body)
	}

	root := s.as("root@example.com")
	if _, err := s.accounts.SetAdmin(ctx, account.Operator(), "root@example.com", true); err != nil {
		t.Fatal(err)
	}
	wantIn(t, root.get("/new"), "admin new page", "New project or team", `value="project"`)
	if p := root.post("/new", url.Values{"kind": {"project"}, "name": {"globex"}, "parent": {"acme"}}); p.status != http.StatusSeeOther {
		t.Fatalf("admin creates a project = %d\n%s", p.status, p.body)
	}
	nss, err := s.reg.Namespaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range nss {
		if ns.Name == "globex" && (ns.Kind != registry.KindProject || ns.Parent != "") {
			t.Errorf("globex = %+v, want a root project (the parent is ignored)", ns)
		}
	}
}

func TestSidebarTree(t *testing.T) {
	s := newUISite(t)
	ctx := t.Context()
	if _, err := s.reg.CreateNamespace(ctx, "globex", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reg.CreateNamespace(ctx, "solo", "", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ ns, email string }{{"solo", "alice@example.com"}, {"globex", "alice@example.com"}} {
		if _, _, err := s.accounts.AddMember(ctx, account.Operator(), m.ns, m.email, account.RoleMember); err != nil {
			t.Fatal(err)
		}
	}
	alice := s.as("alice@example.com")
	p := alice.get("/t/lime")
	// lime sits under acme, whose list is open because lime is the page;
	// globex has no teams of hers; solo has no project.
	wantIn(t, p, "sidebar on lime", `id="sub_acme" aria-label="Teams in acme">`, `aria-expanded="true"`, "Other teams", `href="/t/solo"`)
	wantOut(t, p, "sidebar on lime", `id="sub_globex"`)
	// A folded project keeps its teams in the page; only script hides them,
	// so they stay reachable without it.
	wantIn(t, alice.get("/t/globex"), "sidebar on globex", `id="sub_acme" aria-label="Teams in acme" data-folded>`, `aria-expanded="false"`)
	wantOut(t, alice.get("/t/globex"), "sidebar on globex", `aria-label="Teams in acme" hidden`)

	// One project: always open, and the heading is plain Teams.
	bob := s.as("bob@example.com")
	p = bob.get("/account")
	wantIn(t, p, "one project", `id="sub_acme" aria-label="Teams in acme">`)
	wantOut(t, p, "one project", "Other teams")
}

func TestThemeCookie(t *testing.T) {
	s := newUISite(t)
	b := s.as("alice@example.com")
	if p := b.get("/account"); !strings.Contains(p.body, `value="system" checked`) || strings.Contains(p.body, "data-theme") {
		t.Errorf("account page before a choice: want System checked and no data-theme")
	}
	if p := b.post("/account/theme", url.Values{"theme": {"dark"}}); p.status != http.StatusSeeOther {
		t.Fatalf("set theme = %d", p.status)
	}
	for _, path := range []string{"/account", "/t/lime", "/auth/device"} {
		wantIn(t, b.get(path), path, `data-theme="dark"`)
	}
	b.post("/account/theme", url.Values{"theme": {"system"}})
	wantOut(t, b.get("/t/lime"), "system theme", "data-theme")
	if b.cookie("loadout_theme") != "" {
		t.Error("theme cookie kept after choosing System")
	}
}

func TestUIScript(t *testing.T) {
	s := newUISite(t)
	p := s.browser().get("/auth/static/ui.js")
	if p.status != http.StatusOK || !strings.Contains(p.header.Get("Content-Type"), "javascript") {
		t.Errorf("GET ui.js = %d %s", p.status, p.header.Get("Content-Type"))
	}
	page := s.as("alice@example.com").get("/t/lime")
	if csp := page.header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP = %q, want script-src 'self' and nothing inline", csp)
	}
	wantOut(t, page, "page", "<script>", "style=\"")
}

func TestPackagesFilterAndSort(t *testing.T) {
	s := newUISite(t)
	s.publishKinds()
	b := s.as("bob@example.com")

	p := b.get("/t/lime")
	wantIn(t, p, "filters", `aria-current="true">All`, `href="/t/lime?kind=skill">Skills`, `href="/t/lime?kind=kb">KBs`, `name="sort"`)
	// The Releases column is the one narrower windows drop; the columns
	// after the first fit their content, and a long layer name truncates
	// in its pill with the whole name as a tooltip.
	wantIn(t, p, "columns", `<table class="list fit pkgs">`, `<th scope="col" class="opt">Releases</th>`, `<td class="muted opt">`,
		`<span class="pill pill-clip pill-own" title="lime">lime</span>`)
	wantOut(t, p, "columns", "<colgroup>")

	p = b.get("/t/lime?kind=skill")
	wantIn(t, p, "skills only", "lime/release-notes", `href="/t/lime?kind=skill" aria-current="true"`, `<input type="hidden" name="kind" value="skill">`)
	wantOut(t, p, "skills only", "lime/planner", "lime/runbooks")

	p = b.get("/t/lime?kind=agent&q=nothing-like-this")
	wantIn(t, p, "empty filter", "among agents")

	// Sorting by name puts acme/python-standards (name "python-standards")
	// after lime/planner; the default order keeps lime's own layer first.
	p = b.get("/t/lime?sort=name")
	if i, j := strings.Index(p.body, ">lime/planner<"), strings.Index(p.body, ">acme/python-standards<"); i < 0 || j < 0 || i > j {
		t.Errorf("sort=name: planner at %d, python-standards at %d; want planner first", i, j)
	}
	wantIn(t, p, "sort kept", `<option value="name" selected>`, `href="/t/lime?kind=kb&amp;sort=name"`)

	// Unknown values fall back to the defaults instead of failing.
	if p := b.get("/t/lime?kind=widget&sort=sideways"); p.status != http.StatusOK || !strings.Contains(p.body, `aria-current="true">All`) {
		t.Errorf("unknown kind and sort = %d, want the full list", p.status)
	}
}

// A team page can hide the layers above it, and each filter keeps the others.
func TestPackagesLayerFilter(t *testing.T) {
	s := newUISite(t)
	s.publishKinds()
	b := s.as("bob@example.com")

	p := b.get("/t/lime")
	wantIn(t, p, "layers", `aria-label="Show packages of one layer"`, `aria-current="true">All layers`, `href="/t/lime?layer=lime">lime`, `href="/t/lime?layer=acme">acme`)
	// Closest layer first.
	if i, j := strings.Index(p.body, `?layer=lime"`), strings.Index(p.body, `?layer=acme"`); i < 0 || j < i {
		t.Errorf("layer order: lime at %d, acme at %d", i, j)
	}

	p = b.get("/t/lime?layer=lime")
	wantIn(t, p, "own layer", "lime/planner", "lime/release-notes", `href="/t/lime?layer=lime" aria-current="true"`,
		`<input type="hidden" name="layer" value="lime">`, `href="/t/lime?kind=skill&amp;layer=lime">Skills`)
	wantOut(t, p, "own layer", "acme/python-standards")

	p = b.get("/t/lime?layer=acme&kind=skill")
	wantIn(t, p, "empty layer", "No skills here", `href="/t/lime?kind=skill">All layers`)

	// A layer the page cannot show falls back to all of them.
	for _, l := range []string{"koala", "nope"} {
		if p := b.get("/t/lime?layer=" + l); !strings.Contains(p.body, `aria-current="true">All layers`) {
			t.Errorf("layer=%s did not fall back to all layers", l)
		}
	}
	// A project page has one layer, so no layer filter.
	wantOut(t, b.get("/t/acme"), "project", `aria-label="Show packages of one layer"`)
}

// Every team page links to the guide, which links back to the same page.
// The sidebar's two help links, Docs and What's new, sit above the account
// item and tell the static page which registry page to link back to.
func TestDocsLink(t *testing.T) {
	s := newUISite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/lime?kind=skill")
	wantHelpLinks(t, p, "?from=http%3A%2F%2Flocalhost%2Ft%2Flime%3Fkind%3Dskill", `class="side-item side-user" href="/account"`)
	// A page without the sidebar has neither link.
	wantOut(t, b.get("/t/lime/reviews/"+s.proposal), "proposal page", "side-docs", "side-news")
}

// A registry without internet access points the Docs, What's new and
// install links at its own guide.
func TestDocsLinkFollowsTheConfiguredAddress(t *testing.T) {
	packs, err := builtin.Packs("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	s := newUISiteDocs(t, func(ui *web.UI) { ui.Skills = &web.BuiltinSkills{Packs: packs, Version: "1.2.3"} }, "https://docs.example.com/loadout/")
	b := s.as("bob@example.com")
	wantIn(t, b.get("/t/lime"), "docs link", `href="https://docs.example.com/loadout/?from=`, `href="https://docs.example.com/loadout/whats-new/?from=`)
	wantIn(t, b.get("/skills"), "install link", `href="https://docs.example.com/loadout/?from=http%3A%2F%2Flocalhost%2Fskills#1-install-the-cli"`)
	wantOut(t, b.get("/t/lime"), "docs link", "loadout.lat")
}

// wantHelpLinks checks the Docs and What's new items of the sidebar: their
// address with from, their word (visually hidden on phones, still the
// accessible name), no aria-current (the pages are on another site), and
// the order Docs, What's new, then the account item.
func wantHelpLinks(t *testing.T, p page, from, account string) {
	t.Helper()
	links := []struct{ class, href, label string }{
		{"side-docs", "https://loadout.lat/docs/", `<span class="side-docs-label">Docs</span></a>`},
		{"side-news", "https://loadout.lat/docs/whats-new/", `<span class="side-news-label">What's new</span></a>`},
	}
	at := 0
	for _, l := range links {
		open := `<a class="side-item ` + l.class + `" href="` + l.href + from + `">`
		i := strings.Index(p.body, open)
		if i < 0 {
			t.Errorf("missing %q\n%s", open, p.body)
			continue
		}
		if !strings.HasSuffix(p.body[i:i+strings.Index(p.body[i:], "</a>")+4], l.label) {
			t.Errorf("%s: want it to end in %q", l.class, l.label)
		}
		if i < at {
			t.Errorf("%s comes before the link above it", l.class)
		}
		at = i
	}
	if i := strings.Index(p.body, account); i < at {
		t.Errorf("account item %q at %d, want it after the help links (%d)", account, i, at)
	}
}

// Signing out everywhere asks first; the plain Sign out does not.
func TestSignOutEverywhereAsksFirst(t *testing.T) {
	s := newUISite(t)
	p := s.as("bob@example.com").get("/account")
	wantIn(t, p, "account", `<summary class="btn btn-danger">Sign out everywhere…</summary>`,
		`<input type="hidden" name="action" value="signout-everywhere">`, `<a class="btn" href="/account">Cancel</a>`,
		`<button type="submit" class="btn">Sign out</button>`)
}

// memberRows reads the rows of a settings page's member lists: the text
// each is found by and whether the search hid it.
func memberRows(p page) (names []string, hidden map[string]bool) {
	hidden = map[string]bool{}
	for _, m := range regexp.MustCompile(`<tr data-find="([^"]*)"( hidden)?>`).FindAllStringSubmatch(p.body, -1) {
		name := strings.Fields(m[1])[0]
		names = append(names, name)
		hidden[name] = m[2] != ""
	}
	return names, hidden
}

func memberSearchSite(t *testing.T) *uiSite {
	t.Helper()
	s := newUISite(t)
	ctx, op := t.Context(), account.Operator()
	for _, m := range []struct {
		email, ns string
		role      account.Role
	}{
		{"olga@example.com", "acme", account.RoleOwner},
		{"adam@example.com", "acme", account.RoleOwner},
		{"pat@example.com", "acme", account.RoleMaintainer},
		{"zed@example.com", "acme", account.RoleMember},
		{"carl@example.com", "lime", account.RoleMaintainer},
		{"bob@example.com", "koala", account.RoleMember},
		{"olga@example.com", "koala", account.RoleMember},
	} {
		if _, _, err := s.accounts.AddMember(ctx, op, m.ns, m.email, m.role); err != nil {
			t.Fatal(err)
		}
	}
	// Bea has a name, which sorts before her email; her name sorts by
	// case-folded letters.
	u, err := s.store.UserByEmail(ctx, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.LinkGoogle(ctx, u.ID, "g-bob", "Bob "+xss); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMemberOrder(t *testing.T) {
	s := memberSearchSite(t)
	olga := s.as("olga@example.com")

	// Owners, maintainers, members; each by name or email, ignoring case.
	names, _ := memberRows(olga.get("/t/acme/settings"))
	if diff := cmp.Diff([]string{"adam@example.com", "olga@example.com", "pat@example.com", "zed@example.com"}, names); diff != "" {
		t.Errorf("project rows mismatch (-want +got):\n%s", diff)
	}
	// Maintainers, then members, then the project's owners in their own
	// list. A row's find text starts with the person's name or email.
	names, _ = memberRows(olga.get("/t/lime/settings"))
	want := []string{"alice@example.com", "carl@example.com", "bob", "dan@example.com", "adam@example.com", "olga@example.com"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("team rows mismatch (-want +got):\n%s", diff)
	}
}

func TestMemberSearch(t *testing.T) {
	s := memberSearchSite(t)
	olga := s.as("olga@example.com")

	p := olga.get("/t/lime/settings")
	wantIn(t, p, "teams line", "Teams: koala", `name="q"`, "Search people or teams")
	wantOut(t, p, "teams line", "Teams: lime", "Teams: acme", xss)
	if _, hidden := memberRows(p); slices.Contains(slices.Collect(maps.Values(hidden)), true) {
		t.Errorf("no search: hidden rows %v, want none", hidden)
	}
	wantIn(t, p, "no search", `filter-none" hidden>No member matches.`, `filter-none" hidden>No owner matches.`)

	// A team name finds the people in it, in both lists.
	p = olga.get("/t/lime/settings?q=%20Koala%20")
	wantIn(t, p, "search by team", `value="Koala"`, `name="q" value="Koala" data-keep-q`)
	_, hidden := memberRows(p)
	if hidden["bob"] || hidden["olga@example.com"] || !hidden["alice@example.com"] || !hidden["dan@example.com"] || !hidden["adam@example.com"] {
		t.Errorf("q=koala hidden = %v, want only bob and olga shown", hidden)
	}
	wantIn(t, p, "search by team", `filter-none" hidden>No member matches.`, `filter-none" hidden>No owner matches.`)

	// Names and emails are found too. Nothing matches: both lines show.
	_, hidden = memberRows(olga.get("/t/lime/settings?q=CARL@"))
	if hidden["carl@example.com"] || !hidden["bob"] {
		t.Errorf("q=CARL@ hidden = %v, want only carl shown", hidden)
	}
	p = olga.get("/t/lime/settings?q=nobody")
	wantIn(t, p, "no match", `<p class="hint filter-none">No member matches.</p>`, `<p class="hint filter-none">No owner matches.</p>`)

	// What is typed is escaped, in the box and in the rows.
	p = olga.get("/t/lime/settings?q=" + url.QueryEscape(xss))
	wantIn(t, p, "escaped query", `value="&lt;script&gt;alert(1)&lt;/script&gt;"`)
	wantOut(t, p, "escaped query", xss)
	wantOut(t, olga.get("/t/lime/settings"), "escaped name", xss)
	wantIn(t, olga.get("/t/lime/settings"), "escaped name", "bob &lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestMemberSearchKeptAndDropped(t *testing.T) {
	s := memberSearchSite(t)
	olga := s.as("olga@example.com")

	for _, tc := range []struct{ name, path string }{
		{"role", "/t/lime/members/role"},
		{"remove", "/t/lime/members/remove"},
	} {
		p := olga.post(tc.path, url.Values{"email": {"dan@example.com"}, "role": {"member"}, "q": {"  koala "}})
		if p.status != http.StatusSeeOther {
			t.Fatalf("%s = %d\n%s", tc.name, p.status, p.body)
		}
		u, err := url.Parse(p.location)
		if err != nil || u.Query().Get("q") != "koala" || u.Query().Get("email") != "dan@example.com" {
			t.Errorf("%s redirects to %q, want q=koala and the email kept", tc.name, p.location)
		}
		// The notice still shows with the search in the address.
		wantIn(t, olga.get(p.location), tc.name+" notice", "dan@example.com")
	}
	p := olga.post("/t/lime/members/role", url.Values{"email": {"bob@example.com"}, "role": {"member"}})
	if u, _ := url.Parse(p.location); u.Query().Has("q") {
		t.Errorf("role change without a search redirects to %q, want no q", p.location)
	}

	// The remove popover's Cancel link keeps the search too.
	wantIn(t, olga.get("/t/lime/settings?q=Koala"), "cancel", `href="/t/lime/settings?q=Koala" data-keep-q>Cancel`)

	p = olga.post("/t/lime/members", url.Values{"email": {"new@example.com"}, "role": {"member"}, "q": {"koala"}})
	if p.status != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", p.status, p.body)
	}
	if u, _ := url.Parse(p.location); u.Query().Has("q") {
		t.Errorf("add redirects to %q, want the search cleared", p.location)
	}
}
