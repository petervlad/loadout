package guide_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os/exec"
	"reflect"
	"testing"
)

// backCase is one visit to a docs page: its address, the registry page
// kept for the tab, and the links on the page besides "loadout docs".
type backCase struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Stored    string   `json:"stored,omitempty"`
	NoStorage bool     `json:"noStorage,omitempty"`
	Links     []string `json:"links"`
}

// backResult is where the links point after docs.js ran, and the page it
// keeps for the tab.
type backResult struct {
	Brand  string   `json:"brand"`
	Links  []string `json:"links"`
	Stored string   `json:"stored"`
}

// TestBackLink runs docs.js in Node: "loadout docs" leads back to the
// registry page in ?from, or else to the one kept for the tab, never to
// another site, and links to the other docs pages carry the way back.
// Skipped where Node is not installed.
func TestBackLink(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	const (
		docs  = "https://loadout.lat/docs/"
		wiki  = "https://demo.loadout.lat/t/bro/wiki"
		skill = "https://demo.loadout.lat/skills"
		local = "http://localhost:7431/t/lime?kind=skill"
	)
	links := []string{"/docs/", "/docs/whats-new/", "#install", "https://loadout.lat/docs/whats-new/#top", "mailto:dana@example.com", "https://example.com/docs/"}
	keep := func(from string) []string {
		q := "?from=" + url.QueryEscape(from)
		return []string{"/docs/" + q, "/docs/whats-new/" + q, "#install", "/docs/whats-new/" + q + "#top", "mailto:dana@example.com", "https://example.com/docs/"}
	}
	// A registry may serve its own docs: the page it names is on the
	// same origin as the docs, which is safe whatever the host.
	const (
		ownDocs = "https://203.0.113.7/docs/"
		own     = "https://203.0.113.7/t/lime?kind=agent"
	)
	// On another origin the absolute link to loadout.lat is not a docs page
	// of this site, so it is left alone.
	keepHere := func(from string) []string {
		l := keep(from)
		l[3] = links[3]
		return l
	}
	tests := []struct {
		in   backCase
		want backResult
	}{
		{backCase{Name: "from", URL: docs + "?from=" + url.QueryEscape(wiki), Links: links}, backResult{wiki, keep(wiki), wiki}},
		{backCase{Name: "kept for the tab", URL: docs + "#install", Stored: wiki, Links: links}, backResult{wiki, keep(wiki), wiki}},
		{backCase{Name: "a new from replaces the kept one", URL: docs + "whats-new/?from=" + url.QueryEscape(skill), Stored: wiki, Links: links}, backResult{skill, keep(skill), skill}},
		{backCase{Name: "local registry", URL: docs + "?from=" + url.QueryEscape(local), Links: links}, backResult{local, keep(local), local}},
		{backCase{Name: "another site in from", URL: docs + "?from=" + url.QueryEscape("https://evil.example/x"), Stored: wiki, Links: links}, backResult{wiki, keep(wiki), wiki}},
		{backCase{Name: "look-alike host", URL: docs + "?from=" + url.QueryEscape("https://evilloadout.lat/x"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "javascript in from", URL: docs + "?from=" + url.QueryEscape("javascript:alert(1)"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "another site kept", URL: docs, Stored: "https://evil.example/x", Links: links}, backResult{"#top", links, "https://evil.example/x"}},
		{backCase{Name: "same origin, private IP", URL: ownDocs + "?from=" + url.QueryEscape(own), Links: links}, backResult{own, keepHere(own), own}},
		{backCase{Name: "same origin kept for the tab", URL: ownDocs, Stored: own, Links: links}, backResult{own, keepHere(own), own}},
		{backCase{Name: "same origin with a port", URL: "http://registry.corp:8080/docs/?from=" + url.QueryEscape("http://registry.corp:8080/skills"), Links: links}, backResult{"http://registry.corp:8080/skills", keepHere("http://registry.corp:8080/skills"), "http://registry.corp:8080/skills"}},
		{backCase{Name: "another private IP", URL: ownDocs + "?from=" + url.QueryEscape("https://203.0.113.8/t/lime"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "same host, another port", URL: ownDocs + "?from=" + url.QueryEscape("https://203.0.113.7:8443/t/lime"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "same host, another scheme", URL: ownDocs + "?from=" + url.QueryEscape("http://203.0.113.7/t/lime"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "another https host on a registry with its own docs", URL: ownDocs + "?from=" + url.QueryEscape("https://evil.example/x"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "another private IP kept", URL: ownDocs, Stored: "https://203.0.113.8/t/lime", Links: links}, backResult{"#top", links, "https://203.0.113.8/t/lime"}},
		{backCase{Name: "javascript in from on a registry with its own docs", URL: ownDocs + "?from=" + url.QueryEscape("javascript:alert(1)"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "data in from on a registry with its own docs", URL: ownDocs + "?from=" + url.QueryEscape("data:text/html,<script>alert(1)</script>"), Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "nothing to go back to", URL: docs, Links: links}, backResult{"#top", links, ""}},
		{backCase{Name: "no storage, from", URL: docs + "?from=" + url.QueryEscape(wiki), NoStorage: true, Links: links}, backResult{wiki, keep(wiki), ""}},
		{backCase{Name: "no storage, no from", URL: docs, NoStorage: true, Links: links}, backResult{"#top", links, ""}},
	}
	var in []backCase
	for _, tt := range tests {
		in = append(in, tt.in)
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, "testdata/back.cjs", "docs.js")
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var got []backResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got) != len(tests) {
		t.Fatalf("got %d results for %d cases", len(got), len(tests))
	}
	for i, tt := range tests {
		if !reflect.DeepEqual(got[i], tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.in.Name, got[i], tt.want)
		}
	}
}
