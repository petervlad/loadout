package buildinfo

import (
	"runtime/debug"
	"testing"

	"github.com/petervlad/loadout/internal/semver"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in       string
		release  string // "" for unknown
		after    bool
		modified bool
		str      string
	}{
		{in: "0.4.0", release: "0.4.0", str: "0.4.0"},
		{in: "v0.4.0", release: "0.4.0", str: "0.4.0"},
		{in: "v0.4.0-3-gabc1234", release: "0.4.0", after: true, str: "after 0.4.0 (from abc1234)"},
		{in: "v0.4.0-3-gabc1234-dirty", release: "0.4.0", after: true, modified: true, str: "after 0.4.0 (from abc1234, modified)"},
		{in: "v0.4.0+dirty", release: "0.4.0", modified: true, str: "0.4.0 (modified)"},
		{in: "v0.4.1-0.20261004192155-b42ddb9b6aba", release: "0.4.0", after: true, str: "after 0.4.0 (built 4 Oct 2026 from b42ddb9)"},
		{in: "v0.4.1-0.20261004192155-b42ddb9b6aba+dirty", release: "0.4.0", after: true, modified: true, str: "after 0.4.0 (built 4 Oct 2026 from b42ddb9, modified)"},
		{in: "v0.0.0-20261004192155-b42ddb9b6aba", str: "dev (built 4 Oct 2026 from b42ddb9)"},
		{in: "v0.0.1-0.20261004192155-b42ddb9b6aba", str: "dev (built 4 Oct 2026 from b42ddb9)"},
		{in: "v1.0.0-rc.1", str: "dev"},
		{in: "v1.0.1-rc.1.0.20261004192155-b42ddb9b6aba", str: "dev"},
		{in: "1.4.0+meta", release: "1.4.0", str: "1.4.0"},
		{in: "v1.2.3+build", release: "1.2.3", str: "1.2.3"},
		{in: "dev", str: "dev"},
		{in: "(devel)", str: "dev"},
		{in: "", str: "dev"},
		{in: "b42ddb9", str: "dev (from b42ddb9)"},
		{in: "b42ddb9-dirty", modified: true, str: "dev (from b42ddb9, modified)"},
		{in: "0.0.0", str: "dev"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			b := Parse(tc.in)
			v, ok := b.Release()
			if ok != (tc.release != "") {
				t.Fatalf("Release() known = %v, want %v", ok, tc.release != "")
			}
			if ok && v.String() != tc.release {
				t.Errorf("Release() = %s, want %s", v, tc.release)
			}
			if b.after != tc.after || b.Modified() != tc.modified {
				t.Errorf("after, modified = %v, %v; want %v, %v", b.after, b.Modified(), tc.after, tc.modified)
			}
			if got := b.String(); got != tc.str {
				t.Errorf("String() = %q, want %q", got, tc.str)
			}
		})
	}
}

func TestCurrent(t *testing.T) {
	info := func(version string, settings ...string) *debug.BuildInfo {
		bi := &debug.BuildInfo{Main: debug.Module{Path: "github.com/petervlad/loadout", Version: version}}
		for i := 0; i+1 < len(settings); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}
		return bi
	}
	vcs := []string{"vcs.revision", "b42ddb9b6aba0000", "vcs.time", "2026-10-04T19:21:55Z", "vcs.modified", "false"}
	dirty := []string{"vcs.revision", "b42ddb9b6aba0000", "vcs.time", "2026-10-04T19:21:55Z", "vcs.modified", "true"}
	tests := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		str     string
		raw     string
	}{
		{name: "release ldflags", ldflags: "0.4.0", info: info("(devel)"), ok: true, str: "0.4.0", raw: "0.4.0"},
		{name: "describe ldflags", ldflags: "v0.4.0-3-gabc1234", ok: false, str: "after 0.4.0 (from abc1234)", raw: "v0.4.0-3-gabc1234"},
		{name: "go install on a tag", ldflags: "dev", info: info("v0.4.0", vcs...), ok: true, str: "0.4.0 (built 4 Oct 2026 from b42ddb9)", raw: "v0.4.0"},
		{name: "go install after a tag", ldflags: "dev", info: info("v0.4.1-0.20261004192155-b42ddb9b6aba", vcs...), ok: true, str: "after 0.4.0 (built 4 Oct 2026 from b42ddb9)", raw: "v0.4.1-0.20261004192155-b42ddb9b6aba"},
		{name: "go install, no tag", ldflags: "dev", info: info("v0.0.0-20261004192155-b42ddb9b6aba", vcs...), ok: true, str: "dev (built 4 Oct 2026 from b42ddb9)", raw: "v0.0.0-20261004192155-b42ddb9b6aba"},
		{name: "go install, modified", ldflags: "dev", info: info("v0.4.1-0.20261004192155-b42ddb9b6aba+dirty", dirty...), ok: true, str: "after 0.4.0 (built 4 Oct 2026 from b42ddb9, modified)", raw: "v0.4.1-0.20261004192155-b42ddb9b6aba+dirty"},
		{name: "go run", ldflags: "dev", info: info("(devel)", dirty...), ok: true, str: "dev (built 4 Oct 2026 from b42ddb9, modified)", raw: "b42ddb9-dirty"},
		{name: "go test", ldflags: "dev", info: info("(devel)"), ok: true, str: "dev", raw: "dev"},
		{name: "no build info", ldflags: "dev", str: "dev", raw: "dev"},
		{name: "hash ldflags, no build info", ldflags: "b42ddb9", str: "dev (from b42ddb9)", raw: "b42ddb9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := Current(tc.ldflags, tc.info, tc.ok)
			if got := b.String(); got != tc.str {
				t.Errorf("String() = %q, want %q", got, tc.str)
			}
			if got := b.Raw(); got != tc.raw {
				t.Errorf("Raw() = %q, want %q", got, tc.raw)
			}
		})
	}
}

