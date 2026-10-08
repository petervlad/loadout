// Package hetzner reads the server the registry runs on from the Hetzner
// Cloud API, for the server page only registry admins see: its type,
// location, traffic, backups, volumes and primary IPs, and a monthly cost
// estimated from Hetzner's list prices (the API has no invoices).
//
// It only sends GET requests, with a token that should be read-only, and
// only to api.hetzner.cloud (or a loopback address, for tests and
// previews). The token is never logged, rendered or put in an error.
package hetzner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIURL is the Hetzner Cloud API.
const APIURL = "https://api.hetzner.cloud/v1"

// Timeout bounds one refresh, every request included, so the page that
// asks stays fast.
const Timeout = 5 * time.Second

// How long an answer is reused: a success for a few minutes, a failure
// for less, so a fixed token shows up soon without hammering the API.
const (
	cacheFor     = 5 * time.Minute
	failCacheFor = 30 * time.Second
)

// maxBody caps one API answer; /pricing, the largest, is about 50 KB.
const maxBody = 4 << 20

// Config configures a Client.
type Config struct {
	// Token is a Hetzner Cloud API token of the project the server is in.
	// Read-only is enough.
	Token string
	// Server is the server's id or name. Empty: the server whose public
	// IPv4 is this machine's.
	Server string
	// BaseURL is APIURL unless set; anything else must be plain http on a
	// loopback address (tests, previews), so the token never goes
	// elsewhere.
	BaseURL string
	// HTTP sends the requests; nil uses a client with Timeout.
	HTTP *http.Client
	// Now is time.Now unless set.
	Now func() time.Time
	// LocalIPs lists this machine's addresses, to find the server by its
	// IPv4; nil asks the system.
	LocalIPs func(context.Context) []netip.Addr
	// Log gets failures (never the token); nil discards them.
	Log *slog.Logger
}

// Client reads the server and prices, and keeps the answer for a few
// minutes.
type Client struct {
	cfg  Config
	base string

	mu     sync.Mutex
	cached Summary
	err    error
	until  time.Time
}

// New checks cfg and returns a client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("hetzner: no token")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = APIURL
	}
	if base != APIURL && !loopbackURL(base) {
		return nil, fmt.Errorf("hetzner: API URL must be %s, or plain http on a loopback address for tests", APIURL)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: Timeout}
	}
	// Never follow a redirect: the token goes to the API and nowhere else.
	h := *cfg.HTTP
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.HTTP = &h
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.LocalIPs == nil {
		cfg.LocalIPs = localIPv4s
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	cfg.Server = strings.TrimSpace(cfg.Server)
	return &Client{cfg: cfg, base: base}, nil
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return false
	}
	a, err := netip.ParseAddr(u.Hostname())
	return err == nil && a.IsLoopback()
}

// Summary is what the page shows about the server.
type Summary struct {
	CheckedAt  time.Time
	Server     Server
	Volumes    []Volume
	PrimaryIPs []PrimaryIP
	Cost       Cost
}

// Server is the registry's server.
type Server struct {
	ID       int64
	Name     string
	Status   string
	Created  time.Time
	Type     ServerType
	Location Location
	// Backups is true when Hetzner's daily backups are on.
	Backups bool
	// Traffic in bytes this billing period. TrafficKnown is false when
	// Hetzner has no number yet.
	OutgoingBytes int64
	IngoingBytes  int64
	IncludedBytes int64
	TrafficKnown  bool
	IPv4, IPv6    string
}

// ServerType is the server's size.
type ServerType struct {
	Name        string
	Description string
	Cores       int
	MemoryGB    float64
	DiskGB      int
	CPUType     string
}

// Location is where the server runs.
type Location struct {
	Name, City, Country string
}

// Volume is block storage attached to the server.
type Volume struct {
	ID       int64
	Name     string
	SizeGB   int
	Location string
}

// PrimaryIP is a public address of the server.
type PrimaryIP struct {
	ID       int64
	Type     string // ipv4 or ipv6
	IP       string
	Location string
}

// Summary returns the server and its estimated cost, from the cache when
// it is fresh. A failure is returned as an error whose Reason is a plain
// sentence for the page.
func (c *Client) Summary(ctx context.Context) (Summary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Now()
	if now.Before(c.until) {
		return c.cached, c.err
	}
	// The refresh outlives a page load that gives up, so its answer is
	// still cached for the next one.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), Timeout)
	defer cancel()
	s, err := c.fetch(ctx)
	if err != nil {
		c.cfg.Log.Warn("hetzner: cannot read the server", "err", err)
		c.cached, c.err, c.until = Summary{}, err, now.Add(failCacheFor)
		return Summary{}, err
	}
	s.CheckedAt = now
	c.cached, c.err, c.until = s, nil, now.Add(cacheFor)
	return s, nil
}

