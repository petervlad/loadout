package main

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/mail/mailtest"
)

// testSecret stands in for every secret value in these tests: it must never
// appear in any output.
const testSecret = "Zx9-test-secret-4471" //nolint:gosec // G101: a made-up value the tests search for

// goodEnvFile is a registry host's environment file with every secret set.
const goodEnvFile = `# the registry's settings
LOADOUT_PUBLIC_URL=https://registry.example.com
LOADOUT_SMTP_HOST=smtp.example.com
LOADOUT_SMTP_USER=apikey
LOADOUT_SMTP_PASSWORD="` + testSecret + `"
LOADOUT_MAIL_FROM="loadout <login@example.com>"
LOADOUT_GOOGLE_CLIENT_ID=id.apps.example.com
LOADOUT_GOOGLE_CLIENT_SECRET=` + testSecret + `
LOADOUT_HETZNER_TOKEN=` + testSecret + `
LOADOUT_HOST_PROVIDER=Acme Cloud
LOADOUT_HOST_REGION=eu-1
LOADOUT_DOCS_URL=https://docs.example.com/loadout
`

func (p *person) envFile(content string) string {
	p.t.Helper()
	path := filepath.Join(p.wd, "loadout.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		p.t.Fatal(err)
	}
	return path
}

func TestServeCheckEnvFile(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantCode int
		// want appears in stdout (exit 0) or stderr (exit 1).
		want []string
	}{
		{"everything on", goodEnvFile, 0, []string{
			"public URL: https://registry.example.com",
			"smtp via smtp.example.com:587",
			"google:     on",
			"docs:       https://docs.example.com/loadout/",
			"Hetzner on; host facts: provider, region",
			"unknown:    none",
		}},
		{"login off", "LOADOUT_SMTP_PASSWORD=" + testSecret + "\n", 0, []string{"login:      off", "unknown:    none"}},
		{"simple login", "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_LOGIN=simple\n", 0, []string{"simple (email and password only", "email:      off (simple login sends no mail)", "google:     off"}},
		{"dev mail", "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_DEV=1\n", 0, []string{"log only"}},
		{"smtp port", "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_SMTP_HOST=smtp.example.com\nLOADOUT_SMTP_PORT=2525\nLOADOUT_MAIL_FROM=login@example.com\n", 0, []string{"smtp via smtp.example.com:2525"}},
		{"a typo fails the check", goodEnvFile + "LOADOUT_SMTP_PASS=" + testSecret + "\n", 1, []string{"loadout does not read LOADOUT_SMTP_PASS"}},
		{"smtp without a sender", "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_SMTP_HOST=smtp.example.com\nLOADOUT_SMTP_PASSWORD=" + testSecret + "\n", 1, []string{"LOADOUT_MAIL_FROM"}},
		{"user without password", "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_SMTP_HOST=smtp.example.com\nLOADOUT_MAIL_FROM=login@example.com\nLOADOUT_SMTP_USER=" + testSecret + "\n", 1, []string{"user and the password"}},
		{"half of google", "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_GOOGLE_CLIENT_SECRET=" + testSecret + "\n", 1, []string{"LOADOUT_GOOGLE_CLIENT_ID"}},
		{"bad port", "LOADOUT_SMTP_PORT=mail\nLOADOUT_SMTP_PASSWORD=" + testSecret + "\n", 1, []string{"LOADOUT_SMTP_PORT"}},
		{"bad public URL", "LOADOUT_PUBLIC_URL=registry\n", 1, []string{"public URL"}},
		{"bad docs URL", "LOADOUT_DOCS_URL=docs.example.com\n", 1, []string{"LOADOUT_DOCS_URL"}},
		{"bad login mode", "LOADOUT_LOGIN=simpel\n", 1, []string{"LOADOUT_LOGIN"}},
		{"unclosed quote", "LOADOUT_SMTP_PASSWORD=\"" + testSecret + "\n", 1, []string{"line 1 (LOADOUT_SMTP_PASSWORD)", "quote is not closed"}},
		{"not a pair", testSecret + "\n", 1, []string{"line 1: want KEY=VALUE"}},
		{"export", "export LOADOUT_SMTP_PASSWORD=" + testSecret + "\n", 1, []string{"line 1", "export"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &person{session: newSession(t)}
			path := p.envFile(tt.file)
			out, errOut, code := p.run("serve", "--check", "--env-file", path)
			if code != tt.wantCode {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tt.wantCode, out, errOut)
			}
			shown := out
			if code != 0 {
				shown = errOut
			}
			for _, w := range tt.want {
				if !strings.Contains(shown, w) {
					t.Errorf("output lacks %q:\n%s", w, shown)
				}
			}
			if strings.Contains(out+errOut, testSecret) {
				t.Errorf("a secret value is in the output:\nstdout: %s\nstderr: %s", out, errOut)
			}
		})
	}
}

