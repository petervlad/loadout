package cliupdate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/semver"
)

const (
	office = "https://registry.example.com"
	lan    = "http://10.0.0.5:7420"
)

var day0 = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fixture is a service for build, over a state file in a temp folder.
type fixture struct {
	t     *testing.T
	path  string
	clock *fakeClock
	build buildinfo.Build
	// isQuiet runs the services with LOADOUT_UPDATE_NOTICE=off.
	isQuiet bool
	// method is how the binary was installed; the zero value is from source.
	method buildinfo.Method
}

func newFixture(t *testing.T, build string) *fixture {
	t.Helper()
	return &fixture{t: t, path: filepath.Join(t.TempDir(), "cliupdate.yaml"), clock: &fakeClock{t: day0}, build: buildinfo.Current(build, nil, false)}
}

func (f *fixture) installMethod() buildinfo.Method {
	if f.method.Name != "" {
		return f.method
	}
	return buildinfo.MethodOf(buildinfo.Install{Build: f.build})
}

// service is a new run of loadout: nothing heard yet.
func (f *fixture) service() *Service {
	return New(Options{Store: NewStore(f.path), Build: f.build, Method: f.installMethod(), Now: f.clock.now, Quiet: f.isQuiet})
}

// run is one loadout command run by a script that heard each advice, then
// finished.
func (f *fixture) run(heard map[string]client.CLIAdvice) {
	f.t.Helper()
	f.say(Script, heard)
}

// say is run for audience a, returning the line it printed.
func (f *fixture) say(a Audience, heard map[string]client.CLIAdvice) string {
	f.t.Helper()
	s := f.service()
	for u, adv := range heard {
		s.Saw(u, adv)
	}
	line, err := s.Finish(f.t.Context(), a)
	if err != nil {
		f.t.Fatal(err)
	}
	return line
}

func (f *fixture) file() string {
	f.t.Helper()
	data, _ := os.ReadFile(f.path)
	return string(data)
}

func TestFinishWritesOnlyNews(t *testing.T) {
	f := newFixture(t, "0.5.0") // up to date: no clock moves

	f.run(nil)
	if _, err := os.Stat(f.path); err == nil {
		t.Fatal("a run that heard nothing wrote the file")
	}
	f.run(map[string]client.CLIAdvice{office: {Latest: "0.5.0"}})
	first := f.file()
	if !strings.Contains(first, "latest: 0.5.0") || strings.Contains(first, "minimum") {
		t.Fatalf("file after the first advice:\n%s", first)
	}
	if fi, err := os.Stat(f.path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}

	// The same advice an hour later: nothing to write.
	f.clock.add(time.Hour)
	f.run(map[string]client.CLIAdvice{office: {Latest: "0.5.0"}})
	if got := f.file(); got != first {
		t.Errorf("the same advice rewrote the file:\n%s", got)
	}
	// A day later the record is refreshed.
	f.clock.add(24 * time.Hour)
	f.run(map[string]client.CLIAdvice{office: {Latest: "0.5.0"}})
	if got := f.file(); got == first {
		t.Error("a day-old record was not refreshed")
	}
	// New advice is written at once.
	second := f.file()
	f.run(map[string]client.CLIAdvice{office: {Latest: "0.5.0", Minimum: "0.3.0"}})
	if got := f.file(); got == second || !strings.Contains(got, "minimum: 0.3.0") {
		t.Errorf("a new minimum was not written:\n%s", got)
	}
}

func TestSawDropsWhatIsNotAVersion(t *testing.T) {
	f := newFixture(t, "0.4.0")
	f.run(map[string]client.CLIAdvice{office: {Latest: "lots of words", Minimum: "1.2"}})
	if _, err := os.Stat(f.path); err == nil {
		t.Errorf("advice without a version was stored:\n%s", f.file())
	}
	f.run(map[string]client.CLIAdvice{office: {Latest: "v0.5.0", Minimum: "<b>"}})
	if got := f.file(); !strings.Contains(got, "latest: 0.5.0") || strings.Contains(got, "minimum") || strings.Contains(got, "<b>") {
		t.Errorf("file:\n%s", got)
	}
}

