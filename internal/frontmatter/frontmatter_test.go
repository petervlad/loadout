package frontmatter

import (
	"errors"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantMeta string
		wantBody string
		wantErr  string
	}{
		{name: "plain", in: "---\nname: a\n---\nBody.\n", wantMeta: "name: a", wantBody: "Body.\n"},
		{name: "empty header", in: "---\n---\nBody.", wantMeta: "", wantBody: "Body."},
		{name: "no body", in: "---\nname: a\n---", wantMeta: "name: a", wantBody: ""},
		{name: "byte order mark", in: "\ufeff---\nname: a\n---\nBody.", wantMeta: "name: a", wantBody: "Body."},
		{name: "crlf", in: "---\r\nname: a\r\n---\r\nOne.\r\nTwo.\r\n", wantMeta: "name: a", wantBody: "One.\nTwo.\n"},
		{name: "bom and crlf", in: "\ufeff---\r\nname: a\r\n---\r\nBody.", wantMeta: "name: a", wantBody: "Body."},
		{name: "trailing spaces on delimiters", in: "--- \t\nname: a\n---  \nBody.", wantMeta: "name: a", wantBody: "Body."},
		{name: "indented delimiter stays in the header", in: "---\nnote: |\n  ---\n  more\n---\nBody.", wantMeta: "note: |\n  ---\n  more", wantBody: "Body."},
		{name: "later delimiter stays in the body", in: "---\nname: a\n---\nBody.\n---\nMore.", wantMeta: "name: a", wantBody: "Body.\n---\nMore."},
		{name: "unclosed", in: "---\nname: a\nBody.\n", wantErr: "not closed"},
		{name: "indented close is not a close", in: "---\nname: a\n  ---\nBody.", wantErr: "not closed"},
		{name: "no header", in: "Body.\n", wantErr: ErrMissing.Error()},
		{name: "empty", in: "", wantErr: ErrMissing.Error()},
		{name: "delimiter without a newline", in: "---", wantErr: ErrMissing.Error()},
		{name: "four dashes", in: "----\nname: a\n---\n", wantErr: ErrMissing.Error()},
		{name: "indented open", in: " ---\nname: a\n---\n", wantErr: ErrMissing.Error()},
		{name: "text after the dashes", in: "--- yaml\nname: a\n---\n", wantErr: ErrMissing.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, body, err := Split([]byte(tc.in))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Split() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Split() error: %v", err)
			}
			if string(meta) != tc.wantMeta || string(body) != tc.wantBody {
				t.Errorf("Split() = %q, %q; want %q, %q", meta, body, tc.wantMeta, tc.wantBody)
			}
		})
	}
}

func TestSplitMissingIsSentinel(t *testing.T) {
	if _, _, err := Split([]byte("no header")); !errors.Is(err, ErrMissing) {
		t.Errorf("Split() error = %v, want ErrMissing", err)
	}
}

func TestJoinSplitRoundTrip(t *testing.T) {
	tests := []struct {
		name, meta, body string
	}{
		{"plain", "name: a\n", "Body."},
		{"empty meta", "", "Body."},
		{"empty body", "name: a\n", ""},
		{"body with a delimiter", "name: a\n", "Before.\n---\nAfter."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := Join([]byte(tc.meta), tc.body)
			if !strings.HasPrefix(string(data), Delimiter+"\n") || !strings.HasSuffix(string(data), "\n") {
				t.Errorf("Join() = %q, want a --- first line and a final newline", data)
			}
			meta, body, err := Split(data)
			if err != nil {
				t.Fatalf("Split(Join()) error: %v", err)
			}
			if got, want := string(meta), strings.TrimSuffix(tc.meta, "\n"); got != want {
				t.Errorf("meta = %q, want %q", got, want)
			}
			if got := string(body); got != tc.body+"\n" {
				t.Errorf("body = %q, want %q", got, tc.body+"\n")
			}
		})
	}
}

func TestFields(t *testing.T) {
	tests := []struct {
		name     string
		meta     string
		wantKeys []string
		wantErr  string
	}{
		{name: "empty", meta: ""},
		{name: "null", meta: "~"},
		{name: "in order", meta: "name: a\nallowed-tools: Read\ndescription: d\n", wantKeys: []string{"name", "allowed-tools", "description"}},
		{name: "not a mapping", meta: "- a\n", wantErr: "want fields"},
		{name: "duplicate", meta: "a: 1\na: 2\n", wantErr: `"a" appears twice`},
		{name: "complex key", meta: "? [a]\n: 1\n", wantErr: "plain text"},
		{name: "second document", meta: "a: 1\n---\nb: 2\n", wantErr: "more than one"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := Fields([]byte(tc.meta))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Fields() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fields() error: %v", err)
			}
			var keys []string
			for i := 0; i < len(fields); i += 2 {
				keys = append(keys, fields[i].Value)
			}
			if strings.Join(keys, ",") != strings.Join(tc.wantKeys, ",") {
				t.Errorf("Fields() keys = %v, want %v", keys, tc.wantKeys)
			}
		})
	}
}
