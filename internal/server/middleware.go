package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/registry"
)

// HeaderRequestID is the response header carrying the request id that also
// appears in the server logs.
const HeaderRequestID = "X-Request-Id"

type ctxKey int

const (
	requestKey ctxKey = iota + 1
	principalKey
	viewErrKey
)

// requestState is per-request data that inner handlers fill in for outer
// middleware, such as the token id the access log records.
type requestState struct {
	id      string
	tokenID string
}

func stateFrom(ctx context.Context) *requestState {
	st, _ := ctx.Value(requestKey).(*requestState)
	return st
}

func requestIDFrom(ctx context.Context) string {
	if st := stateFrom(ctx); st != nil {
		return st.id
	}
	return ""
}

// principalFrom returns the principal the auth middleware stored.
func principalFrom(ctx context.Context) (registry.Principal, bool) {
	pr, ok := ctx.Value(principalKey).(registry.Principal)
	return pr, ok
}

// requestID gives every request a fresh id. Incoming ids are ignored so a
// client cannot forge log entries.
func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := &requestState{id: uuid.New().String()}
		w.Header().Set(HeaderRequestID, st.id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey, st)))
	})
}

// accessLog logs one line per request after it completes.
func (s *server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		// The mux sets r.Pattern on the request it is given, so r is read
		// after it returns.
		next.ServeHTTP(rec, r)

		args := []any{
			"method", r.Method,
			"route", r.Pattern,
			"status", rec.statusCode(),
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if st := stateFrom(r.Context()); st != nil {
			args = append(args, "request_id", st.id)
			if st.tokenID != "" {
				args = append(args, "token_id", st.tokenID)
			}
		}
		s.log.InfoContext(r.Context(), "request", args...)
	})
}

// recoverPanic turns a handler panic into a 500 and logs it.
func (s *server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			s.log.Error("handler panic", "request_id", requestIDFrom(r.Context()), "route", r.Pattern, "panic", fmt.Sprint(v))
			if rec, ok := w.(*recorder); ok && rec.status != 0 {
				return // headers are out; nothing sensible left to send
			}
			// writeError logs internal errors itself; the panic is logged
			// above with its value, so only the response is written here.
			s.writeJSON(w, r, http.StatusInternalServerError, internalErrorBody)
		}()
		next.ServeHTTP(w, r)
	})
}

// auth requires a valid bearer token and stores the principal it acts as in
// the request context.
func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearer(r)
		if !ok {
			s.writeError(w, r, registry.ErrUnauthorized)
			return
		}
		pr, err := s.reg.Authenticate(r.Context(), secret)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		// Every answer from here on, errors included, says which loadout
		// is current: a refusal is when "your loadout is old" helps most.
		s.adviseCLI(w)
		// An account token works in the namespace the client names; one
		// package or proposal narrows it to its own namespace later. A view
		// the user lost refuses only the listings (see listingView): a
		// package they still read through another team stays reachable.
		ctx := r.Context()
		if ns := r.Header.Get(api.HeaderNamespace); ns != "" {
			narrowed, err := s.reg.In(ctx, pr, ns)
			switch {
			case errors.Is(err, registry.ErrForbidden):
				ctx = context.WithValue(ctx, viewErrKey, err)
			case err != nil:
				s.writeError(w, r, err)
				return
			default:
				pr = narrowed
			}
		}
		r = r.WithContext(ctx)
		if st := stateFrom(r.Context()); st != nil {
			st.tokenID = pr.TokenID
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, pr)))
	})
}

// listingView reports whether a listing may go on: false, with the error
// written, when the request named a view the user may not use.
func (s *server) listingView(w http.ResponseWriter, r *http.Request, pr registry.Principal) bool {
	if err, ok := r.Context().Value(viewErrKey).(error); ok && !pr.Narrowed() {
		s.writeError(w, r, err)
		return false
	}
	return true
}

// authed wraps a handler that needs the caller's principal in auth.
func (s *server) authed(h func(http.ResponseWriter, *http.Request, registry.Principal)) http.Handler {
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := principalFrom(r.Context())
		if !ok {
			s.writeError(w, r, registry.ErrUnauthorized)
			return
		}
		h(w, r, pr)
	}))
}

// bearer extracts the token from "Authorization: Bearer <token>".
func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// recorder captures the status and size of a response for the access log.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rec *recorder) WriteHeader(code int) {
	if rec.status == 0 {
		rec.status = code
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (rec *recorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func (rec *recorder) statusCode() int {
	if rec.status == 0 {
		return http.StatusOK
	}
	return rec.status
}
