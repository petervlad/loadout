package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/sitecert"
	"github.com/petervlad/loadout/internal/vminfo"
)

// unknown is the value of a fact that could not be read; its note says why.
const unknown = "Unknown"

// systemSection is the System panel: the operating system, kernel, CPUs,
// memory, load, uptime, a pending reboot and the last security updates. Each
// row fails on its own and says why.
func systemSection(m vminfo.Machine, now time.Time) sectionView {
	facts := []fact{
		osFact(m.OS),
		kernelFact(m.Kernel),
		cpuFact(m.CPU),
	}
	facts = append(facts, memoryFacts(m.Memory, m.Cgroup)...)
	facts = append(facts, loadFact(m.Load), upFact(m.Uptime), rebootFact(m.Reboot), updatesFact(m.Updates, now))
	return sectionView{Facts: facts, Hint: "Read each time the page loads."}
}

func osFact(o vminfo.OS) fact {
	if o.Err != "" {
		return fact{Label: "System", Value: unknown, Note: o.Err}
	}
	return fact{Label: "System", Value: o.Name}
}

func kernelFact(k vminfo.KernelInfo) fact {
	if k.Err != "" {
		return fact{Label: "Kernel", Value: unknown, Note: k.Err}
	}
	return fact{Label: "Kernel", Value: k.String()}
}

func cpuFact(c vminfo.CPU) fact {
	f := fact{Label: "CPUs", Value: plural(c.Count, "CPU")}
	if c.Err != "" {
		f.Note = "Model: " + lowerFirst(c.Err)
	} else {
		f.Note = c.Model
	}
	return f
}

// lowerFirst makes a reason fit after a colon: "Hidden by ..." becomes
// "hidden by ...".
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func memoryFacts(m vminfo.Memory, cg vminfo.Cgroup) []fact {
	if m.Err != "" {
		return []fact{{Label: "Memory", Value: unknown, Note: m.Err}}
	}
	facts := []fact{{Label: "Memory", Value: bytesText(int64(m.Total)) + " in all"}} //nolint:gosec // G115: memory is smaller than 8 EB
	free := fact{Label: "Free memory", Value: "Not shown", Note: m.AvailableErr}
	if m.AvailableOK {
		free.Value, free.Note = bytesText(int64(m.Available))+" available", "" //nolint:gosec // G115: memory is smaller than 8 EB
	}
	facts = append(facts, free)
	swap := fact{Label: "Swap", Value: "None set up"}
	if m.SwapTotal > 0 {
		swap.Value = bytesText(int64(m.SwapUsed)) + " in use" //nolint:gosec // G115: swap is smaller than 8 EB
		swap.Note = "of " + bytesText(int64(m.SwapTotal))     //nolint:gosec // G115: swap is smaller than 8 EB
	}
	facts = append(facts, swap)
	if cg.On {
		facts = append(facts, fact{Label: "Used by loadout", Value: bytesText(int64(cg.Used)), Note: "the service's own memory, the cache of its files included"}) //nolint:gosec // G115: memory is smaller than 8 EB
	}
	return facts
}

func loadFact(l vminfo.Load) fact {
	if l.Err != "" {
		return fact{Label: "Load", Value: unknown, Note: l.Err}
	}
	f := fact{
		Label: "Load",
		Value: fmt.Sprintf("%.2f, %.2f, %.2f", l.One, l.Five, l.Fifteen),
		Note:  fmt.Sprintf("over 1, 5 and 15 minutes, on %s", plural(l.CPUs, "CPU")),
	}
	if l.Warn {
		f.Pill, f.Note = "warn", "The 5 minute load is above the number of CPUs ("+plural(l.CPUs, "CPU")+")."
	}
	return f
}

func upFact(u vminfo.Uptime) fact {
	if u.Err != "" {
		return fact{Label: "Machine up", Value: unknown, Note: u.Err}
	}
	return fact{Label: "Machine up", Value: duration(u.For), Note: "since " + u.Since.UTC().Format("2 Jan 2006, 15:04 UTC")}
}

