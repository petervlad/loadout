package deploy_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// downloadsDeploy runs deploy.sh against the stubs, with or without
// LOADOUT_DOWNLOADS, and returns what it printed, the folder the uploaded
// files were copied to and the go builds it made. The stubbed install fails
// on purpose, after the upload.
func downloadsDeploy(t *testing.T, describe string, downloads bool) (out, upload string, goBuilds []string) {
	t.Helper()
	// Read by the test itself, so the test cache notices changes to them.
	for _, f := range []string{"install.sh", "downloads.routes"} {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatal(err)
		}
	}
	upload, goLog := t.TempDir(), filepath.Join(t.TempDir(), "go.log")
	stubs := map[string]string{
		"git": "#!/bin/sh\necho \"$STUB_DESCRIBE\"\n",
		// scp also copies into STUB_UPLOAD, which the test reads, since
		// the install removes what it was sent.
		"scp": `#!/bin/sh
[ "$1" = -q ] && shift
dest=""
for a in "$@"; do dest="$a"; done
dest="${dest#*:}"
while [ $# -gt 1 ]; do
	echo "$(basename "$1")" >> "$STUB_SCP"
	cp "$1" "$dest"
	cp "$1" "$STUB_UPLOAD/"
	shift
done
`,
		// go writes a dummy binary and logs the system and flags it was given.
		"go": `#!/bin/sh
echo "CGO_ENABLED=$CGO_ENABLED $GOOS/$GOARCH $*" >> "$STUB_GOLOG"
while [ $# -gt 0 ]; do
	if [ "$1" = -o ]; then : > "$2"; exit 0; fi
	shift
done
`,
	}
	env := []string{"STUB_UPLOAD=" + upload, "STUB_GOLOG=" + goLog, "STUB_DESCRIBE=" + describe}
	if downloads {
		env = append(env, "LOADOUT_DOWNLOADS=1")
	}
	run := runDeploy(t, deployOpts{stubs: stubs, env: env})
	logged, _ := os.ReadFile(goLog)
	return run.out, upload, strings.Split(strings.TrimSpace(string(logged)), "\n")
}

// untar lists the regular files of a tar (plain or gzipped) by name, with
// the leading "./" taken off.
func untar(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		r = gz
	}
	files := map[string][]byte{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr) //nolint:gosec // G110: a few bytes of test data
		if err != nil {
			t.Fatal(err)
		}
		files[strings.TrimPrefix(h.Name, "./")] = body
	}
}

var downloadTargets = []string{"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"}

// TestDownloadsDeployBuildsTheDownloads: a deploy with LOADOUT_DOWNLOADS=1 builds the CLI for
// the four systems with the registry's flags plus the install method, packs
// each alone in a tar.gz with a stable name, writes checksums.txt that
// the hash tools can read, VERSION and an install.sh that knows its registry.
func TestDownloadsDeployBuildsTheDownloads(t *testing.T) {
	out, upload, builds := downloadsDeploy(t, "v0.1.0", true)

	// One registry build, then the four CLIs, with the same flags.
	const flags = "-trimpath -ldflags -s -w -X main.version=v0.1.0"
	if len(builds) != 5 {
		t.Fatalf("go ran %d times, want 5 (registry and four CLIs):\n%s\n%s", len(builds), strings.Join(builds, "\n"), out)
	}
	if !strings.Contains(builds[0], "CGO_ENABLED=0 linux/amd64 build "+flags) || strings.Contains(builds[0], "installMethod") {
		t.Errorf("registry build = %q, want linux/amd64 with the common flags and no install method", builds[0])
	}
	var got []string
	for _, b := range builds[1:] {
		if !strings.Contains(b, "CGO_ENABLED=0 ") || !strings.Contains(b, flags+" -X main.installMethod=registry") {
			t.Errorf("CLI build %q lacks CGO_ENABLED=0 or the flags %q plus the install method", b, flags)
		}
		got = append(got, strings.Fields(b)[1])
	}
	slices.Sort(got)
	if want := slices.Sorted(slices.Values(downloadTargets)); !slices.Equal(got, want) {
		t.Errorf("CLI targets = %v, want %v", got, want)
	}

	files := untar(t, filepath.Join(upload, "dl.tar"))
	for _, name := range []string{"install.sh", "dl/checksums.txt", "dl/VERSION"} {
		if _, ok := files[name]; !ok {
			t.Errorf("dl.tar has no %s (has %v)", name, slices.Sorted(maps.Keys(files)))
		}
	}
	if v := string(files["dl/VERSION"]); v != "v0.1.0\n" {
		t.Errorf("VERSION = %q, want v0.1.0", v)
	}
	if strings.Contains(string(files["install.sh"]), "__LOADOUT_BASE_URL__") ||
		!strings.Contains(string(files["install.sh"]), `BASE="${LOADOUT_BASE_URL:-https://vm.example}"`) {
		t.Errorf("install.sh was not given the registry address:\n%s", files["install.sh"])
	}
	// The scp glob uploads files, never the hidden build folder.
	if _, err := os.Stat(filepath.Join(upload, "loadout")); err != nil {
		t.Errorf("the registry binary was not uploaded: %v", err)
	}

	sums := map[string]string{}
	for line := range strings.Lines(string(files["dl/checksums.txt"])) {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("checksums.txt line %q is not HASH NAME", line)
		}
		sums[f[1]] = f[0]
	}
	for _, target := range downloadTargets {
		name := "loadout_" + strings.ReplaceAll(target, "/", "_") + ".tar.gz"
		inner := untarArchive(t, files, name)
		if len(inner) != 1 {
			t.Errorf("%s holds %d files, want only loadout", name, len(inner))
		}
		if _, ok := inner["loadout"]; !ok {
			t.Errorf("%s has no loadout file", name)
		}
		if sums[name] == "" {
			t.Errorf("checksums.txt has no line for %s", name)
		}
	}
	if len(sums) != len(downloadTargets) {
		t.Errorf("checksums.txt lists %d files, want %d", len(sums), len(downloadTargets))
	}
}

