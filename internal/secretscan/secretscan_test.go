package secretscan

import (
	"fmt"
	"strings"
	"testing"
)

// Test secrets are assembled at run time so the source holds no string that a
// scanner (or GitHub push protection) would take for a real credential.
var (
	awsKeyID    = "AKIA" + "QWERTYUIOPASDFGH"
	awsSecret   = strings.Repeat("abcdEFGH", 5)
	jwt         = "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW"
	githubToken = "gh" + "p_" + strings.Repeat("a1B2", 9)
	githubPAT   = "github" + "_pat_" + strings.Repeat("a1B2c3", 4)
	atlassian   = "ATATT3" + "xFfGF0abcdEFGH1234ijkl"
	loadoutTok  = "ldm" + "_" + strings.Repeat("a2", 26)
	slackTok    = "xox" + "b-" + "1234567890-abcdef"
	randomRun   = "Zk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0Ya"
	pemHeader   = "-----BEGIN " + "RSA PRIVATE KEY-----"
)

func TestScanRules(t *testing.T) {
	tests := []struct {
		name string
		text string
		rule string // "" means clean
	}{
		{"aws-access-key-id positive", "key " + awsKeyID, "aws-access-key-id"},
		{"aws-access-key-id ASIA", "ASIA" + "QWERTYUIOPASDFGH", "aws-access-key-id"},
		{"aws-access-key-id negative", "AKIA1234 is too short", ""},

		{"aws-secret-access-key positive", "aws_secret_access_key = " + awsSecret, "aws-secret-access-key"},
		{"aws-secret-access-key short form", "secret_access_key: '" + awsSecret + "'", "aws-secret-access-key"},
		{"aws-secret-access-key negative", "aws_secret_access_key = <your key>", ""},

		{"jwt positive", "Authorization: Bearer " + jwt, "jwt"},
		{"jwt negative", "eyJhb.x.y", ""},

		{"private-key positive", pemHeader + "\nMIIE", "private-key"},
		{"private-key openssh", "-----BEGIN " + "OPENSSH PRIVATE KEY-----", "private-key"},
		{"private-key pgp", "-----BEGIN " + "PGP PRIVATE KEY BLOCK-----", "private-key"},
		{"private-key negative", "-----BEGIN PUBLIC KEY-----", ""},

		{"github-token positive", "token " + githubToken, "github-token"},
		{"github-token fine grained", githubPAT, "github-token"},
		{"github-token negative", "ghp_short", ""},

		{"atlassian-token positive", "ATLASSIAN=" + atlassian, "atlassian-token"},
		{"atlassian-token negative", "ATATT3 is the prefix of cloud API tokens", ""},

		{"loadout-token positive", "use " + loadoutTok, "loadout-token"},
		{"loadout-token negative", "ldt_short", ""},

		{"slack-token positive", slackTok, "slack-token"},
		{"slack-token negative", "xoxb-1", ""},

		{"password-assignment positive", "password=hunter22", "password-assignment"},
		{"password-assignment case and colon", "DB_PASSWD: s3cret!", "password-assignment"},
		{"password-assignment secret", "client_secret = abcd", "password-assignment"},
		{"password-assignment negative", "the password reset flow sends mail", ""},
		{"password-assignment too short", "pwd=abc", ""},

		{"json-secret-key password", `{"password": "hunter22"}`, "json-secret-key"},
		{"json-secret-key no space", `{"secret":"hunter22"}`, "json-secret-key"},
		{"json-secret-key api_key", `{"api_key": "` + strings.Repeat("ab", 4) + `"}`, "json-secret-key"},
		{"json-secret-key apikey", `"apiKey" : "k9x2"`, "json-secret-key"},
		{"json-secret-key prefixed", `"db_password": "pw"`, "json-secret-key"},
		{"json-secret-key single character", `"password": "x"`, "json-secret-key"},
		{"json-secret-key negative placeholder", `{"password": "<password>"}`, ""},
		{"json-secret-key negative mask", `{"password": "******"}`, ""},
		{"json-secret-key negative empty", `{"password": ""}`, ""},
		{"json-secret-key negative other key", `{"secret_ref": "vault/team/db"}`, ""},
		{"json-secret-key negative not a value", `{"password": null, "secret": 5}`, ""},

		{"url-password positive", "postgres://app:hunter22@db.internal:5432/lime", "url-password"},
		{"url-password https", "https://bot:pa55w0rd@host.example.com/org/repo.git", "url-password"},
		{"url-password negative no password", "ssh://git@host.example.com/org/repo.git", ""},
		{"url-password negative port", "https://registry.example.com:8443/path", ""},
		{"url-password negative placeholder", "postgres://app:${DB_PASSWORD}@db/lime", ""},
		{"url-password negative placeholder angle", "postgres://app:<password>@db/lime", ""},
		{"url-password negative scp style", "git@host.example.com:org/repo.git", ""},

		{"api-key-assignment api_key", "api_key = " + strings.Repeat("a1", 10), "api-key-assignment"},
		{"api-key-assignment apikey colon", "APIKEY: " + strings.Repeat("Z9", 10), "api-key-assignment"},
		{"api-key-assignment token", "token=" + strings.Repeat("q7", 10), "api-key-assignment"},
		{"api-key-assignment access_token quoted", `access_token: "` + strings.Repeat("k3", 12) + `"`, "api-key-assignment"},
		{"api-key-assignment 19 is short", "token=" + strings.Repeat("a", 19), ""},
		{"api-key-assignment 20 is enough", "token=" + strings.Repeat("a", 20), "api-key-assignment"},
		{"api-key-assignment negative placeholder", "token=${CI_RUNNER_REGISTRATION_TOKEN}", ""},
		{"api-key-assignment negative prose", "the token is rotated every quarter", ""},
		{"api-key-assignment negative short", "token: abc123", ""},

		{"high-entropy positive", "PAT " + randomRun, "high-entropy"},
		{"high-entropy embeds the example key", randomRun + "AKIAIOSFODNN7EXAMPLE", "high-entropy"},
		{"high-entropy negative lower case only", "thequickbrownfoxjumpsoverthelazydogagain", ""},
		{"high-entropy negative low entropy", "Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", ""},
		{"high-entropy negative short", "Zk3Qv9Lm2Xr8Tb5N", ""},

		{"clean prose", "TRK tickets live on tracker.example, never on old.", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := Scan("body", tc.text)
			if tc.rule == "" {
				if ok {
					t.Fatalf("clean text refused by rule %q", f.Rule)
				}
				return
			}
			if !ok || f.Rule != tc.rule {
				t.Fatalf("Scan = %+v, %v, want rule %q", f, ok, tc.rule)
			}
			if f.Field != "body" {
				t.Errorf("Field = %q, want body", f.Field)
			}
		})
	}
}

