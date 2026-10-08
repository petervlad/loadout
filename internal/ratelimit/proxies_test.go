package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/ratelimit"
)

func prefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range ss {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestNewProxiesRefusals(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		trusted []string
		want    string // empty: accepted
	}{
		{"fly", "Fly-Client-IP", []string{"203.0.113.0/24"}, ""},
		{"lower case header", "cf-connecting-ip", []string{"198.51.100.7/32", "2001:db8::/32"}, ""},
		{"nothing is the zero value", "", nil, ""},
		{"prefixes without a header", "", []string{"203.0.113.0/24"}, "name the header"},
		{"header without prefixes", "Fly-Client-IP", nil, "needs the proxies"},
		{"x-forwarded-for", "X-Forwarded-For", []string{"203.0.113.0/24"}, "holds a list"},
		{"x-forwarded-for any case", "x-forwarded-for", []string{"203.0.113.0/24"}, "holds a list"},
		{"forwarded", "Forwarded", []string{"203.0.113.0/24"}, "holds a list"},
		{"everyone v4", "Fly-Client-IP", []string{"0.0.0.0/0"}, "every peer"},
		{"everyone v6", "Fly-Client-IP", []string{"203.0.113.0/24", "::/0"}, "every peer"},
		{"not a header name", "Fly Client", []string{"203.0.113.0/24"}, "not a header name"},
		{"colon in the name", "Fly:IP", []string{"203.0.113.0/24"}, "not a header name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ratelimit.NewProxies(tt.header, prefixes(t, tt.trusted...))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewProxies() = %v, want no error", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewProxies() = %v, want an error naming %q", err, tt.want)
			}
		})
	}
}

func TestProxiesAreCopiedAndCanonical(t *testing.T) {
	in := prefixes(t, "203.0.113.9/24")
	p, err := ratelimit.NewProxies("fly-client-ip", in)
	if err != nil {
		t.Fatal(err)
	}
	in[0] = netip.MustParsePrefix("10.0.0.0/8")
	got := p.Trusted()
	if p.Header() != "Fly-Client-Ip" || len(got) != 1 || got[0].String() != "203.0.113.0/24" {
		t.Fatalf("Header() = %q, Trusted() = %v; want the canonical header and the masked range", p.Header(), got)
	}
	got[0] = netip.MustParsePrefix("10.0.0.0/8")
	if p.Trusted()[0].String() != "203.0.113.0/24" {
		t.Error("Trusted() hands out the inside of the value")
	}
}

func TestProxiesWrap(t *testing.T) {
	proxies, err := ratelimit.NewProxies("Fly-Client-IP", prefixes(t, "203.0.113.0/24", "2001:db8::/32"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		peer   string
		header []string // values of Fly-Client-IP
		xreal  string
		want   string // ClientIP
	}{
		{"trusted peer, one address", "203.0.113.5:4000", []string{"198.51.100.20"}, "", "198.51.100.20"},
		{"trusted peer, spaces around", "203.0.113.5:4000", []string{" 198.51.100.20 "}, "", "198.51.100.20"},
		{"trusted v6 peer, v6 client", "[2001:db8::1]:4000", []string{"2001:db8:ffff::9"}, "", "2001:db8:ffff::9"},
		{"trusted peer, mapped client", "203.0.113.5:4000", []string{"::ffff:198.51.100.20"}, "", "198.51.100.20"},
		{"mapped peer is still trusted", "[::ffff:203.0.113.5]:4000", []string{"198.51.100.20"}, "", "198.51.100.20"},
		{"untrusted peer names a client", "192.0.2.1:4000", []string{"198.51.100.20"}, "", "192.0.2.1"},
		{"trusted peer, no header", "203.0.113.5:4000", nil, "", "203.0.113.5"},
		{"two header values", "203.0.113.5:4000", []string{"198.51.100.20", "198.51.100.21"}, "", "203.0.113.5"},
		{"a list in one value", "203.0.113.5:4000", []string{"198.51.100.20, 198.51.100.21"}, "", "203.0.113.5"},
		{"garbage", "203.0.113.5:4000", []string{"not an address"}, "", "203.0.113.5"},
		{"empty", "203.0.113.5:4000", []string{""}, "", "203.0.113.5"},
		{"zone", "203.0.113.5:4000", []string{"fe80::1%eth0"}, "", "203.0.113.5"},
		{"peer without a port", "203.0.113.5", []string{"198.51.100.20"}, "", "203.0.113.5"},
		// A loopback client named by the proxy must not make X-Real-IP count.
		{"loopback client, spoofed X-Real-IP", "203.0.113.5:4000", []string{"127.0.0.1"}, "198.51.100.99", "127.0.0.1"},
		// The proxies are not loopback, so X-Real-IP from them is not read.
		{"untrusted peer, X-Real-IP", "192.0.2.1:4000", nil, "198.51.100.99", "192.0.2.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := proxies.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ratelimit.ClientIP(r) }))
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			r.RemoteAddr = tt.peer
			for _, v := range tt.header {
				r.Header.Add("Fly-Client-IP", v)
			}
			if tt.xreal != "" {
				r.Header.Set("X-Real-IP", tt.xreal)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			if got != tt.want {
				t.Errorf("ClientIP() = %q, want %q", got, tt.want)
			}
			if r.RemoteAddr != tt.peer || r.Header.Get("X-Real-IP") != tt.xreal {
				t.Errorf("the caller's request changed: RemoteAddr %q, X-Real-IP %q", r.RemoteAddr, r.Header.Get("X-Real-IP"))
			}
		})
	}
}

// The zero value changes nothing: Caddy on this machine is still trusted
// through X-Real-IP, and nobody else.
func TestZeroProxiesWrapIsTransparent(t *testing.T) {
	var p ratelimit.Proxies
	for peer, want := range map[string]string{
		"127.0.0.1:1": "198.51.100.20",
		"192.0.2.1:1": "192.0.2.1",
		"[::1]:1":     "198.51.100.20",
	} {
		var got string
		h := p.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ratelimit.ClientIP(r) }))
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Real-IP", "198.51.100.20")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != want {
			t.Errorf("peer %s: ClientIP() = %q, want %q", peer, got, want)
		}
	}
}
