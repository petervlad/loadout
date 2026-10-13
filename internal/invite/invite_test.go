package invite

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/registry"
)

const secret = "ldt_0123456789abcdef" // gitleaks:allow, a fake token for the test

func encodeJSON(s string) string {
	return Prefix + base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestTokenPrefixMatchesRegistry(t *testing.T) {
	if tokenPrefix != registry.TokenPrefix {
		t.Errorf("tokenPrefix = %q, want registry.TokenPrefix %q", tokenPrefix, registry.TokenPrefix)
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []Invite{
		{URL: "https://registry.example.com", Namespace: "lime", Token: secret, Role: RolePublish},
		{URL: "http://10.0.4.12:7420", Namespace: "north", Token: secret},
		{URL: "https://example.com/loadout", Namespace: "a-b", Token: secret, Role: RoleRead},
	}
	for _, in := range tests {
		t.Run(in.Namespace, func(t *testing.T) {
			s, err := in.Encode()
			if err != nil {
				t.Fatalf("Encode(%v) error: %v", in, err)
			}
			if !strings.HasPrefix(s, Prefix) {
				t.Errorf("Encode(%v) = %q, want prefix %s", in, s, Prefix)
			}
			got, err := Decode("  " + s + "\n")
			if err != nil {
				t.Fatalf("Decode(Encode(%v)) error: %v", in, err)
			}
			if diff := cmp.Diff(in, got); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEncodeTrimsTrailingSlash(t *testing.T) {
	s, err := Invite{URL: "https://r.example.com/", Namespace: "lime", Token: secret}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://r.example.com" {
		t.Errorf("URL = %q, want no trailing slash", got.URL)
	}
}

func TestEncodeRejectsInvalid(t *testing.T) {
	tests := map[string]Invite{
		"no url":        {Namespace: "lime", Token: secret},
		"ftp url":       {URL: "ftp://x", Namespace: "lime", Token: secret},
		"bad namespace": {URL: "https://x", Namespace: "Lime", Token: secret},
		"no token":      {URL: "https://x", Namespace: "lime"},
		"bad role":      {URL: "https://x", Namespace: "lime", Token: secret, Role: "admin"},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := in.Encode(); !errors.Is(err, ErrMalformed) {
				t.Errorf("Encode(%v) error = %v, want ErrMalformed", in, err)
			}
		})
	}
}

func TestDecodeRejectsGarbageAndTampering(t *testing.T) {
	valid, err := Invite{URL: "https://r.example.com", Namespace: "lime", Token: secret}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	flipped := []byte(valid)
	flipped[len(Prefix)+5] ^= 0x01

	tests := map[string]string{
		"empty":           "",
		"bare token":      secret,
		"wrong prefix":    "ldx_" + strings.TrimPrefix(valid, Prefix),
		"not base64":      Prefix + "!!!!",
		"padded base64":   Prefix + base64.URLEncoding.EncodeToString([]byte(`{"v":1}`)),
		"flipped bit":     string(flipped),
		"truncated":       valid[:len(valid)-6],
		"not json":        encodeJSON("hello"),
		"json array":      encodeJSON(`[1,2]`),
		"unknown field":   encodeJSON(`{"v":1,"url":"https://x","namespace":"lime","token":"` + secret + `","admin":true}`),
		"trailing data":   encodeJSON(`{"v":1,"url":"https://x","namespace":"lime","token":"` + secret + `"}{}`),
		"missing token":   encodeJSON(`{"v":1,"url":"https://x","namespace":"lime"}`),
		"token no prefix": encodeJSON(`{"v":1,"url":"https://x","namespace":"lime","token":"secret"}`),
		"token newline":   encodeJSON(`{"v":1,"url":"https://x","namespace":"lime","token":"ldt_a\nb"}`),
		"bad namespace":   encodeJSON(`{"v":1,"url":"https://x","namespace":"../etc","token":"` + secret + `"}`),
		"url with creds":  encodeJSON(`{"v":1,"url":"https://u:p@x","namespace":"lime","token":"` + secret + `"}`),
		"url with query":  encodeJSON(`{"v":1,"url":"https://x?a=b","namespace":"lime","token":"` + secret + `"}`),
		"javascript url":  encodeJSON(`{"v":1,"url":"javascript:alert(1)","namespace":"lime","token":"` + secret + `"}`),
		"bad role":        encodeJSON(`{"v":1,"url":"https://x","namespace":"lime","token":"` + secret + `","role":"root"}`),
		"too long":        Prefix + strings.Repeat("A", maxEncodedLen),
		"version string":  encodeJSON(`{"v":"1","url":"https://x","namespace":"lime","token":"` + secret + `"}`),
	}
	for name, s := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(s)
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Decode(%s) error = %v, want ErrMalformed", name, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("Decode(%s) error %q contains the token", name, err)
			}
		})
	}
}

func TestDecodeVersion(t *testing.T) {
	tests := []struct {
		json string
		want int
	}{
		{`{"v":2,"url":"https://x","namespace":"lime","token":"` + secret + `","new":"field"}`, 2},
		{`{"url":"https://x","namespace":"lime","token":"` + secret + `"}`, 0},
		{`{"v":0}`, 0},
		{`{"v":-1}`, -1},
	}
	for _, tc := range tests {
		_, err := Decode(encodeJSON(tc.json))
		vErr, ok := errors.AsType[*VersionError](err)
		if !ok {
			t.Errorf("Decode(%s) error = %v, want *VersionError", tc.json, err)
			continue
		}
		if vErr.Got != tc.want {
			t.Errorf("Decode(%s) version = %d, want %d", tc.json, vErr.Got, tc.want)
		}
	}
}

func TestSecretStaysOutOfStringsAndLogs(t *testing.T) {
	in := Invite{URL: "https://r.example.com", Namespace: "lime", Token: secret, Role: RoleRead}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("joined", "invite", in)
	for name, s := range map[string]string{
		"String": in.String(),
		"%v":     fmt.Sprintf("%v", in),
		"slog":   buf.String(),
	} {
		if strings.Contains(s, secret) {
			t.Errorf("%s = %q, contains the token", name, s)
		}
	}
}

func FuzzDecode(f *testing.F) {
	valid, err := Invite{URL: "https://r.example.com", Namespace: "lime", Token: secret}.Encode()
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{valid, "", Prefix, secret, encodeJSON(`{"v":1}`), encodeJSON(`null`)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		inv, err := Decode(s)
		if err != nil {
			return
		}
		again, err := inv.Encode()
		if err != nil {
			t.Fatalf("Encode(Decode(%q)) error: %v", s, err)
		}
		back, err := Decode(again)
		if err != nil {
			t.Fatalf("Decode(Encode(Decode(%q))) error: %v", s, err)
		}
		if back != inv {
			t.Errorf("round trip of %q = %v, want %v", s, back, inv)
		}
	})
}