// A typo is printed with the other lines and then fails the check, so a
// deploy stops before the service restarts without the setting.
func TestServeCheckTypoFailsAfterPrinting(t *testing.T) {
	p := &person{session: newSession(t)}
	path := p.envFile(goodEnvFile + "LOADOUT_SMTP_PASS=" + testSecret + "\nLOADOUT_TYPO_TWO=1\n")
	out, errOut, code := p.run("serve", "--check", "--env-file", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "unknown:    LOADOUT_SMTP_PASS, LOADOUT_TYPO_TWO") || !strings.Contains(out, "public URL:") {
		t.Errorf("stdout lacks the lines printed before the failure:\n%s", out)
	}
	if strings.Contains(out+errOut, testSecret) {
		t.Errorf("a secret value is in the output:\n%s%s", out, errOut)
	}
}

// Without --env-file the check reads the process environment, and a file's
// settings are not mixed into it.
func TestServeCheckProcessEnvironment(t *testing.T) {
	p := &person{session: newSession(t)}
	p.env = map[string]string{
		"LOADOUT_PUBLIC_URL":    "https://registry.example.com",
		"LOADOUT_DEV":           "1",
		"LOADOUT_SMTP_PASSWORD": testSecret,
		"LOADOUT_SMTP_PASS":     testSecret,
		"UNRELATED":             "x",
	}
	out, errOut, code := p.run("serve", "--check")
	if code != 1 || !strings.Contains(out, "log only") || !strings.Contains(out, "unknown:    LOADOUT_SMTP_PASS ") || !strings.Contains(errOut, "LOADOUT_SMTP_PASS") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, testSecret) || strings.Contains(out, "UNRELATED") {
		t.Errorf("a secret or an unrelated variable is in the output:\n%s%s", out, errOut)
	}

	// A file that only sets the SMTP host does not see the process's dev mode.
	path := p.envFile("LOADOUT_PUBLIC_URL=https://registry.example.com\n")
	out = p.must("", "serve", "--check", "--env-file", path)
	if strings.Contains(out, "log only") || !strings.Contains(out, "email:      off") {
		t.Errorf("--env-file read the process environment:\n%s", out)
	}
}

func TestServeCheckRefusals(t *testing.T) {
	p := &person{session: newSession(t)}
	for name, args := range map[string][]string{
		"missing file":           {"serve", "--check", "--env-file", filepath.Join(p.wd, "nope.env")},
		"env file without check": {"serve", "--env-file", p.envFile("A=1\n")},
	} {
		if out, errOut, code := p.run(args...); code == 0 {
			t.Errorf("%s: exit 0\nstdout: %s\nstderr: %s", name, out, errOut)
		}
	}
	// --check never opens the database or listens.
	if _, err := os.Stat(filepath.Join(p.home, ".loadout", "server")); err == nil {
		t.Error("serve --check created the registry folder")
	}
}

// mailTestEnv is a configuration with login on and mail from a test mailer.
func mailTestEnv() map[string]string {
	return map[string]string{
		"LOADOUT_PUBLIC_URL": "https://registry.example.com",
		"LOADOUT_SMTP_HOST":  "smtp.example.com",
		"LOADOUT_MAIL_FROM":  "loadout <login@example.com>",
	}
}

