package web_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/serverstats"
	"github.com/petervlad/loadout/internal/sitecert"
	"github.com/petervlad/loadout/internal/vminfo"
)

// fullMachine is a healthy machine.
func fullMachine(now time.Time) vminfo.Machine {
	return vminfo.Machine{
		OS:      vminfo.OS{Name: "Ubuntu 24.04.1 LTS"},
		Kernel:  vminfo.KernelInfo{Release: "6.8.0-45-generic", Machine: "x86_64"},
		CPU:     vminfo.CPU{Count: 2, Model: "Intel Core Processor (Broadwell, IBRS)"},
		Memory:  vminfo.Memory{Total: 4 << 30, AvailableErr: "Hidden by the service's sandbox.", SwapTotal: 1 << 30, SwapUsed: 100 << 20},
		Cgroup:  vminfo.Cgroup{On: true, Used: 300 << 20},
		Load:    vminfo.Load{One: 0.12, Five: 0.3, Fifteen: 0.25, CPUs: 2},
		Uptime:  vminfo.Uptime{Since: now.Add(-72 * time.Hour), For: 72 * time.Hour},
		Reboot:  vminfo.Reboot{AutoRebootAt: "04:30"},
		Updates: vminfo.Updates{Last: now.Add(-20 * time.Hour)},
	}
}

func TestServerPageSystem(t *testing.T) {
	p := serverSite(t, fullReport()).as("root@example.com").get("/account/server")
	wantIn(t, p, "System panel", `id="sys-h">System<`,
		"Ubuntu 24.04.1 LTS", "6.8.0-45-generic x86_64",
		"2 CPUs", "Intel Core Processor (Broadwell, IBRS)",
		"4 GB in all", "Not shown", "Hidden by the service&#39;s sandbox.", "100 MB in use", "of 1 GB",
		"Used by loadout", "300 MB",
		"0.12, 0.30, 0.25", "over 1, 5 and 15 minutes, on 2 CPUs",
		"3 days", "Not needed", "Unattended upgrades reboot at 04:30.", "Ran 20 hours ago")
	wantOut(t, p, "System panel", xss, "Needed", "above the number")
}

func TestServerPageSystemStates(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		change func(*vminfo.Machine)
		want   []string
		absent []string
	}{
		{"everything failing", func(m *vminfo.Machine) {
			*m = vminfo.Machine{
				OS:      vminfo.OS{Err: "It does not exist."},
				Kernel:  vminfo.KernelInfo{Err: "The uname call failed."},
				CPU:     vminfo.CPU{Count: 2, Err: "Hidden by the service's sandbox."},
				Memory:  vminfo.Memory{Err: "The sysinfo call failed."},
				Load:    vminfo.Load{CPUs: 2, Err: "The sysinfo call failed."},
				Uptime:  vminfo.Uptime{Err: "The sysinfo call failed."},
				Reboot:  vminfo.Reboot{Err: "The service may not read it."},
				Updates: vminfo.Updates{Err: "No record of unattended upgrades."},
			}
		}, []string{"It does not exist.", "The uname call failed.", "Model: hidden by the service&#39;s sandbox.", "The sysinfo call failed.", "The service may not read it.", "No record of unattended upgrades.", "Unknown"}, []string{"Used by loadout", "Free memory"}},
		{"busy, reboot wanted, old updates", func(m *vminfo.Machine) {
			m.Load = vminfo.Load{One: 3, Five: 2.5, Fifteen: 2, CPUs: 2, Warn: true}
			m.Reboot = vminfo.Reboot{Pending: true, Packages: []string{"linux-image-6.8.0-50", "libc6"}, More: 3, AutoRebootAt: "04:30"}
			m.Updates = vminfo.Updates{Last: now.Add(-100 * time.Hour), Stale: true}
		}, []string{"pill pill-warn", "The 5 minute load is above the number of CPUs (2 CPUs).", "Needed", "Asked for by linux-image-6.8.0-50, libc6 and 3 more. Unattended upgrades reboot at 04:30.", "more than 3 days ago"}, nil},
		{"live free memory, no swap, no cgroup", func(m *vminfo.Machine) {
			m.Memory = vminfo.Memory{Total: 4 << 30, Available: 2 << 30, AvailableOK: true}
			m.Cgroup = vminfo.Cgroup{}
		}, []string{"2 GB available", "None set up"}, []string{"Used by loadout", "Not shown"}},
		{"hostile names", func(m *vminfo.Machine) {
			m.OS.Name = xss
			m.Reboot = vminfo.Reboot{Pending: true, Packages: []string{xss}}
		}, []string{"&lt;script&gt;alert(1)&lt;/script&gt;"}, []string{xss}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := fullReport()
			tt.change(&rep.Machine)
			p := serverSite(t, rep).as("root@example.com").get("/account/server")
			wantIn(t, p, tt.name, tt.want...)
			wantOut(t, p, tt.name, tt.absent...)
		})
	}
}

