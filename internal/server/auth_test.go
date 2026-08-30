package server_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/logging"
	"github.com/CauaMora1s/Headnet/internal/server"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/internal/storage/storagetest"
	"github.com/CauaMora1s/Headnet/packages/api"
)

const (
	testEmail    = "ada@example.com"
	testPassword = "correct horse battery staple"
)

// authFixture is a server with one known account already created.
type authFixture struct {
	srv  *server.Server
	db   *storage.DB
	user *auth.User
}

// newAuthFixture builds a server and seeds an account directly through the
// store, so the login tests do not depend on bootstrap existing yet.
func newAuthFixture(t *testing.T, tweak func(*config.Config)) *authFixture {
	t.Helper()

	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	if tweak != nil {
		tweak(cfg)
	}

	db := storagetest.Open(t)
	users := auth.NewUserStore(db, auth.StoreOptions{
		// Cheap hashing: these tests are about the flow, not the cost.
		Params: auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})

	var user *auth.User
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		user, err = users.Create(t.Context(), tx, auth.NewUser{
			Email:    testEmail,
			Password: testPassword,
			Role:     auth.RoleAdmin,
		}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("seeding the account failed: %v", err)
	}

	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(), InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	return &authFixture{srv: srv, db: db, user: user}
}

// send issues a request, optionally carrying cookies and a CSRF token.
func (f *authFixture) send(
	t *testing.T, method, path, body string, cookies []*http.Cookie, csrf string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "192.0.2.1:1234"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// login signs in and returns the resulting cookies and CSRF token.
func (f *authFixture) login(t *testing.T) ([]*http.Cookie, string) {
	t.Helper()
	rec := f.send(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+testEmail+`","password":"`+testPassword+`"}`, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed with %d: %s", rec.Code, rec.Body.String())
	}
	var session api.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decoding the login response failed: %v", err)
	}
	return rec.Result().Cookies(), session.CSRFToken
}

func cookieNamed(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoginSucceedsAndSetsCookies(t *testing.T) {
	f := newAuthFixture(t, nil)
	cookies, csrf := f.login(t)

	if csrf == "" {
		t.Fatal("login returned no CSRF token")
	}

	session := cookieNamed(cookies, auth.SessionCookieName)
	if session == nil || session.Value == "" {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly {
		t.Error("the session cookie is not HttpOnly; an XSS bug could exfiltrate it")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", session.SameSite)
	}

	csrfCookie := cookieNamed(cookies, auth.CSRFCookieName)
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatal("no CSRF cookie was set")
	}
	if csrfCookie.HttpOnly {
		t.Error("the CSRF cookie is HttpOnly; the browser client cannot echo what it cannot read")
	}
}

func TestSessionCookieIsSecureOnlyForAnHTTPSDeployment(t *testing.T) {
	// Marking it Secure on a plain-HTTP development server would stop the
	// cookie being sent at all, and the developer would just see a login that
	// silently does not work.
	plain := newAuthFixture(t, nil)
	cookies, _ := plain.login(t)
	if cookieNamed(cookies, auth.SessionCookieName).Secure {
		t.Error("the session cookie is Secure on a plain-HTTP deployment")
	}

	secure := newAuthFixture(t, func(c *config.Config) {
		c.Server.BaseURL = "https://vpn.example.com"
	})
	cookies, _ = secure.login(t)
	if !cookieNamed(cookies, auth.SessionCookieName).Secure {
		t.Error("the session cookie is not Secure on an HTTPS deployment")
	}
}

func TestWrongPasswordAndUnknownAccountAreIndistinguishable(t *testing.T) {
	// If these differed in body, code or status, the login endpoint would be a
	// way to discover which email addresses are registered.
	f := newAuthFixture(t, nil)

	wrongPassword := f.send(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+testEmail+`","password":"definitely not the password"}`, nil, "")
	unknownAccount := f.send(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"nobody@example.com","password":"definitely not the password"}`, nil, "")

	if wrongPassword.Code != http.StatusUnauthorized || unknownAccount.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want both 401", wrongPassword.Code, unknownAccount.Code)
	}

	// The request ID differs by design, so blank it and compare everything
	// else — code, message and any details.
	strip := func(rec *httptest.ResponseRecorder) string {
		var resp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decoding failed: %v", err)
		}
		resp.Error.RequestID = ""
		normalised, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("re-encoding failed: %v", err)
		}
		return string(normalised)
	}
	if strip(wrongPassword) != strip(unknownAccount) {
		t.Fatalf("the two failures are distinguishable:\n  wrong password:  %s\n  unknown account: %s",
			strip(wrongPassword), strip(unknownAccount))
	}
}

func TestAFailedLoginSetsNoSessionCookie(t *testing.T) {
	f := newAuthFixture(t, nil)
	rec := f.send(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+testEmail+`","password":"wrong"}`, nil, "")

	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			t.Fatal("a failed login set a session cookie")
		}
	}
}

