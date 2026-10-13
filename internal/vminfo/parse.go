package vminfo

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxText is the longest value of a text field. The files are root-owned, but
// the page still escapes and bounds what it shows.
const maxText = 255

// cleanText trims s and reports whether it is fit to show: not empty, at most
// maxText characters, printable.
func cleanText(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > maxText || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return s, true
}

// parseOSRelease returns PRETTY_NAME from the text of /etc/os-release, which
// holds shell-style KEY=value lines with optional quotes.
func parseOSRelease(data string) string {
	for line := range strings.Lines(data) {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "PRETTY_NAME" {
			continue
		}
		return unquote(val)
	}
	return ""
}

// unquote strips one pair of matching quotes and the backslashes the
// os-release format allows inside double quotes.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		q := v[0]
		v = v[1 : len(v)-1]
		if q == '"' {
			r := strings.NewReplacer(`\"`, `"`, `\\`, `\`, "\\`", "`", `\$`, `$`)
			v = r.Replace(v)
		}
	}
	return v
}

// meminfo holds the figures of /proc/meminfo this package uses, in bytes. A
// field is zero with its Has flag false when the file did not list it.
type meminfo struct {
	Total, Available, SwapTotal, SwapFree uint64
	HasTotal, HasAvailable, HasSwap       bool
}

// parseMeminfo reads "Name:   1234 kB" lines. The kernel always says kB, which
// means 1024 bytes. Lines in other units, or that do not parse, are skipped.
func parseMeminfo(data string) meminfo {
	var m meminfo
	var hasST, hasSF bool
	for line := range strings.Lines(data) {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) != 2 || f[1] != "kB" {
			continue
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil || kb > (1<<63)/1024 {
			continue
		}
		switch name {
		case "MemTotal":
			m.Total, m.HasTotal = kb*1024, true
		case "MemAvailable":
			m.Available, m.HasAvailable = kb*1024, true
		case "SwapTotal":
			m.SwapTotal, hasST = kb*1024, true
		case "SwapFree":
			m.SwapFree, hasSF = kb*1024, true
		}
	}
	m.HasSwap = hasST && hasSF
	return m
}

// parseCPUModel returns the first "model name" of /proc/cpuinfo.
func parseCPUModel(data string) string {
	for line := range strings.Lines(data) {
		key, val, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// parseHostFacts returns the one key this package reads from the file the
// deploy writes, cpu_model. Other lines are ignored.
func parseHostFacts(data string) string {
	for line := range strings.Lines(data) {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key == "cpu_model" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// parseCgroup returns the cgroup path of a cgroup v2 process, from the text
// of /proc/self/cgroup ("0::/system.slice/loadout.service"). Version 1 lists
// one line per controller and has no "0::" line, so it gives "", false.
func parseCgroup(data string) (string, bool) {
	for line := range strings.Lines(data) {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "0::")
		if !ok || path == "" || path[0] != '/' {
			continue
		}
		// The path is joined under /sys/fs/cgroup, so it may not climb out.
		for seg := range strings.SplitSeq(path, "/") {
			if seg == ".." {
				return "", false
			}
		}
		return path, true
	}
	return "", false
}

// parseAptConf reads lines of the form `Name "value";` from an apt.conf.d
// file and returns them by name. Comments start with //. The last line for a
// name wins, as it does for apt.
func parseAptConf(data string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(data) {
		line, _, _ = strings.Cut(line, "//")
		name, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		rest = strings.TrimSuffix(strings.TrimSpace(rest), ";")
		if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
			out[name] = rest[1 : len(rest)-1]
		}
	}
	return out
}

// validClock reports whether s is a 24 hour time of the form HH:MM.
func validClock(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	h, errH := strconv.Atoi(s[:2])
	m, errM := strconv.Atoi(s[3:])
	return errH == nil && errM == nil && h >= 0 && h < 24 && m >= 0 && m < 60
}

// validPackage reports whether s looks like a Debian package name, which
// keeps anything odd out of the page.
func validPackage(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+' || r == '-' || r == '.' || r == ':' || r == '_':
		default:
			return false
		}
	}
	return true
}
