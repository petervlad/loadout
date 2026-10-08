package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// `user password` gives an account a password on the host, and simple login
// then signs that user in; the password never shows in the output.
func TestUserPasswordThenSimpleSignIn(t *testing.T) {
	host := &person{session: newSession(t)}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple"}
	base, _ := serveInBackground(t, host, env, externals{})
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/login", nil) // the page sets the form token
	signIn := func(pw string) int {
		status, _, _ := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"ada@example.com"}, "password": {pw}})
		return status
	}
	if got := signIn("a long enough password"); got != http.StatusBadRequest {
		t.Fatalf("sign in before a password is set = %d, want 400", got)
	}

	out := host.must("a long enough password\n", "user", "password", "--email", "Ada@Example.com")
	if !strings.Contains(out, "password set for ada@example.com") || strings.Contains(out, "enough") {
		t.Errorf("output = %q, want the address and no password", out)
	}
	if got := signIn("a long enough password"); got != http.StatusSeeOther {
		t.Errorf("sign in with the new password = %d, want 303", got)
	}

	host.must("a second long password\r\n", "user", "password", "--email", "ada@example.com")
	if got := signIn("a long enough password"); got != http.StatusBadRequest {
		t.Errorf("sign in with the replaced password = %d, want 400", got)
	}
	if got := signIn("a second long password"); got != http.StatusSeeOther {
		t.Errorf("sign in with the second password = %d, want 303", got)
	}
}

func TestUserPasswordRefusals(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"no input", "", []string{"--email", "ada@example.com"}, "no password on standard input"},
		{"empty line", "\n", []string{"--email", "ada@example.com"}, "no password on standard input"},
		{"too short", "short\n", []string{"--email", "ada@example.com"}, "at least 12 characters"},
		{"a common password", "password1234\n", []string{"--email", "ada@example.com"}, "most common"},
		{"bad address", "a long enough password\n", []string{"--email", "nope"}, "email"},
		{"no address", "a long enough password\n", nil, "required flag"},
		{"never a flag", "", []string{"--email", "ada@example.com", "--password", "a long enough password"}, "unknown flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &person{session: newSession(t)}
			out, errOut, code := host.runIn(tt.stdin, append([]string{"user", "password"}, tt.args...)...)
			if code == 0 {
				t.Fatalf("exit 0, want a refusal\n%s", out)
			}
			if !strings.Contains(errOut, tt.want) {
				t.Errorf("stderr = %q, want it to say %q", errOut, tt.want)
			}
			if strings.Contains(out+errOut, "enough password") {
				t.Errorf("the password was echoed:\n%s%s", out, errOut)
			}
		})
	}
}
