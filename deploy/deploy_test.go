// Package deploy_test runs deploy.sh against stand-ins for ssh, scp, go and
// curl, with the "VM" being a local folder.
package deploy_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// stubs are the commands deploy.sh calls, replaced for the test: ssh runs
// the remote command here without sudo (and keeps the script it is sent),
// scp copies into the "remote" path, go writes a dummy binary, curl serves
// release files from STUB_RELEASE, mktemp works in one known folder, the
// local loadout answers the settings check, and the install fails at
// creating the user.
var stubs = map[string]string{
	"ssh": `#!/bin/sh
shift
[ "$1" = sudo ] && shift
echo "ssh $*" >> "$STUB_LOG"
if [ "$*" = "sh -s" ]; then
	tee "$STUB_SCRIPT" | sh -s
	exit $?
fi
exec sh -c "$*"
`,
	"scp": `#!/bin/sh
[ "$1" = -q ] && shift
dest=""
for a in "$@"; do dest="$a"; done
dest="${dest#*:}"
while [ $# -gt 1 ]; do echo "$(basename "$1")" >> "$STUB_SCP"; cp "$1" "$dest"; shift; done
`,
	"go": `#!/bin/sh
echo "go $*" >> "$STUB_LOG"
while [ $# -gt 0 ]; do
	if [ "$1" = -o ]; then : > "$2"; exit 0; fi
	shift
done
`,
	// mktemp makes its folders in STUB_TMP (macOS ignores TMPDIR).
	"mktemp": `#!/bin/sh
n=0
while [ -e "$STUB_TMP/d$n" ]; do n=$((n + 1)); done
mkdir -m 0700 "$STUB_TMP/d$n" && echo "$STUB_TMP/d$n"
`,
	"curl": `#!/bin/sh
out=""; url=""
while [ $# -gt 0 ]; do
	case "$1" in -o) out=$2; shift ;; http*) url=$1 ;; esac
	shift
done
echo "curl $url" >> "$STUB_LOG"
if [ -n "$out" ]; then
	f="$STUB_RELEASE/$(basename "$url")"
	[ -f "$f" ] || exit 22
	cp "$f" "$out"
fi
exit 0
`,
	"loadout": `#!/bin/sh
echo "loadout $*" >> "$STUB_LOG"
if [ "$*" = "serve --help" ]; then echo "      --check   check the configuration"; exit 0; fi
exit "${STUB_LOADOUT_EXIT:-0}"
`,
	"id":      "#!/bin/sh\nexit 1\n",
	"useradd": "#!/bin/sh\necho 'useradd: cannot create user' >&2\nexit 1\n",
}

// deployOpts is what one run of deploy.sh is given.
type deployOpts struct {
	stubs map[string]string // replace or add to the default stubs
	env   []string          // KEY=VALUE; LOADOUT_HOST and LOADOUT_DOMAIN are preset
	dir   string            // where deploy.sh sits; default: this folder
}

// deployRun is what one run of deploy.sh left behind.
type deployRun struct {
	out      string
	err      error
	uploaded []string // file names scp was asked to copy
	log      string   // the ssh, go, curl and loadout commands that ran
	script   string   // the script sent to the "VM"
	left     []string // what is still in the "VM"'s temp folder
}

// ran reports whether a command line starting with prefix was run.
func (r deployRun) ran(prefix string) bool {
	for line := range strings.SplitSeq(r.log, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func runDeploy(t *testing.T, o deployOpts) deployRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("deploy.sh is a POSIX shell script")
	}
	// Read by the test itself, so the test cache notices changes to it.
	for _, f := range []string{"deploy.sh", "prepare.sh", "site.caddy", "Caddyfile"} {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatal(err)
		}
	}
	bin, tmp, logs := t.TempDir(), t.TempDir(), t.TempDir()
	all := map[string]string{}
	maps.Copy(all, stubs)
	maps.Copy(all, o.stubs)
	for name, body := range all {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil { //nolint:gosec // G306: the stubs must be executable
			t.Fatal(err)
		}
	}
	dir := o.dir
	if dir == "" {
		dir = "."
	}
	logPath, scpLog, scriptPath := filepath.Join(logs, "cmd.log"), filepath.Join(logs, "scp.log"), filepath.Join(logs, "script.sh")
	cmd := exec.CommandContext(t.Context(), "sh", filepath.Join(dir, "deploy.sh")) //nolint:gosec // G204: the script under test, in a folder the test made
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_TMP="+tmp,
		"STUB_LOG="+logPath,
		"STUB_SCP="+scpLog,
		"STUB_SCRIPT="+scriptPath,
		"LOADOUT_HOST=vm.example",
		"LOADOUT_DOMAIN=vm.example",
	)
	cmd.Env = append(cmd.Env, o.env...)
	out, err := cmd.CombinedOutput()
	run := deployRun{out: string(out), err: err}
	if b, err := os.ReadFile(logPath); err == nil {
		run.log = string(b)
	}
	if b, err := os.ReadFile(scpLog); err == nil {
		run.uploaded = strings.Fields(string(b))
	}
	if b, err := os.ReadFile(scriptPath); err == nil {
		run.script = string(b)
	}
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		run.left = append(run.left, e.Name())
	}
	return run
}

