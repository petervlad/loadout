package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

// World is the set of services the seed writes through. They must all read
// Clock, so what the seed does is dated by it.
type World struct {
	Clock     *Clock
	Registry  *registry.Service
	Proposals *registry.ProposalService
	Deletions *registry.DeletionService
	Accounts  *account.Service
	Memory    *memory.Service
}

// How long the story is, counted back from now.
const history = 35 * 24 * time.Hour

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// event is one thing that happened, ago before now.
type event struct {
	ago time.Duration
	run func(ctx context.Context) error
}

type seeder struct {
	World
	now    time.Time
	ids    map[string]string // email to user id
	events []event
}

// Seed writes the demo world. now is the end of the story: everything is
// dated before it, the earliest five weeks back. It needs empty services
// and is not safe to run twice, see cmd/loadout's demo command for that.
func Seed(ctx context.Context, w World, now time.Time) error {
	s := &seeder{World: w, now: now, ids: map[string]string{}}
	w.Clock.Set(now.Add(-history))
	if err := s.setup(ctx); err != nil {
		return err
	}
	s.people()
	s.packages()
	s.proposals()
	s.wikiAndCourse()
	s.deletion()
	s.memories()
	// Oldest first, so sequence numbers and dates agree.
	sort.SliceStable(s.events, func(i, j int) bool { return s.events[i].ago > s.events[j].ago })
	for _, e := range s.events {
		w.Clock.Set(now.Add(-e.ago))
		if err := e.run(ctx); err != nil {
			return err
		}
	}
	w.Clock.Set(now)
	return nil
}

func (s *seeder) at(ago time.Duration, run func(ctx context.Context) error) {
	s.events = append(s.events, event{ago: ago, run: run})
}

// setup makes the namespaces and the accounts, which everything else needs.
func (s *seeder) setup(ctx context.Context) error {
	op := account.Operator()
	for _, ns := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{{Project, "", registry.KindProject}, {Lime, Project, registry.KindTeam}, {Koala, Project, registry.KindTeam}} {
		if _, err := s.Accounts.CreateNamespace(ctx, op, ns.name, ns.parent, ns.kind); err != nil {
			return fmt.Errorf("demo: %w", err)
		}
	}
	for _, a := range people {
		u, err := s.Accounts.SetPasswordAsOperator(ctx, op, a.Email, Password)
		if err != nil {
			return fmt.Errorf("demo: %w", err)
		}
		s.ids[a.Email] = u.ID
	}
	if _, err := s.Accounts.SetAdmin(ctx, op, "ada@example.com", true); err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	return nil
}

// people puts each person in their teams, on the day they joined.
func (s *seeder) people() {
	for _, m := range []struct {
		ago       time.Duration
		ns, email string
		role      account.Role
	}{
		{days(35), Project, "olga@example.com", account.RoleOwner},
		{days(34), Lime, "alice@example.com", account.RoleMaintainer},
		{days(33), Koala, "alice@example.com", account.RoleMember},
		{days(26), Lime, "bogdan@example.com", account.RoleMember},
	} {
		s.at(m.ago, func(ctx context.Context) error {
			if _, _, err := s.Accounts.AddMember(ctx, account.Operator(), m.ns, m.email, m.role); err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			return nil
		})
	}
}

// as is a person signed in on the web: an account principal with its own
// token id, so four eyes tell two people apart.
func (s *seeder) as(email string) registry.Principal {
	id := s.ids[email]
	return registry.Principal{TokenID: "web:" + id, UserID: id, Label: email + " on the web", Account: true}
}

func (s *seeder) caller(email string) memory.Caller {
	return memory.Caller{UserID: s.ids[email], Label: email}
}

// release publishes one package folder as who.
func (s *seeder) release(ago time.Duration, ref, who string) {
	s.at(ago, func(ctx context.Context) error {
		data, err := bundleOf(ref)
		if err != nil {
			return err
		}
		if _, err := s.Registry.Publish(ctx, s.as(who), data); err != nil {
			return fmt.Errorf("demo: publish %s: %w", ref, err)
		}
		return nil
	})
}

