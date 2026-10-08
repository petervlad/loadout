package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSMTP is a minimal submission server: EHLO, optional STARTTLS, AUTH
// PLAIN, MAIL, RCPT, DATA, QUIT. It records what it saw.
type fakeSMTP struct {
	ln       net.Listener
	tls      *tls.Config
	startTLS bool // offer STARTTLS
	done     chan struct{}

	// Written by the server goroutine, read after done is closed.
	authBeforeTLS bool
	authed        string
	rcpt          string
	data          string
	err           error
}

// newFakeSMTP starts a server with the certificate of an httptest TLS
// server (valid for 127.0.0.1) and returns it with a pool that trusts it.
func newFakeSMTP(t *testing.T, startTLS bool) (*fakeSMTP, *x509.CertPool) {
	t.Helper()
	certSrv := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certSrv.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(certSrv.Certificate())
	certSrv.Close()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, tls: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, startTLS: startTLS, done: make(chan struct{})}
	go f.serve(t.Context())
	t.Cleanup(f.stop)
	return f, pool
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

// stop closes the listener, which may be closed already, and waits for the
// server goroutine.
func (f *fakeSMTP) stop() {
	_ = f.ln.Close() // the test may have closed it to wait for the server
	<-f.done
}

func (f *fakeSMTP) serve(ctx context.Context) {
	defer close(f.done)
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		f.err = err
		return
	}
	tp := textproto.NewConn(conn)
	// A failed write shows up as a client error in the test.
	reply := func(s string) { _ = tp.PrintfLine("%s", s) }
	reply("220 fake ESMTP")
	secure := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			if f.startTLS && !secure {
				reply("250-fake")
				reply("250 STARTTLS")
			} else {
				reply("250-fake")
				reply("250 AUTH PLAIN")
			}
		case "STARTTLS":
			reply("220 go ahead")
			tc := tls.Server(conn, f.tls)
			if err := tc.HandshakeContext(ctx); err != nil {
				f.err = err
				return
			}
			conn = tc
			tp = textproto.NewConn(tc)
			secure = true
		case "AUTH":
			f.authBeforeTLS = !secure
			_, b64, _ := strings.Cut(arg, " ")
			raw, _ := base64.StdEncoding.DecodeString(b64) // garbage fails the test below
			f.authed = strings.ReplaceAll(string(raw), "\x00", "|")
			reply("235 ok")
		case "MAIL":
			reply("250 ok")
		case "RCPT":
			f.rcpt = strings.Trim(strings.TrimPrefix(arg, "TO:"), "<>")
			reply("250 ok")
		case "DATA":
			reply("354 go ahead")
			lines, err := tp.ReadDotLines()
			if err != nil {
				f.err = err
				return
			}
			f.data = strings.Join(lines, "\n")
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

