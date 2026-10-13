package memory_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/secretscan"
)

func TestNewDraftAccepts(t *testing.T) {
	d, err := memory.NewDraft("lime", memory.TypeReference, "  TRK Tracker host  ", " tickets live on tracker.example ", "Never atc.\nSee docs.", []int64{3, 4, 3})
	if err != nil {
		t.Fatalf("NewDraft() error: %v", err)
	}
	if d.Namespace() != "lime" || d.Type() != memory.TypeReference || d.Name() != "TRK Tracker host" ||
		d.Description() != "tickets live on tracker.example" || d.Body() != "Never atc.\nSee docs." {
		t.Errorf("NewDraft() = %q %q %q %q %q", d.Namespace(), d.Type(), d.Name(), d.Description(), d.Body())
	}
	if got := d.Links(); !slices.Equal(got, []int64{3, 4}) {
		t.Errorf("Links() = %v, want repeats dropped: [3 4]", got)
	}
	d.Links()[0] = 99
	if d.Links()[0] != 3 {
		t.Error("Links() returned the draft's own slice, want a copy")
	}
}

func TestNewDraftTypes(t *testing.T) {
	for _, typ := range memory.Types() {
		if _, err := memory.NewDraft("lime", typ, "n", "d", "", nil); err != nil {
			t.Errorf("NewDraft(%q) error: %v", typ, err)
		}
	}
	for _, typ := range []memory.Type{"user", "", "Feedback", "note"} {
		_, err := memory.NewDraft("lime", typ, "n", "d", "", nil)
		wantRefusal(t, "type "+string(typ), err, memory.RuleType, "type", "feedback, project or reference")
	}
}