func (s *seeder) packages() {
	for _, r := range []struct {
		ago time.Duration
		ref string
		who string
	}{
		{days(33), "acme/python-standards@4.1.0", "olga@example.com"},
		{days(33), "acme/docs-writer@1.0.3", "olga@example.com"},
		{days(30), "lime/planner@2.2.0", "alice@example.com"},
		{days(28), "lime/code-review@1.8.0", "alice@example.com"},
		{days(27), "lime/scratch-notes@0.2.0", "alice@example.com"},
		{days(26), "acme/dev-loop@1.0.0", "olga@example.com"},
		{days(25), "lime/builder@1.0.0", "alice@example.com"},
		{days(22), "lime/dev-loop@1.0.0", "alice@example.com"},
		{days(20), "lime/pr-description@1.0.0", "alice@example.com"},
		{days(18), "lime/handbook@1.0.0", "alice@example.com"},
		{days(16), "koala/release-checklist@0.3.0", "olga@example.com"},
		{days(14), "lime/dev-course@1.0.0", "alice@example.com"},
		{days(6), "lime/handbook@1.1.0", "alice@example.com"},
		{days(4), "lime/planner@2.3.0", "alice@example.com"},
		{days(3), "lime/pr-description@1.0.1", "alice@example.com"},
		{days(3), "lime/dev-course@1.1.0", "alice@example.com"},
		{days(1), "lime/code-review@1.8.2", "alice@example.com"},
	} {
		s.release(r.ago, r.ref, r.who)
	}
}

// proposals tells four stories: one released, one rejected, one that fell
// behind a newer release and one open with a report to read.
func (s *seeder) proposals() {
	// Bogdan, a member, is the one who proposes: members propose, maintainers decide.
	const bogdan = "bogdan@example.com"
	propose := func(ago time.Duration, ref, note string, prompts []string) *string {
		id := new(string)
		s.at(ago, func(ctx context.Context) error {
			data, err := bundleOf(ref)
			if err != nil {
				return err
			}
			p, err := s.Proposals.Propose(ctx, s.as(bogdan), data, registry.ProposalInput{Note: note, Prompts: prompts})
			if err != nil {
				return fmt.Errorf("demo: propose %s: %w", ref, err)
			}
			*id = p.ID
			return nil
		})
		return id
	}

	released := propose(days(24), "lime/release-notes@0.9.0", "A first notes agent for the team.", []string{"Draft notes for the 2.4.0 release"})
	s.at(days(23), func(ctx context.Context) error {
		_, _, err := s.Proposals.Approve(ctx, s.as("alice@example.com"), *released, registry.ApproveInput{Note: "Good start. We can tune it as we use it."})
		return wrap("approve release-notes", err)
	})

	rejected := propose(days(12), "lime/builder@1.1.0", "Let the builder tidy the files it touches.", []string{"Build the retry from TRK-1234 and tidy what you touch"})
	s.at(days(11), func(ctx context.Context) error {
		_, err := s.Proposals.Reject(ctx, s.as("alice@example.com"), *rejected, "Rewriting files we did not ask about makes reviews harder. Keep the diff to the plan.")
		return wrap("reject builder", err)
	})

	// Proposed from 1.0.0, then 1.0.1 was released, so it is behind.
	propose(days(5), "lime/pr-description@1.1.0", "Add a test plan section.", []string{"Write the description for a change that adds a retry"})

	prompts := []string{
		"Plan TRK-1234: add a retry to the upload job",
		"Plan a database migration for the releases table",
	}
	open := propose(time.Hour*20, "lime/planner@2.4.0", "Plans now name the exact files and end with a test step.", prompts)
	s.at(time.Hour*18, func(ctx context.Context) error {
		text, data, err := planReport(*open, s.now.Add(-time.Hour*18), prompts)
		if err != nil {
			return err
		}
		_, err = s.Proposals.AddReview(ctx, s.as("alice@example.com"), *open, text, data)
		return wrap("add review", err)
	})
}

