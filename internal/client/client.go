// Package client talks to a loadout registry over HTTP.
//
// It speaks the wire types of package api and verifies every downloaded
// bundle against the digest the registry announced, so a corrupted or
// tampered download is an error, never an install.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// Limits on what the client reads from a response.
const (
	maxJSONSize  = 4 << 20
	maxErrorSize = 64 << 10
	jsonType     = "application/json"
)

// DefaultUserAgent is sent unless WithUserAgent says otherwise.
const DefaultUserAgent = "loadout"

// Timeouts of the default HTTP client. defaultTimeout bounds a whole
// request; transferTimeout one that uploads or downloads a bundle, since the
// largest (32 MB) takes about four minutes at 1 Mbit/s.
const (
	defaultTimeout  = 60 * time.Second
	transferTimeout = 10 * time.Minute
)

// APIError is a non-2xx response from the registry. Code is one of the
// api.Code constants, or empty when the response was not an API error body.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Newest is the wiki's newest revision, sent with api.CodeStale.
	Newest *api.WikiRevision
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("registry: %d %s", e.Status, e.Message)
	}
	return fmt.Sprintf("registry: %d %s: %s", e.Status, e.Code, e.Message)
}

// Is reports whether e matches target: api.ErrNotFound for a not_found
// body, api.ErrStale for a stale one.
func (e *APIError) Is(target error) bool {
	switch target {
	case api.ErrNotFound:
		return e.Code == api.CodeNotFound
	case api.ErrStale:
		return e.Code == api.CodeStale
	case api.ErrBehind:
		return e.Code == api.CodeBehind
	}
	return false
}

// Client is a registry client. It is safe for concurrent use.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
	// transfer sends the requests that carry a bundle.
	transfer  *http.Client
	userAgent string
	// namespace is the view an account token works in; see In.
	namespace string
	// advice, if set, hears what each response says about loadout
	// releases.
	advice func(CLIAdvice)
}

// In returns a copy of c whose requests name namespace ns: the team whose
// packages, and the layers above it, an account token sees. A namespace
// token ignores it.
func (c *Client) In(ns string) *Client {
	cp := *c
	cp.namespace = ns
	return &cp
}

type options struct {
	httpClient *http.Client
	userAgent  string
	caFile     string
	advice     func(CLIAdvice)
}

// Option configures a Client.
type Option func(*options)

// WithHTTPClient uses c for every request instead of the default client
// (60s timeout, 10 minutes for a bundle; proxy settings from the
// environment).
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.httpClient = c } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(o *options) { o.userAgent = ua } }

// WithCAFile trusts the PEM certificates in path in addition to the system
// roots, for example the CA of a TLS-inspecting corporate proxy.
func WithCAFile(path string) Option { return func(o *options) { o.caFile = path } }

// New returns a client for the registry at baseURL (http or https, trailing
// slash optional) authenticating with token. token may be empty for calls
// that need none, such as Health.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	base, err := parseBase(baseURL)
	if err != nil {
		return nil, err
	}
	o := options{userAgent: DefaultUserAgent}
	for _, opt := range opts {
		opt(&o)
	}

	hc := o.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout, Transport: defaultTransport()}
	}
	if o.caFile != "" {
		hc, err = withCA(hc, o.caFile)
		if err != nil {
			return nil, err
		}
	}
	transfer := hc
	if o.httpClient == nil {
		// The same transport, so both share connections and the CA.
		t := *hc
		t.Timeout = transferTimeout
		transfer = &t
	}
	return &Client{base: base, token: token, http: hc, transfer: transfer, userAgent: o.userAgent, advice: o.advice}, nil
}

// HTTPClient returns an HTTP client with the default transport (proxy settings
// from the environment) and no overall timeout, so a long-lived stream is not
// cut. A caFile adds its PEM certificates to the system roots, as WithCAFile
// does. The MCP bridge and the memory import build their connections from it.
//
// It refuses every redirect. These connections carry a memory token, and a
// redirect would hand the token to whatever place the answer names.
func HTTPClient(caFile string) (*http.Client, error) {
	hc := &http.Client{Transport: defaultTransport(), CheckRedirect: refuseRedirect}
	if caFile == "" {
		return hc, nil
	}
	return withCA(hc, caFile)
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return errors.New("the registry answered with a redirect, which loadout does not follow with a memory token. Check the registry address in `loadout remote list`")
}

