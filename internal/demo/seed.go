package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	s.deletions()
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
	}{
		{Acme, "", registry.KindProject},
		{Lime, Acme, registry.KindTeam},
		{Koala, Acme, registry.KindTeam},
		{Globex, "", registry.KindProject},
		{Pear, Globex, registry.KindTeam},
		{Otter, Globex, registry.KindTeam},
	} {
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
	if _, err := s.Accounts.SetAdmin(ctx, op, ada, true); err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	return nil
}

// people puts each person in their teams, on the day they joined.
func (s *seeder) people() {
	for _, m := range memberships {
		s.at(m.ago, func(ctx context.Context) error {
			_, _, err := s.Accounts.AddMember(ctx, account.Operator(), m.ns, m.email, account.Role(m.role))
			return wrap("add "+m.email+" to "+m.ns, err)
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

func (s *seeder) packages() {
	for _, r := range releases {
		s.at(r.ago, func(ctx context.Context) error {
			data, err := bundleOf(r.f)
			if err != nil {
				return err
			}
			_, err = s.Registry.Publish(ctx, s.as(r.who), data)
			return wrap("publish "+string(r.f["loadout.yaml"]), err)
		})
	}
}

// proposals tells the stories of review: released, rejected, behind a newer
// release and open, with a report to read on one of them.
func (s *seeder) proposals() {
	for _, p := range proposals {
		id := new(string)
		s.at(p.ago, func(ctx context.Context) error {
			data, err := bundleOf(p.f)
			if err != nil {
				return err
			}
			prop, err := s.Proposals.Propose(ctx, s.as(p.who), data, registry.ProposalInput{Note: p.note, Prompts: p.prompts})
			if err != nil {
				return wrap("propose "+string(p.f["loadout.yaml"]), err)
			}
			*id = prop.ID
			return nil
		})
		switch p.decide {
		case "approve":
			s.at(p.ago-p.after, func(ctx context.Context) error {
				_, _, err := s.Proposals.Approve(ctx, s.as(p.by), *id, registry.ApproveInput{Note: p.why})
				return wrap("approve", err)
			})
		case "reject":
			s.at(p.ago-p.after, func(ctx context.Context) error {
				_, err := s.Proposals.Reject(ctx, s.as(p.by), *id, p.why)
				return wrap("reject", err)
			})
		}
		if p.reportBy != "" {
			s.at(p.ago-h(2), func(ctx context.Context) error {
				text, data, err := planReport(*id, s.now.Add(-(p.ago - h(2))), p.prompts)
				if err != nil {
					return err
				}
				_, err = s.Proposals.AddReview(ctx, s.as(p.reportBy), *id, text, data)
				return wrap("add review", err)
			})
		}
	}
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
	type page struct {
		ago     time.Duration
		ns, who string
		text    string
		note    string
		base    int
	}
	pages := []page{
		{d(15), Lime, alice, embeddedText("wiki/lime-1.md"), "First page", 0},
		{d(4), Lime, alice, embeddedText("wiki/lime-2.md"), "Deploys and on call", 1},
		{d(11), Koala, alice, wikiText("Koala", "mobile apps", "Ask Olga about store accounts and Alice about releases."), "First page", 0},
		{d(12), Pear, dana, wikiText("Pear", "web app", "Ask Dana about releases and Alice about reviews."), "First page", 0},
		{d(9), Otter, emil, wikiText("Otter", "releases", "Ask Chen who approves this month."), "First page", 0},
	}
	for _, ns := range []struct {
		ago time.Duration
		ns  string
	}{{d(17), Lime}, {d(12), Koala}, {d(13), Pear}, {d(10), Otter}} {
		s.at(ns.ago, func(ctx context.Context) error {
			_, err := s.Accounts.SetWiki(ctx, account.Operator(), ns.ns, true)
			return wrap("turn the wiki on", err)
		})
	}
	for _, p := range pages {
		s.at(p.ago, func(ctx context.Context) error {
			doc, err := wiki.NewDoc(wiki.Markdown, p.text)
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			_, _, err = s.Accounts.SaveWiki(ctx, account.UserActor(s.ids[p.who]), p.ns, p.base, doc, p.note)
			return wrap("save wiki", err)
		})
	}
	// A course is a knowledge base the team page shows under the wiki.
	for _, c := range []struct {
		ago    time.Duration
		ns, id string
	}{{d(13), Lime, "lime/dev-course"}, {d(9), Pear, "pear/onboarding-course"}} {
		s.at(c.ago, func(ctx context.Context) error {
			id, err := pack.ParseID(c.id)
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			_, err = s.Accounts.SetCourse(ctx, account.Operator(), c.ns, id)
			return wrap("set the course", err)
		})
	}
}

func wikiText(team, what, ask string) string {
	return "# " + team + "\n\nEverything the " + strings.ToLower(team) + " team should know about the " + what + ".\n\n## Setup\n\nInstall the CLI, sign in with `loadout login` and run `loadout sync`.\n\n## Who to ask\n\n" + ask + "\n"
}

func (s *seeder) deletions() {
	for _, q := range []struct {
		ago     time.Duration
		who, id string
		why     string
	}{
		{d(2), bogdan, "lime/scratch-notes", "A scratch agent from the first week. Nobody has installed it since, and lime/planner covers it."},
		{d(3), alice, "pear/commit-style", "Replaced by the changelog skill in globex. Nobody in pear uses it any more."},
	} {
		s.at(q.ago, func(ctx context.Context) error {
			id, err := pack.ParseID(q.id)
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			_, err = s.Deletions.Propose(ctx, s.as(q.who), id, q.why)
			return wrap("ask to delete "+q.id, err)
		})
	}
}

// memories writes every team's shared memory: entries over the weeks, a few
// edited by someone else, one retracted and one moved up to the project.
func (s *seeder) memories() {
	for _, sw := range switches {
		s.at(sw.ago, func(ctx context.Context) error {
			return wrap("turn memory on", s.Memory.SetEnabled(ctx, s.caller(sw.who), sw.ns, true))
		})
	}
	ids := map[string]int64{}
	for _, n := range notes {
		s.at(n.ago, func(ctx context.Context) error {
			var links []int64
			for _, k := range n.links {
				links = append(links, ids[k])
			}
			dr, err := memory.NewDraft(n.ns, n.typ, n.name, n.desc, n.body, links)
			if err != nil {
				return fmt.Errorf("demo: memory %q: %w", n.name, err)
			}
			saved, err := s.Memory.Save(ctx, s.caller(n.who), n.ns, dr, true)
			if err != nil {
				return wrap("save memory "+n.name, err)
			}
			ids[n.key] = saved.Entry.ID
			return nil
		})
	}
	for _, e := range edits {
		s.at(e.ago, func(ctx context.Context) error {
			_, err := s.Memory.Update(ctx, s.caller(e.who), ids[e.key], 0, memory.Change{Body: &e.body, Note: e.why})
			return wrap("edit memory "+e.key, err)
		})
	}
	for _, r := range retractions {
		s.at(r.ago, func(ctx context.Context) error {
			_, err := s.Memory.Retract(ctx, s.caller(r.who), ids[r.key], r.why)
			return wrap("retract memory "+r.key, err)
		})
	}
	for _, m := range moves {
		s.at(m.ago, func(ctx context.Context) error {
			_, err := s.Memory.Move(ctx, s.caller(m.who), ids[m.key])
			return wrap("move memory "+m.key, err)
		})
	}
}

func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("demo: %s: %w", what, err)
}
