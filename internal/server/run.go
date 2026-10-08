package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server timeouts and limits.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10
	shutdownTimeout   = 15 * time.Second
	// transferTimeout replaces readTimeout and writeTimeout for a request
	// that moves a bundle (see allowTransfer): the largest, 32 MB, takes
	// about four minutes at 1 Mbit/s. Every other request keeps the short
	// timeouts, so a slow client cannot hold one open for long.
	transferTimeout = 10 * time.Minute
)

// Run listens on addr and serves h until ctx is cancelled; see Serve.
func Run(ctx context.Context, addr string, h http.Handler, log *slog.Logger, tlsCert, tlsKey string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	return Serve(ctx, ln, h, log, tlsCert, tlsKey)
}

// Serve serves h on ln until ctx is cancelled, then shuts down gracefully,
// giving in-flight requests up to 15 seconds. It serves TLS when tlsCert and
// tlsKey (PEM file paths) are both set. A clean shutdown returns nil. Serve
// closes ln.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger, tlsCert, tlsKey string) error {
	useTLS := tlsCert != "" || tlsKey != ""
	if useTLS && (tlsCert == "" || tlsKey == "") {
		return errors.Join(errors.New("serve: TLS needs both a certificate and a key"), ln.Close())
	}

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	if useTLS {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	errc := make(chan error, 1)
	go func() {
		if useTLS {
			errc <- srv.ServeTLS(ln, tlsCert, tlsKey)
			return
		}
		errc <- srv.Serve(ln)
	}()
	log.Info("listening", "addr", ln.Addr().String(), "tls", useTLS)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", shutdownTimeout.String())
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(sctx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, srv.Close())
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("shutdown: %w", shutdownErr)
	}
	log.Info("stopped")
	return nil
}