func TestServerPageWithoutMachine(t *testing.T) {
	rep := fullReport()
	rep.Machine, rep.MachineOn = vminfo.Machine{}, false
	p := serverSite(t, rep).as("root@example.com").get("/account/server")
	wantOut(t, p, "server page", "sys-h", ">System<")
	wantIn(t, p, "server page", "Storage", "Backups")
}

// wantOrder checks that each marker appears, in this order.
func wantOrder(t *testing.T, body string, markers ...string) {
	t.Helper()
	at := 0
	for _, m := range markers {
		i := strings.Index(body[at:], m)
		if i < 0 {
			t.Errorf("%q is missing or out of order", m)
			return
		}
		at += i + len(m)
	}
}

const servedPrint = "55:E5:72:3E:19:E4:D1:6A:30:D3:51:2C:1C:D1:BF:69:5D:C2:3A:31:BF:BF:E0:20:F1:98:E9:FE:F1:39:59:0D"

// fullCerts is a certificate that this machine trusts, with a month left.
func fullCerts(now time.Time) sitecert.Report {
	return sitecert.Report{Served: sitecert.Served{
		Names: []string{"registry.example.com"}, NotBefore: now.Add(-30 * 24 * time.Hour), NotAfter: now.Add(60 * 24 * time.Hour),
		Issuer: "Example Issuing CA", Fingerprint: servedPrint, Intermediate: true, InterNotAfter: time.Date(2027, 3, 12, 0, 0, 0, 0, time.UTC),
		Trusted: true,
	}}
}

func TestServerPageCertificates(t *testing.T) {
	p := serverSite(t, fullReport()).as("root@example.com").get("/account/server")
	wantIn(t, p, "certificates", `id="cert-h">Certificates<`,
		"Site certificate", ">Valid<", "registry.example.com", "Example Issuing CA", "Intermediate valid until 12 Mar 2027",
		`<code class="val">`+strings.ReplaceAll(servedPrint, ":", ":<wbr>")+`</code>`,
		`<span class="pill pill-ok">Yes</span>`, "Kept for 10 minutes")
	wantOut(t, p, "certificates", xss, "tile-bad", "Not trusted")
	// The certificate is the fourth tile, and the panels are in the planned order.
	wantOrder(t, p.body, ">Disk<", ">Last backup<", ">Site certificate<", ">Running for<")
	wantOrder(t, p.body, `id="host-h"`, `id="sys-h"`, `id="disk-h"`, `id="backup-h"`, `id="cert-h"`, `id="proc-h"`, `id="mem-h"`, `id="count-h"`, `id="mail-h"`)
}

func TestServerPageCertificateStates(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		change func(*sitecert.Report)
		want   []string
		absent []string
	}{
		{"not trusted here", func(r *sitecert.Report) {
			r.Served.Trusted = false
			r.Served.TrustProblem = "This machine's trust store does not accept the certificate chain."
		}, []string{"tile-warn", "Not trusted", "By this server&#39;s own certificates", `<span class="pill pill-warn">No</span>`, "trust store does not accept"}, []string{">Valid<"}},
		{"expired", func(r *sitecert.Report) {
			r.Served.Expired = true
			r.Served.NotAfter = now.Add(-time.Hour)
		}, []string{"tile-bad", ">Expired<", `<span class="pill pill-bad">Expired</span>`}, nil},
		{"under a week left", func(r *sitecert.Report) {
			r.Served.Expiring = true
			r.Served.NotAfter = now.Add(3*24*time.Hour + time.Hour)
		}, []string{"tile-warn", "Ending soon", "3 days left", "Ends in 3 days"}, nil},
		{"nothing served", func(r *sitecert.Report) {
			r.Served = sitecert.Served{Err: "the served certificate could not be read: connection refused"}
		}, []string{"tile-warn", "The served certificate cannot be read", "The served certificate could not be read: connection refused."}, []string{"Trusted here", "Fingerprint"}},
		{"hostile names", func(r *sitecert.Report) {
			r.Served.Issuer, r.Served.Names = xss, []string{xss}
		}, []string{"&lt;script&gt;alert(1)&lt;/script&gt;"}, []string{xss}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := fullReport()
			tt.change(&rep.Certs)
			p := serverSite(t, rep).as("root@example.com").get("/account/server")
			if p.status != http.StatusOK {
				t.Fatalf("GET /account/server = %d", p.status)
			}
			wantIn(t, p, tt.name, tt.want...)
			wantOut(t, p, tt.name, tt.absent...)
		})
	}
}