// elf is the start of a linux binary, which is all deploy.sh looks at.
var elf = []byte("\x7fELF\x02\x01\x01 not really a program")

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // G703: a path inside the test's own temp folder
		t.Fatal(err)
	}
	return path
}

// release writes what a GitHub release holds for version into dir: the
// archive for amd64 and arm64 and a checksums.txt listing them (with the
// archives' sums, or wrong ones when corrupt is set).
func release(t *testing.T, dir, version string, corrupt bool) {
	t.Helper()
	var sums strings.Builder
	sums.WriteString("0000000000000000000000000000000000000000000000000000000000000000  loadout_" + version + "_linux_amd64.tar.gz.sbom.json\n")
	for _, arch := range []string{"amd64", "arm64"} {
		name := "loadout_" + version + "_linux_" + arch + ".tar.gz"
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		body := append(slices.Clone(elf), arch...)
		if err := tw.WriteHeader(&tar.Header{Name: "loadout", Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		hexSum := hex.EncodeToString(sum[:])
		if corrupt {
			hexSum = strings.Repeat("a", 64)
		}
		sums.WriteString(hexSum + "  " + name + "\n")
	}
	writeFile(t, filepath.Join(dir, "checksums.txt"), []byte(sums.String()))
}

// TestFailedInstallRemovesTheUpload: when the install on the VM fails, the
// uploaded folder (with the secrets of loadout.env) is removed anyway.
func TestFailedInstallRemovesTheUpload(t *testing.T) {
	env := writeFile(t, filepath.Join(t.TempDir(), "loadout.env"), []byte("LOADOUT_SMTP_PASSWORD=secret\n"))
	run := runDeploy(t, deployOpts{env: []string{"LOADOUT_ENV_FILE=" + env}})
	if run.err == nil {
		t.Fatalf("deploy.sh succeeded with a failing install:\n%s", run.out)
	}
	if !strings.Contains(run.out, "cannot create user") {
		t.Fatalf("deploy.sh did not fail at the install step:\n%s", run.out)
	}
	if !run.ran("ssh mktemp -d") || !run.ran("ssh sh -s") || !slices.Contains(run.uploaded, "loadout.env") {
		t.Fatalf("the upload or the remote install never ran (log %q, uploaded %v):\n%s", run.log, run.uploaded, run.out)
	}
	for _, name := range run.left {
		t.Errorf("left behind after the failed install: %s", name)
	}
}

// TestSiteIsOptional: the static site and its Caddy block are uploaded only
// when both variables are set, and a half setting stops before any ssh.
func TestSiteIsOptional(t *testing.T) {
	site := t.TempDir()
	writeFile(t, filepath.Join(site, "index.html"), []byte("<p>hi</p>"))
	tests := []struct {
		name     string
		env      []string
		wantSite bool
		wantDie  string // deploy.sh refuses with this, before any ssh
	}{
		{name: "neither"},
		{name: "both", env: []string{"LOADOUT_SITE_DIR=" + site, "LOADOUT_SITE_DOMAIN=example.com"}, wantSite: true},
		{name: "dir only", env: []string{"LOADOUT_SITE_DIR=" + site}, wantDie: "both LOADOUT_SITE_DIR and LOADOUT_SITE_DOMAIN"},
		{name: "domain only", env: []string{"LOADOUT_SITE_DOMAIN=example.com"}, wantDie: "both LOADOUT_SITE_DIR and LOADOUT_SITE_DOMAIN"},
		{name: "not a folder", env: []string{"LOADOUT_SITE_DIR=" + filepath.Join(site, "index.html"), "LOADOUT_SITE_DOMAIN=example.com"}, wantDie: "not a folder"},
		{name: "bad domain", env: []string{"LOADOUT_SITE_DIR=" + site, "LOADOUT_SITE_DOMAIN=a b"}, wantDie: "LOADOUT_SITE_DOMAIN is not a valid host name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			run := runDeploy(t, deployOpts{env: tt.env})
			if tt.wantDie != "" {
				if run.err == nil || !strings.Contains(run.out, tt.wantDie) || run.ran("ssh") {
					t.Fatalf("want a refusal naming %q before any ssh (log %q):\n%s", tt.wantDie, run.log, run.out)
				}
				return
			}
			hasSite := slices.Contains(run.uploaded, "site.caddy") && slices.Contains(run.uploaded, "site.tar")
			neither := !slices.Contains(run.uploaded, "site.caddy") && !slices.Contains(run.uploaded, "site.tar")
			if tt.wantSite && !hasSite || !tt.wantSite && !neither {
				t.Errorf("uploaded %v, want the site files: %v", run.uploaded, tt.wantSite)
			}
			if !slices.Contains(run.uploaded, "Caddyfile") {
				t.Errorf("uploaded %v, want the Caddyfile always", run.uploaded)
			}
		})
	}
}

// TestPrepareOnlyWhenAsked: prepare.sh is uploaded and run on the first
// deploy (LOADOUT_PREPARE=1) and never otherwise.
func TestPrepareOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name        string
		env         []string
		wantPrepare bool
		wantDie     string
	}{
		{name: "unset"},
		{name: "zero", env: []string{"LOADOUT_PREPARE=0"}},
		{name: "one", env: []string{"LOADOUT_PREPARE=1"}, wantPrepare: true},
		{name: "yes is refused", env: []string{"LOADOUT_PREPARE=yes"}, wantDie: "LOADOUT_PREPARE is 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			run := runDeploy(t, deployOpts{env: tt.env})
			if tt.wantDie != "" {
				if run.err == nil || !strings.Contains(run.out, tt.wantDie) || run.ran("ssh") {
					t.Fatalf("want a refusal naming %q before any ssh:\n%s", tt.wantDie, run.out)
				}
				return
			}
			if got := slices.Contains(run.uploaded, "prepare.sh"); got != tt.wantPrepare {
				t.Errorf("prepare.sh uploaded = %v, want %v", got, tt.wantPrepare)
			}
			wantFlag := "prepare=''"
			if tt.wantPrepare {
				wantFlag = "prepare='1'"
			}
			if !strings.Contains(run.script, wantFlag+"\n") {
				t.Errorf("the remote script does not start with %s:\n%.200s", wantFlag, run.script)
			}
			// With the stubbed id the script cannot be root, so prepare.sh
			// stops the run with its own message when (and only when) it ran.
			if got := strings.Contains(run.out, "prepare: run as root"); got != tt.wantPrepare {
				t.Errorf("prepare.sh ran = %v, want %v:\n%s", got, tt.wantPrepare, run.out)
			}
		})
	}
}