func TestAllowlist(t *testing.T) {
	sha512hex := strings.Repeat("0123456789abcdef", 8)
	tests := []struct {
		name string
		text string
	}{
		{"git sha", "commit 3f786850e387550fdab836ed7e6dc881de23001b"},
		{"sha256 hex", "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		{"sha256 upper hex", strings.ToUpper("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08")},
		{"uuid", "id 123e4567-e89b-12d3-a456-426614174000"},
		{"sha256 label", "image sha256:" + "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		{"sha512 label", "sha512:" + sha512hex},
		{"sha512 integrity", "integrity sha512-" + "Zk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0YaZk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0Ya=="},
		{"placeholder stars", "password=****"},
		{"placeholder angle", "password: <your-password>"},
		{"placeholder quoted angle", `"password": "<password>",`},
		{"placeholder braces", "PASSWORD=${DB_PASSWORD}"},
		{"placeholder dollar", "password=$DB_PASSWORD"},
		{"placeholder x mask", "secret: xxxxxxxx"},
		{"aws example key id", "AKIAIOSFODNN7EXAMPLE"},
		{"aws example secret", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		{"path with ticket folder", "see /home/projects/TRK-2714-fixes-docs/ for it"},
		{"long path with ticket folder", "/work/loadout/docs/plans/TRK-2714-memory-importer/notes.md"},
		{"ticket style name", "ticket TRK-271400-fix-memory-index-quickly"},
		{"path with digits in a part", "/home/projects/some-thing-v2611/TRKDIR"},
		{"path with underscores", "/var/lib/loadout/Release_Notes_Generation_Runner/data"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if f, ok := Scan("body", tc.text); ok {
				t.Fatalf("allowlisted text refused by rule %q", f.Rule)
			}
		})
	}
}

func TestPlaceholderIsNotAPassword(t *testing.T) {
	// The allowlist must not swallow a real value that merely starts with a
	// placeholder shape.
	for _, v := range []string{"hunter22", "<a>b", "${A}b", "xxxxy"} {
		if isPlaceholder(v) {
			t.Errorf("isPlaceholder(%q) = true", v)
		}
	}
}

func TestExampleDoesNotHideARealKey(t *testing.T) {
	text := "AKIAIOSFODNN7EXAMPLE then " + awsKeyID
	f, ok := Scan("body", text)
	if !ok || f.Rule != "aws-access-key-id" {
		t.Fatalf("Scan = %+v, %v", f, ok)
	}
	if want := len("AKIAIOSFODNN7EXAMPLE then ") + 1; f.Column != want {
		t.Errorf("Column = %d, want %d", f.Column, want)
	}
}

func TestPosition(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		line, col  int
		wantRuleOK bool
	}{
		{"first column", awsKeyID, 1, 1, true},
		{"second line", "one\ntwo " + awsKeyID, 2, 5, true},
		{"crlf", "one\r\ntwo\r\n" + awsKeyID, 3, 1, true},
		{"columns count runes", "café ü " + awsKeyID, 1, 8, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := Scan("description", tc.text)
			if ok != tc.wantRuleOK || f.Line != tc.line || f.Column != tc.col {
				t.Fatalf("Scan = %+v, %v, want line %d column %d", f, ok, tc.line, tc.col)
			}
		})
	}
}

