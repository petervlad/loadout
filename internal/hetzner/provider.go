package hetzner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/petervlad/loadout/internal/serverstats"
)

// trafficWarnPct is the share of the included traffic that marks the
// outgoing figure.
const trafficWarnPct = 80

// Provider shows a Client as the cloud part of the server page.
type Provider struct{ c *Client }

var _ serverstats.CloudProvider = (*Provider)(nil)

// NewProvider wraps a client.
func NewProvider(c *Client) *Provider { return &Provider{c: c} }

// Name is the provider's name on the page.
func (*Provider) Name() string { return "Hetzner" }

// Summary reads the server and writes it in the page's neutral terms. A
// failure becomes an error whose text is the plain sentence Reason gives.
func (p *Provider) Summary(ctx context.Context) (serverstats.CloudSummary, error) {
	s, err := p.c.Summary(ctx)
	if err != nil {
		return serverstats.CloudSummary{}, errors.New(Reason(err))
	}
	return serverstats.CloudSummary{CheckedAt: s.CheckedAt, Facts: facts(s), Cost: cost(s.Cost)}, nil
}

func facts(s Summary) []serverstats.CloudFact {
	srv := s.Server
	status := serverstats.CloudFact{Label: "Status", Value: capitalize(srv.Status), Tone: "warn"}
	if srv.Status == "running" {
		status.Tone = "ok"
	}
	out := []serverstats.CloudFact{
		{Label: "Name", Value: srv.Name, Note: "id " + strconv.FormatInt(srv.ID, 10)},
		status,
		{Label: "Type", Value: strings.ToUpper(srv.Type.Name), Note: serverSize(srv.Type)},
		{Label: "Location", Value: place(srv.Location), Note: srv.Location.Name},
	}
	backups := serverstats.CloudFact{Label: "Backups", Value: "Off", Note: "Hetzner's daily server backups"}
	if srv.Backups {
		backups.Value = "On"
	}
	out = append(out, backups)

	var ips []string
	for _, ip := range s.PrimaryIPs {
		ips = append(ips, ip.IP)
	}
	if len(ips) == 0 {
		ips = []string{"None"}
	}
	out = append(out, serverstats.CloudFact{Label: "Public IPs", Value: ips[0], Note: strings.Join(ips[1:], ", ")})

	vols := serverstats.CloudFact{Label: "Volumes", Value: "None"}
	if len(s.Volumes) > 0 {
		var names []string
		for _, vol := range s.Volumes {
			names = append(names, fmt.Sprintf("%s, %d GB", vol.Name, vol.SizeGB))
		}
		vols.Value, vols.Note = names[0], strings.Join(names[1:], "; ")
	}
	out = append(out, vols)

	traffic := serverstats.CloudFact{Label: "Outgoing traffic", Value: "Not counted yet"}
	if srv.TrafficKnown {
		traffic.Value = serverstats.ByteText(srv.OutgoingBytes)
		if srv.IncludedBytes > 0 {
			traffic.Note = "of " + serverstats.ByteText(srv.IncludedBytes) + " included this billing period"
			if srv.OutgoingBytes*100/srv.IncludedBytes >= trafficWarnPct {
				traffic.Tone = "warn"
			}
		}
	}
	return append(out, traffic, serverstats.CloudFact{Label: "Incoming traffic", Value: serverstats.ByteText(srv.IngoingBytes), Note: "not billed"})
}

func cost(c Cost) *serverstats.CloudCost {
	out := &serverstats.CloudCost{
		Currency: c.Currency,
		Total:    c.Total,
		Note: "Estimated from Hetzner's list prices, with " + c.VATRate + "% VAT as Hetzner states it for your account. " +
			"The Cloud API has no invoices: the bill itself is in the Hetzner Console. Hetzner figures are kept for 5 minutes.",
	}
	for _, l := range c.Lines {
		out.Lines = append(out.Lines, serverstats.CloudCostLine{Label: l.Label, Detail: l.Detail, Monthly: l.Monthly})
	}
	if len(c.Unpriced) > 0 {
		out.Unpriced = "Not in the total, since the price list has no price for it: " + strings.Join(c.Unpriced, ", ") + "."
	}
	return out
}

func serverSize(t ServerType) string {
	cpu := plural(t.Cores, "vCPU")
	if t.CPUType != "" {
		cpu = fmt.Sprintf("%d %s %s", t.Cores, t.CPUType, strings.TrimPrefix(cpu, strconv.Itoa(t.Cores)+" "))
	}
	mem := strings.TrimSuffix(strconv.FormatFloat(t.MemoryGB, 'f', 1, 64), ".0")
	return fmt.Sprintf("%s, %s GB memory, %d GB disk", cpu, mem, t.DiskGB)
}

func place(l Location) string {
	switch {
	case l.City != "" && l.Country != "":
		return l.City + ", " + l.Country
	case l.City != "":
		return l.City
	}
	return l.Name
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func capitalize(s string) string {
	if s == "" {
		return "Unknown"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
