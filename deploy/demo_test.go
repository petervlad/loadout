package deploy_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/demo"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The demo's password is public, so what keeps it from a reachable registry
// is pinned here: the port is published on this machine only, and the image
// carries the switch and the localhost address that serve checks.
func TestDemoStaysOnThisMachine(t *testing.T) {
	compose := read(t, "../compose.yaml")
	ports := regexp.MustCompile(`(?m)^\s*-\s*"?([^"\n]*:\d+:\d+)"?\s*$`).FindAllStringSubmatch(compose, -1)
	if len(ports) != 1 || !strings.HasPrefix(ports[0][1], "127.0.0.1:") {
		t.Errorf("compose.yaml publishes %v, want one port on 127.0.0.1 only", ports)
	}
	if !strings.Contains(compose, `profiles: ["cli"]`) || !strings.Contains(compose, `network_mode: "service:registry"`) {
		t.Error("compose.yaml: the cli service should sit under the cli profile and share the registry's network")
	}
	dockerfile := read(t, "demo/Dockerfile")
	for _, want := range []string{"LOADOUT_DEMO=1", "LOADOUT_PUBLIC_URL=http://localhost:8080", `"demo", "up"`, "AS registry", "AS cli"} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("deploy/demo/Dockerfile does not contain %q", want)
		}
	}
}

// The shell signs alice in with the password the sign-in page shows.
func TestDemoShellUsesTheDemoPassword(t *testing.T) {
	script := read(t, "demo/cli-entrypoint.sh")
	if !strings.Contains(script, "password="+demo.Password+"\n") || !strings.Contains(script, "email=alice@example.com\n") {
		t.Errorf("deploy/demo/cli-entrypoint.sh does not sign in as alice with %q", demo.Password)
	}
}
