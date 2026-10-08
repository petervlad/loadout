package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// DefaultPort is the mail submission port, where STARTTLS is expected.
const DefaultPort = 587

// defaultTimeout bounds one whole delivery.
const defaultTimeout = 30 * time.Second

// ErrNoStartTLS means the server did not offer STARTTLS, so the password
// would travel in plain text; SMTP refuses to send.
var ErrNoStartTLS = errors.New("smtp server does not offer STARTTLS; refusing to authenticate in plain text")

// SMTPConfig configures an SMTP mailer.
type SMTPConfig struct {
	Host string
	// Port defaults to DefaultPort.
	Port     int
	Username string
	// Password is a secret; it is never logged or put in an error.
	Password string
	// From is the sender, for example "loadout <login@example.com>".
	From string
	// TLS replaces the TLS settings (server name = Host, TLS 1.2 or later),
	// for tests with their own certificate authority.
	TLS *tls.Config
	// Timeout bounds one delivery; it defaults to 30 seconds.
	Timeout time.Duration
}

// SMTP sends mail through a submission server with STARTTLS and PLAIN
// authentication. It is safe for concurrent use; each Send is one
// connection.
type SMTP struct {
	addr     string
	host     string
	username string
	password string
	from     *mail.Address
	tls      *tls.Config
	timeout  time.Duration
}

// NewSMTP checks cfg and returns a mailer. It does not connect.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if cfg.Host == "" {
		return nil, errors.New("smtp: host is required")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp: port %d is out of range", cfg.Port)
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("smtp: set both the user and the password, or neither")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil || strings.ContainsAny(cfg.From, "\r\n") {
		return nil, fmt.Errorf("smtp: from address %q: want a name and address like \"loadout <login@example.com>\"", cfg.From)
	}
	tc := cfg.TLS
	if tc == nil {
		tc = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tc = tc.Clone()
	if tc.ServerName == "" {
		tc.ServerName = cfg.Host
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	return &SMTP{
		addr:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		host:     cfg.Host,
		username: cfg.Username,
		password: cfg.Password,
		from:     from,
		tls:      tc,
		timeout:  cfg.Timeout,
	}, nil
}

// Send delivers m. The connection must upgrade with STARTTLS before any
// credentials or content are sent.
func (s *SMTP) Send(ctx context.Context, m Message) (err error) {
	if err := m.Validate(); err != nil {
		return err
	}
	body, err := s.render(m, time.Now())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("smtp: connect to %s: %w", s.addr, err)
	}
	// Cancelling ctx closes the connection, which ends any blocked read;
	// the read then fails with the error Send reports.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return errors.Join(fmt.Errorf("smtp: %w", err), conn.Close())
		}
	}

	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return errors.Join(fmt.Errorf("smtp: greeting from %s: %w", s.addr, err), conn.Close())
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.Close())
		}
	}()
	if err := c.Hello(helloName(s.from.Address)); err != nil {
		return fmt.Errorf("smtp: hello: %w", err)
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return ErrNoStartTLS
	}
	if err := c.StartTLS(s.tls); err != nil {
		return fmt.Errorf("smtp: starttls: %w", err)
	}
	if s.username != "" {
		// PlainAuth itself also refuses to run without TLS.
		if err := c.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return fmt.Errorf("smtp: authenticate as %s: %w", s.username, err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp: sender: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp: recipient %s: %w", Redact(m.To), err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return errors.Join(fmt.Errorf("smtp: write message: %w", err), w.Close())
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: message not accepted: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp: quit: %w", err)
	}
	return nil
}

// helloName is the name sent with EHLO: the sender's domain.
func helloName(from string) string {
	if _, domain, ok := strings.Cut(from, "@"); ok && domain != "" {
		return domain
	}
	return "localhost"
}

// render builds the message: headers, then a plain text part and, if set,
// an HTML alternative, both quoted-printable UTF-8.
func (s *SMTP) render(m Message, now time.Time) ([]byte, error) {
	idRaw := make([]byte, 12)
	if _, err := rand.Read(idRaw); err != nil {
		return nil, fmt.Errorf("smtp: message id: %w", err)
	}
	var buf bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	h("From", s.from.String())
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", now.UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(idRaw)+"@"+helloName(s.from.Address)+">")
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")

	if m.HTML == "" {
		h("Content-Type", "text/plain; charset=utf-8")
		h("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, m.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	h("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	buf.WriteString("\r\n")
	for _, part := range []struct{ typ, body string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", m.HTML},
	} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, fmt.Errorf("smtp: build message: %w", err)
		}
		if err := writeQP(pw, part.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("smtp: build message: %w", err)
	}
	return buf.Bytes(), nil
}

func writeQP(w io.Writer, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qp, s); err != nil {
		return fmt.Errorf("smtp: encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("smtp: encode body: %w", err)
	}
	return nil
}
