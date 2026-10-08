package target_test

import (
	"testing"

	"github.com/petervlad/loadout/internal/target"
)

func TestJoin(t *testing.T) {
	held := target.SessionMessage{Person: "held.", Agent: "held agent."}
	cli := target.SessionMessage{Person: "cli.", Agent: "cli agent."}
	if got := held.Join(cli); got.Person != "held. cli." || got.Agent != "held agent. cli agent." {
		t.Errorf("Join = %+v", got)
	}
	if got := (target.SessionMessage{}).Join(cli); got != cli {
		t.Errorf("empty Join = %+v, want %+v", got, cli)
	}
	if got := held.Join(target.SessionMessage{}); got != held {
		t.Errorf("Join empty = %+v, want %+v", got, held)
	}
}
