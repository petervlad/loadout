package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
)

// maxReviewSize bounds a downloaded review. The registry caps uploads at
// 256 KiB; the extra room is only there so a limit change on the server
// side does not break older clients straight away.
const maxReviewSize = 1 << 20

// proposalID matches the ids the registry hands out. Checking it keeps an id
// such as "../packages" from turning into another path.
var proposalID = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

func checkProposalID(id string) error {
	if !proposalID.MatchString(id) {
		return fmt.Errorf("invalid proposal id %q", id)
	}
	return nil
}

// Propose uploads p as a candidate for review, with the prompts the author
// tested it with. It is not a release until someone approves it.
//
// A registry older than proposal bases decodes the metadata strictly and
// refuses req.Base or req.Replaces by name; Propose then uploads again
// without them, and that registry records neither. The proposal it
// returns then has no Replaces, so the caller can tell the old proposal
// is still open. Only such registries see a second upload.
func (c *Client) Propose(ctx context.Context, p *pack.Pack, req api.ProposalRequest) (api.Proposal, error) {
	data, err := bundle.Encode(p)
	if err != nil {
		return api.Proposal{}, fmt.Errorf("propose %s@%s: %w", p.ID(), p.Version(), err)
	}
	prop, err := c.propose(ctx, p, req, data)
	if (req.Base != "" || req.Replaces != "") && (refusedField(err, "base") || refusedField(err, "replaces")) {
		req.Base, req.Replaces = "", ""
		prop, err = c.propose(ctx, p, req, data)
	}
	return prop, err
}

// refusedField reports whether err is an older registry's strict decoder
// refusing the JSON field name.
func refusedField(err error, name string) bool {
	apiErr, ok := errors.AsType[*APIError](err)
	return ok && apiErr.Code == api.CodeInvalid && strings.Contains(apiErr.Message, `unknown field "`+name+`"`)
}

func (c *Client) propose(ctx context.Context, p *pack.Pack, req api.ProposalRequest, data []byte) (api.Proposal, error) {
	body, contentType, err := proposalBody(req, data)
	if err != nil {
		return api.Proposal{}, fmt.Errorf("propose %s@%s: %w", p.ID(), p.Version(), err)
	}
	resp, err := c.doTransfer(ctx, http.MethodPost, c.url(nil, "v1", "proposals"), body, contentType, jsonType)
	if err != nil {
		return api.Proposal{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusCreated); err != nil {
		return api.Proposal{}, err
	}
	var out api.ProposalResponse
	if err := decodeJSON(resp.Body, &out); err != nil {
		return api.Proposal{}, fmt.Errorf("propose %s@%s: %w", p.ID(), p.Version(), err)
	}
	if out.Proposal.Digest != string(p.Digest()) {
		return api.Proposal{}, fmt.Errorf("propose %s@%s: registry stored digest %s, want %s", p.ID(), p.Version(), out.Proposal.Digest, p.Digest())
	}
	return out.Proposal, nil
}

// proposalBody builds the multipart/form-data body of a proposal.
func proposalBody(req api.ProposalRequest, data []byte) ([]byte, string, error) {
	meta, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, part := range []struct {
		name, contentType string
		body              []byte
	}{
		{api.ProposalMetadataPart, jsonType, meta},
		{api.ProposalBundlePart, bundle.ContentType, data},
	} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, part.name))
		h.Set("Content-Type", part.contentType)
		w, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := w.Write(part.body); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// Proposals lists proposals of the token's team, newest first. An empty pkg
// means every package; status is api.StatusOpen, another status, or
// api.StatusAny.
func (c *Client) Proposals(ctx context.Context, pkg, status string) ([]api.Proposal, error) {
	q := url.Values{}
	if pkg != "" {
		q.Set("package", pkg)
	}
	if status != "" {
		q.Set("status", status)
	}
	var out api.ProposalsResponse
	if err := c.getJSON(ctx, c.url(q, "v1", "proposals"), &out); err != nil {
		return nil, err
	}
	return out.Proposals, nil
}

