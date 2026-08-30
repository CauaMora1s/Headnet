package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/internal/logging"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// request runs a handler against a synthetic request and returns the recorder.
func request(t *testing.T, h http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = "192.0.2.10:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeError parses an error envelope out of a response.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorResponse {
	t.Helper()
	var got api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response body is not an error envelope: %q: %v", rec.Body.String(), err)
	}
	return got
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, r, http.StatusCreated, map[string]string{"id": "dev_1"})
	})

	rec := request(t, h, http.MethodGet, "/", "")
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"id":"dev_1"}` {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestWriteJSONFailsSafelyOnUnencodableValues(t *testing.T) {
	t.Parallel()
	// An encoding failure must not leave a half-written body behind a 200.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, r, http.StatusOK, make(chan int))
	})

	rec := request(t, h, http.MethodGet, "/", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeError(t, rec); got.Error.Code != api.CodeInternal {
		t.Fatalf("code = %q, want internal", got.Error.Code)
	}
}

func TestWriteErrorUsesTheStatusForTheCode(t *testing.T) {
	t.Parallel()
	for _, code := range api.KnownErrorCodes() {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpapi.WriteError(w, r, code, "something went wrong")
		})
		rec := request(t, h, http.MethodGet, "/", "")
		if rec.Code != code.HTTPStatus() {
			t.Errorf("%q produced status %d, want %d", code, rec.Code, code.HTTPStatus())
		}
		if got := decodeError(t, rec); got.Error.Code != code {
			t.Errorf("envelope code = %q, want %q", got.Error.Code, code)
		}
	}
}

func TestErrorsCarryTheRequestID(t *testing.T) {
	t.Parallel()
	// An operator has to be able to take a user's screenshot and find the
	// matching server-side log line.
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpapi.WriteError(w, r, api.CodeForbidden, "denied")
		}),
		httpapi.RequestID(),
	)

	rec := request(t, h, http.MethodGet, "/", "")
	header := rec.Header().Get(httpapi.RequestIDHeader)
	if header == "" {
		t.Fatal("no request ID was returned to the client")
	}
	if got := decodeError(t, rec); got.Error.RequestID != header {
		t.Fatalf("envelope request_id = %q, header = %q; they must match", got.Error.RequestID, header)
	}
}

func TestNotImplementedIsHonest(t *testing.T) {
	t.Parallel()
	// A fabricated success would make an unbuilt feature look finished. For a
	// VPN that is worse than an error: it can convince someone they are
	// protected when nothing is running.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpapi.NotImplemented(w, r, "device enrollment", "Phase 2")
	})

	rec := request(t, h, http.MethodPost, "/api/v1/devices", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Error.Code != api.CodeNotImplemented {
		t.Errorf("code = %q, want not_implemented", got.Error.Code)
	}
	if got.Error.Details["roadmap_phase"] != "Phase 2" {
		t.Errorf("details = %v, want the roadmap phase", got.Error.Details)
	}
	if !strings.Contains(got.Error.Message, "device enrollment") {
		t.Errorf("message = %q, want it to name the feature", got.Error.Message)
	}
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()
	type payload struct {
		Name string `json:"name"`
	}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p payload
		if err := httpapi.DecodeJSON(w, r, &p); err != nil {
			return
		}
		httpapi.WriteJSON(w, r, http.StatusOK, p)
	})

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   api.ErrorCode
	}{
		{"valid", `{"name":"laptop"}`, http.StatusOK, ""},
		{"empty", ``, http.StatusBadRequest, api.CodeBadRequest},
		{"malformed", `{"name":`, http.StatusBadRequest, api.CodeBadRequest},
		{"unknown field", `{"nmae":"laptop"}`, http.StatusBadRequest, api.CodeBadRequest},
		{"trailing content", `{"name":"a"}{"name":"b"}`, http.StatusBadRequest, api.CodeBadRequest},
		{"array instead of object", `[1,2,3]`, http.StatusBadRequest, api.CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode != "" && decodeError(t, rec).Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", decodeError(t, rec).Error.Code, tt.wantCode)
			}
		})
	}
}

func TestDecodeJSONRejectsAMisspelledField(t *testing.T) {
	t.Parallel()
	// Silently defaulting a misspelled security-relevant field is exactly the
	// failure mode strict decoding exists to prevent.
	type payload struct {
		Ephemeral bool `json:"ephemeral"`
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p payload
		if err := httpapi.DecodeJSON(w, r, &p); err != nil {
			return
		}
		t.Error("DecodeJSON accepted a body with a misspelled field")
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ephemerall":true}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestDecodeJSONRejectsNonJSONContentTypes(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct{}
		_ = httpapi.DecodeJSON(w, r, &p)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=laptop"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDecodeJSONAcceptsAContentTypeWithParameters(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Name string `json:"name"`
		}
		if err := httpapi.DecodeJSON(w, r, &p); err != nil {
			return
		}
		httpapi.WriteJSON(w, r, http.StatusOK, p)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"laptop"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestMaxBodyBytes(t *testing.T) {
	t.Parallel()
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var p struct {
				Name string `json:"name"`
			}
			if err := httpapi.DecodeJSON(w, r, &p); err != nil {
				return
			}
			httpapi.WriteJSON(w, r, http.StatusOK, p)
		}),
		httpapi.MaxBodyBytes(32),
	)

	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"`+strings.Repeat("x", 512)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if got := decodeError(t, rec); got.Error.Code != api.CodePayloadTooLarge {
		t.Fatalf("code = %q, want payload_too_large", got.Error.Code)
	}
}