// planReport is a compact comparison report in the shape `loadout review
// compare` attaches, with two prompts answered by the current version and
// the candidate.
func planReport(proposal string, at time.Time, prompts []string) ([]byte, []byte, error) {
	run := func(current, candidate string) map[string]any {
		answer := func(text string, secs int) map[string]any {
			return map[string]any{"answer": map[string]any{"text": text, "duration_ns": secs * 1_000_000_000, "input_tokens": 3600, "output_tokens": 600}}
		}
		return map[string]any{"current": answer(current, 12), "candidate": answer(candidate, 10)}
	}
	report := map[string]any{
		"tool": "claude", "tool_version": "2.1.0", "model": "sonnet", "proposal_id": proposal, "package": "lime/planner",
		"current":      map[string]any{"version": "2.3.0", "digest": "sha256:aa", "source": "installed"},
		"candidate":    map[string]any{"version": "2.4.0", "digest": "sha256:bb", "source": "proposal", "proposal_id": proposal},
		"runs":         1,
		"mode":         "full",
		"context":      []map[string]any{{"id": "lime/code-review", "version": "1.8.2", "kind": "agent"}},
		"generated_at": at.UTC().Format(time.RFC3339),
		"prompts": []map[string]any{
			{"prompt": prompts[0], "runs": []map[string]any{run(
				"Here is a plan for adding a retry to the upload job.\n\n1. Find where the upload happens and wrap it in a retry.\n2. Use exponential backoff with a sensible limit.\n3. Log each failed attempt.\n\nRisks: retries could hide a real outage.",
				"Plan for TRK-1234\n\n1. upload/job.py: wrap push_bundle() in the retry helper, 3 tries, 2 s then 8 s.\n2. upload/config.py: add UPLOAD_RETRIES, default 3.\n3. Retry only on timeouts and 5xx.\n\nRisk: a stuck upstream now takes up to 10 s longer to fail.\nTest: a fake server that returns 503 twice, then 200.")}},
			{"prompt": prompts[1], "runs": []map[string]any{run(
				"Add the column, backfill, then add the index.",
				"1. migrations/0006_releases.sql: add the column as nullable.\n2. Backfill in batches of 1000.\n3. Add the index concurrently.\n\nTest: run the migration against a copy of production.")}},
		},
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, nil, fmt.Errorf("demo: report: %w", err)
	}
	return []byte("# Review of lime/planner 2.4.0\n\nThe candidate names its files and ends with a test. See the side by side view.\n"), data, nil
}

func (s *seeder) wikiAndCourse() {
	page := func(ago time.Duration, file, note string, base int) {
		s.at(ago, func(ctx context.Context) error {
			body, err := text(file)
			if err != nil {
				return err
			}
			doc, err := wiki.NewDoc(wiki.Markdown, body)
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			_, _, err = s.Accounts.SaveWiki(ctx, account.UserActor(s.ids["alice@example.com"]), Lime, base, doc, note)
			return wrap("save wiki", err)
		})
	}
	s.at(days(17), func(ctx context.Context) error {
		_, err := s.Accounts.SetWiki(ctx, account.Operator(), Lime, true)
		return wrap("turn the wiki on", err)
	})
	page(days(15), "wiki/lime-1.md", "First page", 0)
	page(days(4), "wiki/lime-2.md", "Deploys and on call", 1)

	// The course is a knowledge base the team page shows under the wiki.
	s.at(days(13), func(ctx context.Context) error {
		id, err := pack.ParseID("lime/dev-course")
		if err != nil {
			return fmt.Errorf("demo: %w", err)
		}
		_, err = s.Accounts.SetCourse(ctx, account.Operator(), Lime, id)
		return wrap("set the course", err)
	})
}

func (s *seeder) deletion() {
	s.at(days(2), func(ctx context.Context) error {
		id, err := pack.ParseID("lime/scratch-notes")
		if err != nil {
			return fmt.Errorf("demo: %w", err)
		}
		_, err = s.Deletions.Propose(ctx, s.as("bogdan@example.com"), id, "A scratch agent from the first week. Nobody has installed it since, and lime/planner covers it.")
		return wrap("ask to delete scratch-notes", err)
	})
}