func TestStatus(t *testing.T) {
	f := newFixture(t, "0.4.0")
	f.run(map[string]client.CLIAdvice{lan: {Latest: "0.4.0"}})
	f.clock.add(time.Hour)
	f.run(map[string]client.CLIAdvice{office: {Latest: "0.6.0", Minimum: "0.5.0"}})
	st := f.service().Status()
	if !st.Behind() || st.Latest.String() != "0.6.0" || st.From != "registry.example.com" || !st.Seen.Equal(day0.Add(time.Hour)) {
		t.Errorf("Status() = %+v, want 0.6.0 from registry.example.com, behind", st)
	}
	if len(st.TooOld) != 1 || st.TooOld[0].URL != office {
		t.Errorf("TooOld = %v, want the office registry", st.TooOld)
	}

	// 31 days later the office registry has not spoken again: forgotten.
	f.clock.add(31 * 24 * time.Hour)
	f.run(map[string]client.CLIAdvice{lan: {Latest: "0.4.0"}})
	st = f.service().Status()
	if st.Behind() || st.Latest.String() != "0.4.0" || len(st.TooOld) != 0 {
		t.Errorf("Status() after a month = %+v, want only the LAN registry's 0.4.0", st)
	}
	if strings.Contains(f.file(), office) {
		t.Errorf("an expired record was kept:\n%s", f.file())
	}
}

func TestStatusForBuildsThatAreNeverBehind(t *testing.T) {
	for _, build := range []string{"dev", "v0.4.0-dirty", "b42ddb9", "v0.6.0-3-gabc1234"} {
		f := newFixture(t, build)
		f.run(map[string]client.CLIAdvice{office: {Latest: "0.6.0", Minimum: "0.5.0"}})
		st := f.service().Status()
		if st.Behind() || len(st.TooOld) != 0 {
			t.Errorf("build %s: Status() = %+v, want neither behind nor too old", build, st)
		}
	}
}

func TestDamagedFileReadsEmptyAndIsRewritten(t *testing.T) {
	for name, content := range map[string]string{
		"not yaml":        "{{{",
		"unknown field":   "version: 1\nsurprise: true\n",
		"another version": "version: 9\n",
		"bad version":     "version: 1\nregistries:\n  - url: x\n    latest: banana\n    seen: 2026-10-04T09:00:00Z\n",
		"bad audience":    "version: 1\ntold:\n  robot: {version: 0.5.0, at: 2026-10-04T09:00:00Z}\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "0.4.0")
			if err := os.WriteFile(f.path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(f.path).Load(); err == nil || !strings.Contains(err.Error(), "remove it") {
				t.Errorf("Load() error = %v, want damaged with the remedy", err)
			}
			if st := f.service().Status(); st.Behind() || !st.Checked.IsZero() {
				t.Errorf("Status() of a damaged file = %+v, want empty", st)
			}
			f.run(map[string]client.CLIAdvice{office: {Latest: "0.5.0"}})
			if _, err := NewStore(f.path).Load(); err != nil {
				t.Errorf("after a run the file is still damaged: %v", err)
			}
		})
	}
}

func TestConcurrentRunsBothLand(t *testing.T) {
	f := newFixture(t, "0.4.0")
	var wg sync.WaitGroup
	for _, u := range []string{office, lan} {
		wg.Go(func() {
			s := f.service()
			s.Saw(u, client.CLIAdvice{Latest: "0.5.0"})
			if _, err := s.Finish(t.Context(), Script); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	st, err := NewStore(f.path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(st.Registries()); got != 2 {
		t.Errorf("registries after two concurrent runs = %d, want 2:\n%s", got, f.file())
	}
}

func TestAtMostSixteenRegistries(t *testing.T) {
	f := newFixture(t, "0.4.0")
	for i := range 20 {
		f.clock.add(time.Minute)
		f.run(map[string]client.CLIAdvice{"https://r" + string(rune('a'+i)) + ".example": {Latest: "0.5.0"}})
	}
	st, err := NewStore(f.path).Load()
	if err != nil {
		t.Fatal(err)
	}
	regs := st.Registries()
	if len(regs) != maxRegistries || regs[0].URL != "https://rt.example" {
		t.Errorf("kept %d registries, newest %q; want 16, the newest first", len(regs), regs[0].URL)
	}
}

func TestStoreKeepsTheClocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cliupdate.yaml")
	s := NewStore(path)
	v := semver.New(0, 5, 0)
	if err := s.Update(t.Context(), func(st *State) error {
		st.tell(clockAgent, Told{Version: v, At: day0})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := st.lastTold(clockAgent); !ok || got.Version != v || !got.At.Equal(day0) {
		t.Errorf("lastTold(agent) = %v, %v", got, ok)
	}
	if _, ok := st.lastTold(clockPerson); ok {
		t.Error("the person's clock was set too")
	}
}