func TestBehind(t *testing.T) {
	older, same, newer := semver.New(0, 3, 0), semver.New(0, 4, 0), semver.New(0, 5, 0)
	tests := []struct {
		build              string
		older, same, newer bool
	}{
		{build: "0.4.0", newer: true},
		{build: "v0.4.0-3-gabc1234", newer: true},
		{build: "v0.4.1-0.20261004192155-b42ddb9b6aba", newer: true},
		{build: "v0.4.0-3-gabc1234-dirty"},
		{build: "v0.4.0+dirty"},
		{build: "dev"},
		{build: "v0.0.0-20261004192155-b42ddb9b6aba"},
	}
	for _, tc := range tests {
		b := Parse(tc.build)
		for _, c := range []struct {
			v    semver.Version
			want bool
		}{{older, tc.older}, {same, tc.same}, {newer, tc.newer}} {
			if got := b.Behind(c.v); got != c.want {
				t.Errorf("Parse(%q).Behind(%s) = %v, want %v", tc.build, c.v, got, c.want)
			}
		}
	}
	if Parse("v1.2.3+build").Exact() || !Parse("v1.2.3").Exact() {
		t.Error("a tag with build metadata is exact, or a clean tag is not")
	}
	if (Build{}).Behind(newer) {
		t.Error("the zero Build is behind a release, want never")
	}
}

func TestMethodOf(t *testing.T) {
	release := Current("0.4.0", nil, false)
	source := Current("dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.4.0"}}, true)
	tests := []struct {
		name string
		in   Install
		want string
	}{
		{"caskroom", Install{Build: release, Executable: "/opt/homebrew/Caskroom/loadout/0.4.0/loadout", Packaged: true}, "homebrew"},
		{"cellar", Install{Build: source, Executable: "/usr/local/Cellar/loadout/0.4.0/bin/loadout"}, "homebrew"},
		{"release elsewhere", Install{Build: release, Executable: "/usr/local/bin/loadout", Packaged: true}, "release"},
		{"make build of a tag", Install{Build: release, Executable: "/tmp/bin/loadout"}, "source"},
		{"go install on a tag", Install{Build: source, Executable: "/Users/a/go/bin/loadout"}, "source"},
		{"packaged after a tag", Install{Build: Current("v0.4.0-3-gabc1234", nil, false), Packaged: true}, "source"},
		{"packaged and modified", Install{Build: Current("v0.4.0-dirty", nil, false), Packaged: true}, "source"},
		{"registry download", Install{Build: release, Executable: "/Users/a/.local/bin/loadout", Channel: ChannelRegistry}, "registry"},
		{"registry download of a build after a tag", Install{Build: Current("v0.4.0-3-gabc1234", nil, false), Channel: ChannelRegistry}, "registry"},
		{"registry download under homebrew's path", Install{Build: release, Executable: "/opt/homebrew/Cellar/loadout/bin/loadout", Channel: ChannelRegistry}, "registry"},
		{"unknown channel", Install{Build: source, Channel: "usb"}, "source"},
	}
	for _, tc := range tests {
		if got := MethodOf(tc.in).Name; got != tc.want {
			t.Errorf("%s: MethodOf = %s, want %s", tc.name, got, tc.want)
		}
	}
	if got, want := MethodOf(Install{Build: source}).Update(), "To update, in your loadout folder: git pull && go install ./cmd/loadout"; got != want {
		t.Errorf("source Update() = %q, want %q", got, want)
	}
	if got, want := MethodOf(Install{Executable: "/opt/homebrew/Caskroom/loadout/0.4.0/loadout"}).Update(), "To update: brew upgrade --cask loadout"; got != want {
		t.Errorf("homebrew Update() = %q, want %q", got, want)
	}
}

func TestMethodForRegistry(t *testing.T) {
	registry := MethodOf(Install{Channel: ChannelRegistry})
	tests := []struct {
		name, registry, want string
	}{
		{"an ip address", "https://203.0.113.7", "To update: curl -fsSL https://203.0.113.7/install.sh | sh"},
		{"a trailing slash and a path", "https://reg.example.com/some/path/", "To update: curl -fsSL https://reg.example.com/install.sh | sh"},
		{"a port", "http://10.0.0.5:7420", "To update: curl -fsSL http://10.0.0.5:7420/install.sh | sh"},
		{"no registry known", "", "To update: curl -fsSL <the address of your registry>/install.sh | sh"},
		{"not a web address", "file:///etc/passwd", "To update: curl -fsSL <the address of your registry>/install.sh | sh"},
		{"shell characters", "https://x.example;rm -rf ~", "To update: curl -fsSL <the address of your registry>/install.sh | sh"},
	}
	for _, tc := range tests {
		m := registry.For(tc.registry)
		if got := m.Update(); got != tc.want {
			t.Errorf("%s: Update() = %q, want %q", tc.name, got, tc.want)
		}
		if m.InFolder {
			t.Errorf("%s: the registry route runs in no loadout folder", tc.name)
		}
	}
	// The other methods have no registry in them and stay as they are.
	source := MethodOf(Install{})
	if got := source.For("https://203.0.113.7"); got != source {
		t.Errorf("source.For changed the method: %+v", got)
	}
}
