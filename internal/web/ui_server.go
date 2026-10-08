package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/hetzner"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/serverstats"
)

// ServerStats reports on the server and on everything the registry holds.
// The numbers cover every namespace, so only admins see them.
type ServerStats interface {
	Report(ctx context.Context) serverstats.Report
}

var _ ServerStats = (*serverstats.Collector)(nil)

// Thresholds that mark a tile: a disk this full, a backup this old.
const (
	diskWarnPercent = 80
	diskBadPercent  = 90
	backupWarnAge   = 36 * time.Hour
	trafficWarnPct  = 80
)

// serverView is the body of server.html.
type serverView struct {
	Checked time.Time
	Tiles   []statTile
	Hetzner hetznerView
	Host    sectionView
	// HostConsole is the stated console link, drawn as a link.
	HostConsole string
	Disk        diskView
	Storage     []fact
	Backups     sectionView
	Counts      sectionView
	Mail        sectionView
	Process     sectionView
}

// statTile is one number at the top of the page. Tone marks it "warn" or
// "bad"; the note always says why in words.
type statTile struct {
	Label, Value, Note, Tone string
}

// fact is one row of a panel's list; Pill shows the value as a pill of
// that tone ("ok", "warn").
type fact struct {
	Label, Value, Note, Pill string
}

// sectionView is a panel of facts, or a sentence when there are none.
type sectionView struct {
	Facts   []fact
	Message string
	Hint    string
}

// diskView is the meter of the data folder's filesystem.
type diskView struct {
	Shown   bool
	Percent int
	Text    string
	Err     string
}

type hetznerView struct {
	// State is "off" without a token, "error" when it could not be read,
	// else "ok".
	State    string
	Reason   string
	Facts    []fact
	Cost     []costRow
	Total    string
	VAT      string
	Unpriced string
	Checked  time.Time
}

type costRow struct {
	Label, Detail, Amount string
}

