package hetzner

import (
	"fmt"
	"strconv"
	"strings"
)

// Cost is a monthly cost estimated from list prices, with VAT as Hetzner
// returns it for the token's account.
type Cost struct {
	Currency string
	// VATRate is the rate in percent, as Hetzner gives it ("19").
	VATRate string
	Lines   []CostLine
	Total   float64
	// Unpriced names what the price list has no price for; the total
	// leaves it out.
	Unpriced []string
}

// CostLine is one thing that costs money each month.
type CostLine struct {
	Label   string
	Detail  string
	Monthly float64
}

// tib is the unit Hetzner counts traffic in ("20 TB" is 20 TiB).
const tib = 1 << 40

// Estimate adds up what the server costs a month at list prices: its type
// in its location, backups as a share of that, volumes by size, primary
// IPs by type, and outgoing traffic above the included amount so far.
func Estimate(s Server, volumes []Volume, ips []PrimaryIP, p Pricing) Cost {
	c := Cost{Currency: p.Currency, VATRate: trimDecimal(p.VATRate)}
	add := func(label, detail string, v float64) {
		c.Lines = append(c.Lines, CostLine{Label: label, Detail: detail, Monthly: v})
		c.Total += v
	}

	serverPrice, serverOK := 0.0, false
	var perTB Price
	for _, t := range p.ServerTypes {
		if t.Name != s.Type.Name {
			continue
		}
		for _, pr := range t.Prices {
			if pr.Location == s.Location.Name {
				serverPrice, serverOK = parse(pr.PriceMonthly.Gross)
				perTB = pr.PricePerTBTraffic
			}
		}
	}
	if serverOK {
		add("Server", s.Type.Name+" in "+s.Location.Name, serverPrice)
	} else {
		c.Unpriced = append(c.Unpriced, "server type "+s.Type.Name+" in "+s.Location.Name)
	}

	if s.Backups {
		pct, ok := parse(p.ServerBackup.Percentage)
		if ok && serverOK {
			add("Backups", trimDecimal(p.ServerBackup.Percentage)+"% of the server", serverPrice*pct/100)
		} else {
			c.Unpriced = append(c.Unpriced, "backups")
		}
	}

	perGB, perGBOK := parse(p.Volume.PricePerGBMonth.Gross)
	for _, v := range volumes {
		if !perGBOK {
			c.Unpriced = append(c.Unpriced, "volume "+v.Name)
			continue
		}
		add("Volume "+v.Name, fmt.Sprintf("%d GB", v.SizeGB), float64(v.SizeGB)*perGB)
	}

	for _, ip := range ips {
		price, ok := primaryIPPrice(p, ip)
		switch {
		case !ok:
			c.Unpriced = append(c.Unpriced, "primary "+ipLabel(ip.Type)+" "+ip.IP)
		case price > 0:
			add("Primary "+ipLabel(ip.Type), ip.IP, price)
		}
	}

	if s.TrafficKnown && s.IncludedBytes > 0 && s.OutgoingBytes > s.IncludedBytes {
		over := float64(s.OutgoingBytes-s.IncludedBytes) / tib
		if price, ok := parse(perTB.Gross); ok {
			add("Traffic", fmt.Sprintf("%.2f TB over the included amount, so far", over), over*price)
		} else {
			c.Unpriced = append(c.Unpriced, "traffic over the included amount")
		}
	}
	return c
}

func primaryIPPrice(p Pricing, ip PrimaryIP) (float64, bool) {
	for _, t := range p.PrimaryIPs {
		if t.Type != ip.Type {
			continue
		}
		for _, pr := range t.Prices {
			if pr.Location == ip.Location {
				return parse(pr.PriceMonthly.Gross)
			}
		}
	}
	return 0, false
}

func ipLabel(t string) string {
	switch t {
	case "ipv4":
		return "IPv4"
	case "ipv6":
		return "IPv6"
	}
	return "IP"
}

func parse(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil && v >= 0
}

// trimDecimal turns "19.000000" into "19" and "20.500000" into "20.5".
func trimDecimal(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
