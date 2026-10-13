package deploy_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeCLI is a loadout that prints its version, packed as the registry packs
// the real one: a tar.gz holding only the file "loadout".
func fakeCLI(t *testing.T, version string) []byte {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho 'loadout %s'\n", version)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "loadout", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// downloads is a registry's /dl/ folder served over plain http: the files by
// name, and the names that were asked for.
type downloads struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	asked []string
}

func newDownloads(t *testing.T, files map[string][]byte) *downloads {
	t.Helper()
	d := &downloads{files: files}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		name, ok := strings.CutPrefix(r.URL.Path, "/dl/")
		d.asked = append(d.asked, name)
		body, found := d.files[name]
		if !ok || !found {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *downloads) set(name string, body []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files[name] = body
}

// checksumLine is what sha256sum and shasum -a 256 write.
func checksumLine(name string, body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

// release is the files a registry publishes for the four systems.
func cliRelease(t *testing.T, version string) map[string][]byte {
	t.Helper()
	archive := fakeCLI(t, version)
	files := map[string][]byte{}
	var sums strings.Builder
	for _, target := range downloadTargets {
		name := "loadout_" + strings.ReplaceAll(target, "/", "_") + ".tar.gz"
		files[name] = archive
		sums.WriteString(checksumLine(name, archive))
	}
	files["checksums.txt"] = []byte(sums.String())
	return files
}

// installRun is one run of install.sh in a home of its own.
type installRun struct {
	home, bin, tmp string
	env            []string
}

func newInstallRun(t *testing.T, base string) *installRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a POSIX shell script")
	}
	for _, tool := range []string{"curl", "tar", "awk", "uname", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed, install.sh cannot run here", tool)
		}
	}
	_, errSha := exec.LookPath("sha256sum")
	if _, errShasum := exec.LookPath("shasum"); errSha != nil && errShasum != nil {
		t.Skip("neither sha256sum nor shasum is installed, install.sh cannot run here")
	}
	// Read by the test itself, so the test cache notices changes to it.
	if _, err := os.ReadFile("install.sh"); err != nil {
		t.Fatal(err)
	}
	r := &installRun{home: t.TempDir(), tmp: t.TempDir()}
	r.bin = filepath.Join(r.home, ".local", "bin")
	r.env = []string{
		"HOME=" + r.home,
		"TMPDIR=" + r.tmp,
		"LOADOUT_BASE_URL=" + base,
		"PATH=" + os.Getenv("PATH"),
	}
	return r
}

// with adds or replaces environment variables.
func (r *installRun) with(kv ...string) *installRun {
	c := *r
	c.env = append([]string{}, r.env...)
	for _, e := range kv {
		key, _, _ := strings.Cut(e, "=")
		c.env = append(withoutKey(c.env, key), e)
	}
	return &c
}

func withoutKey(env []string, key string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, key+"=") {
			out = append(out, e)
		}
	}
	return out
}

func (r *installRun) run(t *testing.T) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "install.sh")
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// leftovers lists what install.sh left in its temporary folder.
func (r *installRun) leftovers(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(r.tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("install.sh left %s in its temporary folder", e.Name())
	}
}