// TestBinaryNeedsNoGo: with LOADOUT_BINARY the binary is installed as it is
// and go is never called; a file that is not a linux binary is refused.
func TestBinaryNeedsNoGo(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, filepath.Join(dir, "loadout-linux"), elf)
	mac := writeFile(t, filepath.Join(dir, "loadout-mac"), []byte("\xcf\xfa\xed\xfe mach-o"))
	tests := []struct {
		name    string
		env     []string
		wantDie string
	}{
		{name: "a linux binary", env: []string{"LOADOUT_BINARY=" + good}},
		{name: "a mac binary", env: []string{"LOADOUT_BINARY=" + mac}, wantDie: "not a linux binary"},
		{name: "missing", env: []string{"LOADOUT_BINARY=" + filepath.Join(dir, "nope")}, wantDie: "does not exist"},
		{name: "with a version", env: []string{"LOADOUT_BINARY=" + good, "LOADOUT_VERSION=0.2.0"}, wantDie: "not both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			run := runDeploy(t, deployOpts{env: tt.env})
			if run.ran("go ") {
				t.Errorf("go was called:\n%s", run.log)
			}
			if tt.wantDie != "" {
				if run.err == nil || !strings.Contains(run.out, tt.wantDie) || run.ran("ssh") {
					t.Fatalf("want a refusal naming %q before any ssh:\n%s", tt.wantDie, run.out)
				}
				return
			}
			if !slices.Contains(run.uploaded, "loadout") || !strings.Contains(run.out, "Installing the binary") {
				t.Errorf("the binary was not uploaded (uploaded %v):\n%s", run.uploaded, run.out)
			}
		})
	}
}

