package pack

import (
	"testing"
)

func FuzzFromFiles(f *testing.F) {
	const goodAgent = "---\nname: planner\ndescription: Plans.\n---\nYou plan.\n"
	seeds := []struct{ manifest, agent string }{
		{"name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n", goodAgent},
		{"name: lime/planner\nkind: agent\nversion: v2.3.4\ndescription: Plans.\ntargets: [claude, copilot]\n", goodAgent},
		{"name: lime/planner\nkind: agent\nversion: 1.0\ndescription: Plans.\n", goodAgent},
		{"name: lime/other\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n", goodAgent},
		{"name: lime\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n", goodAgent},
		{"name: lime/planner/x\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n", goodAgent},
		{"name: lime/planner\nkind: widget\nversion: 1.0.0\ndescription: Plans.\n", goodAgent},
		{"name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: \"  \"\n", goodAgent},
		{"name: lime/planner\nkind: agent\nversion: [1, 0, 0]\ndescription: Plans.\n", goodAgent},
		{"name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: Plans.\nextra: 1\n", goodAgent},
		{"", goodAgent},
		{"---\n", ""},
		{"name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n", "not an agent"},
	}
	for _, s := range seeds {
		f.Add([]byte(s.manifest), []byte(s.agent))
	}

	f.Fuzz(func(t *testing.T, manifest, agentData []byte) {
		files := map[string][]byte{ManifestFile: manifest, AgentFile: agentData}
		p, err := FromFiles(files)
		if err != nil {
			return
		}
		q, err := FromFiles(files)
		if err != nil {
			t.Fatalf("FromFiles second call error: %v (first call succeeded)", err)
		}
		if p.Digest() != q.Digest() {
			t.Errorf("FromFiles digest = %s then %s, want a stable digest", p.Digest(), q.Digest())
		}
		if def, ok := p.Agent(); !ok || def.Name != p.ID().Name() {
			t.Errorf("FromFiles agent name %q (ok %v), package name %q, want equal", def.Name, ok, p.ID().Name())
		}
		if p.Digest() != digestOf(p.Files()) {
			t.Errorf("Digest() = %s, want digest of Files() %s", p.Digest(), digestOf(p.Files()))
		}
	})
}
