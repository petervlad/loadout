package main

import (
	"log/slog"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/hetzner"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/serverstats"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
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
// off), the host as the operator states it and, with a token, the Hetzner
// server. The token goes only to the Hetzner client and is never logged.
func serverStats(log *slog.Logger, st config.Stats, publicURL, dataDir, version string, store *sqlitestore.Store, queue *mail.Queue) (*serverstats.Collector, error) {
	cfg := serverstats.Config{
		DataDir:   dataDir,
		DBFiles:   []string{dbFile, memoryDBFile},
		BackupDir: st.BackupDir,
		Version:   version,
		Started:   started,
		Now:       time.Now,
		Registry:  store,
		Accounts:  store,
		Host: serverstats.HostFacts{
			Provider: st.Host.Provider, Region: st.Host.Region, Size: st.Host.Size,
			Contact: st.Host.Contact, Console: st.Host.Console,
		},
		PublicURL: publicURL,
	}
	// A nil *mail.Queue in the interface would not be nil.
	if queue != nil {
		cfg.Mail = queue
	}
	if st.HetznerToken != "" {
		c, err := hetzner.New(hetzner.Config{Token: st.HetznerToken, Server: st.HetznerServer, BaseURL: st.HetznerAPI, Log: log})
		if err != nil {
			return nil, err
		}
		cfg.Cloud = c
	}
	// The mistake behind this check: a registry moved to another provider
	// that still carries the old Hetzner lines shows the old server.
	if st.HetznerToken != "" && st.Host.Provider != "" && !strings.Contains(strings.ToLower(st.Host.Provider), "hetzner") {
		log.Warn("server page: a Hetzner token is set although the host provider is not Hetzner, so the page also shows a Hetzner server; remove the LOADOUT_HETZNER_ lines from loadout.env if they are left over", "host_provider", st.Host.Provider)
	}
	log.Info("server page", "hetzner", st.HetznerToken != "", "hetzner_server", st.HetznerServer, "host_provider", st.Host.Provider, "backups", st.BackupDir)
	return serverstats.New(cfg), nil
}