// TestVersionIsChecked: a release is downloaded and its SHA-256 checked
// against checksums.txt before anything is uploaded.
func TestVersionIsChecked(t *testing.T) {
	good, bad := t.TempDir(), t.TempDir()
	release(t, good, "0.2.0", false)
	release(t, bad, "0.2.0", true)
	missing := t.TempDir()
	release(t, missing, "0.2.0", false)
	writeFile(t, filepath.Join(missing, "checksums.txt"), []byte("0000  loadout_0.2.0_linux_amd64.tar.gz.sbom.json\n"))
	tests := []struct {
		name     string
		env      []string
		releases string
		wantDie  string // refused before any ssh
		wantGet  string // the archive that was downloaded
	}{
		{name: "good", releases: good, wantGet: "loadout_0.2.0_linux_amd64.tar.gz"},
		{name: "with a v", env: []string{"LOADOUT_VERSION=v0.2.0"}, releases: good, wantGet: "loadout_0.2.0_linux_amd64.tar.gz"},
		{name: "arm64", env: []string{"LOADOUT_ARCH=arm64"}, releases: good, wantGet: "loadout_0.2.0_linux_arm64.tar.gz"},
		{name: "wrong checksum", releases: bad, wantDie: "does not match checksums.txt"},
		{name: "no entry in checksums.txt", releases: missing, wantDie: "no entry for loadout_0.2.0_linux_amd64.tar.gz"},
		{name: "not a release", env: []string{"LOADOUT_VERSION=9.9.9"}, releases: good, wantDie: "is 9.9.9 a release"},
		{name: "not a version", env: []string{"LOADOUT_VERSION=latest"}, releases: good, wantDie: "a release such as 0.2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := append([]string{"LOADOUT_VERSION=0.2.0", "LOADOUT_RELEASE_URL=https://releases.example.com/dl", "STUB_RELEASE=" + tt.releases}, tt.env...)
			run := runDeploy(t, deployOpts{env: env})
			if run.ran("go ") {
				t.Errorf("go was called:\n%s", run.log)
			}
			if tt.wantDie != "" {
				if run.err == nil || !strings.Contains(run.out, tt.wantDie) || run.ran("ssh") || len(run.uploaded) != 0 {
					t.Fatalf("want a refusal naming %q before any upload (uploaded %v):\n%s", tt.wantDie, run.uploaded, run.out)
				}
				return
			}
			if !run.ran("curl https://releases.example.com/dl/v0.2.0/"+tt.wantGet) || !run.ran("curl https://releases.example.com/dl/v0.2.0/checksums.txt") {
				t.Errorf("the release files were not downloaded:\n%s", run.log)
			}
			if !strings.Contains(run.out, "Checksum ok") || !slices.Contains(run.uploaded, "loadout") {
				t.Errorf("the checked binary was not uploaded (uploaded %v):\n%s", run.uploaded, run.out)
			}
			for _, name := range run.uploaded {
				if strings.HasPrefix(name, "loadout_") || name == "checksums.txt" {
					t.Errorf("%s was uploaded; only the binary should be", name)
				}
			}
		})
	}
}

// TestOutsideACheckoutNamesTheVariables: deploy.sh run from a folder that is
// not inside a loadout checkout, with no binary and no version, says what to
// set instead of failing in go.
func TestOutsideACheckoutNamesTheVariables(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"deploy.sh", "prepare.sh", "Caddyfile", "site.caddy", "loadout.service", "loadout-backup.service", "loadout-backup.timer", "loadout-backup.sh"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, name), data)
	}
	run := runDeploy(t, deployOpts{dir: dir})
	if run.err == nil || !strings.Contains(run.out, "LOADOUT_VERSION=X.Y.Z") || !strings.Contains(run.out, "LOADOUT_BINARY=FILE") || run.ran("go ") || run.ran("ssh") {
		t.Errorf("want a refusal naming both variables, with no go and no ssh (log %q):\n%s", run.log, run.out)
	}
}

