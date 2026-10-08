package cli

import (
	"testing"

	"github.com/petervlad/loadout/internal/api"
)

func TestAuthorName(t *testing.T) {
	tests := []struct {
		actor api.Actor
		want  string
	}{
		{api.Actor{TokenID: "t1", Label: "alice@example.com via loadout login"}, "alice@example.com"},
		{api.Actor{TokenID: "t1", Label: "ci bot"}, "ci bot"},
		{api.Actor{TokenID: "t1"}, "token t1"},
	}
	for _, tt := range tests {
		if got := authorName(tt.actor); got != tt.want {
			t.Errorf("authorName(%+v) = %q, want %q", tt.actor, got, tt.want)
		}
	}
}
