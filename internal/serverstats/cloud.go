package serverstats

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CloudProvider reads the machine from the cloud it runs in, for the
// optional cloud panels of the server page. Hetzner is the built-in one
// (internal/hetzner). Another provider is one type with these two methods
// plus a factory in cmd/loadout/cloud.go, see "Adding a cloud provider" in
// docs/DESIGN.md.
//
// A provider only reads. It must bound its own time, keep its credentials
// out of errors and logs, and cache when its API is slow or priced, since
// the page asks on every load.
type CloudProvider interface {
	// Name is the provider as an admin says it: "Hetzner". It titles the
	// panel ("Hetzner server") and starts the sentence when it is down.
	Name() string
	// Summary reads the facts. An error's text is shown to the admin as it
	// is, so it is one plain sentence that says what to check, and holds
	// no secret.
	Summary(ctx context.Context) (CloudSummary, error)
}

// CloudSummary is what a provider knows about the machine.
type CloudSummary struct {
	CheckedAt time.Time
	// Facts are the rows of the first panel, in order.
	Facts []CloudFact
	// Cost is nil when the provider has no estimate; the cost panel and
	// the cost tile are then left out.
	Cost *CloudCost
}

// CloudFact is one row. Tone shows the value as a pill: "ok" or "warn",
// empty for plain text.
type CloudFact struct {
	Label, Value, Note, Tone string
}

// CloudCost is a monthly estimate.
type CloudCost struct {
	// Currency is an ISO code such as EUR.
	Currency string
	Total    float64
	Lines    []CloudCostLine
	// Unpriced is a sentence about what the total leaves out, or empty.
	Unpriced string
	// Note says where the figures come from and how long they are kept.
	Note string
}

// CloudCostLine is one thing that costs money each month.
type CloudCostLine struct {
	Label, Detail string
	Monthly       float64
}

// Cloud is the cloud part of a report.
type Cloud struct {
	// Configured is false without a provider.
	Configured bool
	// Name is the provider's name.
	Name    string
	Summary CloudSummary
	// Err is a plain reason when it could not be read.
	Err string
}

func (c *Collector) cloud(ctx context.Context) Cloud {
	p := c.cfg.Cloud
	if p == nil {
		return Cloud{}
	}
	s, err := p.Summary(ctx)
	if err != nil {
		return Cloud{Configured: true, Name: p.Name(), Err: err.Error()}
	}
	return Cloud{Configured: true, Name: p.Name(), Summary: s}
}

// ByteText writes a size the short way, in powers of 1024 as df does:
// 812 bytes, 4.2 KB, 1.5 MB, 38.1 GB, 20 TB.
func ByteText(n int64) string {
	const unit = 1024
	if n < unit {
		if n == 1 {
			return "1 byte"
		}
		return fmt.Sprintf("%d bytes", n)
	}
	v, i := float64(n), -1
	for v >= unit && i < 3 {
		v /= unit
		i++
	}
	s := strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
	return s + " " + [...]string{"KB", "MB", "GB", "TB"}[i]
}
