// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/headnet/headnet/packages/api"
)

func TestEveryKnownCodeHasADeliberateStatus(t *testing.T) {
	want := map[api.ErrorCode]int{
		api.CodeBadRequest:          http.StatusBadRequest,
		api.CodeUnauthorized:        http.StatusUnauthorized,
		api.CodeForbidden:           http.StatusForbidden,
		api.CodeNotFound:            http.StatusNotFound,
		api.CodeConflict:            http.StatusConflict,
		api.CodePayloadTooLarge:     http.StatusRequestEntityTooLarge,
		api.CodeRateLimited:         http.StatusTooManyRequests,
		api.CodeUnsupportedProtocol: http.StatusUpgradeRequired,
		api.CodeNotImplemented:      http.StatusNotImplemented,
		api.CodeUnavailable:         http.StatusServiceUnavailable,
		api.CodeInternal:            http.StatusInternalServerError,
	}

	known := api.KnownErrorCodes()
	if len(known) != len(want) {
		t.Fatalf("KnownErrorCodes() has %d entries but the test table has %d; "+
			"a code was added without deciding on its HTTP status", len(known), len(want))
	}
	for _, code := range known {
		expected, ok := want[code]
		if !ok {
			t.Fatalf("error code %q is not covered by the status table", code)
		}
		if got := code.HTTPStatus(); got != expected {
			t.Errorf("%q.HTTPStatus() = %d, want %d", code, got, expected)
		}
	}
}

func TestKnownErrorCodesHasNoDuplicates(t *testing.T) {
	seen := make(map[api.ErrorCode]struct{})
	for _, code := range api.KnownErrorCodes() {
		if _, dup := seen[code]; dup {
			t.Fatalf("error code %q is listed twice", code)
		}
		seen[code] = struct{}{}
	}
}

func TestUnknownCodeFallsBackToInternalServerError(t *testing.T) {
	if got := api.ErrorCode("no_such_code").HTTPStatus(); got != http.StatusInternalServerError {
		t.Fatalf("unknown code status = %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestRetryable(t *testing.T) {
	retryable := map[api.ErrorCode]bool{
		api.CodeRateLimited: true,
		api.CodeUnavailable: true,
		api.CodeInternal:    true,
	}
	for _, code := range api.KnownErrorCodes() {
		if got, want := code.Retryable(), retryable[code]; got != want {
			t.Errorf("%q.Retryable() = %v, want %v", code, got, want)
		}
	}
}

func TestErrorResponseSerialisation(t *testing.T) {
	resp := api.NewErrorResponse(api.CodeBadRequest, "listen address is invalid").
		WithDetail("field", "server.listen").
		WithRequestID("req_ABC")

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshalling the error envelope failed: %v", err)
	}

	const want = `{"error":{"code":"bad_request","message":"listen address is invalid",` +
		`"details":{"field":"server.listen"},"request_id":"req_ABC"}}`
	if string(raw) != want {
		t.Fatalf("error envelope =\n  %s\nwant\n  %s", raw, want)
	}
}

func TestErrorResponseOmitsEmptyOptionalFields(t *testing.T) {
	raw, err := json.Marshal(api.NewErrorResponse(api.CodeInternal, "internal error"))
	if err != nil {
		t.Fatalf("marshalling failed: %v", err)
	}
	const want = `{"error":{"code":"internal","message":"internal error"}}`
	if string(raw) != want {
		t.Fatalf("error envelope = %s, want %s", raw, want)
	}
}

func TestWithDetailDoesNotMutateTheOriginal(t *testing.T) {
	// Handlers decorate shared error templates; if WithDetail mutated in
	// place, one request could leak its context into another's response.
	base := api.NewErrorResponse(api.CodeForbidden, "denied")
	first := base.WithDetail("device", "dev_ONE")
	second := base.WithDetail("device", "dev_TWO")

	if base.Error.Details != nil {
		t.Fatalf("WithDetail mutated the base response: %v", base.Error.Details)
	}
	if first.Error.Details["device"] != "dev_ONE" {
		t.Fatalf("first response = %v, want device=dev_ONE", first.Error.Details)
	}
	if second.Error.Details["device"] != "dev_TWO" {
		t.Fatalf("second response = %v, want device=dev_TWO", second.Error.Details)
	}
}

func TestWithDetailAccumulates(t *testing.T) {
	resp := api.NewErrorResponse(api.CodeBadRequest, "invalid").
		WithDetail("field", "network.ipv4_cidr").
		WithDetail("reason", "not a valid prefix")
	if len(resp.Error.Details) != 2 {
		t.Fatalf("details = %v, want two entries", resp.Error.Details)
	}
}

func TestHealthStatusWorse(t *testing.T) {
	tests := []struct {
		a, b, want api.HealthStatus
	}{
		{api.StatusOK, api.StatusOK, api.StatusOK},
		{api.StatusOK, api.StatusDegraded, api.StatusDegraded},
		{api.StatusDegraded, api.StatusOK, api.StatusDegraded},
		{api.StatusDegraded, api.StatusDown, api.StatusDown},
		{api.StatusDown, api.StatusOK, api.StatusDown},
		{api.StatusDown, api.StatusDegraded, api.StatusDown},
	}
	for _, tt := range tests {
		if got := tt.a.Worse(tt.b); got != tt.want {
			t.Errorf("%q.Worse(%q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestHealthStatusWorseTreatsUnknownAsWorst(t *testing.T) {
	if got := api.StatusDown.Worse("bogus"); got != "bogus" {
		t.Fatalf("Worse with an unrecognised status = %q, want it to win", got)
	}
}

func TestBasePathIsVersioned(t *testing.T) {
	if api.BasePath != "/api/v1" {
		t.Fatalf("BasePath = %q; changing it is a breaking API change and needs an ADR", api.BasePath)
	}
}
