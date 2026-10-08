package server

import (
	"net/http"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/semver"
)

// MinimumCLI is the oldest loadout CLI release this server still supports;
// zero means any. Raise it in the commit that drops a fallback an older CLI
// needs (such as the client's retry without fields an older registry
// refuses), so the code that knows what it supports also says it. It only
// warns: the server never refuses an older CLI.
func MinimumCLI() semver.Version { return semver.Version{} }

// WithCLI makes every response to a request with a valid token say which
// loadout release is the latest (the one this registry was built from) and
// which is the oldest still supported. A zero version is not sent.
func WithCLI(latest, minimum semver.Version) Option {
	return func(s *server) { s.cliLatest, s.cliMinimum = latest, minimum }
}

// adviseCLI sets the CLI headers. Called only once a token checked out, so
// anonymous scanners do not learn what runs here.
func (s *server) adviseCLI(w http.ResponseWriter) {
	h := w.Header()
	if s.cliLatest != (semver.Version{}) {
		h.Set(api.HeaderCLILatest, s.cliLatest.String())
	}
	if s.cliMinimum != (semver.Version{}) {
		h.Set(api.HeaderCLIMinimum, s.cliMinimum.String())
	}
}