func (h *handler) serverPage(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(r)
	if !ok {
		h.toLogin(w, r)
		return
	}
	// Owners of one project must not see the others' numbers or the
	// infrastructure: the page does not exist for anyone but admins.
	if !u.IsAdmin || h.ui.Server == nil {
		h.notFound(w, r, u)
		return
	}
	places, err := h.ui.Places.Places(r.Context(), u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rep := h.ui.Server.Report(r.Context())
	h.renderUI(w, r, http.StatusOK, "server.html", u, places, "", uiView{
		Title: "Server", AccountCurrent: true, Page: newServerView(rep, h.ui.Now()),
	})
}

func newServerView(rep serverstats.Report, now time.Time) serverView {
	v := serverView{Checked: rep.At}
	v.Hetzner = newHetznerView(rep.Hetzner)
	v.Host, v.HostConsole = hostSection(rep.Host), rep.Host.Stated.Console
	v.Tiles = []statTile{firstTile(rep), diskTile(rep.Disk), backupTile(rep.Backups, now), uptimeTile(rep.Process)}
	v.Disk, v.Storage = storageFacts(rep.Disk, rep.Counts)
	v.Backups = backupSection(rep.Backups, now)
	v.Counts = countSection(rep.Counts)
	v.Mail = mailSection(rep.Mail, now)
	v.Process = processSection(rep.Process)
	return v
}

// firstTile is the cost with a Hetzner token, else the host, so a registry
// elsewhere is not pointed at Hetzner.
func firstTile(rep serverstats.Report) statTile {
	if rep.Hetzner.Configured {
		return costTile(rep.Hetzner)
	}
	return hostTile(rep.Host)
}

func hostTile(h serverstats.Host) statTile {
	t := statTile{Label: "Host", Value: h.Stated.Provider, Note: h.Stated.Region}
	if t.Value == "" {
		t.Value = h.Name
	}
	if t.Value == "" {
		t.Value = "Unknown"
	}
	if t.Note == "" && h.Stated.Provider != "" {
		t.Note = h.Name
	}
	return t
}

func costTile(hz serverstats.Hetzner) statTile {
	t := statTile{Label: "Estimated cost"}
	switch {
	case !hz.Configured:
		t.Value, t.Note = "Not set up", "No Hetzner token"
	case hz.Err != "":
		t.Value, t.Note, t.Tone = "Unavailable", "Hetzner did not answer", "warn"
	default:
		c := hz.Summary.Cost
		t.Value, t.Note = money(c.Currency, c.Total), "a month, with VAT"
	}
	return t
}

func diskTile(d serverstats.Disk) statTile {
	t := statTile{Label: "Disk"}
	if d.FSErr != "" {
		t.Value, t.Note, t.Tone = "Unknown", d.FSErr, "warn"
		return t
	}
	p := d.UsedPercent()
	t.Value = strconv.Itoa(p) + "% used"
	t.Note = bytesText(int64(d.FSFree)) + " free" //nolint:gosec // G115: a disk is smaller than 8 EB
	switch {
	case p >= diskBadPercent:
		t.Tone, t.Note = "bad", "Almost full: "+t.Note
	case p >= diskWarnPercent:
		t.Tone, t.Note = "warn", "Filling up: "+t.Note
	}
	return t
}

func backupTile(b serverstats.Backups, now time.Time) statTile {
	t := statTile{Label: "Last backup"}
	switch {
	case b.Off:
		t.Value, t.Note = "Not set up", "No backup folder"
	case b.Err != "":
		t.Value, t.Note, t.Tone = "Unknown", "Cannot read the folder", "warn"
	case b.Count == 0:
		t.Value, t.Note, t.Tone = "None yet", "No archive in the folder", "warn"
	default:
		t.Value, t.Note = ago(now, b.Latest), bytesText(b.LatestBytes)
		if now.Sub(b.Latest) > backupWarnAge {
			t.Tone, t.Note = "warn", "Older than a day and a half"
		}
	}
	return t
}

func uptimeTile(p serverstats.Process) statTile {
	return statTile{Label: "Running for", Value: duration(p.Uptime), Note: "Version " + p.Version}
}

func newHetznerView(hz serverstats.Hetzner) hetznerView {
	switch {
	case !hz.Configured:
		return hetznerView{State: "off"}
	case hz.Err != "":
		return hetznerView{State: "error", Reason: hz.Err}
	}
	s := hz.Summary
	srv := s.Server
	v := hetznerView{State: "ok", Checked: s.CheckedAt}
	status := fact{Label: "Status", Value: capitalize(srv.Status), Pill: "warn"}
	if srv.Status == "running" {
		status.Pill = "ok"
	}
	v.Facts = []fact{
		{Label: "Name", Value: srv.Name, Note: "id " + strconv.FormatInt(srv.ID, 10)},
		status,
		{Label: "Type", Value: strings.ToUpper(srv.Type.Name), Note: serverSize(srv.Type)},
		{Label: "Location", Value: place(srv.Location), Note: srv.Location.Name},
	}
	backups := fact{Label: "Backups", Value: "Off", Note: "Hetzner's daily server backups"}
	if srv.Backups {
		backups.Value = "On"
	}
	v.Facts = append(v.Facts, backups)
	var ips []string
	for _, ip := range s.PrimaryIPs {
		ips = append(ips, ip.IP)
	}
	if len(ips) == 0 {
		ips = []string{"None"}
	}
	v.Facts = append(v.Facts, fact{Label: "Public IPs", Value: ips[0], Note: strings.Join(ips[1:], ", ")})
	vols := fact{Label: "Volumes", Value: "None"}
	if len(s.Volumes) > 0 {
		var names []string
		for _, vol := range s.Volumes {
			names = append(names, fmt.Sprintf("%s, %d GB", vol.Name, vol.SizeGB))
		}
		vols.Value, vols.Note = names[0], strings.Join(names[1:], "; ")
	}
	v.Facts = append(v.Facts, vols)
	out := fact{Label: "Outgoing traffic", Value: "Not counted yet"}
	if srv.TrafficKnown {
		out.Value = bytesText(srv.OutgoingBytes)
		if srv.IncludedBytes > 0 {
			out.Note = "of " + bytesText(srv.IncludedBytes) + " included this billing period"
			if srv.OutgoingBytes*100/srv.IncludedBytes >= trafficWarnPct {
				out.Pill = "warn"
			}
		}
	}
	v.Facts = append(v.Facts, out, fact{Label: "Incoming traffic", Value: bytesText(srv.IngoingBytes), Note: "not billed"})

	c := s.Cost
	for _, l := range c.Lines {
		v.Cost = append(v.Cost, costRow{Label: l.Label, Detail: l.Detail, Amount: money(c.Currency, l.Monthly)})
	}
	v.Total = money(c.Currency, c.Total)
	v.VAT = c.VATRate
	if len(c.Unpriced) > 0 {
		v.Unpriced = "Not in the total, since the price list has no price for it: " + strings.Join(c.Unpriced, ", ") + "."
	}
	return v
}

// hostSection lists what the operator stated and what the machine says.
// Stated values that are empty leave their rows out; with none at all the
// panel says how to state them.
func hostSection(h serverstats.Host) sectionView {
	st := h.Stated
	var facts []fact
	add := func(label, value string) {
		if value != "" {
			facts = append(facts, fact{Label: label, Value: value})
		}
	}
	add("Provider", st.Provider)
	add("Region", st.Region)
	add("Size", st.Size)
	name := fact{Label: "Host name", Value: h.Name}
	if h.NameErr != "" {
		name.Value, name.Note = "Unknown", h.NameErr
	}
	system := fact{Label: "System", Value: h.OS, Note: h.Arch + ", " + plural(h.CPUs, "CPU") + " seen by loadout"}
	if h.OSErr != "" {
		system.Value, system.Note = "Unknown", h.OSErr
	}
	facts = append(facts, name, system)
	add("Address", h.PublicURL)
	add("Contact", st.Contact)
	hint := "Provider, region, size, contact and console are stated in loadout.env, not measured. Host name and system are those of the machine or container that runs loadout."
	if st == (serverstats.HostFacts{}) {
		hint = "Set LOADOUT_HOST_PROVIDER and the other LOADOUT_HOST_ variables in /etc/loadout/loadout.env to describe this host. " + hint
	}
	return sectionView{Facts: facts, Hint: hint}
}

func storageFacts(d serverstats.Disk, c serverstats.Counts) (diskView, []fact) {
	dv := diskView{Err: d.FSErr}
	if d.FSErr == "" {
		dv.Shown, dv.Percent = true, d.UsedPercent()
		dv.Text = fmt.Sprintf("%s used of %s, %s free", bytesText(int64(d.FSUsed())), bytesText(int64(d.FSTotal)), bytesText(int64(d.FSFree))) //nolint:gosec // G115: disks are smaller than 8 EB
	}
	db := fact{Label: "Database", Value: bytesText(d.DBBytes), Note: "registry.db"}
	switch {
	case d.DBErr != "":
		db.Value, db.Note = "Unknown", d.DBErr
	case d.WALBytes > 0:
		db.Note = "registry.db, and " + bytesText(d.WALBytes) + " of write-ahead log not yet merged into it"
	}
	facts := []fact{{Label: "Data folder", Value: d.DataDir}, db}
	if c.Err == "" {
		rc := c.Registry
		facts = append(facts, fact{
			Label: "In the database",
			Value: bytesText(rc.ReleaseBytes) + " of releases",
			Note:  bytesText(rc.ProposalBytes) + " of proposals, " + bytesText(rc.ReviewBytes) + " of review reports",
		})
	}
	all := fact{Label: "Whole folder", Value: bytesText(d.DataBytes), Note: plural(d.DataFiles, "file") + ", the log included"}
	if d.DataErr != "" {
		all.Note = d.DataErr
	}
	return dv, append(facts, all)
}

func backupSection(b serverstats.Backups, now time.Time) sectionView {
	switch {
	case b.Off:
		return sectionView{Message: "No backup folder is set. The service reads it from LOADOUT_BACKUP_DIR, which the systemd unit sets to /var/backups/loadout."}
	case b.Err != "":
		return sectionView{Message: "Cannot read " + b.Dir + ". " + b.Err}
	case b.Count == 0:
		return sectionView{Message: "No archive in " + b.Dir + " yet. The nightly timer writes the first one at about 03:15."}
	}
	return sectionView{
		Facts: []fact{
			{Label: "Latest", Value: ago(now, b.Latest), Note: b.LatestName + ", " + bytesText(b.LatestBytes)},
			{Label: "Kept", Value: plural(b.Count, "archive"), Note: bytesText(b.Bytes) + " in all"},
			{Label: "Folder", Value: b.Dir},
		},
		Hint: "The archives are on the same disk as the registry. Copy them off the server to survive losing it.",
	}
}

func countSection(c serverstats.Counts) sectionView {
	if c.Err != "" {
		return sectionView{Message: c.Err}
	}
	rc, ac := c.Registry, c.Accounts
	packages := 0
	var kinds []string
	for _, k := range pack.Kinds() {
		n := rc.Packages[k]
		packages += n
		if n > 0 {
			kinds = append(kinds, plural(n, kindNoun(k)))
		}
	}
	closed := rc.Proposals[registry.ProposalApproved] + rc.Proposals[registry.ProposalRejected] + rc.Proposals[registry.ProposalWithdrawn]
	return sectionView{Facts: []fact{
		{Label: "Projects", Value: strconv.Itoa(rc.Namespaces[registry.KindProject]), Note: plural(rc.Namespaces[registry.KindTeam], "team")},
		{Label: "Packages", Value: strconv.Itoa(packages), Note: strings.Join(kinds, ", ")},
		{Label: "Releases", Value: strconv.Itoa(rc.Releases)},
		{Label: "Proposals", Value: strconv.Itoa(rc.Proposals[registry.ProposalOpen]) + " open", Note: fmt.Sprintf("%d closed, %s", closed, plural(rc.Reviews, "review report"))},
		{Label: "Accounts", Value: strconv.Itoa(ac.Users), Note: fmt.Sprintf("%d verified, %s", ac.Verified, plural(ac.Admins, "admin"))},
		{
			Label: "Memberships", Value: strconv.Itoa(ac.Members[account.RoleOwner] + ac.Members[account.RoleMaintainer] + ac.Members[account.RoleMember]),
			Note: fmt.Sprintf("%s, %s, %s", plural(ac.Members[account.RoleOwner], "owner"), plural(ac.Members[account.RoleMaintainer], "maintainer"), plural(ac.Members[account.RoleMember], "member")),
		},
		{Label: "Signed in", Value: plural(ac.Sessions, "browser session"), Note: plural(rc.LoginTokens, "terminal login") + " from loadout login"},
		{Label: "Machine tokens", Value: strconv.Itoa(rc.MachineTokens), Note: "for CI and shared machines"},
	}}
}

// kindNoun names a kind in a count: "3 agents", "1 knowledge base".
func kindNoun(k pack.Kind) string {
	if k == pack.KindKB {
		return "knowledge base"
	}
	return string(k)
}

func mailSection(m serverstats.Mail, now time.Time) sectionView {
	if !m.On {
		return sectionView{Message: "Email sign-in is off, so no login mail goes out."}
	}
	failed := fact{Label: "Failed", Value: strconv.Itoa(m.Stats.Failed)}
	if m.Stats.Failed > 0 {
		failed.Pill = "warn"
		failed.Note = "last " + ago(now, m.Stats.LastFailure) + "; the server log says why"
	}
	return sectionView{
		Facts: []fact{
			{Label: "Waiting", Value: strconv.Itoa(m.Stats.Waiting)},
			{Label: "Sent", Value: strconv.Itoa(m.Stats.Sent)},
			failed,
		},
		Hint: "Counted since the server started.",
	}
}

func processSection(p serverstats.Process) sectionView {
	return sectionView{
		Facts: []fact{
			{Label: "Version", Value: p.Version, Note: p.GoVersion},
			{Label: "Started", Value: p.Started.UTC().Format("2 Jan 2006, 15:04 UTC"), Note: duration(p.Uptime) + " ago"},
			{Label: "Memory", Value: bytesText(int64(p.HeapBytes)) + " in use", Note: bytesText(int64(p.SysBytes)) + " taken from the system"}, //nolint:gosec // G115: memory is smaller than 8 EB
			{Label: "Goroutines", Value: strconv.Itoa(p.Goroutines)},
		},
		Hint: "Load and memory of the whole machine are not shown: the service's sandbox lets it see only its own process.",
	}
}

// bytesText writes a size the short way, in powers of 1024 as Hetzner and
// df do: 812 bytes, 4.2 KB, 1.5 MB, 38.1 GB, 20 TB.
func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return plural(int(n), "byte")
	}
	v, i := float64(n), -1
	for v >= unit && i < 3 {
		v /= unit
		i++
	}
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return s + " " + [...]string{"KB", "MB", "GB", "TB"}[i]
}