// TestServerPagePartsOffOrFailing: with the certificate and machine parts
// off, the rest still shows. With everything failing, each panel stays and
// says why.
func TestServerPagePartsOffOrFailing(t *testing.T) {
	rep := fullReport()
	rep.Certs, rep.CertsOn = sitecert.Report{}, false
	rep.Machine, rep.MachineOn = vminfo.Machine{}, false
	p := serverSite(t, rep).as("root@example.com").get("/account/server")
	wantIn(t, p, "parts off", ">Host<", ">Storage<", ">Process<", ">Registry<")
	wantOut(t, p, "parts off", ">System<", "Certificates", "Site certificate", "cert-h")

	rep = fullReport()
	rep.Certs = sitecert.Report{Served: sitecert.Served{Err: "the served certificate could not be read: connection refused"}}
	rep.Machine = vminfo.Machine{OS: vminfo.OS{Err: "It does not exist."}}
	p = serverSite(t, rep).as("root@example.com").get("/account/server")
	wantIn(t, p, "everything failing", ">System<", ">Certificates<", ">Storage<", ">Registry<", "connection refused.", "It does not exist.")
}

func TestServerPageBackupStates(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		change func(*serverstats.Backups)
		want   []string
		absent []string
	}{
		{"last run failed", func(b *serverstats.Backups) {
			b.Last = serverstats.LastRun{Finished: now.Add(-2 * time.Hour), Exit: 1}
		}, []string{`<span class="pill pill-warn">Failed</span>`, "Exit code 1", "The journal of loadout-backup says why.", "tile-warn", "The last run failed"}, []string{"Succeeded"}},
		{
			"no run recorded", func(b *serverstats.Backups) { b.Last = serverstats.LastRun{Missing: true} },
			[]string{"No run recorded yet"},
			[]string{"Succeeded", "tile-warn"},
		},
		{
			"status file unreadable", func(b *serverstats.Backups) { b.Last = serverstats.LastRun{Err: "The status file is incomplete."} },
			[]string{"The status file is incomplete."},
			[]string{"Succeeded"},
		},
		{"a database is missing", func(b *serverstats.Backups) {
			b.Contents = serverstats.Contents{Checked: true, Missing: []string{"memory.db"}}
		}, []string{`<span class="pill pill-warn">Incomplete</span>`, "Missing from the newest archive: memory.db."}, []string{"Both databases"}},
		{
			"archive not listed", func(b *serverstats.Backups) { b.Contents = serverstats.Contents{Err: "It is too large to list here."} },
			[]string{"Not listed", "It is too large to list here."},
			[]string{"Both databases"},
		},
		{"one copy", func(b *serverstats.Backups) { b.Copies, b.CopiesBytes = 1, 1<<10 }, []string{"1 copy", "1 KB in all"}, []string{"1 copies"}},
		{"no copies", func(b *serverstats.Backups) { b.Copies, b.CopiesBytes = 0, 0 }, []string{"0 copies", "Saved when a package is deleted on the web"}, nil},
		{"no archive yet but a failed run", func(b *serverstats.Backups) {
			*b = serverstats.Backups{Dir: "/var/backups/loadout", Last: serverstats.LastRun{Finished: now.Add(-3 * time.Hour), Exit: 1}}
		}, []string{"No archive in /var/backups/loadout yet.", "about 03:15", `<span class="pill pill-warn">Failed</span>`}, []string{"Newest holds"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := fullReport()
			tt.change(&rep.Backups)
			p := serverSite(t, rep).as("root@example.com").get("/account/server")
			if p.status != http.StatusOK {
				t.Fatalf("GET /account/server = %d", p.status)
			}
			wantIn(t, p, tt.name, tt.want...)
			wantOut(t, p, tt.name, tt.absent...)
		})
	}
}

func TestFingerprintBreaksOnlyAtColons(t *testing.T) {
	rep := fullReport()
	rep.Certs.Served.Fingerprint = "AA:BB<script>"
	p := serverSite(t, rep).as("root@example.com").get("/account/server")
	wantIn(t, p, "fingerprint", `<code class="val">AA:<wbr>BB&lt;script&gt;</code>`)
	wantOut(t, p, "fingerprint", "BB<script>")
}
