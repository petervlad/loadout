package cli

import (
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/remote"
)

// depsWithRemotes has the remotes named, the first the default, and an env
// that may hold LOADOUT_MEMORY_TOKEN.
func depsWithRemotes(t *testing.T, env map[string]string, names ...string) Deps {
	t.Helper()
	store := remote.NewStore(t.TempDir(), func(k string) string { return env[k] })
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := cfg.Add(remote.Remote{Name: n, URL: "https://" + n + ".example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return Deps{Remotes: store}
}

// TestEnvTokenGoesOnlyToTheRemoteNamed: LOADOUT_MEMORY_TOKEN stands for no
// remote in particular, so with several remotes it is used only when the
// command names one. Without a name the stored token of the resolved remote is
// used, and with none there is an error that says to name the remote.
func TestEnvTokenGoesOnlyToTheRemoteNamed(t *testing.T) {
	env := map[string]string{"LOADOUT_MEMORY_TOKEN": "ldm_env"}
	d := depsWithRemotes(t, env, "office", "home")

	cfg, err := d.bridgeConfig("lime", "home")
	if err != nil || cfg.Token != "ldm_env" || cfg.URL != "https://home.example.com" {
		t.Errorf("bridgeConfig(--remote home) = %+v, %v, want the env token for home", cfg, err)
	}

	_, err = d.bridgeConfig("lime", "")
	if err == nil || !strings.Contains(err.Error(), "--remote") {
		t.Errorf("bridgeConfig(no remote, two remotes, env token) = %v, want an error naming --remote", err)
	}

	if err := d.Remotes.SetMemoryToken("office", "ldm_stored"); err != nil {
		t.Fatal(err)
	}
	cfg, err = d.bridgeConfig("lime", "")
	if err != nil || cfg.Token != "ldm_stored" {
		t.Errorf("bridgeConfig(no remote) = %+v, %v, want the stored token and never the env one", cfg, err)
	}
}

// TestEnvTokenWithOneRemote: with a single remote there is nothing to mix up.
func TestEnvTokenWithOneRemote(t *testing.T) {
	d := depsWithRemotes(t, map[string]string{"LOADOUT_MEMORY_TOKEN": "ldm_env"}, "office")
	cfg, err := d.bridgeConfig("lime", "")
	if err != nil || cfg.Token != "ldm_env" {
		t.Errorf("bridgeConfig() = %+v, %v, want the env token", cfg, err)
	}
}
