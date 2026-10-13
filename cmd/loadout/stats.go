package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/serverstats"
	"github.com/petervlad/loadout/internal/sitecert"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
	"github.com/petervlad/loadout/internal/vminfo"
)

// started is when this process started, for the server page's uptime.
var started = time.Now()

// The databases in the --data folder: packages and accounts, and team memory.
const (
	dbFile       = "registry.db"
	memoryDBFile = "memory.db"
)

// serverStats builds what the admins' server page reports: the data
// folder, the store's counts, backups, the mail queue (nil when email is
// off), the host as the operator states it and, with a cloud provider set up
// (see cloud.go), the server as the cloud sees it.
func serverStats(log *slog.Logger, st config.Stats, publicURL, dataDir, version string, store *sqlitestore.Store, mem *sqlitestore.MemoryStore, queue *mail.Queue) (*serverstats.Collector, error) {
	cfg := serverstats.Config{
		DataDir:   dataDir,
		DBFiles:   []string{dbFile, memoryDBFile},
		BackupDir: st.BackupDir,
		Version:   version,
		Started:   started,
		Now:       time.Now,
		Registry:  store,
		Accounts:  store,
		// The kernel and a few public files, read through the sandbox.
		Machine: vminfo.New(os.DirFS("/"), vminfo.System(), time.Now),
		Host: serverstats.HostFacts{
			Provider: st.Host.Provider, Region: st.Host.Region, Size: st.Host.Size,
			Contact: st.Host.Contact, Console: st.Host.Console,
		},
		PublicURL: publicURL,
	}
	// A nil pointer in an interface would not be nil.
	if mem != nil {
		cfg.Memory = mem
	}
	if queue != nil {
		cfg.Mail = queue
	}
	cloud, err := cloudProvider(st, log)
	if err != nil {
		return nil, err
	}
	// A nil provider in the interface would not be nil.
	cloudName := ""
	if cloud != nil {
		cfg.Cloud, cloudName = cloud, cloud.Name()
	}
	certs, err := siteCerts(publicURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.EnvPublicURL, err)
	}
	if certs != nil {
		cfg.Certs = certs
	}
	log.Info("server page", "cloud", cloudName, "host_provider", st.Host.Provider, "backups", st.BackupDir, "certificates", cfg.Certs != nil)
	return serverstats.New(cfg), nil
}

// siteCerts checks the certificate the site serves, with a handshake to this
// very server on the public port. It is off for an address that is not https,
// which has no certificate to check.
func siteCerts(publicURL string) (*sitecert.Checker, error) {
	pub, err := url.Parse(publicURL)
	switch {
	case err != nil:
		// A bad address is login's to refuse, not this part's.
		return nil, nil //nolint:nilnil,nilerr // off is not an error
	case pub.Scheme != "https":
		return nil, nil //nolint:nilnil // off is not an error
	}
	port := pub.Port()
	if port == "" {
		port = "443"
	}
	return sitecert.New(sitecert.Config{Addr: net.JoinHostPort("127.0.0.1", port), Host: pub.Hostname()})
}
