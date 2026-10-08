package main

import (
	"testing"

	"github.com/petervlad/loadout/internal/buildinfo"
)

// TestAdvertisedIsAnExactRelease: a registry tells CLIs about its release
// only when it was built exactly from a clean tag.
func TestAdvertisedIsAnExactRelease(t *testing.T) {
	tests := map[string]string{
		"v0.1.0":            "0.1.0",
		"0.1.0":             "0.1.0",
		"v0.1.0-3-gabc1234": "0.0.0",
		"v0.1.0-dirty":      "0.0.0",
		"b42ddb9":           "0.0.0",
		"dev":               "0.0.0",
		"v1.0.0-rc.1":       "0.0.0",
		"v1.2.3.4":          "0.0.0",
		"v01.2.3":           "0.0.0",
		"v0.0.0":            "0.0.0",
		"v1.2.3+build":      "0.0.0",
	}
	for in, want := range tests {
		if got := advertised(buildinfo.Current(in, nil, false)).String(); got != want {
			t.Errorf("advertised(%s) = %s, want %s", in, got, want)
		}
	}
}
