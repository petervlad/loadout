package hetzner

import (
	"encoding/json"
	"testing"
)

func testPricing(t *testing.T) Pricing {
	t.Helper()
	var doc struct {
		Pricing Pricing `json:"pricing"`
	}
	if err := json.Unmarshal(readFile(t, "pricing.json"), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Pricing
}

func TestEstimate(t *testing.T) {
	cx23 := Server{Type: ServerType{Name: "cx23"}, Location: Location{Name: "nbg1"}, IncludedBytes: 20 * tib, TrafficKnown: true}
	v4 := PrimaryIP{Type: "ipv4", IP: "203.0.113.7", Location: "nbg1"}
	v6 := PrimaryIP{Type: "ipv6", IP: "2a01:4f8::/64", Location: "nbg1"}
	with := func(s Server, change func(*Server)) Server { change(&s); return s }

	tests := []struct {
		name      string
		server    Server
		volumes   []Volume
		ips       []PrimaryIP
		wantLines []string
		wantTotal float64
		unpriced  int
	}{
		{name: "server alone", server: cx23, wantLines: []string{"Server"}, wantTotal: 4.1531},
		{
			name: "IPv4 costs, IPv6 is free", server: cx23, ips: []PrimaryIP{v4, v6},
			wantLines: []string{"Server", "Primary IPv4"}, wantTotal: 4.1531 + 0.595,
		},
		{
			name: "backups are a fifth of the server", server: with(cx23, func(s *Server) { s.Backups = true }),
			wantLines: []string{"Server", "Backups"}, wantTotal: 4.1531 * 1.2,
		},
		{
			name: "volumes by size", server: cx23, volumes: []Volume{{Name: "data", SizeGB: 10}, {Name: "logs", SizeGB: 50}},
			wantLines: []string{"Server", "Volume data", "Volume logs"}, wantTotal: 4.1531 + 60*0.05236,
		},
		{
			name: "traffic above the included amount", server: with(cx23, func(s *Server) { s.OutgoingBytes = 21*tib + tib/2 }),
			wantLines: []string{"Server", "Traffic"}, wantTotal: 4.1531 + 1.5*1.19,
		},
		{
			name: "traffic within the included amount", server: with(cx23, func(s *Server) { s.OutgoingBytes = 19 * tib }),
			wantLines: []string{"Server"}, wantTotal: 4.1531,
		},
		{
			name: "traffic not known yet", server: with(cx23, func(s *Server) { s.OutgoingBytes, s.TrafficKnown = 30*tib, false }),
			wantLines: []string{"Server"}, wantTotal: 4.1531,
		},
		{
			name: "location without prices", server: with(cx23, func(s *Server) { s.Location.Name = "hel1"; s.Backups = true }),
			ips: []PrimaryIP{{Type: "ipv4", IP: "1.2.3.4", Location: "hel1"}}, unpriced: 3,
		},
		{
			name: "unknown server type", server: with(cx23, func(s *Server) { s.Type.Name = "cx99" }), ips: []PrimaryIP{v4},
			wantLines: []string{"Primary IPv4"}, wantTotal: 0.595, unpriced: 1,
		},
	}
	p := testPricing(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Estimate(tt.server, tt.volumes, tt.ips, p)
			var labels []string
			for _, l := range c.Lines {
				labels = append(labels, l.Label)
			}
			if len(labels) != len(tt.wantLines) {
				t.Fatalf("lines = %q, want %q", labels, tt.wantLines)
			}
			for i := range labels {
				if labels[i] != tt.wantLines[i] {
					t.Errorf("line %d = %q, want %q", i, labels[i], tt.wantLines[i])
				}
			}
			if !near(c.Total, tt.wantTotal) {
				t.Errorf("Total = %v, want %v", c.Total, tt.wantTotal)
			}
			if len(c.Unpriced) != tt.unpriced {
				t.Errorf("Unpriced = %q, want %d", c.Unpriced, tt.unpriced)
			}
			if c.Currency != "EUR" || c.VATRate != "19" {
				t.Errorf("Currency, VAT = %q, %q", c.Currency, c.VATRate)
			}
		})
	}
}

func TestTrimDecimal(t *testing.T) {
	for in, want := range map[string]string{"19.000000": "19", "20.500000": "20.5", "7": "7", "0.000": "0", "5.10": "5.1"} {
		if got := trimDecimal(in); got != want {
			t.Errorf("trimDecimal(%q) = %q, want %q", in, got, want)
		}
	}
}
