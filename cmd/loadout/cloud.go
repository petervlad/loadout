package main

import (
	"log/slog"
	"strings"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/hetzner"
	"github.com/petervlad/loadout/internal/serverstats"
)

// cloudFactory builds a cloud provider for the server page from the
// settings, or returns nil, nil when its settings are not there.
type cloudFactory func(st config.Stats, log *slog.Logger) (serverstats.CloudProvider, error)

// cloudFactories are the providers the server page can use, in the order
// they are tried. The first one whose settings are present wins. To add a
// provider, write its factory in a new file and add it here.
func cloudFactories() []cloudFactory {
	return []cloudFactory{hetznerFactory}
}

// cloudProvider returns the first provider whose settings are present, or
// nil when none is, which leaves the cloud panels out of the page.
func cloudProvider(st config.Stats, log *slog.Logger) (serverstats.CloudProvider, error) {
	for _, f := range cloudFactories() {
		p, err := f(st, log)
		if err != nil || p != nil {
			return p, err
		}
	}
	return nil, nil
}

// hetznerFactory is on with LOADOUT_HETZNER_TOKEN. The token goes only to
// the Hetzner client and is never logged.
func hetznerFactory(st config.Stats, log *slog.Logger) (serverstats.CloudProvider, error) {
	if st.HetznerToken == "" {
		return nil, nil
	}
	c, err := hetzner.New(hetzner.Config{Token: st.HetznerToken, Server: st.HetznerServer, BaseURL: st.HetznerAPI, Log: log})
	if err != nil {
		return nil, err
	}
	// The mistake behind this check: a registry moved to another provider
	// that still carries the old Hetzner lines shows the old server.
	if st.Host.Provider != "" && !strings.Contains(strings.ToLower(st.Host.Provider), "hetzner") {
		log.Warn("server page: a Hetzner token is set although the host provider is not Hetzner, so the page also shows a Hetzner server; remove the LOADOUT_HETZNER_ lines from loadout.env if they are left over", "host_provider", st.Host.Provider)
	}
	return hetzner.NewProvider(c), nil
}
