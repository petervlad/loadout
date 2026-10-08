package hetzner

import "time"

// The shapes below are the parts of the Hetzner Cloud API answers loadout
// reads; unknown fields are ignored, since Hetzner adds fields often.

type serverList struct {
	Servers []apiServer `json:"servers"`
	Meta    struct {
		Pagination struct {
			NextPage *int `json:"next_page"`
		} `json:"pagination"`
	} `json:"meta"`
}

type apiServer struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Created   time.Time `json:"created"`
	PublicNet struct {
		IPv4 *apiServerIP `json:"ipv4"`
		IPv6 *apiServerIP `json:"ipv6"`
	} `json:"public_net"`
	ServerType struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Cores       int     `json:"cores"`
		Memory      float64 `json:"memory"`
		Disk        int     `json:"disk"`
		CPUType     string  `json:"cpu_type"`
	} `json:"server_type"`
	// Location is where the server runs. Older answers had it only under
	// datacenter, which Hetzner is phasing out.
	Location   *apiLocation `json:"location"`
	Datacenter *struct {
		Location apiLocation `json:"location"`
	} `json:"datacenter"`
	// BackupWindow is null when backups are off.
	BackupWindow    *string `json:"backup_window"`
	OutgoingTraffic *int64  `json:"outgoing_traffic"`
	IngoingTraffic  *int64  `json:"ingoing_traffic"`
	IncludedTraffic int64   `json:"included_traffic"`
	Volumes         []int64 `json:"volumes"`
}

type apiServerIP struct {
	ID int64  `json:"id"`
	IP string `json:"ip"`
}

type apiLocation struct {
	Name    string `json:"name"`
	City    string `json:"city"`
	Country string `json:"country"`
}

type apiVolume struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	Size     int         `json:"size"`
	Location apiLocation `json:"location"`
}

type apiPrimaryIP struct {
	ID         int64        `json:"id"`
	Type       string       `json:"type"`
	IP         string       `json:"ip"`
	Location   *apiLocation `json:"location"`
	Datacenter *struct {
		Location apiLocation `json:"location"`
	} `json:"datacenter"`
}

func (s apiServer) toServer() Server {
	out := Server{
		ID:      s.ID,
		Name:    s.Name,
		Status:  s.Status,
		Created: s.Created,
		Type: ServerType{
			Name: s.ServerType.Name, Description: s.ServerType.Description, Cores: s.ServerType.Cores,
			MemoryGB: s.ServerType.Memory, DiskGB: s.ServerType.Disk, CPUType: s.ServerType.CPUType,
		},
		Backups:       s.BackupWindow != nil,
		IncludedBytes: s.IncludedTraffic,
	}
	switch {
	case s.Location != nil:
		out.Location = Location(*s.Location)
	case s.Datacenter != nil:
		out.Location = Location(s.Datacenter.Location)
	}
	if s.OutgoingTraffic != nil {
		out.OutgoingBytes, out.TrafficKnown = *s.OutgoingTraffic, true
	}
	if s.IngoingTraffic != nil {
		out.IngoingBytes = *s.IngoingTraffic
	}
	if s.PublicNet.IPv4 != nil {
		out.IPv4 = s.PublicNet.IPv4.IP
	}
	if s.PublicNet.IPv6 != nil {
		out.IPv6 = s.PublicNet.IPv6.IP
	}
	return out
}

// toPrimaryIP converts an address, taking the server's location when the
// answer names none.
func (p apiPrimaryIP) toPrimaryIP(serverLocation string) PrimaryIP {
	out := PrimaryIP{ID: p.ID, Type: p.Type, IP: p.IP, Location: serverLocation}
	switch {
	case p.Location != nil && p.Location.Name != "":
		out.Location = p.Location.Name
	case p.Datacenter != nil && p.Datacenter.Location.Name != "":
		out.Location = p.Datacenter.Location.Name
	}
	return out
}

// Pricing is the part of GET /pricing the estimate uses. Hetzner gives
// prices as decimal strings, net and gross (with the VAT of the token's
// account).
type Pricing struct {
	Currency     string `json:"currency"`
	VATRate      string `json:"vat_rate"`
	ServerBackup struct {
		Percentage string `json:"percentage"`
	} `json:"server_backup"`
	ServerTypes []struct {
		Name   string `json:"name"`
		Prices []struct {
			Location          string `json:"location"`
			PriceMonthly      Price  `json:"price_monthly"`
			IncludedTraffic   int64  `json:"included_traffic"`
			PricePerTBTraffic Price  `json:"price_per_tb_traffic"`
		} `json:"prices"`
	} `json:"server_types"`
	PrimaryIPs []struct {
		Type   string `json:"type"`
		Prices []struct {
			Location     string `json:"location"`
			PriceMonthly Price  `json:"price_monthly"`
		} `json:"prices"`
	} `json:"primary_ips"`
	Volume struct {
		PricePerGBMonth Price `json:"price_per_gb_month"`
	} `json:"volume"`
}

// Price is one price, net and with VAT.
type Price struct {
	Net   string `json:"net"`
	Gross string `json:"gross"`
}
