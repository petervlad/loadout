package semver

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "1.3.0", want: "1.3.0"},
		{in: "v2.0.10", want: "2.0.10"},
		{in: "0.0.0", want: "0.0.0"},
		{in: "1.3", wantErr: true},
		{in: "1.3.0.1", wantErr: true},
		{in: "1.x.0", wantErr: true},
		{in: "1.-1.0", wantErr: true},
		{in: "01.0.0", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("Parse(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestBump(t *testing.T) {
	v := New(1, 3, 7)
	tests := []struct {
		part Part
		want string
	}{
		{Patch, "1.3.8"},
		{Minor, "1.4.0"},
		{Major, "2.0.0"},
	}
	for _, tc := range tests {
		if got := v.Bump(tc.part).String(); got != tc.want {
			t.Errorf("%s.Bump(%d) = %s, want %s", v, tc.part, got, tc.want)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.10.0", "1.9.9", 1},
		{"2.0.0", "10.0.0", -1},
	}
	for _, tc := range tests {
		a, b := mustParse(t, tc.a), mustParse(t, tc.b)
		if got := a.Compare(b); got != tc.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", a, b, got, tc.want)
		}
	}
}

func TestTextRoundTrip(t *testing.T) {
	var v Version
	if err := v.UnmarshalText([]byte("3.2.1")); err != nil {
		t.Fatal(err)
	}
	b, err := v.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "3.2.1" {
		t.Errorf("round trip = %q, want %q", b, "3.2.1")
	}
}

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func TestMajor(t *testing.T) {
	if got := New(3, 2, 1).Major(); got != 3 {
		t.Errorf("New(3, 2, 1).Major() = %d, want 3", got)
	}
}
