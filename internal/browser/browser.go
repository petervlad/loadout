// Package browser opens a web page in the person's default browser, for
// `loadout login`.
package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"runtime"
)

// Opener opens pages with the operating system's own command.
type Opener struct {
	log *slog.Logger
}

// New returns an opener.
func New(log *slog.Logger) *Opener { return &Opener{log: log} }

// Open opens rawURL, which must be an http or https URL, so nothing else
// ever reaches the command line. It does not wait for the browser.
func (o *Opener) Open(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("open browser: %q is not a web address", rawURL)
	}
	name, args := "xdg-open", []string{u.String()}
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", u.String()}
	}
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: a fixed command; the argument is a checked web address
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	// Some openers stay attached to the browser, so nobody waits for them;
	// this goroutine only reaps the process and ends with it.
	go func() {
		if err := cmd.Wait(); err != nil {
			o.log.Debug("browser opener exited", "err", err)
		}
	}()
	return nil
}