func (c *Client) fetch(ctx context.Context) (Summary, error) {
	srv, err := c.findServer(ctx)
	if err != nil {
		return Summary{}, err
	}
	var s Summary
	s.Server = srv.toServer()
	for _, id := range srv.Volumes {
		var v struct {
			Volume apiVolume `json:"volume"`
		}
		if err := c.get(ctx, "/volumes/"+strconv.FormatInt(id, 10), &v); err != nil {
			return Summary{}, fmt.Errorf("volume %d: %w", id, err)
		}
		s.Volumes = append(s.Volumes, Volume{ID: v.Volume.ID, Name: v.Volume.Name, SizeGB: v.Volume.Size, Location: v.Volume.Location.Name})
	}
	for _, ip := range []*apiServerIP{srv.PublicNet.IPv4, srv.PublicNet.IPv6} {
		if ip == nil || ip.ID == 0 {
			continue
		}
		var p struct {
			PrimaryIP apiPrimaryIP `json:"primary_ip"`
		}
		if err := c.get(ctx, "/primary_ips/"+strconv.FormatInt(ip.ID, 10), &p); err != nil {
			return Summary{}, fmt.Errorf("primary IP %d: %w", ip.ID, err)
		}
		s.PrimaryIPs = append(s.PrimaryIPs, p.PrimaryIP.toPrimaryIP(s.Server.Location.Name))
	}
	var pr struct {
		Pricing Pricing `json:"pricing"`
	}
	if err := c.get(ctx, "/pricing", &pr); err != nil {
		return Summary{}, fmt.Errorf("pricing: %w", err)
	}
	s.Cost = Estimate(s.Server, s.Volumes, s.PrimaryIPs, pr.Pricing)
	return s, nil
}

// findServer looks the server up by id, by name, or by this machine's
// public IPv4.
func (c *Client) findServer(ctx context.Context) (apiServer, error) {
	if id, err := strconv.ParseInt(c.cfg.Server, 10, 64); err == nil && id > 0 {
		var one struct {
			Server apiServer `json:"server"`
		}
		err := c.get(ctx, "/servers/"+strconv.FormatInt(id, 10), &one)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return apiServer{}, &notFoundError{fmt.Sprintf("No server with id %d in the token's project.", id)}
		}
		return one.Server, err
	}
	if c.cfg.Server != "" {
		var list serverList
		if err := c.get(ctx, "/servers?"+url.Values{"name": {c.cfg.Server}}.Encode(), &list); err != nil {
			return apiServer{}, err
		}
		if len(list.Servers) == 0 {
			return apiServer{}, &notFoundError{fmt.Sprintf("No server named %s in the token's project.", c.cfg.Server)}
		}
		return list.Servers[0], nil
	}
	mine := map[string]bool{}
	for _, a := range c.cfg.LocalIPs(ctx) {
		mine[a.String()] = true
	}
	for page := 1; page <= 20; page++ {
		var list serverList
		if err := c.get(ctx, "/servers?"+url.Values{"page": {strconv.Itoa(page)}, "per_page": {"50"}}.Encode(), &list); err != nil {
			return apiServer{}, err
		}
		for _, s := range list.Servers {
			if s.PublicNet.IPv4 != nil && mine[s.PublicNet.IPv4.IP] {
				return s, nil
			}
		}
		if list.Meta.Pagination.NextPage == nil {
			break
		}
	}
	return apiServer{}, &notFoundError{"No server in the token's project has this machine's public IPv4. Set LOADOUT_HETZNER_SERVER to its id or name."}
}

// get sends one GET and decodes the JSON answer into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "loadout")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		ae := &APIError{Status: resp.StatusCode}
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil {
			ae.Code = e.Error.Code
		}
		return ae
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &decodeError{err}
	}
	return nil
}

// APIError is an answer other than 200 from the API.
type APIError struct {
	Status int
	// Code is Hetzner's error code, such as "unauthorized".
	Code string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("hetzner API answered %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("hetzner API answered %d", e.Status)
}

type notFoundError struct{ reason string }

func (e *notFoundError) Error() string { return e.reason }

type decodeError struct{ err error }

func (e *decodeError) Error() string { return "hetzner API answer: " + e.err.Error() }
func (e *decodeError) Unwrap() error { return e.err }

// Reason turns an error of Summary into a plain sentence for the page.
func Reason(err error) string {
	var (
		ae  *APIError
		nf  *notFoundError
		de  *decodeError
		ne  net.Error
		dns *net.DNSError
	)
	switch {
	case err == nil:
		return ""
	case errors.As(err, &nf):
		return nf.reason
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return fmt.Sprintf("Hetzner did not answer within %d seconds.", int(Timeout.Seconds()))
	case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
		return "Hetzner refused the token. Check that it is valid and not deleted."
	case errors.As(err, &ae) && ae.Status == http.StatusForbidden:
		return "The token may not read this project."
	case errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests:
		return "Too many requests to Hetzner. Try again in a minute."
	case errors.As(err, &ae) && ae.Status >= 500:
		return fmt.Sprintf("Hetzner's API has a problem (it answered %d).", ae.Status)
	case errors.As(err, &ae):
		return fmt.Sprintf("Hetzner's API answered %d.", ae.Status)
	case errors.As(err, &de):
		return "Hetzner's answer could not be read."
	case errors.As(err, &dns), errors.As(err, &ne):
		return "Cannot reach api.hetzner.cloud."
	}
	return "Cannot reach api.hetzner.cloud."
}

// localIPv4s lists this machine's public IPv4 addresses. Listing the
// interfaces needs netlink, which the systemd unit does not allow, so it
// also asks which address a packet to the API would leave from: a UDP
// "connection" sends nothing.
func localIPv4s(ctx context.Context) []netip.Addr {
	var out []netip.Addr
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && p.Addr().IsGlobalUnicast() {
				out = append(out, p.Addr())
			}
		}
	}
	var d net.Dialer
	if conn, err := d.DialContext(ctx, "udp4", "api.hetzner.cloud:443"); err == nil {
		if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			if a, ok := netip.AddrFromSlice(ua.IP.To4()); ok {
				out = append(out, a)
			}
		}
		_ = conn.Close()
	}
	return out
}