func newTestSMTP(t *testing.T, f *fakeSMTP, pool *x509.CertPool) *SMTP {
	t.Helper()
	s, err := NewSMTP(SMTPConfig{
		Host:     "127.0.0.1",
		Port:     f.port(),
		Username: "login-user",
		Password: "smtp-secret",
		From:     "loadout <login@example.com>",
		TLS:      &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSMTPSendsOverStartTLS(t *testing.T) {
	f, pool := newFakeSMTP(t, true)
	s := newTestSMTP(t, f, pool)
	msg := Message{To: "alice@example.com", Subject: "Your loadout code: 123456", Text: "Code: 123456\n", HTML: "<p>Code: <b>123456</b></p>"}
	if err := s.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	f.stop()
	if f.err != nil {
		t.Fatalf("server error: %v", f.err)
	}
	if f.authBeforeTLS {
		t.Error("the client authenticated before STARTTLS")
	}
	if f.authed != "|login-user|smtp-secret" {
		t.Errorf("AUTH PLAIN = %q, want the configured user and password", f.authed)
	}
	if f.rcpt != "alice@example.com" {
		t.Errorf("RCPT = %q, want alice@example.com", f.rcpt)
	}
	for _, want := range []string{
		"From: \"loadout\" <login@example.com>",
		"To: alice@example.com",
		"Subject: Your loadout code: 123456",
		"multipart/alternative",
		"text/plain; charset=utf-8",
		"text/html; charset=utf-8",
		"Code: 123456",
	} {
		if !strings.Contains(f.data, want) {
			t.Errorf("message is missing %q:\n%s", want, f.data)
		}
	}
}

func TestSMTPRefusesPlaintext(t *testing.T) {
	f, pool := newFakeSMTP(t, false)
	s := newTestSMTP(t, f, pool)
	err := s.Send(t.Context(), Message{To: "alice@example.com", Subject: "s", Text: "t"})
	if !errors.Is(err, ErrNoStartTLS) {
		t.Fatalf("Send() without STARTTLS error = %v, want ErrNoStartTLS", err)
	}
	f.stop()
	if f.authed != "" {
		t.Error("credentials were sent over a plain connection")
	}
	if strings.Contains(err.Error(), "smtp-secret") {
		t.Error("the error contains the password")
	}
}

func TestSMTPRejectsUntrustedCertificate(t *testing.T) {
	f, _ := newFakeSMTP(t, true)
	s := newTestSMTP(t, f, x509.NewCertPool())
	if err := s.Send(t.Context(), Message{To: "alice@example.com", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("Send() to a server with an untrusted certificate succeeded")
	}
}

func TestNewSMTPValidates(t *testing.T) {
	good := SMTPConfig{Host: "smtp.example.com", Username: "u", Password: "s3cret-smtp-key", From: "loadout <login@example.com>"} //nolint:gosec // G101: a test value
	s, err := NewSMTP(good)
	if err != nil {
		t.Fatal(err)
	}
	if s.addr != "smtp.example.com:"+strconv.Itoa(DefaultPort) {
		t.Errorf("addr = %s, want port %d by default", s.addr, DefaultPort)
	}
	for name, mod := range map[string]func(*SMTPConfig){
		"no host":        func(c *SMTPConfig) { c.Host = "" },
		"bad port":       func(c *SMTPConfig) { c.Port = 70000 },
		"user only":      func(c *SMTPConfig) { c.Password = "" },
		"bad from":       func(c *SMTPConfig) { c.From = "not an address" },
		"header in from": func(c *SMTPConfig) { c.From = "a <a@b.c>\r\nBcc: x@y.z" },
		"empty from":     func(c *SMTPConfig) { c.From = "" },
		"negative port":  func(c *SMTPConfig) { c.Port = -1 },
	} {
		c := good
		mod(&c)
		_, err := NewSMTP(c)
		if err == nil {
			t.Errorf("NewSMTP(%s) succeeded, want an error", name)
			continue
		}
		if strings.Contains(err.Error(), c.Password) && c.Password != "" {
			t.Errorf("NewSMTP(%s) error contains the password", name)
		}
	}
}

func TestMessageValidate(t *testing.T) {
	ok := Message{To: "a@example.com", Subject: "hi", Text: "body"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Message{
		"header injection in subject": {To: "a@example.com", Subject: "hi\r\nBcc: x@y.z", Text: "b"},
		"named recipient":             {To: "Eve <a@example.com>", Subject: "hi", Text: "b"},
		"two recipients":              {To: "a@example.com, b@example.com", Subject: "hi", Text: "b"},
		"no body":                     {To: "a@example.com", Subject: "hi"},
	} {
		if err := m.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%s) = %v, want ErrInvalid", name, err)
		}
	}
}

// slowMailer blocks until released, to show that the queue does not.
type slowMailer struct {
	release chan struct{}
	got     chan Message
}

func (s *slowMailer) Send(ctx context.Context, m Message) error {
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.got <- m
	return nil
}

func TestQueueSendsInTheBackground(t *testing.T) {
	sm := &slowMailer{release: make(chan struct{}), got: make(chan Message, 1)}
	q := NewQueue(sm, slog.New(slog.DiscardHandler))
	msg := Message{To: "a@example.com", Subject: "s", Text: "t"}
	if err := q.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send() error: %v", err) // it returned while the mailer blocks
	}
	close(sm.release)
	select {
	case got := <-sm.got:
		if got.To != msg.To {
			t.Errorf("delivered %+v, want %+v", got, msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("queued mail was not delivered")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Send(t.Context(), msg); !errors.Is(err, ErrClosed) {
		t.Errorf("Send() after Close error = %v, want ErrClosed", err)
	}
}

// failFirst fails its first mail and sends the rest.
type failFirst struct{ n int }

func (f *failFirst) Send(context.Context, Message) error {
	f.n++
	if f.n == 1 {
		return errors.New("relay down")
	}
	return nil
}

func TestQueueStats(t *testing.T) {
	q := NewQueue(&failFirst{}, slog.New(slog.DiscardHandler))
	if got := q.Stats(); got != (QueueStats{}) {
		t.Errorf("Stats() of a new queue = %+v, want zeros", got)
	}
	msg := Message{To: "a@example.com", Subject: "s", Text: "t"}
	for range 3 {
		if err := q.Send(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Close(); err != nil { // drains the queue
		t.Fatal(err)
	}
	got := q.Stats()
	if got.Waiting != 0 || got.Sent != 2 || got.Failed != 1 || got.LastFailure.IsZero() {
		t.Errorf("Stats() = %+v, want 2 sent, 1 failed with its time", got)
	}
}

func TestLogMailerRedacts(t *testing.T) {
	var buf strings.Builder
	l := NewLog(slog.New(slog.NewTextHandler(&buf, nil)))
	if err := l.Send(t.Context(), Message{To: "alice@example.com", Subject: "code 123456", Text: "123456"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "a***@example.com") || strings.Contains(out, "alice@") || strings.Contains(out, "123456") {
		t.Errorf("log = %q, want only the redacted address", out)
	}
}