func rebootFact(r vminfo.Reboot) fact {
	auto := ""
	if r.AutoRebootAt != "" {
		prefix := "Unattended upgrades reboot at "
		if !strings.ContainsAny(r.AutoRebootAt[:1], "0123456789") {
			prefix = "Unattended upgrades reboot "
		}
		auto = prefix + r.AutoRebootAt + "."
	}
	f := fact{Label: "Reboot", Value: "Not needed", Note: auto}
	if r.Err != "" {
		f.Value, f.Note = unknown, r.Err
		return f
	}
	if !r.Pending {
		return f
	}
	f.Value, f.Pill = "Needed", "warn"
	var note []string
	if len(r.Packages) > 0 {
		list := strings.Join(r.Packages, ", ")
		if r.More > 0 {
			list += fmt.Sprintf(" and %d more", r.More)
		}
		note = append(note, "Asked for by "+list+".")
	}
	if auto != "" {
		note = append(note, auto)
	}
	f.Note = strings.Join(note, " ")
	return f
}

func updatesFact(u vminfo.Updates, now time.Time) fact {
	if u.Err != "" {
		return fact{Label: "Security updates", Value: unknown, Note: u.Err}
	}
	f := fact{Label: "Security updates", Value: "Ran " + ago(now, u.Last), Note: u.Last.UTC().Format("2 Jan 2006, 15:04 UTC")}
	if u.Stale {
		f.Pill, f.Note = "warn", "Last ran "+u.Last.UTC().Format("2 Jan 2006")+", more than 3 days ago."
	}
	return f
}

// certSentence turns a lower case reason of sitecert into a sentence.
func certSentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// certTile is the Site certificate tile. The worst thing wins: a certificate
// that cannot be read, an expired one, one this machine does not trust, then
// one that is about to run out.
func certTile(r sitecert.Report, now time.Time) statTile {
	t, s := statTile{Label: "Site certificate"}, r.Served
	switch {
	case s.Err != "":
		t.Value, t.Note, t.Tone = unknown, "The served certificate cannot be read", "warn"
	case s.Expired:
		t.Value, t.Note, t.Tone = "Expired", "Ended "+s.NotAfter.UTC().Format("2 Jan 2006, 15:04 UTC"), "bad"
	case !s.Trusted:
		t.Value, t.Note, t.Tone = "Not trusted", "By this server's own certificates", "warn"
	case s.Expiring:
		t.Value, t.Note, t.Tone = "Ending soon", duration(s.NotAfter.Sub(now))+" left", "warn"
	default:
		t.Value, t.Note = "Valid", "Until "+s.NotAfter.UTC().Format("2 Jan 2006")
	}
	return t
}

// certSection is the Certificates panel: the certificate the site really
// serves and whether this machine's trust store accepts it.
func certSection(r sitecert.Report, now time.Time) sectionView {
	s := r.Served
	if s.Err != "" {
		return sectionView{Facts: []fact{{Label: "Served", Value: unknown, Note: certSentence(s.Err)}}, Hint: certHint}
	}
	served := fact{Label: "Served", Value: strings.Join(s.Names, ", "), Note: "Valid from " + s.NotBefore.UTC().Format("2 Jan 2006, 15:04") + " until " + s.NotAfter.UTC().Format("2 Jan 2006, 15:04 UTC")}
	if len(s.Names) == 0 {
		served.Value = "No name in it"
	}
	switch {
	case s.Expired:
		served.Badge, served.BadgeTone = "Expired", "bad"
	case s.Expiring:
		served.Badge, served.BadgeTone = "Ends in "+duration(s.NotAfter.Sub(now)), "warn"
	}
	issuer := fact{Label: "Issued by", Value: s.Issuer}
	if s.Intermediate {
		issuer.Note = "Intermediate valid until " + s.InterNotAfter.UTC().Format("2 Jan 2006")
	}
	trusted := fact{Label: "Trusted here", Value: "Yes", Pill: "ok"}
	if !s.Trusted {
		trusted.Value, trusted.Pill, trusted.Note = "No", "warn", s.TrustProblem
	}
	return sectionView{
		Facts: []fact{served, issuer, {Label: "Fingerprint", Value: s.Fingerprint, Code: true, Note: "SHA-256"}, trusted},
		Hint:  certHint,
	}
}

const certHint = "Checked with a connection from this server to itself, against the system's own trusted certificates. A browser may trust more or fewer. Kept for 10 minutes."