// CheckMemoryURL accepts the address of a registry that may be sent a memory
// token: https, or http only when the host is this machine. Anything else would
// put the token on the network in clear.
func CheckMemoryURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("registry url %q is not an address", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("registry url %q uses http, which would send your memory token unencrypted. Use the https address of the registry", raw)
	}
	return fmt.Errorf("registry url %q: scheme must be https", raw)
}

// isLoopback is true for localhost and for loopback IP addresses.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func parseBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("registry url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("registry url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("registry url %q: host is missing", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("registry url %q: must not contain credentials, a query or a fragment", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func defaultTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment}
}

// withCA returns a copy of hc whose transport also trusts the CAs in path.
func withCA(hc *http.Client, path string) (*http.Client, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA file %s: no PEM certificates found", path)
	}

	var t *http.Transport
	switch rt := hc.Transport.(type) {
	case nil:
		t = defaultTransport()
	case *http.Transport:
		t = rt.Clone()
	default:
		return nil, fmt.Errorf("CA file %s: the HTTP client's transport is %T, want *http.Transport", path, rt)
	}
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	t.TLSClientConfig.RootCAs = pool

	out := *hc
	out.Transport = t
	return &out, nil
}

// Health reports the registry's status. It needs no token.
func (c *Client) Health(ctx context.Context) (api.HealthResponse, error) {
	var out api.HealthResponse
	err := c.getJSON(ctx, c.url(nil, "v1", "health"), &out)
	return out, err
}

// Packages lists the latest release of each package the token can read:
// its own namespace first, then each ancestor.
func (c *Client) Packages(ctx context.Context) ([]api.Release, error) {
	var out api.PackagesResponse
	if err := c.getJSON(ctx, c.url(nil, "v1", "packages"), &out); err != nil {
		return nil, err
	}
	return out.Packages, nil
}

// Releases lists every release of id, lowest version first.
func (c *Client) Releases(ctx context.Context, id pack.ID) ([]api.Release, error) {
	var out api.ReleasesResponse
	if err := c.getJSON(ctx, c.url(nil, "v1", "packages", id.Team(), id.Name()), &out); err != nil {
		return nil, err
	}
	return out.Releases, nil
}

// Layers lists the namespaces the token reads, closest first. A registry
// from before layers answers 404 (an *APIError with Status 404).
func (c *Client) Layers(ctx context.Context) ([]api.Layer, error) {
	var out api.LayersResponse
	if err := c.getJSON(ctx, c.url(nil, "v1", "layers"), &out); err != nil {
		return nil, err
	}
	return out.Layers, nil
}

// Changes lists the releases the token can read published after since.
// Pass the returned Next as since on the next call.
func (c *Client) Changes(ctx context.Context, since int64) (api.ChangesResponse, error) {
	var out api.ChangesResponse
	q := url.Values{"since": {strconv.FormatInt(since, 10)}}
	err := c.getJSON(ctx, c.url(q, "v1", "changes"), &out)
	return out, err
}

// Fetch downloads version of id ("latest" or MAJOR.MINOR.PATCH) and returns
// the decoded package. It fails if the package's digest differs from the one
// the registry announced, or if it is not the package or version asked for.
// The resolved version and digest are the package's Version() and Digest().
func (c *Client) Fetch(ctx context.Context, id pack.ID, version string) (*pack.Pack, error) {
	var want semver.Version
	if version != "latest" {
		v, err := semver.Parse(version)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", id, err)
		}
		want = v
	}

	u := c.url(nil, "v1", "packages", id.Team(), id.Name(), version)
	resp, err := c.doTransfer(ctx, http.MethodGet, u, nil, "", bundle.ContentType)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}

	data, err := readLimited(resp.Body, bundle.MaxBundleSize)
	if err != nil {
		return nil, fmt.Errorf("fetch %s@%s: %w", id, version, err)
	}
	p, err := bundle.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("fetch %s@%s: %w", id, version, err)
	}

	announced := resp.Header.Get(api.HeaderDigest)
	switch {
	case announced == "":
		return nil, fmt.Errorf("fetch %s@%s: registry sent no %s header", id, version, api.HeaderDigest)
	case pack.Digest(announced) != p.Digest():
		return nil, fmt.Errorf("fetch %s@%s: digest mismatch: registry announced %s, content is %s", id, version, announced, p.Digest())
	case p.ID() != id:
		return nil, fmt.Errorf("fetch %s@%s: registry sent package %s", id, version, p.ID())
	case version != "latest" && p.Version() != want:
		return nil, fmt.Errorf("fetch %s@%s: registry sent version %s", id, version, p.Version())
	}
	if hv := resp.Header.Get(api.HeaderVersion); hv != "" && hv != p.Version().String() {
		return nil, fmt.Errorf("fetch %s@%s: registry announced version %s, package is %s", id, version, hv, p.Version())
	}
	return p, nil
}