func TestNewDraftLimits(t *testing.T) {
	const ok = memory.TypeProject
	tests := []struct {
		name                  string
		ns, mname, desc, body string
		links                 []int64
		rule, field, says     string // empty rule means accepted
	}{
		{name: "name at 64", ns: "k", mname: strings.Repeat("a", 64), desc: "d"},
		{name: "name at 65", ns: "k", mname: strings.Repeat("a", 65), desc: "d", rule: memory.RuleMaxName, field: "name", says: "64 characters"},
		{name: "name counts runes", ns: "k", mname: strings.Repeat("é", 64), desc: "d"},
		{name: "name runes over", ns: "k", mname: strings.Repeat("é", 65), desc: "d", rule: memory.RuleMaxName, field: "name", says: "64 characters"},
		{name: "name trimmed before counting", ns: "k", mname: " " + strings.Repeat("a", 64) + " ", desc: "d"},
		{name: "name empty", ns: "k", mname: "", desc: "d", rule: memory.RuleRequired, field: "name", says: "cannot be empty"},
		{name: "name blank", ns: "k", mname: " \t ", desc: "d", rule: memory.RuleRequired, field: "name", says: "cannot be empty"},
		{name: "name two lines", ns: "k", mname: "a\nb", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name carriage return", ns: "k", mname: "a\rb", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "description at 150", ns: "k", mname: "n", desc: strings.Repeat("a", 150)},
		{name: "description at 151", ns: "k", mname: "n", desc: strings.Repeat("a", 151), rule: memory.RuleMaxDesc, field: "description", says: "150 characters"},
		{name: "description empty", ns: "k", mname: "n", desc: "", rule: memory.RuleRequired, field: "description", says: "cannot be empty"},
		{name: "description two lines", ns: "k", mname: "n", desc: "a\nb", rule: memory.RuleOneLine, field: "description", says: "one line"},
		{name: "body empty", ns: "k", mname: "n", desc: "d", body: ""},
		{name: "body at 4096 bytes", ns: "k", mname: "n", desc: "d", body: strings.Repeat("a", 4096)},
		{name: "body at 4097 bytes", ns: "k", mname: "n", desc: "d", body: strings.Repeat("a", 4097), rule: memory.RuleMaxBody, field: "body", says: "4 KB"},
		{name: "body counts bytes", ns: "k", mname: "n", desc: "d", body: strings.Repeat("é", 2048)},
		{name: "body bytes over", ns: "k", mname: "n", desc: "d", body: strings.Repeat("é", 2049), rule: memory.RuleMaxBody, field: "body", says: "team docs"},
		{name: "body keeps lines", ns: "k", mname: "n", desc: "d", body: "a\n\nb\r\n"},
		{name: "body invalid utf-8", ns: "k", mname: "n", desc: "d", body: "a\xffb", rule: memory.RuleText, field: "body", says: "not valid text"},
		{name: "body null", ns: "k", mname: "n", desc: "d", body: "a\x00b", rule: memory.RuleText, field: "body", says: "not valid text"},
		{name: "name line separator", ns: "k", mname: "a\u2028b", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name paragraph separator", ns: "k", mname: "a\u2029b", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name next line", ns: "k", mname: "a\u0085b", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name bidi override", ns: "k", mname: "a\u202eb", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name bidi embedding", ns: "k", mname: "a\u202ab", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name bidi isolate", ns: "k", mname: "a\u2066b", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name bidi isolate pop", ns: "k", mname: "a\u2069b", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name zero width space", ns: "k", mname: "a\u200bb", desc: "d", rule: memory.RuleOneLine, field: "name", says: "one line"},
		{name: "name accents are fine", ns: "k", mname: "Café Zürich ✓", desc: "d"},
		{name: "description line separator", ns: "k", mname: "n", desc: "a\u2028b", rule: memory.RuleOneLine, field: "description", says: "one line"},
		{name: "description bidi override", ns: "k", mname: "n", desc: "a\u202db", rule: memory.RuleOneLine, field: "description", says: "one line"},
		{name: "description plain", ns: "k", mname: "n", desc: "tickets live on tracker.example, never atc"},
		{name: "body escape sequence", ns: "k", mname: "n", desc: "d", body: "a\x1b[2Jb", rule: memory.RuleText, field: "body", says: "control characters"},
		{name: "body bell", ns: "k", mname: "n", desc: "d", body: "a\x07b", rule: memory.RuleText, field: "body", says: "control characters"},
		{name: "body delete", ns: "k", mname: "n", desc: "d", body: "a\x7fb", rule: memory.RuleText, field: "body", says: "control characters"},
		{name: "body tab newline and return", ns: "k", mname: "n", desc: "d", body: "a\tb\nc\r\nd"},
		{name: "ten links", ns: "k", mname: "n", desc: "d", links: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{name: "eleven links", ns: "k", mname: "n", desc: "d", links: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, rule: memory.RuleMaxLinks, field: "links", says: "at most 10"},
		{name: "repeats do not count", ns: "k", mname: "n", desc: "d", links: []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}},
		{name: "link zero", ns: "k", mname: "n", desc: "d", links: []int64{0}, rule: memory.RuleMaxLinks, field: "links", says: "id of another memory"},
		{name: "link negative", ns: "k", mname: "n", desc: "d", links: []int64{-2}, rule: memory.RuleMaxLinks, field: "links", says: "id of another memory"},
		{name: "no namespace", ns: " ", mname: "n", desc: "d", rule: memory.RuleNamespace, field: "namespace", says: "needs a team"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := memory.NewDraft(tt.ns, ok, tt.mname, tt.desc, tt.body, tt.links)
			if tt.rule == "" {
				if err != nil {
					t.Fatalf("NewDraft() error: %v", err)
				}
				return
			}
			wantRefusal(t, tt.name, err, tt.rule, tt.field, tt.says)
		})
	}
}

func TestRefusalNeverHoldsText(t *testing.T) {
	const marker = "MARKER-0xBEEF"
	_, err := memory.NewDraft("k", memory.TypeProject, strings.Repeat(marker, 10), "d", "", nil)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Errorf("NewDraft() error = %v, want a refusal without the text", err)
	}
	_, err = memory.NewDraft("k", memory.TypeProject, "n", "d", marker+strings.Repeat("x", memory.MaxBody), nil)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Errorf("NewDraft() error = %v, want a refusal without the text", err)
	}
}

// The filter must beat the limits, so someone who pastes a long key learns to
// rotate it. It needs the real filter, so it skips while only the stub exists.
func TestSecretIsRefusedBeforeLimits(t *testing.T) {
	const key = "-----BEGIN RSA " + "PRIVATE KEY-----"
	if _, found := secretscan.Scan("body", key); !found {
		t.Skip("secretscan is still the stub")
	}
	long := key + "\n" + strings.Repeat("a", memory.MaxBody)
	_, err := memory.NewDraft("k", memory.TypeProject, strings.Repeat("n", 100), "d", long, nil)
	wantRefusal(t, "secret", err, "private-key", "body", "looks like it holds a secret")
	r, _ := errors.AsType[*memory.Refusal](err)
	if r.Line != 1 || r.Column != 1 {
		t.Errorf("position = line %d column %d, want 1 and 1", r.Line, r.Column)
	}
	if strings.Contains(err.Error(), "BEGIN") {
		t.Errorf("error %q repeats the text", err)
	}
}