func (r *installRun) installedVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), filepath.Join(r.bin, "loadout")).CombinedOutput() //nolint:gosec // G204: the file the test just installed
	if err != nil {
		t.Fatalf("the installed loadout does not run: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *installRun) installed() bool {
	_, err := os.Stat(filepath.Join(r.bin, "loadout")) //nolint:gosec // G703: a temp folder of the test
	return err == nil
}

func TestInstallScriptInstallsTheCLI(t *testing.T) {
	d := newDownloads(t, cliRelease(t, "0.9.0"))
	r := newInstallRun(t, d.URL)
	out, err := r.run(t)
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if got := r.installedVersion(t); got != "loadout 0.9.0" {
		t.Errorf("installed version = %q, want loadout 0.9.0", got)
	}
	info, err := os.Stat(filepath.Join(r.bin, "loadout"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed loadout is not executable: %v %v", info, err)
	}
	for _, want := range []string{
		"loadout 0.9.0", // the version it prints
		r.bin + " is not on your PATH",
		`echo 'export PATH="` + r.bin + `:$PATH"' >> ~/.zshrc`,
		"loadout login --remote " + d.URL,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"curl -k", "CA ", "--ca-file", "fingerprint"} {
		if strings.Contains(out, banned) {
			t.Errorf("output mentions %q, and install.sh has no certificate step:\n%s", banned, out)
		}
	}
	// Nothing but the CLI is written: no certificate folder in the home.
	if _, err := os.Stat(filepath.Join(r.home, ".loadout")); !os.IsNotExist(err) {
		t.Errorf("install.sh made %s (stat error %v)", filepath.Join(r.home, ".loadout"), err)
	}
	if strings.Contains(out, "\u2014") {
		t.Errorf("output has an em-dash:\n%s", out)
	}
	r.leftovers(t)
}

func TestInstallScriptSaysNothingAboutPathWhenItIsThere(t *testing.T) {
	d := newDownloads(t, cliRelease(t, "0.9.0"))
	r := newInstallRun(t, d.URL)
	r = r.with("PATH=" + r.bin + string(os.PathListSeparator) + os.Getenv("PATH"))
	out, err := r.run(t)
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if strings.Contains(out, "PATH") {
		t.Errorf("the folder is on PATH but the output talks about PATH:\n%s", out)
	}
}

func TestInstallScriptUsesTheBinFolderItIsGiven(t *testing.T) {
	d := newDownloads(t, cliRelease(t, "0.9.0"))
	r := newInstallRun(t, d.URL)
	r.bin = filepath.Join(t.TempDir(), "my", "bin")
	if out, err := r.with("LOADOUT_BIN_DIR=" + r.bin).run(t); err != nil || !r.installed() {
		t.Fatalf("install.sh into %s: %v\n%s", r.bin, err, out)
	}
}

func TestInstallScriptUpdatesInPlace(t *testing.T) {
	d := newDownloads(t, cliRelease(t, "0.9.0"))
	r := newInstallRun(t, d.URL)
	if out, err := r.run(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	for name, body := range cliRelease(t, "0.10.0") {
		d.set(name, body)
	}
	out, err := r.run(t)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if got := r.installedVersion(t); got != "loadout 0.10.0" {
		t.Errorf("after the second run the version is %q, want loadout 0.10.0", got)
	}
	entries, _ := os.ReadDir(r.bin)
	if len(entries) != 1 {
		t.Errorf("%d files in %s after an update, want only loadout", len(entries), r.bin)
	}
	r.leftovers(t)
}

func TestInstallScriptRefusesWhatDoesNotCheckOut(t *testing.T) {
	good := fakeCLI(t, "0.9.0")
	tests := []struct {
		name      string
		checksums func(archive string) string
		want      string
	}{
		{"wrong checksum", func(a string) string { return strings.Repeat("0", 64) + "  " + a + "\n" }, "does not match"},
		{"no line for this system", func(string) string { return checksumLine("loadout_plan9_arm64.tar.gz", good) }, "no line for"},
		{"empty checksums", func(string) string { return "" }, "no line for"},
		{"a name that only ends the same", func(a string) string { return checksumLine("other_"+a, good) }, "no line for"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := cliRelease(t, "0.9.0")
			archive := "loadout_" + hostOS(t) + "_" + hostArch(t) + ".tar.gz"
			files["checksums.txt"] = []byte(tc.checksums(archive))
			d := newDownloads(t, files)
			r := newInstallRun(t, d.URL)
			out, err := r.run(t)
			if err == nil {
				t.Fatalf("install.sh succeeded:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
			if r.installed() {
				t.Error("loadout was installed anyway")
			}
			r.leftovers(t)
		})
	}
}

func TestInstallScriptKeepsTheOldCLIWhenTheNewOneIsRefused(t *testing.T) {
	d := newDownloads(t, cliRelease(t, "0.9.0"))
	r := newInstallRun(t, d.URL)
	if out, err := r.run(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	files := cliRelease(t, "0.10.0")
	files["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  x\n")
	for name, body := range files {
		d.set(name, body)
	}
	if out, err := r.run(t); err == nil {
		t.Fatalf("second run succeeded:\n%s", out)
	}
	if got := r.installedVersion(t); got != "loadout 0.9.0" {
		t.Errorf("version after a refused update = %q, want loadout 0.9.0", got)
	}
}

func TestInstallScriptFailsPlainlyOnADownloadError(t *testing.T) {
	archive := func() string { return "loadout_" + hostOS(t) + "_" + hostArch(t) + ".tar.gz" }
	tests := []struct {
		name    string
		without func() string
	}{
		{"archive missing", archive},
		{"checksums missing", func() string { return "checksums.txt" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := cliRelease(t, "0.9.0")
			delete(files, tc.without())
			d := newDownloads(t, files)
			r := newInstallRun(t, d.URL)
			out, err := r.run(t)
			if err == nil || !strings.Contains(out, "cannot download "+d.URL+"/dl/"+tc.without()) {
				t.Errorf("want a failure that names the file it could not download, got %v:\n%s", err, out)
			}
			if r.installed() {
				t.Error("loadout was installed anyway")
			}
			r.leftovers(t)
		})
	}
}

// hostOS and hostArch are the names install.sh gives this machine.
func hostOS(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux":
		return runtime.GOOS
	}
	t.Skipf("install.sh does not support %s", runtime.GOOS)
	return ""
}

func hostArch(t *testing.T) string {
	t.Helper()
	switch runtime.GOARCH {
	case "arm64", "amd64":
		return runtime.GOARCH
	}
	t.Skipf("install.sh does not support %s", runtime.GOARCH)
	return ""
}

// fakeUname makes install.sh see another system.
func fakeUname(t *testing.T, system, arch string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; *) echo %s ;; esac\n", system, arch, system)
	if err := osWriteExecutable(filepath.Join(dir, "uname"), script); err != nil {
		t.Fatal(err)
	}
	return dir
}

func osWriteExecutable(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o755) //nolint:gosec // G306: a stand-in command must be executable
}

func TestInstallScriptPicksTheArchiveOfThisSystem(t *testing.T) {
	tests := []struct{ os, arch, want string }{
		{"Darwin", "arm64", "loadout_darwin_arm64.tar.gz"},
		{"Darwin", "x86_64", "loadout_darwin_amd64.tar.gz"},
		{"Linux", "x86_64", "loadout_linux_amd64.tar.gz"},
		{"Linux", "aarch64", "loadout_linux_arm64.tar.gz"},
		{"Linux", "arm64", "loadout_linux_arm64.tar.gz"},
		{"Linux", "amd64", "loadout_linux_amd64.tar.gz"},
	}
	for _, tc := range tests {
		t.Run(tc.os+"/"+tc.arch, func(t *testing.T) {
			d := newDownloads(t, cliRelease(t, "0.9.0"))
			r := newInstallRun(t, d.URL)
			r = r.with("PATH=" + fakeUname(t, tc.os, tc.arch) + string(os.PathListSeparator) + os.Getenv("PATH"))
			if out, err := r.run(t); err != nil {
				t.Fatalf("install.sh: %v\n%s", err, out)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if len(d.asked) == 0 || d.asked[0] != tc.want {
				t.Errorf("asked for %v first, want %s", d.asked, tc.want)
			}
		})
	}
}

func TestInstallScriptNamesWhatItDoesNotSupport(t *testing.T) {
	tests := []struct{ os, arch, want string }{
		{"FreeBSD", "amd64", "'FreeBSD'"},
		{"MINGW64_NT-10.0", "x86_64", "'MINGW64_NT-10.0'"},
		{"Linux", "riscv64", "'riscv64'"},
		{"Darwin", "ppc", "'ppc'"},
	}
	for _, tc := range tests {
		t.Run(tc.os+"/"+tc.arch, func(t *testing.T) {
			d := newDownloads(t, cliRelease(t, "0.9.0"))
			r := newInstallRun(t, d.URL)
			r = r.with("PATH=" + fakeUname(t, tc.os, tc.arch) + string(os.PathListSeparator) + os.Getenv("PATH"))
			out, err := r.run(t)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Errorf("want a failure naming %s, got %v:\n%s", tc.want, err, out)
			}
			if len(d.asked) != 0 || r.installed() {
				t.Errorf("it went on: asked %v, installed %v", d.asked, r.installed())
			}
		})
	}
}

func TestInstallScriptWantsToKnowItsRegistry(t *testing.T) {
	r := newInstallRun(t, "")
	r = r.with("LOADOUT_BASE_URL=")
	out, err := r.run(t)
	if err == nil || !strings.Contains(out, "does not know its registry") {
		t.Errorf("want an error that says it does not know its registry, got %v:\n%s", err, out)
	}
}

// TestInstallScriptHasNoCertificateHandling: the registry serves the CLI over
// ordinary TLS, so the script never turns the check off or fetches a CA.
func TestInstallScriptHasNoCertificateHandling(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{" -k ", "-fsSLk", "--insecure", "--cacert", "ca.crt", ".crt", "ca-file"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("install.sh contains %q", banned)
		}
	}
}