// untarArchive checks one archive of dl.tar against its checksum and lists it.
func untarArchive(t *testing.T, files map[string][]byte, name string) map[string][]byte {
	t.Helper()
	body, ok := files["dl/"+name]
	if !ok {
		t.Fatalf("dl.tar has no dl/%s", name)
	}
	var want string
	for line := range strings.Lines(string(files["dl/checksums.txt"])) {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("%s hashes to %s, checksums.txt says %s", name, got, want)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return untar(t, path)
}

// TestPlainDeployPublishesNoDownloads: without LOADOUT_DOWNLOADS only the
// registry is built and nothing else is uploaded, and the routes file is
// not sent.
func TestPlainDeployPublishesNoDownloads(t *testing.T) {
	_, upload, builds := downloadsDeploy(t, "v0.1.0", false)
	if len(builds) != 1 {
		t.Errorf("go ran %d times, want 1:\n%s", len(builds), strings.Join(builds, "\n"))
	}
	for _, name := range []string{"dl.tar", "downloads.routes"} {
		if _, err := os.Stat(filepath.Join(upload, name)); !os.IsNotExist(err) {
			t.Errorf("%s was uploaded without LOADOUT_DOWNLOADS (stat error %v)", name, err)
		}
	}
}

// TestDownloadsExcludeABinaryOrARelease: the CLI is built from the checkout.
func TestDownloadsExcludeABinaryOrARelease(t *testing.T) {
	for _, env := range [][]string{{"LOADOUT_VERSION=0.2.0"}, {"LOADOUT_BINARY=/nonexistent"}} {
		run := runDeploy(t, deployOpts{env: append([]string{"LOADOUT_DOWNLOADS=1"}, env...)})
		if run.err == nil || !strings.Contains(run.out, "builds the CLI from this checkout") {
			t.Errorf("%v: want the refusal, got %v:\n%s", env, run.err, run.out)
		}
	}
}

// TestDownloadFolderIsInstalledAtomically: what the host does with dl.tar.
// The install runs on the host as root and cannot run here, so this reads the
// script for the properties that matter.
func TestDownloadFolderIsInstalledAtomically(t *testing.T) {
	script, err := os.ReadFile("deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/var/www/loadout-dl.new",
		"mv /var/www/loadout-dl.new /var/www/loadout-dl",
		"chown -R root:root /var/www/loadout-dl.new",
		"chmod 0755 {}",
		"chmod 0644 {}",
		"rm -rf /etc/caddy/loadout.d/downloads.routes /var/www/loadout-dl", // taken down when not asked for
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("deploy.sh does not contain %q", want)
		}
	}
	// The site folder is swapped on its own, untouched by the download one.
	if strings.Count(string(script), "loadout-site.new") < 3 {
		t.Error("the site swap seems to have changed")
	}
}

// TestRoutesServeDownloadsBeforeTheProxy: /install.sh and /dl/* come from the
// download folder, and the Caddyfile imports them ahead of reverse_proxy.
func TestRoutesServeDownloadsBeforeTheProxy(t *testing.T) {
	caddyfile, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	proxy := regexp.MustCompile(`(?m)^\s*reverse_proxy `).FindIndex(caddyfile)
	imp := strings.Index(string(caddyfile), "import /etc/caddy/loadout.d/*.routes")
	if proxy == nil || imp < 0 || imp > proxy[0] {
		t.Fatalf("the routes import is at %d, want it before reverse_proxy at %v", imp, proxy)
	}
	b, err := os.ReadFile("downloads.routes")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"@install path /install.sh", "@downloads path /dl/*", "root * /var/www/loadout-dl", `Cache-Control "no-cache"`, `X-Content-Type-Options "nosniff"`, "text/plain", "application/gzip", "hide .*"} {
		if !strings.Contains(text, want) {
			t.Errorf("downloads.routes lacks %q", want)
		}
	}
	for _, banned := range []string{"crt", "x509", "tls "} {
		if strings.Contains(text, banned) {
			t.Errorf("downloads.routes mentions %q: the registry serves no certificate", banned)
		}
	}
}