func TestDailyRefusal(t *testing.T) {
	oldest := time.Date(2026, 10, 9, 8, 30, 0, 0, time.UTC)
	r := memory.DailyRefusal(oldest)
	if !errors.Is(r, registry.ErrInvalid) || r.Rule != memory.RuleWritesPerDay {
		t.Errorf("DailyRefusal() = %v, want ErrInvalid with rule %s", r, memory.RuleWritesPerDay)
	}
	for _, want := range []string{"200 writes a day", "2026-10-10 08:30 UTC"} {
		if !strings.Contains(r.Error(), want) {
			t.Errorf("DailyRefusal() = %q, want it to say %q", r, want)
		}
	}
	if got := memory.DailyRefusal(time.Time{}).Error(); !strings.Contains(got, "200 writes a day") {
		t.Errorf("DailyRefusal(zero) = %q, want the limit", got)
	}
}

func TestCheckReason(t *testing.T) {
	tests := []struct {
		name, in, want, rule string
	}{
		{name: "plain", in: " out of date ", want: "out of date"},
		{name: "at 200", in: strings.Repeat("a", 200), want: strings.Repeat("a", 200)},
		{name: "over 200", in: strings.Repeat("a", 201), rule: memory.RuleMaxNote},
		{name: "empty", in: " ", rule: memory.RuleRequired},
		{name: "two lines", in: "a\nb", rule: memory.RuleOneLine},
		{name: "invalid utf-8", in: "a\xff", rule: memory.RuleText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := memory.CheckReason(tt.in)
			if tt.rule == "" {
				if err != nil || got != tt.want {
					t.Errorf("CheckReason(%q) = %q, %v, want %q", tt.in, got, err, tt.want)
				}
				return
			}
			wantRefusal(t, tt.name, err, tt.rule, "reason", "")
		})
	}
}

func TestRevise(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, -8, 0)
	e := memory.Entry{ID: 7, Namespace: "lime", Type: memory.TypeReference, Name: "Host", Description: "desc", Body: "body", Links: []int64{2}, ConfirmedAt: old, Status: memory.StatusActive, Version: 3}
	str := func(s string) *string { return &s }
	typ := memory.TypeProject
	links := []int64{5, 6}

	t.Run("confirm only", func(t *testing.T) {
		r, err := memory.Change{Confirm: true, Note: " still true "}.Revise(e, now)
		if err != nil {
			t.Fatalf("Revise() error: %v", err)
		}
		if r.Action() != memory.ActionConfirm || !r.ConfirmedAt().Equal(now) || r.Name() != "Host" || r.Body() != "body" || r.Note() != "still true" {
			t.Errorf("Revise() = %v %v %q %q %q", r.Action(), r.ConfirmedAt(), r.Name(), r.Body(), r.Note())
		}
	})
	t.Run("confirm skips content checks", func(t *testing.T) {
		bad := e
		bad.Name = strings.Repeat("a", 100)
		if _, err := (memory.Change{Confirm: true}).Revise(bad, now); err != nil {
			t.Errorf("Revise() error: %v, want an old entry to be confirmable", err)
		}
	})
	t.Run("update keeps what is not set", func(t *testing.T) {
		r, err := memory.Change{Body: str("new body"), Links: &links}.Revise(e, now)
		if err != nil {
			t.Fatalf("Revise() error: %v", err)
		}
		if r.Action() != memory.ActionUpdate || r.Body() != "new body" || r.Name() != "Host" || r.Type() != memory.TypeReference ||
			!slices.Equal(r.Links(), links) || !r.ConfirmedAt().Equal(old) {
			t.Errorf("Revise() = %v %q %q %v %v", r.Action(), r.Body(), r.Name(), r.Links(), r.ConfirmedAt())
		}
	})
	t.Run("update with confirm moves confirmed_at", func(t *testing.T) {
		r, err := memory.Change{Type: &typ, Description: str("d2"), Confirm: true}.Revise(e, now)
		if err != nil || r.Action() != memory.ActionUpdate || r.Type() != typ || r.Description() != "d2" || !r.ConfirmedAt().Equal(now) {
			t.Errorf("Revise() = %v, %v", r, err)
		}
	})
	t.Run("can clear links and body", func(t *testing.T) {
		none := []int64{}
		r, err := memory.Change{Body: str(""), Links: &none}.Revise(e, now)
		if err != nil || r.Body() != "" || len(r.Links()) != 0 {
			t.Errorf("Revise() = %q %v, %v", r.Body(), r.Links(), err)
		}
	})
	t.Run("the entry is not changed", func(t *testing.T) {
		_, _ = memory.Change{Links: &links}.Revise(e, now)
		if !slices.Equal(e.Links, []int64{2}) {
			t.Errorf("entry links = %v, want them untouched", e.Links)
		}
	})

	refusals := []struct {
		name, rule, field string
		c                 memory.Change
	}{
		{"nothing", memory.RuleNoChange, "", memory.Change{}},
		{"only a note", memory.RuleNoChange, "", memory.Change{Note: "x"}},
		{"name too long", memory.RuleMaxName, "name", memory.Change{Name: str(strings.Repeat("a", 65))}},
		{"description too long", memory.RuleMaxDesc, "description", memory.Change{Description: str(strings.Repeat("a", 151))}},
		{"body too long", memory.RuleMaxBody, "body", memory.Change{Body: str(strings.Repeat("a", 4097))}},
		{"too many links", memory.RuleMaxLinks, "links", memory.Change{Links: &[]int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}}},
		{"type user", memory.RuleType, "type", memory.Change{Type: new(memory.Type("user"))}},
		{"empty name", memory.RuleRequired, "name", memory.Change{Name: str(" ")}},
		{"note too long", memory.RuleMaxNote, "note", memory.Change{Confirm: true, Note: strings.Repeat("a", 201)}},
		{"note two lines", memory.RuleOneLine, "note", memory.Change{Confirm: true, Note: "a\nb"}},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.c.Revise(e, now)
			wantRefusal(t, tt.name, err, tt.rule, tt.field, "")
		})
	}
}

