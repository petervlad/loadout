package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const e2eTemplate = `start: plan
slots:
  plan: {brief: Plan it., writes: plan.md, reads: [ticket], verdicts: [READY]}
  dev: {brief: Build it., writes: dev.md, reads: [plan.md], verdicts: [DONE]}
edges:
  - {from: plan, on: READY, to: dev, via: gate}
  - {from: dev, on: DONE, to: end, via: gate}
`

// writePackage writes a pipeline package folder, dev-loop, in the person's working
// directory.
func (p *person) writePackage(id, version, body string) {
	p.t.Helper()
	dir := filepath.Join(p.wd, "dev-loop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.t.Fatal(err)
	}
	manifest := "name: " + id + "\nkind: pipeline\nversion: " + version + "\ndescription: The dev loop.\n"
	for name, text := range map[string]string{"loadout.yaml": manifest, "pipeline.yaml": body} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			p.t.Fatal(err)
		}
	}
}

// TestPipelineTemplateAndBinding: a project maintainer publishes a template,
// a team's binding is refused until every slot has an agent, and a teammate's
// install --all writes the conductor for both tools and skips the template.
func TestPipelineTemplateAndBinding(t *testing.T) {
	host := &person{session: newSession(t)}
	maintainer := &person{session: newSession(t)}
	author := &person{session: newSession(t)}
	reader := &person{session: newSession(t)}
	if err := os.Mkdir(filepath.Join(reader.home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}

	url := startServer(t, host)
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	acmePub := createTokenFor(t, host, "acme", "publish")
	limePub := createTokenFor(t, host, "lime", "publish")
	limeRead := createTokenFor(t, host, "lime", "read")

	maintainer.must(acmePub+"\n", "remote", "add", "office", url, "--token-stdin")
	maintainer.writePackage("acme/dev-loop", "1.0.0", e2eTemplate)
	maintainer.must("", "publish", "dev-loop")

	author.must(limePub+"\n", "remote", "add", "office", url, "--token-stdin")
	unfilled := "uses: acme/dev-loop@1\nbind:\n  plan: lime-planner\nwork_folder: explorations/\n"
	author.writePackage("lime/dev-loop", "1.0.0", unfilled)
	_, errOut, code := author.runIn("", "publish", "dev-loop")
	if code == 0 || !strings.Contains(errOut, "slot dev has no agent") || !strings.Contains(errOut, "The agent lime-planner is not published") {
		t.Fatalf("publishing an unfilled binding: exit %d, stderr %q; want both problems named", code, errOut)
	}

	for _, name := range []string{"lime-planner", "lime-dev"} {
		author.must("", "new", "agent", "lime/"+name)
		author.must("", "publish", name)
	}
	filled := "uses: acme/dev-loop@1\nbind:\n  plan: lime-planner\n  dev: lime-dev\nwork_folder: explorations/\n"
	author.writePackage("lime/dev-loop", "1.0.0", filled)
	author.must("", "publish", "dev-loop")

	// Installing the binding before its agents works and warns.
	reader.must(limeRead+"\n", "remote", "add", "office", url, "--token-stdin")
	out := reader.must("", "install", "lime/dev-loop")
	for _, want := range []string{"installed lime/dev-loop@1.0.0", "warning: lime/dev-loop needs the agent lime-planner, which is not installed here", "needs the agent lime-dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("install lime/dev-loop is missing %q:\n%s", want, out)
		}
	}
	if _, errOut, code := reader.runIn("", "install", "acme/dev-loop"); code == 0 || !strings.Contains(errOut, "installs through a team's binding") {
		t.Errorf("installing the template: exit %d, stderr %q; want it refused with the reason", code, errOut)
	}

	out = reader.must("", "install", "--all")
	if !strings.Contains(out, "skipped acme/dev-loop: a pipeline template installs through a team's binding") {
		t.Errorf("install --all did not skip the template:\n%s", out)
	}
	if strings.Contains(out, "needs the agent") {
		t.Errorf("install --all warned about a missing agent although every agent is installed:\n%s", out)
	}
	// The scaffolded planner has no shell, which Copilot's conductor needs
	// to make the run folder.
	if !strings.Contains(out, "lime-planner starts the run and has no shell tool, so on copilot") {
		t.Errorf("install --all did not print the render warning:\n%s", out)
	}
	for _, path := range []string{
		filepath.Join(reader.home, ".claude", "agents", "lime-dev-loop.md"),
		filepath.Join(reader.home, ".copilot", "agents", "lime-dev-loop.md"),
	} {
		assertManagedBy(t, path, "lime/dev-loop@1.0.0")
		if data, _ := os.ReadFile(path); !strings.Contains(string(data), "lime-planner") || !strings.Contains(string(data), "Verdict:") {
			t.Errorf("%s is missing the bound agent or the verdict rule:\n%s", path, data)
		}
	}
	if status := reader.must("", "status"); !strings.Contains(status, "lime/dev-loop uses acme/dev-loop@1.0.0") {
		t.Errorf("status does not say what the conductor uses:\n%s", status)
	}

	// Inside a major, a template may not rename a slot.
	renamed := strings.NewReplacer("dev", "build").Replace(e2eTemplate)
	maintainer.writePackage("acme/dev-loop", "1.1.0", renamed)
	if _, errOut, code := maintainer.runIn("", "publish", "dev-loop"); code == 0 || !strings.Contains(errOut, "publish 2.0.0") {
		t.Errorf("publishing a renamed slot as 1.1.0: exit %d, stderr %q; want a request for 2.0.0", code, errOut)
	}
}

