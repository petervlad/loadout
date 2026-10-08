// Package mail sends the registry's login emails: codes and links that
// confirm an address or finish a sign-in.
//
// SMTP sends through a submission server (port 587) and refuses to
// authenticate unless the connection was upgraded with STARTTLS. Log only
// logs that a mail was sent, for development. Queue sends in the background
// so a request never waits for, or reveals, how long a mail server takes.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Message is one email. Text is required; HTML is an optional alternative
// body.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// ErrInvalid means a message or address cannot be sent as given.
var ErrInvalid = errors.New("invalid message")

// Validate checks that the message has one plain recipient address, a
// subject without line breaks (which would let a value add headers) and a
// text body.
func (m Message) Validate() error {
	if _, err := parseAddress(m.To); err != nil {
		return fmt.Errorf("%w: recipient: %w", ErrInvalid, err)
	}
	if m.Subject == "" || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: subject must be one non-empty line", ErrInvalid)
	}
	if m.Text == "" {
		return fmt.Errorf("%w: text body is empty", ErrInvalid)
	}
	return nil
}

// parseAddress accepts a bare address such as a@example.com.
func parseAddress(s string) (*mail.Address, error) {
	if strings.ContainsAny(s, "\r\n<>\",") {
		return nil, fmt.Errorf("address %q: want a bare address", Redact(s))
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return nil, fmt.Errorf("address %q: want a bare address", Redact(s))
	}
	return a, nil
}

// Redact shortens an address for logs: "alice@example.com" becomes
// "a***@example.com".
func Redact(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" {
		return "***"
	}
	r, _ := utf8.DecodeRuneInString(local)
	return string(r) + "***@" + domain
}

// Log is a mailer for development: it logs that a mail was sent, never its
// content, and delivers nothing.
type Log struct {
	log *slog.Logger
}

// NewLog returns a mailer that only logs.
func NewLog(log *slog.Logger) *Log { return &Log{log: log} }

// Send implements the mailer interface of the account service.
func (l *Log) Send(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	l.log.InfoContext(ctx, "sent login email", "to", Redact(m.To))
	return nil
}