// Publish uploads p as a new release.
func (c *Client) Publish(ctx context.Context, p *pack.Pack) (api.Release, error) {
	data, err := bundle.Encode(p)
	if err != nil {
		return api.Release{}, fmt.Errorf("publish %s@%s: %w", p.ID(), p.Version(), err)
	}
	resp, err := c.doTransfer(ctx, http.MethodPost, c.url(nil, "v1", "packages"), data, bundle.ContentType, jsonType)
	if err != nil {
		return api.Release{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusCreated); err != nil {
		return api.Release{}, err
	}
	var out api.PublishResponse
	if err := decodeJSON(resp.Body, &out); err != nil {
		return api.Release{}, fmt.Errorf("publish %s@%s: %w", p.ID(), p.Version(), err)
	}
	if out.Release.Digest != string(p.Digest()) {
		return api.Release{}, fmt.Errorf("publish %s@%s: registry stored digest %s, want %s", p.ID(), p.Version(), out.Release.Digest, p.Digest())
	}
	return out.Release, nil
}

func (c *Client) url(q url.Values, segments ...string) *url.URL {
	u := c.base.JoinPath(segments...)
	u.RawQuery = q.Encode()
	return u
}

func (c *Client) getJSON(ctx context.Context, u *url.URL, out any) error {
	resp, err := c.do(ctx, http.MethodGet, u, nil, "", jsonType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	if err := decodeJSON(resp.Body, out); err != nil {
		return fmt.Errorf("%s %s: %w", http.MethodGet, u.Path, err)
	}
	return nil
}

// do sends one request. Transport errors name the host only; the token is
// sent in a header and never appears in an error.
func (c *Client) do(ctx context.Context, method string, u *url.URL, body []byte, contentType, accept string) (*http.Response, error) {
	return c.send(ctx, c.http, method, u, body, contentType, accept)
}

// doTransfer is do for a request that uploads or downloads a bundle.
func (c *Client) doTransfer(ctx context.Context, method string, u *url.URL, body []byte, contentType, accept string) (*http.Response, error) {
	return c.send(ctx, c.transfer, method, u, body, contentType, accept)
}

func (c *Client) send(ctx context.Context, hc *http.Client, method string, u *url.URL, body []byte, contentType, accept string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, u.Path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		if c.namespace != "" {
			req.Header.Set(api.HeaderNamespace, c.namespace)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			err = uerr.Err
		}
		return nil, fmt.Errorf("%s %s on %s: %w", method, u.Path, c.base.Host, err)
	}
	if c.advice != nil {
		if a, ok := adviceOf(resp); ok {
			c.advice(a)
		}
	}
	return resp, nil
}

// checkStatus returns an *APIError unless resp has status want.
func checkStatus(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	apiErr := &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	data, err := readLimited(resp.Body, maxErrorSize)
	if err != nil {
		return apiErr
	}
	var body api.ErrorResponse
	if json.Unmarshal(data, &body) == nil && body.Error.Code != "" {
		apiErr.Code = body.Error.Code
		apiErr.Message = body.Error.Message
		apiErr.Newest = body.Error.Newest
	}
	return apiErr
}

func decodeJSON(r io.Reader, out any) error {
	data, err := readLimited(r, maxJSONSize)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// errResponseTooLarge is returned when a response exceeds its read limit.
var errResponseTooLarge = errors.New("response is too large")

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: over %d bytes", errResponseTooLarge, limit)
	}
	return data, nil
}
