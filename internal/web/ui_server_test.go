package web_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/hetzner"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/serverstats"
	"github.com/petervlad/loadout/internal/web"
)

// fakeStats returns a fixed report.
type fakeStats struct{ rep serverstats.Report }

func (f fakeStats) Report(context.Context) serverstats.Report { return f.rep }

// fullReport has every part filled, with names only an attacker would
// choose where the page shows text from outside.
func fullReport() serverstats.Report {
	now := time.Now()
	return serverstats.Report{
		At:      now,
		Process: serverstats.Process{Version: "v1.4.0", GoVersion: "go1.27.1", Started: now.Add(-50 * time.Hour), Uptime: 50 * time.Hour, HeapBytes: 12 << 20, SysBytes: 30 << 20, Goroutines: 18},
		Disk: serverstats.Disk{
			DataDir: "/var/lib/loadout", FSTotal: 40 << 30, FSFree: 30 << 30,
			DBBytes: 12 << 20, WALBytes: 4 << 20, DataBytes: 17 << 20, DataFiles: 5,
		},
		Counts: serverstats.Counts{
			Registry: registry.Counts{
				Namespaces: map[registry.NamespaceKind]int{registry.KindProject: 1, registry.KindTeam: 2},
				Packages:   map[pack.Kind]int{pack.KindAgent: 3, pack.KindSkill: 1, pack.KindKB: 1},
				Releases:   12, ReleaseBytes: 4 << 20,
				Proposals:     map[registry.ProposalStatus]int{registry.ProposalOpen: 2, registry.ProposalApproved: 3},
				MachineTokens: 3, LoginTokens: 2, Reviews: 1,
			},
			Accounts: account.Counts{Users: 5, Verified: 4, Admins: 1, Members: map[account.Role]int{account.RoleOwner: 1, account.RoleMember: 3}, Sessions: 3},
		},
		Backups: serverstats.Backups{Dir: "/var/backups/loadout", Count: 14, Bytes: 17 << 20, Latest: now.Add(-9 * time.Hour), LatestName: "loadout-" + xss + ".tar.gz", LatestBytes: 1 << 20},
		Mail:    serverstats.Mail{On: true, Stats: mail.QueueStats{Sent: 12, Failed: 1, LastFailure: now.Add(-2 * time.Hour)}},
		Host: serverstats.Host{
			Stated: serverstats.HostFacts{
				Provider: "Example Cloud", Region: "project-x", Size: "2 vCPU, 4 GB RAM, 40 GB disk",
				Contact: "ops " + xss, Console: "https://console.example.com/overview",
			},
			Name: "loadout-1", OS: "Ubuntu 24.04.1 LTS", CPUs: 2, Arch: "amd64", PublicURL: "https://registry.example.com",
		},
		Hetzner: serverstats.Hetzner{Configured: true, Summary: hetzner.Summary{
			CheckedAt: now,
			Server: hetzner.Server{
				ID: 167857226, Name: "loadout-registry " + xss, Status: "running",
				Type:          hetzner.ServerType{Name: "cx23", Cores: 2, MemoryGB: 4, DiskGB: 40, CPUType: "shared"},
				Location:      hetzner.Location{Name: "nbg1", City: "Nuremberg", Country: "DE"},
				OutgoingBytes: 1843289600, IncludedBytes: 20 << 40, TrafficKnown: true,
			},
			PrimaryIPs: []hetzner.PrimaryIP{{Type: "ipv4", IP: "203.0.113.7", Location: "nbg1"}},
			Cost: hetzner.Cost{Currency: "EUR", VATRate: "19", Total: 4.7481, Lines: []hetzner.CostLine{
				{Label: "Server", Detail: "cx23 in nbg1", Monthly: 4.1531},
				{Label: "Primary IPv4", Detail: "203.0.113.7", Monthly: 0.595},
			}},
		}},
	}
}

