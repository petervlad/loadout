package cliupdate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
)

// The texts are built from fixed words, two parsed versions, the host as
// the person's own config names it and the install method's command, so a
// registry cannot put words in front of a person or an agent.

// Line is the one line for a, on stderr after a command: the too-old
// warning when a registry needs a newer loadout, else the newer release
// when this build is behind it, else empty. It ignores the clocks.
func (s Status) Line(a Audience) string {
	switch {
	case len(s.TooOld) > 0 && a == Agent:
		return "loadout, for the agent: " + s.tooOld("this is") + " Tell the user now, in one sentence, " + s.howTo() + " Do not update it yourself unless they ask."
	case len(s.TooOld) > 0:
		return "loadout: " + s.tooOld("this is") + " " + s.Method.Update()
	case !s.Behind():
		return ""
	case a == Agent:
		return fmt.Sprintf("loadout, for the agent: a newer loadout CLI is available (%s; this is %s). Tell the user once, in one sentence, %s Do not update it yourself unless they ask.", s.Latest, s.have(), s.howTo())
	}
	return fmt.Sprintf("loadout %s is out (you have %s). %s", s.Latest, s.have(), s.Method.Update())
}

// Session is what the session hooks say: the same news, for a person who
// may see it and an agent asked to pass it on in its first reply.
func (s Status) Session() target.SessionMessage {
	switch {
	case len(s.TooOld) > 0:
		return target.SessionMessage{
			Person: "loadout: " + s.tooOld("this is") + " " + s.Method.Update(),
			Agent:  s.tooOld("the installed one is") + " Tell the user in your first reply, in one sentence, " + s.howTo() + " Do not update it yourself unless they ask.",
		}
	case !s.Behind():
		return target.SessionMessage{}
	}
	return target.SessionMessage{
		Person: fmt.Sprintf("loadout %s is out (you have %s). %s", s.Latest, s.have(), s.Method.Update()),
		Agent:  fmt.Sprintf("A newer loadout CLI is available (%s; installed %s). Mention it to the user once, in one sentence, in your first reply, %s Do not update it yourself unless they ask.", s.Latest, s.have(), s.howTo()),
	}
}

// Footer is the sentence status prints below its table, with where and
// when the release was seen; empty when there is no news.
func (s Status) Footer() string {
	if len(s.TooOld) > 0 {
		return "loadout: " + s.tooOld("this is") + " " + s.Method.Update()
	}
	if !s.Behind() {
		return ""
	}
	return fmt.Sprintf("loadout %s is out (you have %s; seen on %s %s). %s", s.Latest, s.have(), s.From, s.Seen.Local().Format("2 Jan"), s.Method.Update())
}

// Version is what loadout version prints after its first line: the newer
// release, that this build is up to date, or nothing when no registry has
// said anything.
func (s Status) Version() string {
	switch {
	case len(s.TooOld) > 0 || s.Behind():
		return strings.TrimPrefix(s.Footer(), "loadout ")
	case s.Latest == (semver.Version{}):
		return ""
	}
	// A developer's build is not "up to date": it has no release to be.
	if _, known := s.Build.Release(); !known || s.Build.Modified() {
		return fmt.Sprintf("The newest release is %s (seen on %s %s).", s.Latest, s.From, s.Seen.Local().Format("2 Jan"))
	}
	return fmt.Sprintf("Up to date as of %s (the newest release on %s is %s).", s.Seen.Local().Format("2 Jan"), s.From, s.Latest)
}

// tooOld says which registry needs a newer loadout than this one: the one
// with the highest minimum.
func (s Status) tooOld(this string) string {
	worst := slices.MaxFunc(s.TooOld, func(a, b Advice) int { return a.Minimum.Compare(b.Minimum) })
	return fmt.Sprintf("%s needs loadout %s or newer and %s %s, so some commands may fail.", host(worst.URL), worst.Minimum, this, s.have())
}

// have names this build: its release, or the release it was built after.
func (s Status) have() string {
	v, _ := s.Build.Release()
	if s.Build.Exact() {
		return v.String()
	}
	return "a build after " + v.String()
}

// howTo ends an agent's instruction with the way to update.
func (s Status) howTo() string {
	if s.Method.InFolder {
		return "with the command to update it: " + s.Method.Command + ", in their loadout folder."
	}
	return "with the way to update it: " + s.Method.Command + "."
}

// Push is the title and message of a push notification.
func (s Status) Push() (title, message string) {
	update := s.Method.Update() + "."
	if len(s.TooOld) > 0 {
		worst := slices.MaxFunc(s.TooOld, func(a, b Advice) int { return a.Minimum.Compare(b.Minimum) })
		return fmt.Sprintf("loadout %s is too old for %s", s.have(), host(worst.URL)),
			fmt.Sprintf("It needs %s or newer, so some commands may fail, and %s is out. %s", worst.Minimum, s.Latest, update)
	}
	return fmt.Sprintf("loadout %s is out", s.Latest), fmt.Sprintf("You have %s. %s", s.have(), update)
}
