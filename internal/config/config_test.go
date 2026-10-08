package config

import (
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantState string
		wantLevel slog.Level
		wantErr   bool
	}{
		{name: "defaults", env: map[string]string{"HOME": "/home/a"}, wantState: "/home/a/.loadout", wantLevel: slog.LevelWarn},
		{name: "overrides", env: map[string]string{"HOME": "/home/a", EnvStateDir: "/tmp/s", EnvLog: "debug"}, wantState: "/tmp/s", wantLevel: slog.LevelDebug},
		{name: "windows home", env: map[string]string{"USERPROFILE": "/c/users/a"}, wantState: "/c/users/a/.loadout", wantLevel: slog.LevelWarn},
		{name: "no home", env: map[string]string{}, wantErr: true},
		{name: "relative home", env: map[string]string{"HOME": "home"}, wantErr: true},
		{name: "bad log level", env: map[string]string{"HOME": "/home/a", EnvLog: "loud"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Load(env(tc.env))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if got.StateDir != tc.wantState || got.LogLevel != tc.wantLevel {
				t.Errorf("Load() = %+v, want state %q level %v", got, tc.wantState, tc.wantLevel)
			}
		})
	}
}

func TestLoadStats(t *testing.T) {
	got, err := Load(env(map[string]string{
		"HOME":           "/home/a",
		EnvHetznerToken:  " tok\n",
		EnvHetznerServer: "167857226",
		EnvBackupDir:     "/var/backups/loadout",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{HetznerToken: "tok", HetznerServer: "167857226", BackupDir: "/var/backups/loadout"}
	if got.Stats != want {
		t.Errorf("Stats = %+v, want %+v", got.Stats, want)
	}
	if got, _ := Load(env(map[string]string{"HOME": "/home/a"})); got.Stats != (Stats{}) {
		t.Errorf("Stats without variables = %+v, want zero", got.Stats)
	}
}

func TestLoadLogin(t *testing.T) {
	got, err := Load(env(map[string]string{
		"HOME":                "/home/a",
		EnvPublicURL:          "https://registry.example.com/",
		EnvGoogleClientID:     "id",
		EnvGoogleClientSecret: "secret",
		EnvSMTPHost:           "smtp.example.com",
		EnvSMTPPort:           "2525",
		EnvSMTPUser:           "user",
		EnvSMTPPassword:       "pass",
		EnvMailFrom:           "loadout <login@example.com>",
		EnvDev:                "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Login{
		PublicURL: "https://registry.example.com", GoogleClientID: "id", GoogleClientSecret: "secret",
		SMTPHost: "smtp.example.com", SMTPPort: 2525, SMTPUser: "user", SMTPPassword: "pass",
		MailFrom: "loadout <login@example.com>", Dev: true,
	}
	if got.Login != want {
		t.Errorf("Login = %+v, want %+v", got.Login, want)
	}
	if _, err := Load(env(map[string]string{"HOME": "/home/a", EnvSMTPPort: "smtp"})); err == nil {
		t.Error("Load(bad SMTP port) succeeded")
	}
	empty, err := Load(env(map[string]string{"HOME": "/home/a"}))
	if err != nil || empty.Login != (Login{}) {
		t.Errorf("Load() without login settings = %+v, %v; want all empty", empty.Login, err)
	}
}

func TestLoadLoginSimple(t *testing.T) {
	tests := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{"", false, false},
		{"simple", true, false},
		{" Simple ", true, false},
		{"google", false, true},
	}
	for _, tt := range tests {
		got, err := Load(env(map[string]string{"HOME": "/home/a", EnvLogin: tt.value}))
		if (err != nil) != tt.wantErr || got.Login.Simple != tt.want {
			t.Errorf("%s=%q: Simple = %v, err = %v; want %v, error %v", EnvLogin, tt.value, got.Login.Simple, err, tt.want, tt.wantErr)
		}
	}
}

func TestLoadHost(t *testing.T) {
	long := strings.Repeat("x", maxHostValue+1)
	tests := []struct {
		name    string
		vars    map[string]string
		want    Host
		wantErr string
	}{
		{name: "unset"},
		{
			name: "trimmed and good",
			vars: map[string]string{
				EnvHostProvider: "  Example Cloud\n", EnvHostRegion: "eu-1", EnvHostSize: "2 vCPU, 4 GB RAM",
				EnvHostContact: "ops@example.com", EnvHostConsole: "https://console.example.com/overview",
			},
			want: Host{Provider: "Example Cloud", Region: "eu-1", Size: "2 vCPU, 4 GB RAM", Contact: "ops@example.com", Console: "https://console.example.com/overview"},
		},
		{name: "too long", vars: map[string]string{EnvHostRegion: long}, wantErr: EnvHostRegion},
		{name: "control character", vars: map[string]string{EnvHostSize: "a\x1bb"}, wantErr: EnvHostSize},
		{name: "http console", vars: map[string]string{EnvHostConsole: "http://console.example.com"}, wantErr: EnvHostConsole},
		{name: "javascript console", vars: map[string]string{EnvHostConsole: "javascript:alert(1)"}, wantErr: EnvHostConsole},
		{name: "console without host", vars: map[string]string{EnvHostConsole: "https:///path"}, wantErr: EnvHostConsole},
		{name: "relative console", vars: map[string]string{EnvHostConsole: "/overview"}, wantErr: EnvHostConsole},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{"HOME": "/home/a"}
			maps.Copy(vars, tc.vars)
			got, err := Load(env(vars))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load() error = %v, want one naming %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Stats.Host != tc.want {
				t.Errorf("Host = %+v, want %+v", got.Stats.Host, tc.want)
			}
		})
	}
}

func TestLoadProxies(t *testing.T) {
	tests := []struct {
		name        string
		vars        map[string]string
		wantHeader  string
		wantTrusted []string
		wantErr     string
	}{
		{name: "unset"},
		{
			name:        "ranges and an address",
			vars:        map[string]string{EnvTrustedProxies: " 203.0.113.0/24 , 2001:db8::/32,198.51.100.7", EnvClientIPHeader: " Fly-Client-IP "},
			wantHeader:  "Fly-Client-IP",
			wantTrusted: []string{"203.0.113.0/24", "2001:db8::/32", "198.51.100.7/32"},
		},
		{name: "v6 address alone", vars: map[string]string{EnvTrustedProxies: "2001:db8::1", EnvClientIPHeader: "CF-Connecting-IP"}, wantHeader: "CF-Connecting-IP", wantTrusted: []string{"2001:db8::1/128"}},
		{name: "ranges without a header", vars: map[string]string{EnvTrustedProxies: "203.0.113.0/24"}, wantErr: EnvClientIPHeader},
		{name: "header without ranges", vars: map[string]string{EnvClientIPHeader: "Fly-Client-IP"}, wantErr: EnvTrustedProxies},
		{name: "x-forwarded-for", vars: map[string]string{EnvTrustedProxies: "203.0.113.0/24", EnvClientIPHeader: "X-Forwarded-For"}, wantErr: "holds a list"},
		{name: "everyone", vars: map[string]string{EnvTrustedProxies: "0.0.0.0/0", EnvClientIPHeader: "Fly-Client-IP"}, wantErr: "every peer"},
		{name: "not an address", vars: map[string]string{EnvTrustedProxies: "fly", EnvClientIPHeader: "Fly-Client-IP"}, wantErr: `"fly"`},
		{name: "empty entry", vars: map[string]string{EnvTrustedProxies: "203.0.113.0/24,,198.51.100.0/24", EnvClientIPHeader: "Fly-Client-IP"}, wantErr: "empty entry"},
		{name: "zone", vars: map[string]string{EnvTrustedProxies: "fe80::1%eth0", EnvClientIPHeader: "Fly-Client-IP"}, wantErr: "fe80::1%eth0"},
		{name: "bad prefix length", vars: map[string]string{EnvTrustedProxies: "203.0.113.0/33", EnvClientIPHeader: "Fly-Client-IP"}, wantErr: "203.0.113.0/33"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{"HOME": "/home/a"}
			maps.Copy(vars, tc.vars)
			got, err := Load(env(vars))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load() error = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var trusted []string
			for _, p := range got.Proxies.Trusted {
				trusted = append(trusted, p.String())
			}
			if got.Proxies.Header != tc.wantHeader || !slices.Equal(trusted, tc.wantTrusted) {
				t.Errorf("Proxies = %q %v, want %q %v", got.Proxies.Header, trusted, tc.wantHeader, tc.wantTrusted)
			}
		})
	}
}
