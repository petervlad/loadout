package semver

import (
	"testing"
)

func FuzzParse(f *testing.F) {
	seeds := []struct{ a, b string }{
		{"1.3.0", "1.2.9"},
		{"v0.0.0", "0.0.1"},
		{"10.20.30", "10.20.30"},
		{"01.2.3", "1.2.3"},
		{"1.2", "1.2.3.4"},
		{"1.2.3-rc.1", "1.2.3+build"},
		{"-1.2.3", "+1.2.3"},
		{"9223372036854775807.0.0", "0.9223372036854775807.0"},
		{"0.0.9223372036854775807", "9223372036854775808.0.0"},
		{"vv1.2.3", " 1.2.3"},
		{"", "..."},
	}
	for _, s := range seeds {
		f.Add(s.a, s.b)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		va, errA := Parse(a)
		vb, errB := Parse(b)
		if errA == nil {
			checkVersion(t, a, va)
		}
		if errB == nil {
			checkVersion(t, b, vb)
		}
		if errA != nil || errB != nil {
			return
		}
		if got, want := va.Compare(vb), -vb.Compare(va); got != want {
			t.Errorf("Parse(%q).Compare(Parse(%q)) = %d, want %d (the negated reverse comparison)", a, b, got, want)
		}
		if (va.Compare(vb) == 0) != (va == vb) {
			t.Errorf("Parse(%q).Compare(Parse(%q)) = %d, but equality is %v", a, b, va.Compare(vb), va == vb)
		}
	})
}

// checkVersion asserts the properties every successfully parsed version has.
func checkVersion(t *testing.T, in string, v Version) {
	t.Helper()
	again, err := Parse(v.String())
	if err != nil {
		t.Fatalf("Parse(Parse(%q).String() = %q) error: %v", in, v.String(), err)
	}
	if again != v {
		t.Errorf("Parse(Parse(%q).String()) = %v, want %v", in, again, v)
	}
	if got := v.Compare(again); got != 0 {
		t.Errorf("Parse(%q).Compare(its String round trip) = %d, want 0", in, got)
	}
	for _, p := range []Part{Patch, Minor, Major} {
		if got := v.Bump(p); got.Compare(v) <= 0 {
			t.Errorf("Parse(%q).Bump(%d) = %v, want a version above %v", in, p, got, v)
		}
	}
}
