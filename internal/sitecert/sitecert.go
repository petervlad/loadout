// Package sitecert checks the certificate the registry serves on its public
// address: when it ends, who issued it, and whether this machine's own trust
// store accepts the chain. It makes one TLS handshake to a loopback address
// and never sends a request. It carries no certificate authority of its own.
package sitecert

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	probeLimit = 2 * time.Second
	okFor      = 10 * time.Minute
	failedFor  = time.Minute

	// LeafWarn is how little time left turns the served certificate into a
	// warning. Automatic renewals start well before this, so a certificate
	// that gets here has not been renewed.
	LeafWarn = 7 * 24 * time.Hour
)

// Config says what to check.
type Config struct {
	Addr string // host:port to dial, must be loopback
	Host string // the site's host from LOADOUT_PUBLIC_URL, checked against the certificate
	// Roots are the trusted roots. Nil means the system's own, which is
	// what production uses. Tests pass their own pool.
	Roots *x509.CertPool
	Now   func() time.Time // nil means time.Now
}

// Served is what the site hands to a client.
type Served struct {
	Names         []string // IP and DNS names the certificate is valid for
	NotBefore     time.Time
	NotAfter      time.Time
	Issuer        string
	Fingerprint   string // SHA-256 of the certificate, as `openssl x509 -fingerprint -sha256` prints it
	Expired       bool
	Expiring      bool // under LeafWarn left
	Intermediate  bool // the server sent an intermediate
	InterNotAfter time.Time
	// Trusted is true when the chain leads to a root in the system's trust
	// store and the certificate is valid for the site's host. TrustProblem
	// says why not, in a sentence.
	Trusted      bool
	TrustProblem string
	Err          string // empty when the handshake worked
}

// Report is what the checker found.
type Report struct {
	Served Served
}

// Checker runs the check and keeps the answer for a while.
type Checker struct {
	cfg Config

	mu      sync.Mutex
	chain   []*x509.Certificate
	chainAt time.Time
	chainEr string
	fetched bool
}

// New refuses an Addr that is not a loopback address, so the checker can never
// be pointed at another host.
func New(cfg Config) (*Checker, error) {
	if cfg.Host == "" {
		return nil, errors.New("sitecert: the site host is empty, set LOADOUT_PUBLIC_URL")
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("sitecert: address %q is not host:port: %w", cfg.Addr, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("sitecert: address %q is not a loopback address, so it is refused", cfg.Addr)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Checker{cfg: cfg}, nil
}

// Check returns the current report. The served chain is fetched at most every
// 10 minutes (1 minute after a failure).
func (c *Checker) Check(ctx context.Context) Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Now()
	c.refreshChain(ctx, now)
	return Report{Served: c.served(now)}
}

// Fingerprint is the SHA-256 of the DER form, in the upper-case colon form
// that openssl prints.
func Fingerprint(crt *x509.Certificate) string {
	sum := sha256.Sum256(crt.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// reason keeps the path and address out of the page and says only what went wrong.
func reason(err error) string {
	if oe, ok := errors.AsType[*net.OpError](err); ok && oe.Err != nil {
		err = oe.Err
	}
	return err.Error()
}

func (c *Checker) refreshChain(ctx context.Context, now time.Time) {
	age := now.Sub(c.chainAt)
	ttl := okFor
	if c.chainEr != "" {
		ttl = failedFor
	}
	if c.fetched && age >= 0 && age < ttl {
		return
	}
	c.chain, c.chainEr = nil, ""
	chain, err := handshake(ctx, c.cfg.Addr, c.cfg.Host)
	if err != nil {
		c.chainEr = "the served certificate could not be read: " + reason(err)
	} else {
		c.chain = chain
	}
	c.chainAt, c.fetched = now, true
}

// handshake reads the chain the server offers. Go's own verification is off
// only so the chain can be read, since the caller verifies it against the
// trust store and says why when it fails.
func handshake(ctx context.Context, addr, host string) ([]*x509.Certificate, error) {
	// The probe outlives a page load that gives up, so its answer is cached
	// for the next one instead of a "context canceled" failure.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeLimit)
	defer cancel()
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // the chain is verified against the trust store in trust
	// A site with a name picks its certificate by that name.
	if _, err := netip.ParseAddr(host); err != nil {
		cfg.ServerName = host
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: probeLimit}, Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return nil, errors.New("not a TLS connection")
	}
	chain := tc.ConnectionState().PeerCertificates
	if len(chain) == 0 {
		return nil, errors.New("the server sent no certificate")
	}
	return chain, nil
}

func (c *Checker) served(now time.Time) Served {
	if c.chainEr != "" {
		return Served{Err: c.chainEr}
	}
	leaf := c.chain[0]
	s := Served{
		NotBefore:   leaf.NotBefore,
		NotAfter:    leaf.NotAfter,
		Issuer:      leaf.Issuer.CommonName,
		Fingerprint: Fingerprint(leaf),
		Expired:     !now.Before(leaf.NotAfter),
		Expiring:    leaf.NotAfter.Sub(now) < LeafWarn,
	}
	for _, ip := range leaf.IPAddresses {
		s.Names = append(s.Names, ip.String())
	}
	s.Names = append(s.Names, leaf.DNSNames...)
	if len(c.chain) > 1 {
		s.Intermediate, s.InterNotAfter = true, c.chain[1].NotAfter
	}
	s.Trusted, s.TrustProblem = c.trust(now)
	return s
}

// trust verifies the leaf against the trust store. The time is moved inside
// the leaf's life so an expired leaf still answers "who signed it", since
// expiry is reported on its own.
func (c *Checker) trust(now time.Time) (bool, string) {
	leaf := c.chain[0]
	at := now
	if !at.Before(leaf.NotAfter) {
		at = leaf.NotAfter.Add(-time.Second)
	} else if at.Before(leaf.NotBefore) {
		at = leaf.NotBefore.Add(time.Second)
	}
	inters := x509.NewCertPool()
	for _, crt := range c.chain[1:] {
		inters.AddCert(crt)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       c.cfg.Host,
		Roots:         c.cfg.Roots,
		Intermediates: inters,
		CurrentTime:   at,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		return true, ""
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return false, "The certificate is not valid for " + c.cfg.Host + "."
	}
	return false, "This machine's trust store does not accept the certificate chain. It may be signed by a private authority, or the server may not send its intermediate."
}
