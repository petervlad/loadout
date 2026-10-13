package mcpmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// call is one request's view of the six tools: the person, the namespace of
// the endpoint and the layers the person reads through it. A call lives for
// one HTTP request, since the server is stateless.
type call struct {
	srv    *Server
	caller memory.Caller
	ns     string
	layers []string
}

// handlers maps a tool name to its method. Adding a tool is a schema in
// tools() and a row here.
func (c *call) handlers() map[string]func(context.Context, json.RawMessage) *mcp.CallToolResult {
	return map[string]func(context.Context, json.RawMessage) *mcp.CallToolResult{
		"search":  c.search,
		"get":     c.get,
		"save":    c.save,
		"update":  c.update,
		"retract": c.retract,
		"recent":  c.recent,
	}
}

func (c *call) register(s *mcp.Server) {
	hs := c.handlers()
	for _, t := range tools() {
		h := hs[t.Name]
		s.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Never a protocol error: the person's agent shows a tool error
			// to the person, a protocol error to nobody.
			return h(ctx, req.Params.Arguments), nil
		})
	}
}

func (c *call) now() time.Time { return c.srv.svc.Now() }

func (c *call) views(es []memory.Entry, full bool) []view {
	vs := make([]view, 0, len(es))
	for _, e := range es {
		vs = append(vs, newView(e, c.now(), full))
	}
	return vs
}

func ok(text string, structured any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, StructuredContent: structured}
}

