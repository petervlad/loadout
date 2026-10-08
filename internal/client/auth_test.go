package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
)

func TestDeviceCalls(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/device":
			var req api.DeviceStartRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Namespace != "lime" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(api.DeviceStartResponse{DeviceCode: "d", UserCode: "BCDF-GHJK", Interval: 5, ExpiresIn: 900})
		case "/v1/auth/device/token":
			_ = json.NewEncoder(w).Encode(api.DeviceTokenResponse{Status: api.DeviceApproved, Token: "ldt_x", Namespace: "lime", Role: "read"})
		case "/v1/auth/token":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	anon, err := client.New(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	start, err := anon.StartDevice(t.Context(), "lime")
	if err != nil || start.UserCode != "BCDF-GHJK" || start.Interval != 5 {
		t.Fatalf("StartDevice() = %+v, %v", start, err)
	}
	poll, err := anon.PollDevice(t.Context(), start.DeviceCode)
	if err != nil || poll.Token != "ldt_x" {
		t.Fatalf("PollDevice() = %+v, %v", poll, err)
	}
	authed, err := client.New(srv.URL, "ldt_x")
	if err != nil {
		t.Fatal(err)
	}
	if err := authed.RevokeToken(t.Context()); err != nil {
		t.Fatalf("RevokeToken() error: %v", err)
	}
	want := []string{"POST /v1/auth/device ", "POST /v1/auth/device/token ", "DELETE /v1/auth/token Bearer ldt_x"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got, want)
		}
	}
}
