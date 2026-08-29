// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

// Package api defines the JSON data-transfer objects and error contract of the
// Headnet control-plane HTTP API.
//
// It is deliberately free of business logic and of any dependency on the
// server internals so that third-party clients — including clients written
// against the Apache-2.0 licensed part of this repository — can reuse it
// verbatim. The canonical machine-readable definition of the same contract
// lives in docs/api/openapi.yaml; the two are kept in step by
// TestOpenAPIMatchesErrorCodes in the server package.
package api

import "net/http"

// ErrorCode is a stable, machine-readable classification of a failure.
//
// Codes are part of the public API: clients branch on them, so a code may be
// added but never renamed or repurposed. Human-readable messages, by contrast,
// may change at any time and must not be parsed.
type ErrorCode string

// The complete set of error codes the API may return.
const (
	// CodeBadRequest means the request was malformed: unparseable JSON, a
	// missing required field, or a value that failed validation.
	CodeBadRequest ErrorCode = "bad_request"
	// CodeUnauthorized means no valid credential was presented. The client
	// should authenticate and retry.
	CodeUnauthorized ErrorCode = "unauthorized"
	// CodeForbidden means the caller authenticated successfully but is not
	// permitted to perform this action. Retrying will not help.
	CodeForbidden ErrorCode = "forbidden"
	// CodeNotFound means the addressed resource does not exist, or the caller
	// is not allowed to know that it exists.
	CodeNotFound ErrorCode = "not_found"
	// CodeConflict means the request collided with the current state of the
	// resource, such as enrolling a device whose public key is already in use.
	CodeConflict ErrorCode = "conflict"
	// CodePayloadTooLarge means the request body exceeded the server limit.
	CodePayloadTooLarge ErrorCode = "payload_too_large"
	// CodeRateLimited means the caller exceeded its request budget. The
	// Retry-After header carries the number of seconds to wait.
	CodeRateLimited ErrorCode = "rate_limited"
	// CodeUnsupportedProtocol means the client and server protocol version
	// ranges do not overlap. One side must be upgraded.
	CodeUnsupportedProtocol ErrorCode = "unsupported_protocol_version"
	// CodeNotImplemented means the endpoint is a declared part of the API but
	// the behaviour behind it has not been built yet. Details carry the
	// roadmap phase that will deliver it.
	//
	// Headnet returns this rather than a fabricated success response: an
	// unimplemented feature must be visibly unimplemented.
	CodeNotImplemented ErrorCode = "not_implemented"
	// CodeUnavailable means a dependency the server needs (typically the
	// database) is not reachable right now. The request may succeed later.
	CodeUnavailable ErrorCode = "unavailable"
	// CodeInternal means the server hit an unexpected condition. The response
	// deliberately carries no detail; the request ID correlates it with the
	// server log.
	CodeInternal ErrorCode = "internal"
)

// HTTPStatus maps an error code to the HTTP status the server sends with it.
// Keeping the mapping here, rather than at each call site, is what guarantees
// that a given code always arrives with the same status.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeUnsupportedProtocol:
		// 426 tells the client the request cannot be served over the protocol
		// version it offered, which is exactly the situation here.
		return http.StatusUpgradeRequired
	case CodeNotImplemented:
		return http.StatusNotImplemented
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeInternal:
		return http.StatusInternalServerError
	default:
		// An unrecognised code is a programming error on the server side, and
		// 500 is the honest answer.
		return http.StatusInternalServerError
	}
}

// Retryable reports whether a client may reasonably retry the same request
// later. CLI and daemon backoff logic keys off this rather than duplicating a
// list of status codes.
func (c ErrorCode) Retryable() bool {
	switch c {
	case CodeRateLimited, CodeUnavailable, CodeInternal:
		return true
	default:
		return false
	}
}

// KnownErrorCodes lists every code the server may emit, in a stable order. It
// backs the contract test that keeps the OpenAPI document honest.
func KnownErrorCodes() []ErrorCode {
	return []ErrorCode{
		CodeBadRequest,
		CodeUnauthorized,
		CodeForbidden,
		CodeNotFound,
		CodeConflict,
		CodePayloadTooLarge,
		CodeRateLimited,
		CodeUnsupportedProtocol,
		CodeNotImplemented,
		CodeUnavailable,
		CodeInternal,
	}
}

// Error is the body of every non-2xx API response.
//
// Errors are always wrapped in an object rather than returned bare so that the
// envelope can grow new fields without breaking clients that already parse it.
type Error struct {
	// Code is the stable machine-readable classification.
	Code ErrorCode `json:"code"`
	// Message is a human-readable explanation. It is safe to display to an
	// operator and never contains secrets, key material or internal stack
	// detail.
	Message string `json:"message"`
	// Details carries structured, code-specific context — for example the
	// field that failed validation, or the roadmap phase behind a
	// not_implemented response. It is omitted when empty.
	Details map[string]string `json:"details,omitempty"`
	// RequestID correlates the response with the server-side log entry. An
	// operator reporting a bug should always be able to quote it.
	RequestID string `json:"request_id,omitempty"`
}

// ErrorResponse is the top-level envelope: {"error": {...}}.
type ErrorResponse struct {
	Error Error `json:"error"`
}

// NewErrorResponse builds an envelope for the given code and message.
func NewErrorResponse(code ErrorCode, message string) ErrorResponse {
	return ErrorResponse{Error: Error{Code: code, Message: message}}
}

// WithDetail returns a copy of the response carrying an extra detail field.
// It is a value method so that shared, package-level error templates cannot be
// mutated by a handler that decorates them.
func (r ErrorResponse) WithDetail(key, value string) ErrorResponse {
	details := make(map[string]string, len(r.Error.Details)+1)
	for k, v := range r.Error.Details {
		details[k] = v
	}
	details[key] = value
	r.Error.Details = details
	return r
}

// WithRequestID returns a copy of the response tagged with a request ID.
func (r ErrorResponse) WithRequestID(id string) ErrorResponse {
	r.Error.RequestID = id
	return r
}
