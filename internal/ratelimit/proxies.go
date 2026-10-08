package ratelimit

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Proxies says which peers may name the client, and in which header. It is
// for a proxy that is not on this machine (a container platform, a tunnel,
// a load balancer): without it every request would carry the proxy's address
// and the per-address limits would become one bucket for everyone. The zero
// value trusts X-Real-IP from a loopback peer only, as ClientIP does.
type Proxies struct {
	header  string
	trusted []netip.Prefix
}

// NewProxies returns the rule "a request from a peer inside trusted names its
// client in header". It refuses what lets clients choose their own key: no
// header, a header that holds a list (X-Forwarded-For, Forwarded), and
// prefixes that cover every address. No prefixes at all is the zero value.
func NewProxies(header string, trusted []netip.Prefix) (Proxies, error) {
	if len(trusted) == 0 && header == "" {
		return Proxies{}, nil
	}
	if header == "" {
		return Proxies{}, errors.New("name the header that carries the client address, for example Fly-Client-IP or CF-Connecting-IP")
	}
	if len(trusted) == 0 {
		return Proxies{}, errors.New("a client address header needs the proxies that may set it: list their addresses or ranges")
	}
	if !validHeaderName(header) {
		return Proxies{}, fmt.Errorf("%q is not a header name", header)
	}
	switch strings.ToLower(header) {
	case "x-forwarded-for", "forwarded":
		return Proxies{}, fmt.Errorf("%s holds a list of addresses that clients can extend; use the single-address header your proxy sets (Fly-Client-IP, CF-Connecting-IP, True-Client-IP, X-Real-IP)", header)
	}
	out := make([]netip.Prefix, 0, len(trusted))
	for _, p := range trusted {
		if !p.IsValid() {
			return Proxies{}, errors.New("a trusted proxy range is not valid")
		}
		if p.Bits() == 0 {
			return Proxies{}, fmt.Errorf("%s would trust every peer, and then clients choose their own rate-limit key; list only your proxies", p)
		}
		out = append(out, p.Masked())
	}
	return Proxies{header: http.CanonicalHeaderKey(header), trusted: out}, nil
}

// Header is the header the client address is read from; empty for the zero
// value.
func (p Proxies) Header() string { return p.header }

// Trusted returns a copy of the trusted ranges.
func (p Proxies) Trusted() []netip.Prefix { return append([]netip.Prefix(nil), p.trusted...) }

// Wrap sets r.RemoteAddr to the address in the header when the peer is in a
// trusted range and the header holds exactly one valid address; any other
// request goes on untouched. ClientIP and ClientKey then work as before.
func (p Proxies) Wrap(next http.Handler) http.Handler {
	if p.header == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if client, ok := p.client(r); ok {
			r = r.WithContext(r.Context()) // a copy: the caller's request stays as it was
			r.RemoteAddr = client
			// ClientIP trusts X-Real-IP from a loopback peer; the client
			// named here may be one, so the header must not apply again.
			r.Header = r.Header.Clone()
			r.Header.Del("X-Real-IP")
		}
		next.ServeHTTP(w, r)
	})
}

// client returns the new RemoteAddr, keeping the peer's port.
func (p Proxies) client(r *http.Request) (string, bool) {
	host, port, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", false
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !p.trustsPeer(peer.Unmap()) {
		return "", false
	}
	values := r.Header.Values(p.header)
	if len(values) != 1 {
		return "", false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(values[0]))
	if err != nil || addr.Zone() != "" {
		return "", false
	}
	return net.JoinHostPort(addr.Unmap().String(), port), true
}

func (p Proxies) trustsPeer(peer netip.Addr) bool {
	for _, prefix := range p.trusted {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

// validHeaderName accepts letters, digits and hyphens, which every header
// the platforms use is made of.
func validHeaderName(h string) bool {
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}