func TestLoginValidatesItsInput(t *testing.T) {
	f := newAuthFixture(t, nil)
	for _, body := range []string{
		``,
		`not json`,
		`{"email":"` + testEmail + `"}`, // no password
		`{"emial":"x","password":"y"}`,  // misspelled field
		`{"email":"` + testEmail + `","password":1}`, // wrong type
	} {
		rec := f.send(t, http.MethodPost, "/api/v1/auth/login", body, nil, "")
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnauthorized {
			t.Errorf("body %q gave %d, want 400 or 401", body, rec.Code)
		}
	}
}

func TestSessionEndpointReturnsTheSignedInUser(t *testing.T) {
	f := newAuthFixture(t, nil)
	cookies, _ := f.login(t)

	rec := f.send(t, http.MethodGet, "/api/v1/auth/session", "", cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var session api.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if session.User.Email != testEmail {
		t.Errorf("email = %q, want %q", session.User.Email, testEmail)
	}
	if session.User.Role != string(auth.RoleAdmin) {
		t.Errorf("role = %q, want admin", session.User.Role)
	}
	if session.ExpiresAt.IsZero() {
		t.Error("the session response carries no expiry")
	}
}

func TestTheSessionResponseNeverCarriesAPasswordHash(t *testing.T) {
	f := newAuthFixture(t, nil)
	cookies, _ := f.login(t)

	for _, path := range []string{"/api/v1/auth/session"} {
		body := f.send(t, http.MethodGet, path, "", cookies, "").Body.String()
		for _, leak := range []string{"argon2", "password", "hash"} {
			if strings.Contains(strings.ToLower(body), leak) {
				t.Fatalf("%s response mentions %q:\n%s", path, leak, body)
			}
		}
	}
}

func TestProtectedEndpointsRejectAnUnauthenticatedCaller(t *testing.T) {
	f := newAuthFixture(t, nil)

	rec := f.send(t, http.MethodGet, "/api/v1/auth/session", "", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var resp api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if resp.Error.Code != api.CodeUnauthorized {
		t.Fatalf("code = %q, want unauthorized", resp.Error.Code)
	}
}

func TestAnInvalidSessionCookieIsRejectedAndCleared(t *testing.T) {
	f := newAuthFixture(t, nil)

	rec := f.send(t, http.MethodGet, "/api/v1/auth/session", "",
		[]*http.Cookie{{Name: auth.SessionCookieName, Value: "forged"}}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	// The browser is told to drop it, so every later request does not retry a
	// token that can never work.
	cleared := cookieNamed(rec.Result().Cookies(), auth.SessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("the useless session cookie was not cleared: %+v", cleared)
	}
}

func TestLogoutRevokesTheSessionImmediately(t *testing.T) {
	// The whole reason sessions are stored server-side rather than being
	// self-contained tokens.
	f := newAuthFixture(t, nil)
	cookies, csrf := f.login(t)

	rec := f.send(t, http.MethodPost, "/api/v1/auth/logout", "", cookies, csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// The same cookie must now be worthless.
	after := f.send(t, http.MethodGet, "/api/v1/auth/session", "", cookies, "")
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("the session still works after logout: %d", after.Code)
	}
}

func TestLogoutClearsBothCookies(t *testing.T) {
	f := newAuthFixture(t, nil)
	cookies, csrf := f.login(t)

	rec := f.send(t, http.MethodPost, "/api/v1/auth/logout", "", cookies, csrf)
	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		c := cookieNamed(rec.Result().Cookies(), name)
		if c == nil || c.MaxAge >= 0 {
			t.Errorf("%s was not cleared on logout: %+v", name, c)
		}
	}
}

func TestStateChangingRequestsRequireACSRFToken(t *testing.T) {
	f := newAuthFixture(t, nil)
	cookies, csrf := f.login(t)

	tests := []struct {
		name string
		csrf string
	}{
		{"missing", ""},
		{"wrong", "not-the-token"},
		{"the session token instead", cookieNamed(cookies, auth.SessionCookieName).Value},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.send(t, http.MethodPost, "/api/v1/auth/logout", "", cookies, tt.csrf)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}

	// And the correct one still works, so the session was not damaged by the
	// rejected attempts.
	if rec := f.send(t, http.MethodPost, "/api/v1/auth/logout", "", cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("the valid CSRF token was rejected: %d", rec.Code)
	}
}

func TestACSRFTokenFromAnotherSessionIsRejected(t *testing.T) {
	// Binding the token to the session is what defeats an attacker who can set
	// a cookie on the victim's domain: they cannot produce a pair this server
	// issued for this session.
	f := newAuthFixture(t, nil)
	victimCookies, _ := f.login(t)
	_, attackerCSRF := f.login(t)

	rec := f.send(t, http.MethodPost, "/api/v1/auth/logout", "", victimCookies, attackerCSRF)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a CSRF token from another session", rec.Code)
	}
}

func TestSafeMethodsDoNotRequireACSRFToken(t *testing.T) {
	// Requiring a header on ordinary navigation would break the UI for no
	// benefit, since a GET must not change state anyway.
	f := newAuthFixture(t, nil)
	cookies, _ := f.login(t)

	if rec := f.send(t, http.MethodGet, "/api/v1/auth/session", "", cookies, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET with no CSRF token gave %d, want 200", rec.Code)
	}
}

func TestAuthStatusIsUnauthenticatedAndDisclosesLittle(t *testing.T) {
	f := newAuthFixture(t, nil)

	rec := f.send(t, http.MethodGet, "/api/v1/auth/status", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var status api.AuthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if status.BootstrapRequired {
		t.Error("bootstrap_required = true, but an account already exists")
	}
	if len(status.Providers) == 0 {
		t.Error("no providers were reported")
	}

	// It must not become a way to enumerate accounts or read configuration.
	body := rec.Body.String()
	for _, leak := range []string{testEmail, "net_TEST", "sqlite", "100.100"} {
		if strings.Contains(body, leak) {
			t.Errorf("the auth status response leaked %q:\n%s", leak, body)
		}
	}
}

func TestSessionsSurviveTheServerBeingRebuilt(t *testing.T) {
	// Sessions live in the database, not in server memory, so a restart must
	// not sign everybody out.
	f := newAuthFixture(t, nil)
	cookies, _ := f.login(t)

	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	restarted, err := server.New(server.Options{
		Config: cfg, DB: f.db, Logger: logging.Discard(), InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("rebuilding the server failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status after a restart = %d, want the session to survive", rec.Code)
	}
}

func TestAuthenticationHasATighterRateLimitThanTheRestOfTheAPI(t *testing.T) {
	// Repeated guessing is the entire attack against login, so its ceiling has
	// to be lower than the general one.
	f := newAuthFixture(t, func(c *config.Config) {
		c.RateLimit.Enabled = true
		c.RateLimit.RequestsPerMinute = 120
		c.RateLimit.Burst = 60
		c.RateLimit.AuthRequestsPerMinute = 10
		c.RateLimit.AuthBurst = 3
	})

	var limited bool
	for range 10 {
		rec := f.send(t, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+testEmail+`","password":"wrong"}`, nil, "")
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("the login endpoint was never rate limited within its burst allowance")
	}

	// A non-auth endpoint, with its far larger budget, is still available.
	if rec := f.send(t, http.MethodGet, "/api/v1/version", "", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("/api/v1/version gave %d; the auth limiter must not throttle the whole API", rec.Code)
	}
}

func TestLoginRehashesAWeakPasswordInPlace(t *testing.T) {
	// A user whose hash predates a cost increase gets upgraded on their next
	// sign-in, without noticing or having to change anything.
	db := storagetest.Open(t)
	weak := auth.NewUserStore(db, auth.StoreOptions{
		Params: auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})

	var user *auth.User
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		user, err = weak.Create(t.Context(), tx,
			auth.NewUser{Email: testEmail, Password: testPassword}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("seeding failed: %v", err)
	}
	if _, needsRehash, _ := user.VerifyPassword(testPassword); !needsRehash {
		t.Fatal("the seeded hash was not weak enough to need rehashing")
	}

	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(), InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"email":"`+testEmail+`","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}

	current := auth.NewUserStore(db, auth.StoreOptions{})
	reloaded, err := current.ByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reloading the user failed: %v", err)
	}
	ok, needsRehash, err := reloaded.VerifyPassword(testPassword)
	if err != nil || !ok {
		t.Fatalf("the password stopped working after the rehash: ok=%v err=%v", ok, err)
	}
	if needsRehash {
		t.Fatal("the hash was not upgraded on sign-in")
	}
}
