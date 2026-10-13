package memory

import "time"

// SpaceStats is what one namespace's memory holds, as numbers. It never
// carries a name, an author or any text, so an operator's page can show it
// to someone who may not read the memories.
type SpaceStats struct {
	Namespace string
	Enabled   bool
	// Active and Retracted count entries, Versions every version of them.
	Active, Retracted, Versions int
	// Newest is when the last save or update was made; zero if none.
	Newest time.Time
	// Bytes is the text of the entries as they are now: name, description
	// and body.
	Bytes int64
}

// Stats is the numbers about all of memory: a row for each namespace that
// has a switch row, and the memory tokens that can still be used.
type Stats struct {
	Spaces      []SpaceStats // by namespace
	ValidTokens int
}
