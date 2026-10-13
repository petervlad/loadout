package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// maxEnvLine bounds one line of an environment file; a longer one is a
// mistake, not a value.
const maxEnvLine = 64 * 1024

// ParseEnvFile reads the subset of systemd's EnvironmentFile syntax that
// the registry host uses: blank lines, comments (# or ;), KEY=VALUE, and one
// pair of matching quotes around a value. Later keys replace earlier ones,
// as systemd does. It only reads text: nothing in the file is run or
// expanded. An error names the line, never its value, because the file
// holds secrets.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	env := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxEnvLine)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text()) // also drops a CRLF's \r
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		switch {
		case !ok:
			return nil, fmt.Errorf("line %d: want KEY=VALUE", n)
		case strings.HasPrefix(key, "export ") || strings.ContainsAny(key, " \t"):
			return nil, fmt.Errorf("line %d: write KEY=VALUE without \"export\" or spaces in the name, as systemd reads it", n)
		case !validEnvKey(key):
			return nil, fmt.Errorf("line %d: %q is not a variable name (letters, digits and underscores)", n, key)
		}
		v, err := unquote(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", n, key, err)
		}
		env[key] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: %w", n+1, err)
	}
	return env, nil
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// unquote removes one pair of matching quotes. Inside them nothing is
// interpreted, so a value that needs a quote of its own uses the other kind.
func unquote(v string) (string, error) {
	if v == "" || (v[0] != '"' && v[0] != '\'') {
		return v, nil
	}
	if len(v) < 2 || v[len(v)-1] != v[0] {
		return "", errors.New("the quote is not closed; put the value in one pair of matching quotes")
	}
	inner := v[1 : len(v)-1]
	if strings.IndexByte(inner, v[0]) >= 0 {
		return "", errors.New("a quote inside the quotes; use the other kind of quote around the value")
	}
	return inner, nil
}

// knownEnv is every LOADOUT_ variable a loadout program reads, for Unknown.
var knownEnv = []string{
	EnvStateDir, EnvLog, EnvPushoverToken, EnvPushoverUser, EnvUpdateNotice,
	EnvPublicURL, EnvGoogleClientID, EnvGoogleClientSecret,
	EnvSMTPHost, EnvSMTPPort, EnvSMTPUser, EnvSMTPPassword, EnvMailFrom,
	EnvDev, EnvLogin, EnvDemo, EnvAdminPassword, EnvDocsURL, EnvTrustedProxies, EnvClientIPHeader,
	EnvHetznerToken, EnvHetznerServer, EnvHetznerAPI, EnvBackupDir,
	EnvHostProvider, EnvHostRegion, EnvHostSize, EnvHostContact, EnvHostConsole,
	// Read by the CLI (remote.EnvToken) and by deploy/loadout-backup.sh.
	"LOADOUT_TOKEN", "LOADOUT_DATA", "LOADOUT_BACKUP_KEEP_DAYS",
}

// Known returns every LOADOUT_ variable a loadout program reads, so docs and
// templates can be checked against it.
func Known() []string { return slices.Clone(knownEnv) }

// Unknown names the LOADOUT_ variables in env that loadout does not read,
// sorted, so a typo such as LOADOUT_SMTP_PASS is reported instead of
// silently turning a feature off. Variables without the prefix are not
// loadout's to judge.
func Unknown(env map[string]string) []string {
	var out []string
	for k := range env {
		if strings.HasPrefix(k, "LOADOUT_") && !slices.Contains(knownEnv, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
