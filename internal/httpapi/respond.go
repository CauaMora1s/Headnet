// Package httpapi provides the HTTP building blocks shared by every
// control-plane endpoint: the JSON response and error helpers, and the
// middleware chain that wraps the router.
//
// Two rules shape everything here:
//
//   - Every failure leaves the server as one of the codes in packages/api,
//     with a consistent envelope. A client should never have to parse prose to
//     find out what went wrong.
//   - Nothing that is not implemented yet may look implemented. Endpoints that
//     are declared but unbuilt return 501 with the roadmap phase attached,
//     never a plausible-looking empty success.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/CauaMora1s/Headnet/internal/logging"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// WriteJSON sends a JSON response with the given status code.
//
// The body is encoded into a buffer before anything is written, so an encoding
// failure cannot leave a half-written body behind a 200 status.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		logging.FromContext(r.Context()).ErrorContext(r.Context(),
			"encoding a response failed", "error", err, "path", r.URL.Path)
		WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		// The client hung up mid-response. Nothing can be done about it, but
		// it is worth a debug record when chasing flaky clients.
		logging.FromContext(r.Context()).DebugContext(r.Context(),
			"writing a response failed", "error", err, "path", r.URL.Path)
	}
}

// WriteError sends an error envelope with the status that belongs to the code.
func WriteError(w http.ResponseWriter, r *http.Request, code api.ErrorCode, message string) {
	writeErrorResponse(w, r, api.NewErrorResponse(code, message))
}

// WriteErrorDetail sends an error envelope carrying one structured detail,
// typically the field that failed validation.
func WriteErrorDetail(w http.ResponseWriter, r *http.Request, code api.ErrorCode, message, key, value string) {
	writeErrorResponse(w, r, api.NewErrorResponse(code, message).WithDetail(key, value))
}

func writeErrorResponse(w http.ResponseWriter, r *http.Request, resp api.ErrorResponse) {
	// Stamping the request ID onto every error is what lets an operator take a
	// user's screenshot and find the matching server-side log line.
	resp = resp.WithRequestID(logging.RequestIDFrom(r.Context()))

	body, err := json.Marshal(resp)
	if err != nil {
		// The envelope is a fixed shape of plain strings, so this is
		// unreachable in practice; fall back to a hand-written body rather
		// than sending nothing.
		http.Error(w, `{"error":{"code":"internal","message":"internal error"}}`,
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.Error.Code.HTTPStatus())
	_, _ = w.Write(body)
}

// NotImplemented reports that an endpoint exists in the API surface but has not
// been built yet, naming the roadmap phase that will deliver it.
//
// This is the honest alternative to a stub that returns an empty list or
// {"connected": true}. A fabricated success is worse than an error: it makes a
// feature look finished, it hides the gap from anyone testing the system, and
// for a VPN it can convince a user they are protected when they are not.
func NotImplemented(w http.ResponseWriter, r *http.Request, feature, phase string) {
	writeErrorResponse(w, r,
		api.NewErrorResponse(api.CodeNotImplemented,
			fmt.Sprintf("%s is not implemented yet", feature)).
			WithDetail("roadmap_phase", phase).
			WithDetail("roadmap", "https://github.com/CauaMora1s/Headnet/blob/main/docs/ROADMAP.md"))
}

// DecodeJSON reads and validates a JSON request body.
//
// It is strict on purpose. Unknown fields are rejected so a client that
// misspells a security-relevant field learns about it instead of silently
// getting the default, and the body is capped and required to contain exactly
// one JSON value.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mediaType := strings.TrimSpace(strings.Split(ct, ";")[0])
		if !strings.EqualFold(mediaType, "application/json") {
			WriteErrorDetail(w, r, api.CodeBadRequest,
				"request body must be JSON", "content_type", mediaType)
			return errors.New("unsupported content type")
		}
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var maxBytes *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytes):
			WriteError(w, r, api.CodePayloadTooLarge,
				fmt.Sprintf("request body must not exceed %d bytes", maxBytes.Limit))
		case errors.Is(err, io.EOF):
			WriteError(w, r, api.CodeBadRequest, "request body must not be empty")
		default:
			// json's messages name the offending field, which is useful, and
			// they never echo the value, which keeps secrets out of the
			// response.
			WriteError(w, r, api.CodeBadRequest, "request body is not valid JSON: "+err.Error())
		}
		return err
	}

	// Anything after the first JSON value means the client sent something
	// other than the single object the endpoint expects.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, r, api.CodeBadRequest, "request body must contain exactly one JSON object")
		return errors.New("trailing content in request body")
	}
	return nil
}

// NotFoundHandler answers unmatched routes with the standard envelope, so a
// mistyped path produces the same shape as every other error rather than Go's
// default plain-text 404.
func NotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, api.CodeNotFound, "no such endpoint")
	})
}

// MethodNotAllowed answers a known path used with the wrong method.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	WriteErrorDetail(w, r, api.CodeBadRequest,
		"method not allowed for this endpoint", "allowed", strings.Join(allowed, ", "))
}