func serverSite(t *testing.T, rep serverstats.Report) *uiSite {
	t.Helper()
	s := newUISiteWith(t, func(ui *web.UI) { ui.Server = fakeStats{rep} })
	ctx, op := t.Context(), account.Operator()
	if _, err := s.accounts.SetAdmin(ctx, op, "root@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.accounts.AddMember(ctx, op, "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestServerPageForAdmins(t *testing.T) {
	s := serverSite(t, fullReport())
	root := s.as("root@example.com")
	wantIn(t, root.get("/account"), "account", `href="/account/server"`, "Server details")

	p := root.get("/account/server")
	if p.status != http.StatusOK {
		t.Fatalf("GET /account/server = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "server page",
		`aria-current="page"`, `href="/account">`,
		"Estimated cost", "€4.75", "a month, with VAT",
		"25% used", "30 GB free",
		"9 hours ago", "Running for", "2 days",
		"Example Cloud", "project-x", "2 vCPU, 4 GB RAM, 40 GB disk", "loadout-1", "Ubuntu 24.04.1 LTS", "amd64, 2 CPUs seen by loadout",
		"https://registry.example.com", "ops &lt;script&gt;", `href="https://console.example.com/overview"`, "stated in loadout.env",
		"Hetzner server", "loadout-registry &lt;script&gt;", "id 167857226", "CX23", "2 shared vCPUs, 4 GB memory, 40 GB disk",
		"Nuremberg, DE", "Running", "203.0.113.7", "1.7 GB", "of 20 TB included this billing period",
		"Estimated monthly cost", "€4.15", "€0.59", "€4.75", "Estimated from Hetzner's list prices, with 19% VAT",
		`<meter class="meter"`, `value="25"`, "10 GB used of 40 GB, 30 GB free", "4 MB of write-ahead log",
		"14 archives", "loadout-&lt;script&gt;",
		"3 agents, 1 skill, 1 knowledge base", "2 open", "3 closed", "5", "4 verified, 1 admin", "3 browser sessions", "2 terminal logins",
		"Waiting", "Failed", "last 2 hours ago",
		"v1.4.0", "go1.27.1", "12 MB in use",
	)
	wantOut(t, p, "server page", xss, "Hetzner not configured", "Hetzner unavailable", "Set LOADOUT_HOST_PROVIDER")
}

func TestServerPageHiddenFromOthers(t *testing.T) {
	s := serverSite(t, fullReport())
	for _, email := range []string{"olga@example.com", "alice@example.com", "bob@example.com"} {
		b := s.as(email)
		wantOut(t, b.get("/account"), email+" account", "/account/server", "Server details")
		if p := b.get("/account/server"); p.status != http.StatusNotFound {
			t.Errorf("GET /account/server as %s = %d, want 404", email, p.status)
		} else {
			wantOut(t, p, email+" 404", "Estimated", "loadout-registry", "/var/lib/loadout")
		}
	}
	p := s.browser().get("/account/server")
	want := "/auth/login?" + url.Values{"next": {"/account/server"}}.Encode()
	if p.status != http.StatusSeeOther || p.location != want {
		t.Errorf("GET /account/server signed out = %d %s, want a redirect to sign in", p.status, p.location)
	}
}

func TestServerPageWithoutStats(t *testing.T) {
	s := newUISite(t)
	if _, err := s.accounts.SetAdmin(t.Context(), account.Operator(), "root@example.com", true); err != nil {
		t.Fatal(err)
	}
	root := s.as("root@example.com")
	wantOut(t, root.get("/account"), "account", "/account/server")
	if p := root.get("/account/server"); p.status != http.StatusNotFound {
		t.Errorf("GET /account/server without a report = %d, want 404", p.status)
	}
}

// TestServerPageDegrades checks that each missing part says so in words
// and the rest still shows.
func TestServerPageDegrades(t *testing.T) {
	tests := []struct {
		name   string
		change func(*serverstats.Report)
		want   []string
		absent []string
	}{
		{
			"no token", func(r *serverstats.Report) { r.Hetzner = serverstats.Hetzner{} },
			[]string{"Example Cloud", "project-x", "ops &lt;script&gt;", `href="https://console.example.com/overview"`, "company network or its VPN", "14 archives", ">Host<"},
			[]string{"Hetzner", "LOADOUT_HETZNER_TOKEN", "Estimated cost", "€", "Not set up</dd><dd class=\"note\">No Hetzner token", "<script>"},
		},
		{
			"nothing stated", func(r *serverstats.Report) {
				r.Hetzner = serverstats.Hetzner{}
				r.Host.Stated = serverstats.HostFacts{}
			},
			[]string{"Set LOADOUT_HOST_PROVIDER and the other LOADOUT_HOST_ variables in /etc/loadout/loadout.env", "loadout-1", "Ubuntu 24.04.1 LTS"},
			[]string{"<dt>Provider</dt>", "<dt>Region</dt>", "<dt>Contact</dt>", "Open the host&#39;s console", "Hetzner"},
		},
		{
			"host facts unreadable", func(r *serverstats.Report) {
				r.Hetzner = serverstats.Hetzner{}
				r.Host.Name, r.Host.NameErr, r.Host.OS, r.Host.OSErr = "", "It could not be read.", "", "It does not exist."
			},
			[]string{"It could not be read.", "It does not exist.", "Unknown"},
			nil,
		},
		{
			"API down", func(r *serverstats.Report) {
				r.Hetzner = serverstats.Hetzner{Configured: true, Err: "Hetzner did not answer within 5 seconds."}
			},
			[]string{"Hetzner unavailable:", "Hetzner did not answer within 5 seconds.", "Unavailable", "10 GB used of 40 GB", "Example Cloud"},
			[]string{"Hetzner not configured"},
		},
		{
			"backups unreadable, mail off, counts failed", func(r *serverstats.Report) {
				r.Backups = serverstats.Backups{Dir: "/var/backups/loadout", Err: "The service may not read it."}
				r.Mail = serverstats.Mail{}
				r.Counts = serverstats.Counts{Err: "The database could not be counted."}
			},
			[]string{"Cannot read /var/backups/loadout. The service may not read it.", "Cannot read the folder", "Email sign-in is off", "The database could not be counted.", "€4.75"},
			[]string{"14 archives", "terminal login"},
		},
		{
			"disk unreadable, full disk", func(r *serverstats.Report) {
				r.Disk.FSErr = "Not available on this system."
			},
			[]string{"The disk could not be read. Not available on this system.", "Unknown"},
			[]string{"<meter"},
		},
		{
			"almost full, old backup, traffic nearly used", func(r *serverstats.Report) {
				r.Disk.FSFree = 2 << 30
				r.Backups.Latest = time.Now().Add(-72 * time.Hour)
				r.Hetzner.Summary.Server.OutgoingBytes = 18 << 40
			},
			[]string{"95% used", "tile-bad", "Almost full: 2 GB free", "Older than a day and a half", "tile-warn", `<span class="pill pill-warn">18 TB</span>`},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := fullReport()
			tt.change(&rep)
			p := serverSite(t, rep).as("root@example.com").get("/account/server")
			if p.status != http.StatusOK {
				t.Fatalf("GET /account/server = %d", p.status)
			}
			wantIn(t, p, tt.name, tt.want...)
			wantOut(t, p, tt.name, tt.absent...)
		})
	}
}