// Proposal returns one proposal.
func (c *Client) Proposal(ctx context.Context, id string) (api.Proposal, error) {
	if err := checkProposalID(id); err != nil {
		return api.Proposal{}, err
	}
	var out api.ProposalResponse
	if err := c.getJSON(ctx, c.url(nil, "v1", "proposals", id), &out); err != nil {
		return api.Proposal{}, err
	}
	return out.Proposal, nil
}

// FetchProposal downloads the candidate of prop and returns the decoded
// package. Like Fetch, it fails unless the content matches the digest the
// registry announced, and also unless it is the package, version and digest
// prop describes.
func (c *Client) FetchProposal(ctx context.Context, prop api.Proposal) (*pack.Pack, error) {
	if err := checkProposalID(prop.ID); err != nil {
		return nil, err
	}
	resp, err := c.doTransfer(ctx, http.MethodGet, c.url(nil, "v1", "proposals", prop.ID, "bundle"), nil, "", bundle.ContentType)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	data, err := readLimited(resp.Body, bundle.MaxBundleSize)
	if err != nil {
		return nil, fmt.Errorf("fetch proposal %s: %w", prop.ID, err)
	}
	p, err := bundle.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("fetch proposal %s: %w", prop.ID, err)
	}
	announced := resp.Header.Get(api.HeaderDigest)
	switch {
	case announced == "":
		return nil, fmt.Errorf("fetch proposal %s: registry sent no %s header", prop.ID, api.HeaderDigest)
	case pack.Digest(announced) != p.Digest():
		return nil, fmt.Errorf("fetch proposal %s: digest mismatch: registry announced %s, content is %s", prop.ID, announced, p.Digest())
	case string(p.Digest()) != prop.Digest:
		return nil, fmt.Errorf("fetch proposal %s: content is %s, the proposal says %s", prop.ID, p.Digest(), prop.Digest)
	case p.ID().String() != prop.Package || p.Version().String() != prop.Version:
		return nil, fmt.Errorf("fetch proposal %s: registry sent %s@%s, the proposal says %s@%s", prop.ID, p.ID(), p.Version(), prop.Package, prop.Version)
	}
	return p, nil
}

// ErrOwnerApprovalUnsupported means the registry is older than approving
// your own proposal as owner.
var ErrOwnerApprovalUnsupported = errors.New("this registry is older than approving as owner; ask a teammate to approve, or update the registry")

// ErrOverUnsupported means the registry is older than approving a
// proposal over a newer release.
var ErrOverUnsupported = errors.New("this registry is older than approving over a newer release; ask the author to bring the proposal up to date, or update the registry")

// Approve turns a proposal into a release. req.AsOwner approves the
// caller's own proposal as an owner of its namespace; a registry too old
// for that answers ErrOwnerApprovalUnsupported. req.Over approves a
// proposal that is behind over the latest release; a registry too old for
// that answers ErrOverUnsupported.
func (c *Client) Approve(ctx context.Context, id string, req api.DecisionRequest) (api.ApproveResponse, error) {
	var out api.ApproveResponse
	err := c.decide(ctx, id, "approve", req, &out)
	// An older registry decodes the body strictly and names the field.
	if apiErr, ok := errors.AsType[*APIError](err); ok && req.AsOwner && apiErr.Code == api.CodeInvalid && strings.Contains(apiErr.Message, `"as_owner"`) {
		return out, fmt.Errorf("approve %s: %w", id, ErrOwnerApprovalUnsupported)
	}
	if req.Over != "" && refusedField(err, "over") {
		return out, fmt.Errorf("approve %s: %w", id, ErrOverUnsupported)
	}
	return out, err
}

// Reject closes a proposal without releasing it.
func (c *Client) Reject(ctx context.Context, id, note string) (api.Proposal, error) {
	var out api.ProposalResponse
	err := c.decide(ctx, id, "reject", api.DecisionRequest{Note: note}, &out)
	return out.Proposal, err
}

// Withdraw closes the caller's own proposal.
func (c *Client) Withdraw(ctx context.Context, id, note string) (api.Proposal, error) {
	var out api.ProposalResponse
	err := c.decide(ctx, id, "withdraw", api.DecisionRequest{Note: note}, &out)
	return out.Proposal, err
}

