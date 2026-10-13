package main

import (
	"log/slog"
	"testing"

	"github.com/petervlad/loadout/internal/config"
)

func TestCloudProviderChoice(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if p, err := cloudProvider(config.Stats{}, log); p != nil || err != nil {
		t.Errorf("cloudProvider() without settings = %v, %v, want none", p, err)
	}
	p, err := cloudProvider(config.Stats{HetznerToken: "tok"}, log)
	if err != nil || p == nil || p.Name() != "Hetzner" {
		t.Errorf("cloudProvider() with a Hetzner token = %v, %v", p, err)
	}
}
