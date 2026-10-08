package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTokenFor(t *testing.T, admin *person, namespace, scope string) string {
	t.Helper()
	ensureTeam(admin, namespace)
	out := admin.must("", "token", "create", "--team", namespace, "--"+scope)
	tok := tokenRE.FindString(out)
	if tok == "" {
		t.Fatalf("no token in output: %s", out)
	}
	return tok
}

func TestProjectLayerUnderATeam(t *testing.T) {
	host := &person{session: newSession(t)}
	maintainer := &person{session: newSession(t)} // publishes for the project
	author := &person{session: newSession(t)}     // publishes for lime
	reader := &person{session: newSession(t)}     // a lime teammate

	url := startServer(t, host)
	host.must("", "namespace", "create", "acme", "--kind", "project")
	if out := host.must("", "namespace", "create", "lime", "--parent", "acme"); !strings.Contains(out, "under acme") {
		t.Errorf("namespace create output = %q", out)
	}
	if _, errOut, code := host.runIn("", "namespace", "create", "orphan", "--parent", "ghost"); code == 0 || !strings.Contains(errOut, "does not exist") {
		t.Errorf("namespace with a missing parent: exit %d, stderr %q", code, errOut)
	}
	if out := host.must("", "namespace", "list"); !strings.Contains(out, "lime") || !strings.Contains(out, "acme") || !strings.Contains(out, "project") {
		t.Errorf("namespace list = %q", out)
	}
	projectPub := createTokenFor(t, host, "acme", "publish")
	limePub := createTokenFor(t, host, "lime", "publish")
	limeRead := createTokenFor(t, host, "lime", "read")

	// The project maintainer ships a planner and shared standards.
	maintainer.must(projectPub+"\n", "remote", "add", "office", url, "--token-stdin")
	maintainer.must("", "new", "agent", "acme/planner")
	maintainer.must("", "publish", "planner")
	maintainer.must("", "new", "agent", "acme/standards")
	maintainer.must("", "publish", "standards")

	// Lime ships its own planner, and cannot publish to the project.
	author.must(limePub+"\n", "remote", "add", "office", url, "--token-stdin")
	author.must("", "new", "agent", "lime/planner")
	author.must("", "publish", "planner")
	author.must("", "new", "agent", "acme/sneaky")
	if _, errOut, code := author.runIn("", "publish", "sneaky"); code == 0 || !strings.Contains(errOut, "forbidden") {
		t.Errorf("lime publishing to acme: exit %d, stderr %q; want forbidden", code, errOut)
	}

	// A lime reader sees both layers.
	reader.must(limeRead+"\n", "remote", "add", "office", url, "--token-stdin")
	layers := reader.must("", "layers")
	if !strings.Contains(layers, "L1") || !strings.Contains(layers, "lime") || !strings.Contains(layers, "L0") || !strings.Contains(layers, "acme") {
		t.Errorf("layers output = %q, want lime as L1 and acme as L0", layers)
	}
	list := reader.must("", "list")
	for _, want := range []string{"LAYER", "lime/planner", "acme/planner", "acme/standards", "L0 project", "L1 team"} {
		if !strings.Contains(list, want) {
			t.Errorf("list output is missing %q:\n%s", want, list)
		}
	}

	out := reader.must("", "install", "--all")
	if !strings.Contains(out, "skipped acme/planner@0.1.0: shadowed by lime/planner") {
		t.Errorf("install --all output = %q, want acme/planner shadowed by lime/planner", out)
	}
	agents := filepath.Join(reader.home, ".claude", "agents")
	planner := filepath.Join(agents, "planner.md")
	assertManagedBy(t, planner, "lime/planner@0.1.0")
	assertManagedBy(t, filepath.Join(agents, "standards.md"), "acme/standards@0.1.0")
	if status := reader.must("", "status"); !strings.Contains(status, "shadowed by lime/planner") {
		t.Errorf("status = %q, want the acme planner shown as shadowed", status)
	}

	// A project update to its planner never replaces the team's planner.
	reader.must("", "policy", "acme/planner", "auto")
	maintainer.must("", "publish", "planner", "--bump", "minor")
	if out := reader.must("", "sync"); !strings.Contains(out, "shadowed by a closer layer") {
		t.Errorf("sync = %q, want acme/planner reported as shadowed", out)
	}
	assertManagedBy(t, planner, "lime/planner@0.1.0")
	if out := reader.must("", "install", "--all"); !strings.Contains(out, "lime/planner@0.1.0 is already installed") {
		t.Errorf("second install --all = %q, want lime/planner unchanged", out)
	}
	assertManagedBy(t, planner, "lime/planner@0.1.0")
}

func assertManagedBy(t *testing.T, path, stamp string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "managed by loadout: "+stamp+" ") {
		t.Errorf("%s is not %s:\n%s", path, stamp, data)
	}
}
