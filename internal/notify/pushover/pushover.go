// Package pushover sends update notifications to phones and desktops through
// Pushover (https://pushover.net/api).
package pushover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/update"
)

// DefaultEndpoint is Pushover's message API.
const DefaultEndpoint = "https://api.pushover.net/1/messages.json"

// Notifier sends one Pushover message per notification.
type Notifier struct {
	appToken string
	userKey  string
	endpoint string
	http     *http.Client
}

var _ update.Notifier = (*Notifier)(nil)

// Option configures a Notifier.
type Option func(*Notifier)

// WithEndpoint overrides the API URL, for tests.
func WithEndpoint(u string) Option { return func(n *Notifier) { n.endpoint = u } }

// New returns a notifier for the given application token and user (or
// group) key.
func New(appToken, userKey string, opts ...Option) (*Notifier, error) {
	if appToken == "" || userKey == "" {
		return nil, errors.New("pushover needs both an application token and a user key")
	}
	n := &Notifier{
		appToken: appToken,
		userKey:  userKey,
		endpoint: DefaultEndpoint,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(n)
	}
	return n, nil
}

type apiResponse struct {
	Status int      `json:"status"`
	Errors []string `json:"errors"`
}

// text is the title and message for x. A held-back update gives a count of
// the edited files, never their names: paths stay on this machine.
func text(x update.Notification) (title, message string) {
	held := len(x.Held)
	switch {
	case x.ModelsChanged && held > 0:
		return fmt.Sprintf("%s: model settings held back", x.ID),
			fmt.Sprintf("Your team changed which model %s runs, and you edited %s, so loadout kept yours. Run loadout status.", x.ID, files(held))
	case x.ModelsChanged:
		return fmt.Sprintf("%s: model settings changed", x.ID),
			fmt.Sprintf("Your team changed which model %s runs. Apply with: loadout install %s (remote %s)", x.ID, x.ID, x.Remote)
	case held > 0:
		return fmt.Sprintf("%s %s held back", x.ID, x.Latest),
			fmt.Sprintf("You edited %s, so loadout kept %s. Keep your edits and update with: loadout install %s --merge (remote %s). Run loadout status for the other choices.", files(held), x.Current, x.ID, x.Remote)
	}
	return fmt.Sprintf("%s %s available", x.ID, x.Latest),
		fmt.Sprintf("You have %s. Update with: loadout install %s (remote %s)", x.Current, x.ID, x.Remote)
}

func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// Notify implements update.Notifier.
func (n *Notifier) Notify(ctx context.Context, x update.Notification) error {
	title, message := text(x)
	return n.Push(ctx, title, message)
}

// Push sends one message with title.
func (n *Notifier) Push(ctx context.Context, title, message string) error {
	form := url.Values{
		"token":   {n.appToken},
		"user":    {n.userKey},
		"title":   {title},
		"message": {message},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("pushover: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.http.Do(req)
	if err != nil {
		// Do's error contains the URL, never the form, so no secret leaks.
		return fmt.Errorf("pushover: %w", err)
	}
	defer resp.Body.Close()

	var body apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return fmt.Errorf("pushover: status %d, unreadable response: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.Status != 1 {
		return fmt.Errorf("pushover: status %d: %s", resp.StatusCode, strings.Join(body.Errors, "; "))
	}
	return nil
}