// TestPipelineTemplateUpdatesReachTheTeamOnSync: a template minor renders the
// team's conductor again under policy auto and waits under notify, and a new
// major prints one line without changing anything.
func TestPipelineTemplateUpdatesReachTheTeamOnSync(t *testing.T) {
	host := &person{session: newSession(t)}
	maintainer := &person{session: newSession(t)}
	author := &person{session: newSession(t)}
	auto := &person{session: newSession(t)}
	notify := &person{session: newSession(t)}

	url := startServer(t, host)
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	acmePub := createTokenFor(t, host, "acme", "publish")
	limePub := createTokenFor(t, host, "lime", "publish")
	limeRead := createTokenFor(t, host, "lime", "read")

	maintainer.must(acmePub+"\n", "remote", "add", "office", url, "--token-stdin")
	maintainer.writePackage("acme/dev-loop", "1.0.0", e2eTemplate)
	maintainer.must("", "publish", "dev-loop")

	author.must(limePub+"\n", "remote", "add", "office", url, "--token-stdin")
	for _, name := range []string{"lime-planner", "lime-dev"} {
		author.must("", "new", "agent", "lime/"+name)
		author.must("", "publish", name)
	}
	author.writePackage("lime/dev-loop", "1.0.0", "uses: acme/dev-loop@1\nbind:\n  plan: lime-planner\n  dev: lime-dev\nwork_folder: explorations/\n")
	author.must("", "publish", "dev-loop")

	conductor := func(p *person) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(p.home, ".claude", "agents", "lime-dev-loop.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, p := range []*person{auto, notify} {
		p.must(limeRead+"\n", "remote", "add", "office", url, "--token-stdin")
		p.must("", "install", "--all")
	}
	auto.must("", "policy", "lime/dev-loop", "auto")
	notify.must("", "policy", "lime/dev-loop", "notify")
	if strings.Contains(conductor(auto), "Plan it carefully.") {
		t.Fatal("the first conductor already holds the text of the next template")
	}

	// 1.1.0 changes only a brief: a compatible minor.
	maintainer.writePackage("acme/dev-loop", "1.1.0", strings.Replace(e2eTemplate, "Plan it.", "Plan it carefully.", 1))
	maintainer.must("", "publish", "dev-loop")

	out := auto.must("", "sync")
	if !strings.Contains(out, "pipeline template updated") || !strings.Contains(conductor(auto), "Plan it carefully.") {
		t.Errorf("auto: sync did not render the conductor again from template 1.1.0:\n%s", out)
	}
	if status := auto.must("", "status"); !strings.Contains(status, "lime/dev-loop uses acme/dev-loop@1.1.0") {
		t.Errorf("auto: status does not name template 1.1.0:\n%s", status)
	}
	out = notify.must("", "sync")
	if !strings.Contains(out, "pipeline template changed") || strings.Contains(conductor(notify), "Plan it carefully.") {
		t.Errorf("notify: sync should report the template and leave the conductor:\n%s", out)
	}
	if out = auto.must("", "sync"); strings.Contains(out, "pipeline template") {
		t.Errorf("auto: a second sync has nothing left to do:\n%s", out)
	}

	// 2.0.0 renames a slot: a new major.
	maintainer.writePackage("acme/dev-loop", "2.0.0", strings.NewReplacer("dev", "build").Replace(e2eTemplate))
	maintainer.must("", "publish", "dev-loop")
	want := "acme/dev-loop 2.0.0 is out. lime/dev-loop uses major 1. Move it with `uses: acme/dev-loop@2` and publish."
	before := conductor(auto)
	out = auto.must("", "sync")
	if !strings.Contains(out, want) || conductor(auto) != before {
		t.Errorf("auto: sync should print the new-major line and change nothing:\n%s", out)
	}
}