// runMailTest runs `mail test` with extra environment and a test mailer.
func runMailTest(t *testing.T, env map[string]string, mailer mail.Mailer, args ...string) (string, string, int) {
	t.Helper()
	s := newSession(t)
	all := map[string]string{"HOME": s.home}
	maps.Copy(all, env)
	var out, errOut bytes.Buffer
	ext := externals{mailer: mailer}
	code := runWith(t.Context(), append([]string{"mail", "test"}, args...), strings.NewReader(""), &out, &errOut, func(k string) string { return all[k] }, s.wd, ext)
	return out.String(), errOut.String(), code
}

func TestMailTest(t *testing.T) {
	t.Run("sent", func(t *testing.T) {
		rec := &mailtest.Recorder{}
		out, errOut, code := runMailTest(t, mailTestEnv(), rec, "--to", "ada@example.com")
		if code != 0 || strings.TrimSpace(out) != "sent" {
			t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
		}
		msg := rec.Sent()
		if len(msg) != 1 || msg[0].To != "ada@example.com" || !strings.Contains(msg[0].Text, "https://registry.example.com") {
			t.Errorf("sent = %+v, want one test mail naming the registry", msg)
		}
	})

	t.Run("refused", func(t *testing.T) {
		out, errOut, code := runMailTest(t, mailTestEnv(), refusingMailer{}, "--to", "ada@example.com")
		if code == 0 || out != "" || !strings.Contains(errOut, "did not accept the message: 550 relay denied") {
			t.Errorf("exit %d, want the server's refusal\nstdout: %s\nstderr: %s", code, out, errOut)
		}
	})

	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"no public URL", map[string]string{}, []string{"--to", "ada@example.com"}, "LOADOUT_PUBLIC_URL"},
		{"email off", map[string]string{"LOADOUT_PUBLIC_URL": "https://registry.example.com"}, []string{"--to", "ada@example.com"}, "email is off"},
		{"dev mail", map[string]string{"LOADOUT_PUBLIC_URL": "https://registry.example.com", "LOADOUT_DEV": "1"}, []string{"--to", "ada@example.com"}, "LOADOUT_DEV=1"},
		{"simple login", map[string]string{"LOADOUT_PUBLIC_URL": "https://registry.example.com", "LOADOUT_LOGIN": "simple"}, []string{"--to", "ada@example.com"}, "sends no mail"},
		{"bad address", mailTestEnv(), []string{"--to", "nope"}, "recipient"},
		{"no address", mailTestEnv(), nil, "required flag"},
		{"bad config", map[string]string{"LOADOUT_PUBLIC_URL": "https://registry.example.com", "LOADOUT_SMTP_HOST": "smtp.example.com"}, []string{"--to", "ada@example.com"}, "LOADOUT_MAIL_FROM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := runMailTest(t, tt.env, nil, tt.args...)
			if code == 0 || out != "" || !strings.Contains(errOut, tt.want) {
				t.Errorf("exit %d, want a failure naming %q\nstdout: %s\nstderr: %s", code, tt.want, out, errOut)
			}
		})
	}
}

// A refusal comes back to the operator, and an unreachable server's error
// carries no password.
func TestMailTestRefusalsHideTheSecret(t *testing.T) {
	env := mailTestEnv()
	env["LOADOUT_SMTP_HOST"] = "127.0.0.1"
	env["LOADOUT_SMTP_PORT"] = "1" // nothing listens here
	env["LOADOUT_SMTP_USER"] = "apikey"
	env["LOADOUT_SMTP_PASSWORD"] = testSecret
	out, errOut, code := runMailTest(t, env, nil, "--to", "ada@example.com")
	if code == 0 || !strings.Contains(errOut, "did not accept") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, testSecret) {
		t.Errorf("the password is in the output:\n%s%s", out, errOut)
	}
}

// refusingMailer answers like a server that turns the message away.
type refusingMailer struct{}

func (refusingMailer) Send(context.Context, mail.Message) error {
	return errors.New("550 relay denied")
}
