package memory

import "time"

// Limits of team memory. Each refusal names the limit it hit.
const (
	MaxName        = 64   // runes, one line
	MaxDescription = 150  // runes, one line
	MaxBody        = 4096 // bytes of UTF-8
	MaxLinks       = 10
	MaxNote        = 200 // runes, one line: update notes and retract reasons
	WritesPerDay   = 200 // per person, all namespaces

	// WriteWindow is the rolling period WritesPerDay counts over.
	WriteWindow = 24 * time.Hour

	// UnconfirmedMonths is how long a memory may go unchecked before it is
	// marked. It is never hidden.
	UnconfirmedMonths = 6

	// IndexLines caps the index a client receives, and IndexDescription cuts
	// the description on each line. IndexFresh is how recent an update must
	// be to put the entry first.
	IndexLines       = 150
	IndexDescription = 100
	IndexFresh       = 14 * 24 * time.Hour

	// MaxQueryWords caps the words of a search, MaxCandidates the entries
	// the duplicate check reads.
	MaxQueryWords = 16
	MaxCandidates = 20
)