// TestEnvFileIsCheckedBeforeAnythingChanges: the settings file is checked
// locally before the upload, and on the host with the new binary before it
// replaces the running one; neither prints the file.
func TestEnvFileIsCheckedBeforeAnythingChanges(t *testing.T) {
	const secret = "Zx9-test-secret-4471" //nolint:gosec // G101: a made-up value the test searches for
	env := writeFile(t, filepath.Join(t.TempDir(), "loadout.env"), []byte("LOADOUT_SMTP_PASSWORD="+secret+"\n"))

	t.Run("a bad file stops before the upload", func(t *testing.T) {
		run := runDeploy(t, deployOpts{env: []string{"LOADOUT_ENV_FILE=" + env, "STUB_LOADOUT_EXIT=1"}})
		if run.err == nil || !strings.Contains(run.out, "settings file is not right") || run.ran("ssh") || len(run.uploaded) != 0 {
			t.Errorf("want a refusal before any ssh (uploaded %v):\n%s", run.uploaded, run.out)
		}
		if strings.Contains(run.out, secret) {
			t.Errorf("the secret is in the output:\n%s", run.out)
		}
	})

	t.Run("a good file is checked locally, then on the host first", func(t *testing.T) {
		run := runDeploy(t, deployOpts{env: []string{"LOADOUT_ENV_FILE=" + env}})
		if !run.ran("loadout serve --check --env-file " + env) {
			t.Errorf("the local check did not run:\n%s", run.log)
		}
		check := strings.Index(run.script, "serve --check --env-file")
		replace := strings.Index(run.script, "mv -f /usr/local/bin/loadout.new /usr/local/bin/loadout")
		restart := strings.Index(run.script, "systemctl restart loadout.service")
		if check < 0 || replace < check || restart < replace {
			t.Errorf("the host must check, then replace the binary, then restart (offsets %d, %d, %d)", check, replace, restart)
		}
		if strings.Contains(run.out+run.script, secret) {
			t.Errorf("the secret is in the output or the script:\n%s", run.out)
		}
	})

	t.Run("an older local loadout is skipped and the host still checks", func(t *testing.T) {
		// A stub that answers --help without --check models an older loadout.
		older := map[string]string{"loadout": "#!/bin/sh\necho \"loadout $*\" >> \"$STUB_LOG\"\necho usage\n"}
		run := runDeploy(t, deployOpts{stubs: older, env: []string{"LOADOUT_ENV_FILE=" + env}})
		if !strings.Contains(run.out, "too old for serve --check") || run.ran("loadout serve --check") {
			t.Errorf("want the local check skipped with a line saying why:\n%s", run.out)
		}
		if !strings.Contains(run.script, "serve --check --env-file") {
			t.Error("the host check is missing from the remote script")
		}
	})
}

func TestDeploySaysWhatItAdvertises(t *testing.T) {
	tests := []struct{ describe, want string }{
		{"v0.1.0", "tell older loadout CLIs that 0.1.0 is out"},
		{"v0.1.0-3-gabc1234", "advertise no CLI release"},
		{"v0.1.0-dirty", "advertise no CLI release"},
		{"v1.0.0-rc.1", "advertise no CLI release"},
		{"b42ddb9", "advertise no CLI release"},
		// Tags that are not a release loadout reads: the registry built
		// from them advertises nothing, so deploy.sh must not say it does.
		{"v1.2.3.4", "advertise no CLI release"},
		{"v01.2.3", "advertise no CLI release"},
		{"v1.2.3a", "advertise no CLI release"},
		{"v0.0.0", "advertise no CLI release"},
		{"v1.2.3+build", "advertise no CLI release"},
		{"v10.20.30", "tell older loadout CLIs that 10.20.30 is out"},
	}
	for _, tc := range tests {
		t.Run(tc.describe, func(t *testing.T) {
			t.Parallel()
			git := map[string]string{"git": "#!/bin/sh\necho \"$STUB_DESCRIBE\"\n"}
			run := runDeploy(t, deployOpts{stubs: git, env: []string{"STUB_DESCRIBE=" + tc.describe}}) // the stubbed install fails on purpose
			if !strings.Contains(run.out, tc.want) {
				t.Errorf("deploy at %s: want %q in:\n%s", tc.describe, tc.want, run.out)
			}
		})
	}
}