func (c *Client) decide(ctx context.Context, id, verb string, req api.DecisionRequest, out any) error {
	if err := checkProposalID(id); err != nil {
		return err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", verb, id, err)
	}
	return c.post(ctx, c.url(nil, "v1", "proposals", id, verb), body, jsonType, http.StatusOK, out)
}

// AddReview attaches a Markdown review, such as a comparison report, to a
// proposal. report is the same result as JSON, which the registry's web
// pages show side by side; nil sends the text alone. A registry too old to
// take the JSON gets the text alone.
func (c *Client) AddReview(ctx context.Context, id string, text, report []byte) (api.Review, error) {
	if err := checkProposalID(id); err != nil {
		return api.Review{}, err
	}
	u := c.url(nil, "v1", "proposals", id, "reviews")
	var out api.ReviewResponse
	if report != nil {
		body, contentType, err := reviewBody(text, report)
		if err != nil {
			return api.Review{}, fmt.Errorf("add review to %s: %w", id, err)
		}
		err = c.post(ctx, u, body, contentType, http.StatusCreated, &out)
		if apiErr, ok := errors.AsType[*APIError](err); !ok || apiErr.Status != http.StatusUnsupportedMediaType {
			return out.Review, err
		}
	}
	if err := c.post(ctx, u, text, api.ReviewContentType, http.StatusCreated, &out); err != nil {
		return api.Review{}, err
	}
	return out.Review, nil
}

// reviewBody builds the multipart/form-data body of a review with its JSON
// report.
func reviewBody(text, report []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, part := range []struct {
		name, contentType string
		body              []byte
	}{
		{api.ReviewMarkdownPart, api.ReviewContentType, text},
		{api.ReviewReportPart, jsonType, report},
	} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, part.name))
		h.Set("Content-Type", part.contentType)
		w, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := w.Write(part.body); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// ReviewText returns the text of one attached review.
func (c *Client) ReviewText(ctx context.Context, id string, seq int) ([]byte, error) {
	if err := checkProposalID(id); err != nil {
		return nil, err
	}
	u := c.url(nil, "v1", "proposals", id, "reviews", strconv.Itoa(seq))
	resp, err := c.do(ctx, http.MethodGet, u, nil, "", "text/markdown")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	text, err := readLimited(resp.Body, maxReviewSize)
	if err != nil {
		return nil, fmt.Errorf("review %d of %s: %w", seq, id, err)
	}
	return text, nil
}

// AddPrompt adds a test prompt to an open proposal and returns it with its
// number. Anyone who reads the proposal may add one.
func (c *Client) AddPrompt(ctx context.Context, id, text string) (api.Prompt, error) {
	if err := checkProposalID(id); err != nil {
		return api.Prompt{}, err
	}
	var out api.PromptResponse
	err := c.sendJSON(ctx, http.MethodPost, http.StatusCreated, api.PromptRequest{Text: text}, &out, "v1", "proposals", id, "prompts")
	return out.Prompt, err
}

// EditPrompt replaces the text of prompt seq, which the caller added.
func (c *Client) EditPrompt(ctx context.Context, id string, seq int, text string) (api.Prompt, error) {
	if err := checkProposalID(id); err != nil {
		return api.Prompt{}, err
	}
	var out api.PromptResponse
	err := c.sendJSON(ctx, http.MethodPut, http.StatusOK, api.PromptRequest{Text: text}, &out, "v1", "proposals", id, "prompts", strconv.Itoa(seq))
	return out.Prompt, err
}

// RemovePrompt removes prompt seq, which the caller added. Its number is
// not given to a later prompt.
func (c *Client) RemovePrompt(ctx context.Context, id string, seq int) error {
	if err := checkProposalID(id); err != nil {
		return err
	}
	return c.sendJSON(ctx, http.MethodDelete, http.StatusNoContent, nil, nil, "v1", "proposals", id, "prompts", strconv.Itoa(seq))
}

// post makes one POST request with a body and decodes a JSON response.
func (c *Client) post(ctx context.Context, u *url.URL, body []byte, contentType string, want int, out any) error {
	resp, err := c.do(ctx, http.MethodPost, u, body, contentType, jsonType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, want); err != nil {
		return err
	}
	if err := decodeJSON(resp.Body, out); err != nil {
		return fmt.Errorf("POST %s: %w", u.Path, err)
	}
	return nil
}
