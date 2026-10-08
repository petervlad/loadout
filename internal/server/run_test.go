package server_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/server"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// get is safe to call from any goroutine: it reports errors, never t.Fatal.
func get(ctx context.Context, c *http.Client, url string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func TestServeGracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})

	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, ln, h, slog.New(slog.DiscardHandler), "", "") }()

	type result struct {
		status int
		body   string
		err    error
	}
	inFlight := make(chan result, 1)
	go func() {
		status, body, err := get(t.Context(), http.DefaultClient, "http://"+ln.Addr().String()+"/")
		inFlight <- result{status, body, err}
	}()

	<-started
	cancel()
	// Shutdown must wait for the in-flight request rather than cut it off.
	select {
	case err := <-served:
		t.Fatalf("Serve returned %v while a request was in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	if r := <-inFlight; r.err != nil || r.status != http.StatusOK || r.body != "done" {
		t.Errorf("in-flight request = %d %q %v, want 200 done", r.status, r.body, r.err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve() = %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

func TestServeTLS(t *testing.T) {
	certFile, keyFile, pool := selfSigned(t)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secure") })

	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, ln, h, slog.New(slog.DiscardHandler), certFile, keyFile) }()

	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	status, body, err := get(t.Context(), c, "https://"+ln.Addr().String()+"/")
	if err != nil || status != http.StatusOK || body != "secure" {
		t.Errorf("GET over TLS = %d %q %v, want 200 secure", status, body, err)
	}
	cancel()
	if err := <-served; err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
}

func TestServeTLSNeedsBoth(t *testing.T) {
	err := server.Serve(t.Context(), listen(t), http.NotFoundHandler(), slog.New(slog.DiscardHandler), "cert.pem", "")
	if err == nil {
		t.Error("Serve(cert without key) = nil, want an error")
	}
}

func TestRunBadAddress(t *testing.T) {
	err := server.Run(t.Context(), "256.0.0.1:bad", http.NotFoundHandler(), slog.New(slog.DiscardHandler), "", "")
	if err == nil {
		t.Error("Run(bad addr) = nil, want an error")
	}
}

// selfSigned writes a certificate for 127.0.0.1 and its key, and returns a
// pool that trusts it.
func selfSigned(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "loadout test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}