// TestPrepareRefusesBeforeChangingAnything: prepare.sh checks who runs it
// and for whom before it touches the machine.
func TestPrepareRefusesBeforeChangingAnything(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no user", nil, "usage"},
		{"a bad user name", []string{"Root; reboot"}, "not a plain user name"},
		{"a user that does not exist", []string{"no-such-user-zz9"}, "does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "sh", append([]string{"prepare.sh"}, tt.args...)...) //nolint:gosec // G204: the script under test, with arguments from this table
			out, err := cmd.CombinedOutput()
			// Not root: refused at once. Root (a CI container): refused by the
			// check this case is about. Either way nothing was changed.
			if err == nil || !strings.Contains(string(out), tt.want) && !strings.Contains(string(out), "run as root") {
				t.Errorf("want a refusal mentioning %q or root, got %v:\n%s", tt.want, err, out)
			}
		})
	}
}

// TestScriptsParse: every shell script is valid POSIX sh, so a typo is
// caught here even where shellcheck is not installed.
func TestScriptsParse(t *testing.T) {
	for _, name := range []string{"deploy.sh", "prepare.sh", "loadout-backup.sh"} {
		t.Run(name, func(t *testing.T) {
			if out, err := exec.CommandContext(t.Context(), "sh", "-n", name).CombinedOutput(); err != nil {
				t.Errorf("sh -n %s: %v\n%s", name, err, out)
			}
		})
	}
}

// TestUnitCanWriteTheBackupFolder: the server writes a copy of the database
// before it deletes a package, and the unit's ReadWritePaths is the only
// place it may write besides the data folder.
func TestUnitCanWriteTheBackupFolder(t *testing.T) {
	unit, err := os.ReadFile("loadout.service")
	if err != nil {
		t.Fatal(err)
	}
	var paths, dir string
	for line := range strings.Lines(string(unit)) {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "ReadWritePaths="); ok {
			paths = v
		}
		if v, ok := strings.CutPrefix(line, "Environment=LOADOUT_BACKUP_DIR="); ok {
			dir = v
		}
	}
	if dir == "" || !slices.Contains(strings.Fields(paths), dir) {
		t.Errorf("ReadWritePaths = %q, want it to list LOADOUT_BACKUP_DIR %q", paths, dir)
	}
}

// TestBackupPrunesDeleteCopies: the nightly script removes before-delete-*.db
// copies after KEEP_DAYS like the archives, and keeps newer ones.
func TestBackupPrunesDeleteCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("loadout-backup.sh is a POSIX shell script")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	data, dest := t.TempDir(), t.TempDir()
	old, fresh := filepath.Join(dest, "before-delete-lime-x-20200101T000000Z.db"), filepath.Join(dest, "before-delete-lime-y-20261006T000000Z.db")
	for _, f := range []string{old, fresh} {
		if err := os.WriteFile(f, []byte("db"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	longAgo := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(old, longAgo, longAgo); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", "loadout-backup.sh")
	cmd.Env = append(os.Environ(), "LOADOUT_DATA="+data, "LOADOUT_BACKUP_DIR="+dest, "LOADOUT_BACKUP_KEEP_DAYS=14")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("loadout-backup.sh: %v\n%s", err, out)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the 30 day old copy is still there (stat error %v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the new copy was removed: %v", err)
	}
}

// TestBackupHoldsMemoryDB: memory.db sits beside registry.db and the nightly
// script copies both into the archive, each passing its integrity check and
// still holding its rows.
func TestBackupHoldsMemoryDB(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("loadout-backup.sh is a POSIX shell script")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	rows := map[string]string{"registry.db": "package", "memory.db": "fact"}
	data, dest := t.TempDir(), t.TempDir()
	for name, row := range rows {
		stmts := "PRAGMA journal_mode=WAL; CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('" + row + "');"
		out, err := exec.CommandContext(t.Context(), "sqlite3", filepath.Join(data, name), stmts).CombinedOutput() //nolint:gosec // G204: a fixed command on a temp folder
		if err != nil {
			t.Fatalf("sqlite3 %s: %v\n%s", name, err, out)
		}
	}
	cmd := exec.CommandContext(t.Context(), "sh", "loadout-backup.sh")
	cmd.Env = append(os.Environ(), "LOADOUT_DATA="+data, "LOADOUT_BACKUP_DIR="+dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("loadout-backup.sh: %v\n%s", err, out)
	}
	archives, err := filepath.Glob(filepath.Join(dest, "loadout-*.tar.gz"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives = %v, %v, want one", archives, err)
	}
	restored := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "tar", "-C", restored, "-xzf", archives[0]).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, out)
	}
	for name, row := range rows {
		out, err := exec.CommandContext(t.Context(), "sqlite3", filepath.Join(restored, name), "PRAGMA integrity_check; SELECT v FROM t;").CombinedOutput() //nolint:gosec // G204: a fixed command on a temp folder
		if err != nil {
			t.Fatalf("restored %s: %v\n%s", name, err, out)
		}
		if got := strings.Fields(string(out)); len(got) != 2 || got[0] != "ok" || got[1] != row {
			t.Errorf("restored %s says %q, want ok and %q", name, strings.TrimSpace(string(out)), row)
		}
	}
}

