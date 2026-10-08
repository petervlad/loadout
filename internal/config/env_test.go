package config

import (
	"maps"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[string]string
		wantErr string
	}{
		{"empty", "", map[string]string{}, ""},
		{"comments and blanks", "# a\n\n; b\n  # c\nA=1\n", map[string]string{"A": "1"}, ""},
		{"spaces around", "  A = 1 \n", map[string]string{"A": "1"}, ""},
		{"double quotes", `A="x y"` + "\n", map[string]string{"A": "x y"}, ""},
		{"single quotes", "A='x y'\n", map[string]string{"A": "x y"}, ""},
		{"other quote inside", `A="it's"` + "\n", map[string]string{"A": "it's"}, ""},
		{"empty value", "A=\nB=\"\"\n", map[string]string{"A": "", "B": ""}, ""},
		{"hash and equals inside a value", "A=p#ss=word\n", map[string]string{"A": "p#ss=word"}, ""},
		{"no expansion", "A=$HOME\n", map[string]string{"A": "$HOME"}, ""},
		{"CRLF", "A=1\r\nB=\"2\"\r\n", map[string]string{"A": "1", "B": "2"}, ""},
		{"no final newline", "A=1", map[string]string{"A": "1"}, ""},
		{"duplicate takes the last", "A=1\nA=2\n", map[string]string{"A": "2"}, ""},
		{"no equals", "A=1\nBROKEN\n", nil, "line 2: want KEY=VALUE"},
		{"export refused", "export A=1\n", nil, "without \"export\""},
		{"space in name", "A B=1\n", nil, "without \"export\""},
		{"bad name", "1A=x\n", nil, "not a variable name"},
		{"empty name", "=x\n", nil, "not a variable name"},
		{"open quote", "A=\"abc\n", nil, "line 1 (A): the quote is not closed"},
		{"quote inside quotes", `A="a"b"` + "\n", nil, "a quote inside the quotes"},
		{"line too long", "A=" + strings.Repeat("x", maxEnvLine) + "\n", nil, "line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEnvFile(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseEnvFile error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("ParseEnvFile = %q, want %q", got, tt.want)
			}
		})
	}
}

// A parse error says where and never what: the file holds secrets.
func TestParseEnvFileErrorsHideValues(t *testing.T) {
	const secret = "s3cret-value-9f2"
	for _, in := range []string{
		"A=\"" + secret + "\n",
		"A=\"" + secret + "\"x\"\n",
		"export A=" + secret + "\n",
		secret + "\n",
	} {
		_, err := ParseEnvFile(strings.NewReader(in))
		if err == nil {
			t.Fatalf("%q parsed, want an error", in)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks the value", err)
		}
	}
}

func TestUnknown(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"none", map[string]string{}, nil},
		{"all known", map[string]string{EnvPublicURL: "x", EnvDocsURL: "x", "LOADOUT_TOKEN": "x"}, nil},
		{"other prefixes are not ours", map[string]string{"HOME": "x", "PATH": "x", "OTHER_X": "x"}, nil},
		{"typos sorted", map[string]string{"LOADOUT_SMTP_PASS": "x", "LOADOUT_PUBLIC_URI": "x", EnvSMTPHost: "x"}, []string{"LOADOUT_PUBLIC_URI", "LOADOUT_SMTP_PASS"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unknown(tt.env)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("Unknown = %v, want %v", got, tt.want)
			}
		})
	}
}

// Every variable Load reads is known to Unknown, so --check never calls a
// real setting a typo.
func TestKnownEnvCoversTheConstants(t *testing.T) {
	for _, k := range []string{
		EnvStateDir, EnvLog, EnvPushoverToken, EnvPushoverUser, EnvUpdateNotice, EnvPublicURL,
		EnvGoogleClientID, EnvGoogleClientSecret, EnvSMTPHost, EnvSMTPPort, EnvSMTPUser, EnvSMTPPassword,
		EnvMailFrom, EnvDev, EnvLogin, EnvDocsURL, EnvHetznerToken, EnvHetznerServer, EnvHetznerAPI,
		EnvBackupDir, EnvHostProvider, EnvHostRegion, EnvHostSize, EnvHostContact, EnvHostConsole,
		EnvTrustedProxies, EnvClientIPHeader, EnvAdminPassword,
	} {
		if got := Unknown(map[string]string{k: "x"}); len(got) != 0 {
			t.Errorf("%s reported unknown", k)
		}
	}
}

func TestLoadDocsURL(t *testing.T) {
	tests := []struct {
		raw, want, wantErr string
	}{
		{"", "", ""},
		{"https://docs.example.com/loadout", "https://docs.example.com/loadout/", ""},
		{" https://docs.example.com/ ", "https://docs.example.com/", ""},
		{"http://intranet.example.com:8080/guide/", "http://intranet.example.com:8080/guide/", ""},
		{"docs.example.com", "", "absolute http or https"},
		{"ftp://docs.example.com/", "", "absolute http or https"},
		{"https://docs.example.com/?x=1", "", "without a query"},
		{"https://docs.example.com/#top", "", "without a query"},
		{"https://user:pw@docs.example.com/", "", "absolute http or https"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := Load(env(map[string]string{"HOME": "/home/a", EnvDocsURL: tt.raw}))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), EnvDocsURL) {
					t.Fatalf("Load error = %v, want %s and %q", err, EnvDocsURL, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Login.DocsURL != tt.want {
				t.Errorf("DocsURL = %q, want %q", got.Login.DocsURL, tt.want)
			}
		})
	}
}

func TestKnownIsACopy(t *testing.T) {
	k := Known()
	if len(k) == 0 {
		t.Fatal("Known() is empty")
	}
	k[0] = "LOADOUT_CHANGED"
	if got := Unknown(map[string]string{Known()[0]: "x"}); len(got) != 0 {
		t.Errorf("changing the result of Known() changed what is known: %v", got)
	}
}
