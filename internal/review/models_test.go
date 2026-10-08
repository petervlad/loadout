package review_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/target/copilot"
	"github.com/petervlad/loadout/internal/tier"
)

// TestCompareRendersWithTheReviewersModels: both versions run with the
// models the reviewer's team would install, not the built-in ones.
func TestCompareRendersWithTheReviewersModels(t *testing.T) {
	deep := func(version string) *pack.Pack {
		p, err := pack.FromFiles(map[string][]byte{
			pack.ManifestFile: []byte("name: lime/planner\nkind: agent\nversion: " + version + "\ndescription: d\n"),
			pack.AgentFile:    []byte("---\nname: planner\ndescription: d\nmodel: deep\n---\nYou plan.\n"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	models, err := tier.New(map[tier.Key]tier.Choice{{Tool: copilot.Name, Hint: agent.ModelDeep}: {Model: "Claude Sonnet 5", Effort: tier.EffortHigh}})
	if err != nil {
		t.Fatal(err)
	}
	cop := &fakeRunner{agentDir: filepath.Join(".github", "agents")}
	c := newComparer(t, &fakeRunner{}, cop)
	_, err = c.Compare(t.Context(), review.Options{
		Tool: copilot.Name, Current: deep("0.1.0"), Candidate: deep("0.2.0"), Models: models,
		Prompts: []string{"plan"}, Runs: 1, Timeout: time.Minute, Mode: review.ModeSafe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cop.files) != 2 {
		t.Fatalf("runs saw %d agent files, want 2", len(cop.files))
	}
	for _, f := range cop.files {
		if !strings.Contains(f, "models: [claude-sonnet-5]") || !strings.Contains(f, "reasoning-effort: high") {
			t.Errorf("reviewed agent file lacks the team's model:\n%s", f)
		}
	}
}
