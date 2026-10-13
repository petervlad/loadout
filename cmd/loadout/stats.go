package main

import (
	"log/slog"
	"time"

	"github.com/petervlad/loadout/internal/config"
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
// off), the host as the operator states it and, with a cloud provider set up
// (see cloud.go), the server as the cloud sees it.
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
	cloud, err := cloudProvider(st, log)
	if err != nil {
		return nil, err
	}
	// A nil provider in the interface would not be nil.
	cloudName := ""
	if cloud != nil {
		cfg.Cloud, cloudName = cloud, cloud.Name()
	}
	log.Info("server page", "cloud", cloudName, "host_provider", st.Host.Provider, "backups", st.BackupDir)
	return serverstats.New(cfg), nil
}
