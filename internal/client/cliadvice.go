package client

import (
	"net/http"

	"github.com/petervlad/loadout/internal/api"
)

// maxAdvice bounds each CLI header the client passes on; a version is a few
// bytes, and anything longer is not one.
const maxAdvice = 32

// CLIAdvice is what a registry said about loadout releases, as sent and
// not parsed: the receiver decides what it means. Empty means it said
// nothing.
type CLIAdvice struct{ Latest, Minimum string }

// WithCLIAdvice calls fn with the CLI advice of every response that
// carries any. fn must be safe for concurrent use and fast.
func WithCLIAdvice(fn func(CLIAdvice)) Option { return func(o *options) { o.advice = fn } }

// adviceOf reads the CLI headers of resp, each dropped when too long.
func adviceOf(resp *http.Response) (CLIAdvice, bool) {
	get := func(name string) string {
		if v := resp.Header.Get(name); len(v) <= maxAdvice {
			return v
		}
		return ""
	}
	a := CLIAdvice{Latest: get(api.HeaderCLILatest), Minimum: get(api.HeaderCLIMinimum)}
	return a, a != CLIAdvice{}
}