func TestUnconfirmed(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	edge := now.AddDate(0, -6, 0)
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"today", now, false},
		{"five months", now.AddDate(0, -5, 0), false},
		{"exactly six months", edge, false},
		{"a second over six months", edge.Add(-time.Second), true},
		{"a year", now.AddDate(-1, 0, 0), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (memory.Entry{ConfirmedAt: tt.at}).Unconfirmed(now); got != tt.want {
				t.Errorf("Unconfirmed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWords(t *testing.T) {
	long := strings.Repeat("w1 w2 w3 w4 w5 ", 5) + "x6 x7 x8 x9 x10 x11 x12 x13 x14 x15 x16 x17"
	tests := []struct {
		name, in string
		want     []string
	}{
		{"lowercase", "TRK Tracker Host", []string{"trk", "tracker", "host"}},
		{"punctuation splits", `a-b "c" (d), e.f; g/h`, []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
		{"fts syntax is only words", `NEAR(a b) OR "x" * ^ -y`, []string{"near", "a", "b", "or", "x", "y"}},
		{"repeats dropped", "go Go GO go", []string{"go"}},
		{"digits and accents", "été 2026", []string{"été", "2026"}},
		{"nothing", " ... ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := memory.Words(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("Words(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	if got := memory.Words(long); len(got) != memory.MaxQueryWords {
		t.Errorf("Words(long) has %d words, want the cap %d", len(got), memory.MaxQueryWords)
	}
}

// wantRefusal checks err is a *Refusal wrapping ErrInvalid that names the rule
// and field, and whose message contains says.
func wantRefusal(t *testing.T, name string, err error, rule, field, says string) {
	t.Helper()
	r, ok := errors.AsType[*memory.Refusal](err)
	if !ok {
		t.Errorf("%s: error = %v, want a *Refusal", name, err)
		return
	}
	if !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("%s: error %v does not wrap registry.ErrInvalid", name, err)
	}
	if r.Rule != rule || r.Field != field {
		t.Errorf("%s: refusal rule %q field %q, want %q and %q", name, r.Rule, r.Field, rule, field)
	}
	if !strings.Contains(err.Error(), says) {
		t.Errorf("%s: message %q, want it to say %q", name, err, says)
	}
	if !strings.HasSuffix(err.Error(), ".") {
		t.Errorf("%s: message %q should be a sentence ending with a full stop", name, err)
	}
}