// TestSyncRendersAConductorWhoseRequirementsWereLost: with the side file of
// requirements gone, the next sync renders the conductor again and records
// the template, so the warning the lock logs is true.
func TestSyncRendersAConductorWhoseRequirementsWereLost(t *testing.T) {
	host := &person{session: newSession(t)}
	maintainer := &person{session: newSession(t)}
	author := &person{session: newSession(t)}
	reader := &person{session: newSession(t)}

	url := startServer(t, host)
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	acmePub := createTokenFor(t, host, "acme", "publish")
	limePub := createTokenFor(t, host, "lime", "publish")
	limeRead := createTokenFor(t, host, "lime", "read")
	maintainer.must(acmePub+"\n", "remote", "add", "office", url, "--token-stdin")
	maintainer.writePackage("acme/dev-loop", "1.0.0", e2eTemplate)
	maintainer.must("", "publish", "dev-loop")
	author.must(limePub+"\n", "remote", "add", "office", url, "--token-stdin")
	for _, name := range []string{"lime-planner", "lime-dev"} {
		author.must("", "new", "agent", "lime/"+name)
		author.must("", "publish", name)
	}
	author.writePackage("lime/dev-loop", "1.0.0", "uses: acme/dev-loop@1\nbind:\n  plan: lime-planner\n  dev: lime-dev\nwork_folder: explorations/\n")
	author.must("", "publish", "dev-loop")

	reader.must(limeRead+"\n", "remote", "add", "office", url, "--token-stdin")
	reader.must("", "install", "--all")
	reader.must("", "policy", "lime/dev-loop", "auto")

	var sides []string
	err := filepath.WalkDir(reader.home, func(path string, _ os.DirEntry, _ error) error {
		if strings.HasSuffix(path, ".requires.yaml") {
			sides = append(sides, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sides) != 1 {
		t.Fatalf("found requires files %v, want one", sides)
	}
	if err := os.Remove(sides[0]); err != nil {
		t.Fatal(err)
	}
	if status := reader.must("", "status"); strings.Contains(status, "lime/dev-loop uses") {
		t.Fatalf("status still names the template after its record was removed:\n%s", status)
	}
	// The conductor's files come out the same, so sync only records the
	// template it renders from.
	reader.must("", "sync")
	if status := reader.must("", "status"); !strings.Contains(status, "lime/dev-loop uses acme/dev-loop@1.0.0") {
		t.Errorf("status does not name the template again:\n%s", status)
	}
}

// TestNewPipelineScaffolds: a maintainer scaffolds a template and publishes
// it as it is, a team scaffolds a binding with --uses, publish refuses it
// until every slot has an agent, and then accepts it.
func TestNewPipelineScaffolds(t *testing.T) {
	host := &person{session: newSession(t)}
	maintainer := &person{session: newSession(t)}
	author := &person{session: newSession(t)}

	url := startServer(t, host)
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	acmePub := createTokenFor(t, host, "acme", "publish")
	limePub := createTokenFor(t, host, "lime", "publish")

	maintainer.must(acmePub+"\n", "remote", "add", "office", url, "--token-stdin")
	out := maintainer.must("", "new", "pipeline", "acme/dev-loop")
	t.Logf("new pipeline acme/dev-loop:\n%s", out)
	if !strings.Contains(out, "loadout publish") {
		t.Errorf("new pipeline prints no publish hint:\n%s", out)
	}
	// A fresh template starts at 1.0.0, so --uses ...@1 works with no flags.
	maintainer.must("", "publish", "dev-loop")

	author.must(limePub+"\n", "remote", "add", "office", url, "--token-stdin")
	out = author.must("", "new", "pipeline", "lime/dev-loop", "--uses", "acme/dev-loop@1")
	t.Logf("new pipeline lime/dev-loop --uses acme/dev-loop@1:\n%s", out)
	if !strings.Contains(out, "fill bind:") {
		t.Errorf("new pipeline --uses prints no fill hint:\n%s", out)
	}

	_, errOut, code := author.runIn("", "publish", "dev-loop")
	if code == 0 || !strings.Contains(errOut, "slot draft, check has no agent") {
		t.Fatalf("publishing the scaffolded binding: exit %d, stderr %q; want both empty slots named", code, errOut)
	}

	for _, name := range []string{"lime-writer", "lime-judge"} {
		author.must("", "new", "agent", "lime/"+name)
		author.must("", "publish", name)
	}
	file := filepath.Join(author.wd, "dev-loop", "pipeline.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	filled := strings.Replace(string(data), "  draft:\n", "  draft: lime-writer\n", 1)
	filled = strings.Replace(filled, "  check:\n", "  check: lime-judge\n", 1)
	author.writePackage("lime/dev-loop", "1.0.0", filled)
	author.must("", "publish", "dev-loop")
}
