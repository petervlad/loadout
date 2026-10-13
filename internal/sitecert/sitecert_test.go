package sitecert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

const decade = 10 * 365 * 24 * time.Hour

type cert struct {
	crt *x509.Certificate
	key *ecdsa.PrivateKey
}

// issue makes a certificate signed by parent (self-signed when parent is nil).
func issue(t *testing.T, tpl *x509.Certificate, parent *cert) cert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tpl.SerialNumber = serial
	signer, signerCrt := key, tpl
	if parent != nil {
		signer, signerCrt = parent.key, parent.crt
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signerCrt, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	crt, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert{crt, key}
}

func ca(t *testing.T, name string, left time.Duration, parent *cert) cert {
	t.Helper()
	return issue(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(left),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}, parent)
}

func leaf(t *testing.T, from, until time.Time, parent cert) cert {
	t.Helper()
	return issue(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "leaf"},
		NotBefore:   from,
		NotAfter:    until,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &parent)
}

// start serves leaf then inter over TLS and returns the server (close it to drop the listener).
func start(t *testing.T, l, inter cert) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{l.crt.Raw, inter.crt.Raw},
		PrivateKey:  l.key,
	}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func pool(c cert) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.crt)
	return p
}

func checker(t *testing.T, roots *x509.CertPool, addr, host string) *Checker {
	t.Helper()
	c, err := New(Config{Roots: roots, Addr: addr, Host: host, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheck(t *testing.T) {
	good := ca(t, "Test Root", decade, nil)
	inter := ca(t, "Test Intermediate", decade/2, &good)
	other := ca(t, "Other Root", decade, nil)
	otherInter := ca(t, "Other Intermediate", decade/2, &other)
	tests := []struct {
		name  string
		inter cert
		from  time.Time
		until time.Time
		host  string
		check func(t *testing.T, s Served)
	}{
		{"all good", inter, now.Add(-time.Hour), now.Add(30 * 24 * time.Hour), "127.0.0.1", func(t *testing.T, s Served) {
			t.Helper()
			if s.Err != "" || s.Expired || s.Expiring || !s.Trusted || !s.Intermediate {
				t.Errorf("served: %+v", s)
			}
			if len(s.Names) != 1 || s.Names[0] != "127.0.0.1" || s.Issuer != "Test Intermediate" || len(s.Fingerprint) != 95 {
				t.Errorf("names, issuer or fingerprint: %+v", s)
			}
			if !s.InterNotAfter.Equal(inter.crt.NotAfter) || !s.NotAfter.Equal(now.Add(30*24*time.Hour)) {
				t.Errorf("dates: %+v", s)
			}
		}},
		{"chain to a root the store does not hold", otherInter, now.Add(-time.Hour), now.Add(30 * 24 * time.Hour), "127.0.0.1", func(t *testing.T, s Served) {
			t.Helper()
			if s.Trusted || !strings.Contains(s.TrustProblem, "trust store") {
				t.Errorf("served: %+v", s)
			}
		}},
		{"a host the certificate is not valid for", inter, now.Add(-time.Hour), now.Add(30 * 24 * time.Hour), "registry.example.com", func(t *testing.T, s Served) {
			t.Helper()
			if s.Trusted || !strings.Contains(s.TrustProblem, "not valid for registry.example.com") {
				t.Errorf("served: %+v", s)
			}
		}},
		{"expired", inter, now.Add(-48 * time.Hour), now.Add(-time.Hour), "127.0.0.1", func(t *testing.T, s Served) {
			t.Helper()
			if !s.Expired || !s.Trusted {
				t.Errorf("served: %+v", s)
			}
		}},
		{"under a week left", inter, now.Add(-time.Hour), now.Add(3 * 24 * time.Hour), "127.0.0.1", func(t *testing.T, s Served) {
			t.Helper()
			if !s.Expiring || s.Expired || !s.Trusted {
				t.Errorf("served: %+v", s)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := start(t, leaf(t, tc.from, tc.until, tc.inter), tc.inter)
			c := checker(t, pool(good), srv.Listener.Addr().String(), tc.host)
			tc.check(t, c.Check(context.Background()).Served)
		})
	}
}

func TestNewRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"10.0.0.7:443", "example.com:443", "192.168.1.1:443", "127.0.0.1", "[2001:db8::1]:443"} {
		if _, err := New(Config{Addr: addr, Host: "h"}); err == nil {
			t.Errorf("New(%q) accepted", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:443", "[::1]:8443", "127.5.5.5:1"} {
		if _, err := New(Config{Addr: addr, Host: "h"}); err != nil {
			t.Errorf("New(%q): %v", addr, err)
		}
	}
	if _, err := New(Config{Addr: "127.0.0.1:443"}); err == nil {
		t.Error("empty host accepted")
	}
}

func TestHandshakeFailureAndCache(t *testing.T) {
	root := ca(t, "Test Root", decade, nil)
	inter := ca(t, "Test Intermediate", decade, &root)
	srv := start(t, leaf(t, now.Add(-time.Hour), now.Add(10*time.Hour), inter), inter)
	clock := now
	c, err := New(Config{
		Roots: pool(root),
		Addr:  srv.Listener.Addr().String(),
		Host:  "127.0.0.1",
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := c.Check(context.Background()); r.Served.Err != "" {
		t.Fatalf("first: %+v", r.Served)
	}
	srv.Close()

	clock = now.Add(9 * time.Minute)
	if r := c.Check(context.Background()); r.Served.Err != "" || !r.Served.Trusted {
		t.Errorf("inside 10 minutes the answer is kept: %+v", r.Served)
	}
	clock = now.Add(11 * time.Minute)
	r := c.Check(context.Background())
	if r.Served.Err == "" {
		t.Fatal("a closed server should fail the handshake")
	}
	if strings.Contains(r.Served.Err, "127.0.0.1:") {
		t.Errorf("address leaked: %q", r.Served.Err)
	}
}

// TestFingerprintFormat: upper-case hex pairs joined by colons, as openssl prints them.
func TestFingerprintFormat(t *testing.T) {
	crt := ca(t, "Test Root", decade, nil).crt
	sum := sha256.Sum256(crt.Raw)
	var want []string
	for _, b := range sum {
		want = append(want, strings.ToUpper(hex.EncodeToString([]byte{b})))
	}
	if got := Fingerprint(crt); got != strings.Join(want, ":") {
		t.Errorf("fingerprint = %s", got)
	}
}

// TestCancelledCallerKeepsGoodAnswer: an admin who closes the tab mid-load
// must not cache "context canceled" as a failure for everyone.
func TestCancelledCallerKeepsGoodAnswer(t *testing.T) {
	root := ca(t, "Test Root", decade, nil)
	inter := ca(t, "Test Intermediate", decade, &root)
	srv := start(t, leaf(t, now.Add(-time.Hour), now.Add(10*time.Hour), inter), inter)
	c := checker(t, pool(root), srv.Listener.Addr().String(), "127.0.0.1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := c.Check(ctx); r.Served.Err != "" || !r.Served.Trusted {
		t.Fatalf("cancelled caller: %+v", r.Served)
	}
	if r := c.Check(context.Background()); r.Served.Err != "" || c.chainEr != "" {
		t.Errorf("cached failure %q after a cancelled caller", c.chainEr)
	}
}
