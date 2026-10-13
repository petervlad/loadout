package mcpmemory

import (
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/memory"
)

// The input schemas are written by hand, not derived from a Go type, so the
// bytes a client sees are the ones reviewed here. Every one is closed
// (additionalProperties false). The server checks the arguments itself in
// args.go and does not echo them, so the schemas are for the client's model.

type obj = map[string]any

func prop(typ, desc string, extra obj) obj {
	p := obj{"type": typ, "description": desc}
	maps.Copy(p, extra)
	return p
}

func schema(required []string, props obj) obj {
	s := obj{"type": "object", "additionalProperties": false, "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func typeEnum() []string {
	var out []string
	for _, t := range memory.Types() {
		out = append(out, string(t))
	}
	return out
}

// Tools returns the six tools without handlers, in the order clients see
// them. Each call builds new values, so a caller may change what it gets. The
// stdio bridge serves them when the registry cannot be reached.
func Tools() []*mcp.Tool { return tools() }

// tools returns the six tools without their handlers, in the order clients
// see them.
func tools() []*mcp.Tool {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}
	write := &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
	return []*mcp.Tool{
		{
			Name:        "search",
			Description: "Search the team's memories and the project's by words. Returns up to limit memories with a body excerpt. Search before you save.",
			Annotations: readOnly,
			InputSchema: schema([]string{"query"}, obj{
				"query":     prop("string", "Words to look for.", obj{"minLength": 1, "maxLength": 200}),
				"type":      prop("string", "Only memories of this type.", obj{"enum": typeEnum()}),
				"namespace": prop("string", "Only memories of the team or only those of its project. Default: both.", nil),
				"limit":     prop("integer", "How many memories at most. Default 10.", obj{"minimum": 1, "maximum": 20}),
			}),
		},
		{
			Name:        "get",
			Description: "Read one memory in full, with the names of the memories it links to.",
			Annotations: readOnly,
			InputSchema: schema([]string{"id"}, obj{
				"id": prop("integer", "The id of the memory.", obj{"minimum": 1}),
			}),
		},
		{
			Name:        "save",
			Description: "Save a new team memory under your name. If close matches exist nothing is saved and they are returned: update one, or call again with new: true. Secrets are refused.",
			Annotations: write,
			InputSchema: schema([]string{"type", "name", "description", "body"}, obj{
				"type":        prop("string", "feedback, project or reference. There is no user type.", obj{"enum": typeEnum()}),
				"name":        prop("string", "A short title, one line, at most 64 characters.", obj{"minLength": 1, "maxLength": memory.MaxName}),
				"description": prop("string", "What it says in one line, at most 150 characters.", obj{"minLength": 1, "maxLength": memory.MaxDescription}),
				"body":        prop("string", "The fact and why it holds, at most 4 KB. Longer text belongs in the team docs, with a link here.", nil),
				"links":       prop("array", "Ids of related memories, at most 10.", obj{"items": obj{"type": "integer", "minimum": 1}, "maxItems": memory.MaxLinks}),
				"new":         prop("boolean", "Save even though close matches exist. Default false.", nil),
			}),
		},
		{
			Name:        "update",
			Description: "Change a memory. Every earlier version is kept. Set confirm: true when you checked that it is still true, even if nothing else changes.",
			Annotations: write,
			InputSchema: schema([]string{"id"}, obj{
				"id":          prop("integer", "The id of the memory.", obj{"minimum": 1}),
				"version":     prop("integer", "The version you read. If the memory has changed since, the update is refused and nothing is lost. Leave it out to update the newest version.", obj{"minimum": 1}),
				"type":        prop("string", "New type.", obj{"enum": typeEnum()}),
				"name":        prop("string", "New name.", obj{"minLength": 1, "maxLength": memory.MaxName}),
				"description": prop("string", "New description.", obj{"minLength": 1, "maxLength": memory.MaxDescription}),
				"body":        prop("string", "New body.", nil),
				"links":       prop("array", "New ids of related memories.", obj{"items": obj{"type": "integer", "minimum": 1}, "maxItems": memory.MaxLinks}),
				"confirm":     prop("boolean", "Say the memory is still true: sets its confirmed date to today.", nil),
				"note":        prop("string", "Why it changed, one line.", obj{"maxLength": memory.MaxNote}),
			}),
		},
		{
			Name:        "retract",
			Description: "Hide a memory that is wrong or no longer true. Its history is kept. Only its author and the team's maintainers may.",
			Annotations: destructive,
			InputSchema: schema([]string{"id", "reason"}, obj{
				"id":     prop("integer", "The id of the memory.", obj{"minimum": 1}),
				"reason": prop("string", "Why, one line.", obj{"minLength": 1, "maxLength": memory.MaxNote}),
			}),
		},
		{
			Name:        "recent",
			Description: "List the memories saved or changed lately, newest first.",
			Annotations: readOnly,
			InputSchema: schema(nil, obj{
				"days":  prop("integer", "How many days back. Default 7.", obj{"minimum": 1, "maximum": 30}),
				"limit": prop("integer", "How many memories at most. Default 20.", obj{"minimum": 1, "maximum": 50}),
			}),
		},
	}
}