// A Finding goes into logs and error messages, so nothing of the match may be
// reachable from it.
func TestFindingHoldsNoMatch(t *testing.T) {
	secrets := []string{awsKeyID, awsSecret, jwt, githubToken, atlassian, loadoutTok, slackTok, randomRun, "hunter22"}
	for _, s := range secrets {
		f, ok := Scan("body", "x "+s+" "+s)
		if !ok {
			continue
		}
		if dump := fmt.Sprintf("%+v %#v %v", f, f, f); strings.Contains(dump, s) {
			t.Errorf("finding for %q leaks the match: %s", s[:4], dump)
		}
	}
}

func TestRulesAreNamedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range rules {
		if r.name == "" || seen[r.name] {
			t.Errorf("rule name %q is empty or repeated", r.name)
		}
		seen[r.name] = true
	}
	if len(rules) != 13 {
		t.Errorf("have %d rules, the plan lists 13", len(rules))
	}
}

// Linear time is a property of the engine, but a large near-miss input guards
// against a rule that someone later rewrites with a quadratic check.
func TestLargeInputStaysFast(t *testing.T) {
	text := strings.Repeat("password a1B2 ghp_ eyJ.eyJ. AKIA ", 2000)
	if _, ok := Scan("body", text); ok {
		t.Fatal("near-misses were refused")
	}
}

// Word-like paths are allowed, but a random secret, alone or inside a path,
// is still refused.
func TestWordyAllowanceKeepsSecretsOut(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"random base64", "Zk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0YaZk3Qv9Lm"},
		{"base64url jwt-like part", "Zk3Qv9Lm2Xr8Tb5Nc7Wd-4Hf6Jg1Ps0YaZk3Qv9_Lm2Xr8"},
		{"random with one dash", "Zk3Qv9Lm2Xr8Tb5Nc7-Wd4Hf6Jg1Ps0YaZk3Qv9Lm2Xr8"},
		{"service token", "ghp_" + "Zk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0YaZk3Q"},
		{"aws secret key", "aws_secret_access_key = " + "Zk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0YaZk3Qv9Lm"},
		{"token in a path", "/home/x/Zk3Qv9Lm2Xr8Tb5Nc7Wd4Hf6Jg1Ps0YaZk3Qv9Lm/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := Scan("body", tc.text); !ok {
				t.Fatal("secret was allowed")
			}
		})
	}
}