// money writes an amount with its currency: €4.75, $4.75, 4.75 CHF.
func money(currency string, v float64) string {
	amount := strconv.FormatFloat(v, 'f', 2, 64)
	switch currency {
	case "EUR":
		return "€" + amount
	case "USD":
		return "$" + amount
	}
	return strings.TrimSpace(amount + " " + currency)
}

// duration says how long, in its largest unit: "5 minutes", "3 hours",
// "12 days".
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	}
	return plural(int(d.Hours()/24), "day")
}

func serverSize(t hetzner.ServerType) string {
	cpu := plural(t.Cores, "vCPU")
	if t.CPUType != "" {
		cpu = fmt.Sprintf("%d %s %s", t.Cores, t.CPUType, strings.TrimPrefix(cpu, strconv.Itoa(t.Cores)+" "))
	}
	mem := strings.TrimSuffix(strconv.FormatFloat(t.MemoryGB, 'f', 1, 64), ".0")
	return fmt.Sprintf("%s, %s GB memory, %d GB disk", cpu, mem, t.DiskGB)
}

func place(l hetzner.Location) string {
	switch {
	case l.City != "" && l.Country != "":
		return l.City + ", " + l.Country
	case l.City != "":
		return l.City
	}
	return l.Name
}

func capitalize(s string) string {
	if s == "" {
		return "Unknown"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
