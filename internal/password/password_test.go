package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery"
	h, err := Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("Hash() = %q, want an argon2id PHC string with the OWASP parameters", h)
	}
	if ok, err := Verify(pw, h); err != nil || !ok {
		t.Errorf("Verify(right password) = %v, %v; want true", ok, err)
	}
	if ok, err := Verify(pw+"!", h); err != nil || ok {
		t.Errorf("Verify(wrong password) = %v, %v; want false", ok, err)
	}
	again, err := Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if again == h {
		t.Error("two hashes of one password are equal; want a random salt each")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	good, err := Hash("some long password")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good, "$")
	tests := map[string]string{
		"empty":          "",
		"bcrypt":         "$2a$10$abcdefghijklmnopqrstuv",
		"argon2i":        strings.Replace(good, "argon2id", "argon2i", 1),
		"old version":    strings.Replace(good, "v=19", "v=16", 1),
		"huge memory":    strings.Replace(good, "m=19456", "m=99999999", 1),
		"too many turns": strings.Replace(good, "t=2", "t=5000", 1),
		"bad salt":       strings.Join([]string{"", parts[1], parts[2], parts[3], "!!", parts[5]}, "$"),
		"short key":      strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], "AAAA"}, "$"),
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			if ok, err := Verify("some long password", h); ok || !errors.Is(err, ErrMalformed) {
				t.Errorf("Verify(%q) = %v, %v; want false, ErrMalformed", h, ok, err)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		pw   string
		want string // part of the reason, empty for accepted
	}{
		{"short", "at least 12"},
		{strings.Repeat("ab1cd", 26), "at most 128"},
		{"Password1234", "common"},
		{"QWERTY123456", "common"},
		{"aaaaaaaaaaaaaaaa", "too few"},
		{"ababababababab", "too few"},
		{"alice.smith@example.com", "email"},
		{"alice.smithx", ""}, // close to the email, but not it
		{"tangerine kettle river", ""},
		{"ünïcödé-pässwörd", ""},
	}
	for _, tt := range tests {
		err := Check(tt.pw, "alice.smith@example.com")
		if tt.want == "" {
			if err != nil {
				t.Errorf("Check(%q) = %v, want nil", tt.pw, err)
			}
			continue
		}
		weak, ok := errors.AsType[*WeakError](err)
		if !ok || !strings.Contains(weak.Error(), tt.want) {
			t.Errorf("Check(%q) = %v, want a WeakError mentioning %q", tt.pw, err, tt.want)
		}
	}
	if err := Check("alicesmith12", "alicesmith12@example.com"); err == nil {
		t.Error("Check(local part of the email, 12 chars) = nil, want an error")
	}
}

func TestCommonListIsNormalised(t *testing.T) {
	for pw := range common {
		if pw != strings.ToLower(pw) || len([]rune(pw)) < MinLength {
			t.Errorf("common entry %q must be lowercase and at least %d characters", pw, MinLength)
		}
	}
}