// memories writes the teams' shared memory: older entries that the team has
// leaned on, and a few from the last days that the page marks as new.
func (s *seeder) memories() {
	type entry struct {
		ago             time.Duration
		ns, who         string
		typ             memory.Type
		name, desc, doc string
	}
	entries := []entry{
		{
			days(24), Lime, "alice@example.com", memory.TypeFeedback, "Name the files in every plan",
			"Plans that list their files get reviewed faster.",
			"The planner names each file it would touch and ends with one test.\n\n**Why:** reviews stalled twice on plans that only described the idea.\n\n**How to apply:** ask for the file list before you read the rest.",
		},
		{
			days(20), Lime, "alice@example.com", memory.TypeProject, "Release train leaves every second Thursday",
			"What goes out on a release day and who decides.",
			"A release leaves the gate job every second Thursday at 10:00. Alice decides what goes in on Wednesday.\n\n**Why:** support wants a fixed day to plan around.",
		},
		{
			days(14), Lime, "bogdan@example.com", memory.TypeReference, "Where to look when staging is slow",
			"The first panel to open on the staging dashboard.",
			"The staging dashboard is linked from the wiki under Deploys. Open the queue depth panel first. A queue over 200 means the worker pool is stuck, not the database.",
		},
		{
			days(9), Lime, "bogdan@example.com", memory.TypeFeedback, "Keep commits small",
			"One idea per commit, named in plain words.",
			"Reviewers read commit by commit. A commit that mixes a rename with a fix is read twice.\n\n**How to apply:** commit the rename on its own.",
		},
		{
			days(3), Lime, "alice@example.com", memory.TypeProject, "The web app moves to the new router in November",
			"New routes use the new router from now on.",
			"From now on new web routes go on the new router. Old routes move when someone touches them, and the old router goes away after November.",
		},
		{
			days(2), Lime, "bogdan@example.com", memory.TypeFeedback, "Do not run the whole e2e suite on every push",
			"Run it on pull requests and before a release.",
			"The suite takes 25 minutes. Pushes run the fast tests and the pull request runs everything.\n\n**Why:** the queue was full by noon.",
		},
		{
			days(1), Lime, "alice@example.com", memory.TypeReference, "The handbook has the deploy steps",
			"Read the handbook package before you deploy or roll back.",
			"The lime/handbook knowledge base has the deploy steps, the rollback and a page for the slow database. Install it with the rest of the team's packages.",
		},
		{
			days(20), Project, "olga@example.com", memory.TypeReference, "Python house rules",
			"The rules every acme team follows for Python.",
			"The rules live in the acme/python-standards agent. Type hints on public functions, tests next to the code, no clever code.",
		},
		{
			days(2), Project, "olga@example.com", memory.TypeProject, "Code freeze starts on 20 December",
			"Every team stops releasing on the same day.",
			"No team releases between 20 December and 5 January. Plan the last release for the week before.",
		},
	}
	for _, ns := range []struct {
		ago time.Duration
		ns  string
		who string
	}{{days(25), Lime, "alice@example.com"}, {days(24), Project, "olga@example.com"}} {
		s.at(ns.ago, func(ctx context.Context) error {
			return wrap("turn memory on", s.Memory.SetEnabled(ctx, s.caller(ns.who), ns.ns, true))
		})
	}
	// Dated order inside a team, so ids follow the story.
	slices.SortStableFunc(entries, func(a, b entry) int { return int(b.ago - a.ago) })
	for _, e := range entries {
		s.at(e.ago, func(ctx context.Context) error {
			d, err := memory.NewDraft(e.ns, e.typ, e.name, e.desc, e.doc, nil)
			if err != nil {
				return fmt.Errorf("demo: memory %q: %w", e.name, err)
			}
			_, err = s.Memory.Save(ctx, s.caller(e.who), e.ns, d, true)
			return wrap("save memory "+e.name, err)
		})
	}
}

func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("demo: %s: %w", what, err)
}
