package pipeline

import "testing"

func TestRounds(t *testing.T) {
	for n, want := range map[int]string{1: "1 round", 2: "2 rounds", 10: "10 rounds"} {
		if got := Rounds(n); got != want {
			t.Errorf("Rounds(%d) = %q, want %q", n, got, want)
		}
	}
}