// lastRun reads the .last-run file of a backup folder as key and value pairs.
func lastRun(t *testing.T, dest string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dest, ".last-run"))
	if err != nil {
		t.Fatalf("no .last-run: %v", err)
	}
	got := map[string]string{}
	for line := range strings.Lines(string(data)) {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		got[k] = v
	}
	return got
}

// TestBackupWritesLastRun: a good run records result=ok, the exit code and the
// archive it wrote, a run that cannot start records result=failed, and the
// file is never mistaken for an archive or pruned.
func TestBackupWritesLastRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("loadout-backup.sh is a POSIX shell script")
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not installed")
	}
	data, dest := t.TempDir(), t.TempDir()
	if out, err := exec.CommandContext(t.Context(), sqlite, filepath.Join(data, "registry.db"), "PRAGMA journal_mode=WAL; CREATE TABLE t (v TEXT);").CombinedOutput(); err != nil { //nolint:gosec // G204: a fixed command on a temp folder
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	run := func(path string) error {
		cmd := exec.CommandContext(t.Context(), "sh", "loadout-backup.sh")
		cmd.Env = append(os.Environ(), "PATH="+path, "LOADOUT_DATA="+data, "LOADOUT_BACKUP_DIR="+dest)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("loadout-backup.sh: %v\n%s", err, out)
		}
		return err
	}

	if err := run(os.Getenv("PATH")); err != nil {
		t.Fatal(err)
	}
	archives, _ := filepath.Glob(filepath.Join(dest, "loadout-*.tar.gz"))
	if len(archives) != 1 {
		t.Fatalf("archives = %v, want one", archives)
	}
	got := lastRun(t, dest)
	if got["result"] != "ok" || got["exit"] != "0" || got["archive"] != filepath.Base(archives[0]) {
		t.Errorf(".last-run after a good run = %v, want ok, exit 0 and %s", got, filepath.Base(archives[0]))
	}
	if _, err := time.Parse(time.RFC3339, got["finished"]); err != nil || !strings.HasSuffix(got["finished"], "Z") {
		t.Errorf("finished = %q, want a UTC time: %v", got["finished"], err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".last-run.tmp")); !os.IsNotExist(err) {
		t.Errorf(".last-run.tmp is left behind (stat error %v)", err)
	}

	// The same script without sqlite3 on the PATH: only the tools the exit
	// trap needs are linked in.
	bin := t.TempDir()
	for _, tool := range []string{"date", "mv", "rm", "basename"} {
		toolPath, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed", tool)
		}
		if err := os.Symlink(toolPath, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(bin); err == nil {
		t.Fatal("the script should fail without sqlite3")
	}
	got = lastRun(t, dest)
	if got["result"] != "failed" || got["exit"] != "1" || got["archive"] != "" {
		t.Errorf(".last-run after a failed run = %v, want failed, exit 1 and no archive", got)
	}
	// The earlier archive is still there: a failed run takes nothing away.
	if archives, _ := filepath.Glob(filepath.Join(dest, "loadout-*.tar.gz")); len(archives) != 1 {
		t.Errorf("archives after a failed run = %v, want the earlier one", archives)
	}
}