func fail(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// failure turns an error into a tool error. Refusals and the service's own
// sentences are sent as they are: they name a limit or a rule and never hold
// the refused text. Anything else is logged and answered with a fixed
// sentence.
func (c *call) failure(ctx context.Context, tool string, err error) *mcp.CallToolResult {
	var ue userError
	switch {
	case errors.As(err, &ue):
		return fail(ue.Error())
	case errors.Is(err, memory.ErrNoMemory):
		return fail("No team memory here, or no memory with that id.")
	case errors.Is(err, memory.ErrStale):
		return fail("The memory changed since you read it. Get it again and retry.")
	case errors.Is(err, registry.ErrInvalid), errors.Is(err, registry.ErrForbidden):
		return fail(sentence(err))
	}
	c.srv.log.ErrorContext(ctx, "memory tool failed", "tool", tool, "namespace", c.ns, "user", c.caller.UserID, "error", err)
	return fail("Team memory had a problem. Try again in a moment.")
}

// sentence is the part of err a person reads: without the leading sentinel
// text, with a capital and a full stop.
func sentence(err error) string {
	msg := err.Error()
	for _, s := range []error{registry.ErrInvalid, registry.ErrForbidden} {
		msg = strings.TrimPrefix(msg, s.Error()+": ")
	}
	if msg == "" {
		return "That is not allowed."
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// --- search, get, recent ---

func (c *call) search(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
	q, err := c.searchQuery(raw)
	if err != nil {
		return c.failure(ctx, "search", err)
	}
	found, err := c.srv.svc.Search(ctx, c.caller, c.ns, q)
	if err != nil {
		return c.failure(ctx, "search", err)
	}
	if len(found) == 0 {
		return ok("No memories matched. Try fewer or different words.", obj{"results": []entryJSON{}})
	}
	vs := c.views(found, false)
	return ok(dataText("", vs), obj{"results": jsonOf(vs)})
}

type searchArgs struct {
	Query     string `json:"query"`
	Type      string `json:"type"`
	Namespace string `json:"namespace"`
	Limit     *int   `json:"limit"`
}

// searchQuery checks the arguments of search and builds the query.
func (c *call) searchQuery(raw json.RawMessage) (memory.Query, error) {
	var a searchArgs
	if err := decodeArgs(raw, &a); err != nil {
		return memory.Query{}, err
	}
	text, err := oneLineArg("query", a.Query, 200)
	if err != nil {
		return memory.Query{}, err
	}
	limit, err := inRange("limit", a.Limit, memory.DefaultSearchLimit, 1, 20)
	if err != nil {
		return memory.Query{}, err
	}
	q := memory.Query{Text: text, Namespaces: c.layers, Type: memory.Type(a.Type), Limit: limit}
	if a.Type != "" && !q.Type.Valid() {
		return memory.Query{}, userError("The argument type must be feedback, project or reference.")
	}
	if a.Namespace != "" {
		if !slices.Contains(c.layers, a.Namespace) {
			return memory.Query{}, userError("The argument namespace must be " + strings.Join(c.layers, " or ") + ".")
		}
		q.Namespaces = []string{a.Namespace}
	}
	return q, nil
}

func (c *call) get(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
	var a struct {
		ID int64 `json:"id"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return c.failure(ctx, "get", err)
	}
	if err := requireID(a.ID); err != nil {
		return c.failure(ctx, "get", err)
	}
	e, err := c.srv.svc.Get(ctx, c.caller, c.ns, a.ID)
	if err != nil {
		return c.failure(ctx, "get", err)
	}
	v := newView(e, c.now(), true)
	if v.links, err = c.srv.svc.Linked(ctx, c.caller, c.ns, e.Links); err != nil {
		return c.failure(ctx, "get", err)
	}
	return ok(dataText("", []view{v}), obj{"memory": v.json()})
}

func (c *call) recent(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
	var a struct {
		Days  *int `json:"days"`
		Limit *int `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return c.failure(ctx, "recent", err)
	}
	days, err := inRange("days", a.Days, 7, 1, 30)
	if err != nil {
		return c.failure(ctx, "recent", err)
	}
	limit, err := inRange("limit", a.Limit, 20, 1, memory.MaxSearchLimit)
	if err != nil {
		return c.failure(ctx, "recent", err)
	}
	found, err := c.srv.svc.Recent(ctx, c.caller, c.ns, c.now().AddDate(0, 0, -days), limit)
	if err != nil {
		return c.failure(ctx, "recent", err)
	}
	if len(found) == 0 {
		return ok(fmt.Sprintf("Nothing was saved or changed in the last %d days.", days), obj{"results": []entryJSON{}})
	}
	vs := c.views(found, false)
	return ok(dataText("", vs), obj{"results": jsonOf(vs)})
}

// --- save, update, retract ---

func (c *call) save(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
	var a struct {
		Type        string  `json:"type"`
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Body        string  `json:"body"`
		Links       []int64 `json:"links"`
		New         bool    `json:"new"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return c.failure(ctx, "save", err)
	}
	d, err := memory.NewDraft(c.ns, memory.Type(a.Type), a.Name, a.Description, a.Body, a.Links)
	if err != nil {
		return c.failure(ctx, "save", err)
	}
	r, err := c.srv.svc.Save(ctx, c.caller, c.ns, d, a.New)
	if err != nil {
		return c.failure(ctx, "save", err)
	}
	if !r.Done() {
		vs := c.views(r.Duplicates, false)
		return ok(dataText("Nothing was saved. Update one of these, or call save again with new: true.", vs),
			obj{"saved": nil, "duplicates": jsonOf(vs)})
	}
	c.srv.cache.drop()
	v := newView(r.Entry, c.now(), true)
	return ok(dataText(fmt.Sprintf("Saved as memory %d. Tell the user.", r.Entry.ID), []view{v}), obj{"saved": v.json()})
}

func (c *call) update(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
	var a struct {
		ID          int64    `json:"id"`
		Version     int      `json:"version"`
		Type        *string  `json:"type"`
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Body        *string  `json:"body"`
		Links       *[]int64 `json:"links"`
		Confirm     bool     `json:"confirm"`
		Note        string   `json:"note"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return c.failure(ctx, "update", err)
	}
	if err := requireID(a.ID); err != nil {
		return c.failure(ctx, "update", err)
	}
	if a.Version < 0 {
		return c.failure(ctx, "update", userError("The version must be the number you read with get, 1 or more."))
	}
	ch := memory.Change{Name: a.Name, Description: a.Description, Body: a.Body, Links: a.Links, Confirm: a.Confirm, Note: a.Note}
	if a.Type != nil {
		ch.Type = new(memory.Type(*a.Type))
	}
	// The version the agent read, so an update on top of someone else's newer
	// one is refused instead of overwriting it. Without it the newest is used.
	e, err := c.srv.svc.Update(ctx, c.caller, a.ID, a.Version, ch)
	if err != nil {
		return c.failure(ctx, "update", err)
	}
	c.srv.cache.drop()
	v := newView(e, c.now(), true)
	return ok(dataText(fmt.Sprintf("Updated memory %d to version %d. Tell the user.", e.ID, e.Version), []view{v}),
		obj{"updated": v.json(), "version": e.Version})
}

func (c *call) retract(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
	var a struct {
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return c.failure(ctx, "retract", err)
	}
	if err := requireID(a.ID); err != nil {
		return c.failure(ctx, "retract", err)
	}
	e, err := c.srv.svc.Retract(ctx, c.caller, a.ID, a.Reason)
	if err != nil {
		return c.failure(ctx, "retract", err)
	}
	c.srv.cache.drop()
	return ok(fmt.Sprintf("Retracted memory %d. It is hidden and its history is kept. Tell the user.", e.ID), obj{"retracted": e.ID})
}
