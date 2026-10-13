package hetzner

import (
	"errors"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/serverstats"
)

func TestProviderFacts(t *testing.T) {
	s := Summary{
		Server: Server{
			ID: 7, Name: "reg", Status: "running", Type: ServerType{Name: "cx23", Cores: 2, MemoryGB: 4, DiskGB: 40, CPUType: "shared"},
			Location: Location{Name: "nbg1", City: "Nuremberg", Country: "DE"}, TrafficKnown: true, OutgoingBytes: 18 << 40, IncludedBytes: 20 << 40,
		},
		PrimaryIPs: []PrimaryIP{{Type: "ipv4", IP: "203.0.113.7"}},
		Cost:       Cost{Currency: "EUR", VATRate: "19", Total: 4.5, Unpriced: []string{"snapshots"}, Lines: []CostLine{{Label: "Server", Monthly: 4.5}}},
	}
	got := map[string]serverstats.CloudFact{}
	for _, f := range facts(s) {
		got[f.Label] = f
	}
	if f := got["Status"]; f.Value != "Running" || f.Tone != "ok" {
		t.Errorf("Status = %+v", f)
	}
	if f := got["Type"]; f.Value != "CX23" || f.Note != "2 shared vCPUs, 4 GB memory, 40 GB disk" {
		t.Errorf("Type = %+v", f)
	}
	if f := got["Outgoing traffic"]; f.Value != "18 TB" || f.Tone != "warn" {
		t.Errorf("Outgoing traffic = %+v", f)
	}
	c := cost(s.Cost)
	if c.Total != 4.5 || len(c.Lines) != 1 || !strings.Contains(c.Note, "19% VAT") || !strings.Contains(c.Unpriced, "snapshots") {
		t.Errorf("cost = %+v", c)
	}
}

func TestProviderErrorIsTheReason(t *testing.T) {
	err := errors.New(Reason(&APIError{Status: 401}))
	if !strings.Contains(err.Error(), "refused the token") {
		t.Errorf("error = %q", err)
	}
}
