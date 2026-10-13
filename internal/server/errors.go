package server

import (
	"errors"
	"net/http"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// errUnsupportedMedia is returned for an upload that is not a bundle.
var errUnsupportedMedia = errors.New("unsupported media type")

// internalMessage is all a client learns about an unexpected error.
const internalMessage = "internal server error"

var internalErrorBody = api.ErrorResponse{Error: api.ErrorBody{Code: api.CodeInternal, Message: internalMessage}}

// classify maps an error to its HTTP status and API error code. It is the one
// place that decides how errors look on the wire.
func classify(err error) (status int, code string) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge, api.CodeTooLarge
	}
	switch {
	case errors.Is(err, registry.ErrNotFound):
		return http.StatusNotFound, api.CodeNotFound
	case errors.Is(err, registry.ErrProposalClosed):
		return http.StatusConflict, api.CodeProposalClosed
	case errors.Is(err, registry.ErrVersionConflict):
		return http.StatusConflict, api.CodeVersionConflict
	case errors.Is(err, registry.ErrExists):
		return http.StatusConflict, api.CodeExists
	case errors.Is(err, registry.ErrBehind):
		return http.StatusConflict, api.CodeBehind
	case errors.Is(err, registry.ErrStale):
		return http.StatusConflict, api.CodeStale
	case errors.Is(err, registry.ErrUnauthorized):
		return http.StatusUnauthorized, api.CodeUnauthorized
	case errors.Is(err, registry.ErrForbidden):
		return http.StatusForbidden, api.CodeForbidden
	case errors.Is(err, bundle.ErrTooLarge), errors.Is(err, pack.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, api.CodeTooLarge
	case errors.Is(err, errRateLimited), errors.Is(err, device.ErrTooMany):
		return http.StatusTooManyRequests, api.CodeTooManyRequests
	case errors.Is(err, device.ErrFull):
		return http.StatusServiceUnavailable, api.CodeTooManyRequests
	case errors.Is(err, errUnsupportedMedia):
		return http.StatusUnsupportedMediaType, api.CodeInvalid
	case errors.Is(err, registry.ErrInvalid):
		return http.StatusBadRequest, api.CodeInvalid
	default:
		return http.StatusInternalServerError, api.CodeInternal
	}
}

// writeError writes err as an ErrorResponse. Client errors carry their
// message; anything unexpected is logged with the request id and the client
// only sees a generic message.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := classify(err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		s.log.Error("request failed", "request_id", requestIDFrom(r.Context()), "route", r.Pattern, "err", err)
		msg = internalMessage
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	body := api.ErrorBody{Code: code, Message: msg}
	if stale, ok := errors.AsType[*registry.StaleError](err); ok {
		// The CLI names who saved in between without parsing the message.
		newest := toAPIWikiRevision(stale.Newest)
		body.Newest = &newest
	}
	s.writeJSON(w, r, status, api.ErrorResponse{Error: body})
}