func TestRecovererTurnsAPanicIntoA500(t *testing.T) {
	t.Parallel()
	// One malformed request must not take down a control plane a whole
	// network depends on.
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.Options{Output: buf})
	if err != nil {
		t.Fatalf("building the logger failed: %v", err)
	}

	h := httpapi.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("something exploded")
		}),
		httpapi.RequestID(),
		httpapi.Recoverer(logger),
	)

	rec := request(t, h, http.MethodGet, "/", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(buf.String(), "recovered from a panic") {
		t.Errorf("the panic was not logged:\n%s", buf.String())
	}
}

func TestRecovererDoesNotLeakInternals(t *testing.T) {
	t.Parallel()
	// A stack trace discloses internal paths and structure, so it belongs in
	// the log and nowhere near the response.
	buf := &bytes.Buffer{}
	logger, _ := logging.New(logging.Options{Output: buf})

	h := httpapi.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("secret internal detail /var/lib/headnet/key")
		}),
		httpapi.Recoverer(logger),
	)

	rec := request(t, h, http.MethodGet, "/", "")
	body := rec.Body.String()
	for _, leak := range []string{"secret internal detail", "/var/lib/headnet", "goroutine"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the response leaked %q:\n%s", leak, body)
		}
	}
}

func TestRecovererRepanicsOnAbortHandler(t *testing.T) {
	t.Parallel()
	// http.ErrAbortHandler is how a handler abandons a response on purpose;
	// swallowing it would produce a spurious 500 in the logs.
	logger := logging.Discard()
	h := httpapi.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		}),
		httpapi.Recoverer(logger),
	)

	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("Recoverer swallowed http.ErrAbortHandler")
		}
	}()
	request(t, h, http.MethodGet, "/", "")
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpapi.SecurityHeaders(false),
	)

	rec := request(t, h, http.MethodGet, "/", "")
	want := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
}

func TestHSTSIsOnlySentWhenAsked(t *testing.T) {
	t.Parallel()
	// Sending HSTS from a plain-HTTP development server would pin a
	// developer's browser to HTTPS on localhost, which is tedious to undo.
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	off := request(t, httpapi.Chain(ok, httpapi.SecurityHeaders(false)), http.MethodGet, "/", "")
	if got := off.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS was sent with hsts=false: %q", got)
	}

	on := request(t, httpapi.Chain(ok, httpapi.SecurityHeaders(true)), http.MethodGet, "/", "")
	if got := on.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=") {
		t.Errorf("HSTS = %q, want a max-age", got)
	}
}

func TestNotFoundHandlerUsesTheStandardEnvelope(t *testing.T) {
	t.Parallel()
	rec := request(t, httpapi.NotFoundHandler(), http.MethodGet, "/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := decodeError(t, rec); got.Error.Code != api.CodeNotFound {
		t.Fatalf("code = %q, want not_found", got.Error.Code)
	}
}

func TestChainAppliesMiddlewareOutermostFirst(t *testing.T) {
	t.Parallel()
	var order []string
	mark := func(name string) httpapi.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := httpapi.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }),
		mark("first"), mark("second"),
	)
	request(t, h, http.MethodGet, "/", "")

	want := []string{"first", "second", "handler"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
}

func TestClientIPReadsTheTransportPeer(t *testing.T) {
	t.Parallel()
	// Forwarding headers are trivially forged by anyone who can reach the
	// server directly; honouring them would let an attacker rotate a header
	// to evade the rate limiter entirely.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	req.Header.Set("X-Real-IP", "203.0.113.98")

	if got := httpapi.ClientIP(req); got != "192.0.2.10" {
		t.Fatalf("ClientIP = %q, want the transport peer 192.0.2.10", got)
	}
}

func TestClientIPHandlesAnAddressWithoutAPort(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "@"
	if got := httpapi.ClientIP(req); got != "@" {
		t.Fatalf("ClientIP = %q, want the address returned verbatim", got)
	}
}

func TestRequestLoggerOmitsTheQueryString(t *testing.T) {
	t.Parallel()
	// OIDC callbacks and enrollment flows carry codes and tokens as query
	// parameters, and logs are routinely shipped somewhere less protected.
	buf := &bytes.Buffer{}
	logger, _ := logging.New(logging.Options{Output: buf})

	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpapi.RequestLogger(logger),
	)
	request(t, h, http.MethodGet, "/api/v1/callback?code=SUPER_SECRET_CODE&state=xyz", "")

	if strings.Contains(buf.String(), "SUPER_SECRET_CODE") {
		t.Fatalf("the request log leaked a query parameter:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "/api/v1/callback") {
		t.Fatalf("the request log did not record the path:\n%s", buf.String())
	}
}

func TestRequestLoggerRecordsTheStatusAndSize(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger, _ := logging.New(logging.Options{Output: buf})

	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("hello"))
		}),
		httpapi.RequestLogger(logger),
	)
	request(t, h, http.MethodGet, "/", "")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if rec["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want 418", rec["status"])
	}
	if rec["bytes"] != float64(5) {
		t.Errorf("bytes = %v, want 5", rec["bytes"])
	}
}
