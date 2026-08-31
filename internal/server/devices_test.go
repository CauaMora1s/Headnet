package server_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// wgKey returns a syntactically valid WireGuard public key.
func wgKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a key failed: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// signedIn is a fixture with an authenticated session ready to use.
type signedIn struct {
	*authFixture
	cookies []*http.Cookie
	csrf    string
}

func newSignedIn(t *testing.T, tweak func(*config.Config)) *signedIn {
	t.Helper()
	f := newAuthFixture(t, tweak)
	cookies, csrf := f.login(t)
	return &signedIn{authFixture: f, cookies: cookies, csrf: csrf}
}

// addMember creates a second, non-admin account and signs in as it.
func (s *signedIn) addMember(t *testing.T, email string) ([]*http.Cookie, string) {
	t.Helper()

	users := auth.NewUserStore(s.db, auth.StoreOptions{
		Params: auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	err := s.db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := users.Create(t.Context(), tx, auth.NewUser{
			Email: email, Password: testPassword, Role: auth.RoleMember,
		}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("creating %s failed: %v", email, err)
	}

	rec := s.send(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"`+testPassword+`"}`, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("signing in as %s failed: %d %s", email, rec.Code, rec.Body.String())
	}
	var session api.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	return rec.Result().Cookies(), session.CSRFToken
}

// registerDevice adds a device as the given identity.
func (s *signedIn) registerDevice(
	t *testing.T, name string, cookies []*http.Cookie, csrf string,
) api.Device {
	t.Helper()
	body := `{"name":"` + name + `","public_key":"` + wgKey(t) + `","os":"linux"}`
	rec := s.send(t, http.MethodPost, "/api/v1/devices", body, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("registering %s failed: %d %s", name, rec.Code, rec.Body.String())
	}
	var device api.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &device); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	return device
}

func TestRegisterDeviceOverTheAPI(t *testing.T) {
	f := newSignedIn(t, nil)
	device := f.registerDevice(t, "laptop", f.cookies, f.csrf)

	if device.Name != "laptop" {
		t.Errorf("Name = %q", device.Name)
	}
	if device.IPv4 == "" {
		t.Error("no address was allocated")
	}
	if device.RevokedAt != nil {
		t.Error("a new device is revoked")
	}
	if device.UserID != f.user.ID {
		t.Errorf("UserID = %q, want the signed-in user", device.UserID)
	}
}

func TestDeviceResponsesCarryNoPrivateKeyMaterial(t *testing.T) {
	// The property the whole control-plane security argument rests on. There
	// is no field for it, so this asserts the shape rather than a value.
	f := newSignedIn(t, nil)
	f.registerDevice(t, "laptop", f.cookies, f.csrf)

	for _, path := range []string{"/api/v1/devices"} {
		body := f.send(t, http.MethodGet, path, "", f.cookies, "").Body.String()
		for _, forbidden := range []string{"private_key", "privatekey", "secret_key"} {
			if strings.Contains(strings.ToLower(body), forbidden) {
				t.Fatalf("%s mentions %q:\n%s", path, forbidden, body)
			}
		}
	}
}

func TestRegisterDeviceValidatesItsInput(t *testing.T) {
	f := newSignedIn(t, nil)

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"no name", `{"public_key":"` + wgKey(t) + `"}`, http.StatusBadRequest},
		{"blank name", `{"name":"  ","public_key":"` + wgKey(t) + `"}`, http.StatusBadRequest},
		{"no key", `{"name":"laptop"}`, http.StatusBadRequest},
		{"malformed key", `{"name":"laptop","public_key":"not base64"}`, http.StatusBadRequest},
		{"short key", `{"name":"laptop","public_key":"` +
			base64.StdEncoding.EncodeToString(make([]byte, 16)) + `"}`, http.StatusBadRequest},
		{"all-zero key", `{"name":"laptop","public_key":"` +
			base64.StdEncoding.EncodeToString(make([]byte, 32)) + `"}`, http.StatusBadRequest},
		{"unknown field", `{"name":"laptop","public_ky":"x"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.send(t, http.MethodPost, "/api/v1/devices", tt.body, f.cookies, f.csrf)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestDuplicatePublicKeyIsAConflict(t *testing.T) {
	f := newSignedIn(t, nil)
	key := wgKey(t)
	body := `{"name":"first","public_key":"` + key + `"}`

	if rec := f.send(t, http.MethodPost, "/api/v1/devices", body, f.cookies, f.csrf); rec.Code != http.StatusCreated {
		t.Fatalf("the first registration failed: %d", rec.Code)
	}

	rec := f.send(t, http.MethodPost, "/api/v1/devices",
		`{"name":"second","public_key":"`+key+`"}`, f.cookies, f.csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var resp api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if resp.Error.Code != api.CodeConflict {
		t.Fatalf("code = %q, want conflict", resp.Error.Code)
	}
}

func TestDeviceEndpointsRequireASession(t *testing.T) {
	f := newSignedIn(t, nil)
	device := f.registerDevice(t, "laptop", f.cookies, f.csrf)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/devices"},
		{http.MethodPost, "/api/v1/devices"},
		{http.MethodGet, "/api/v1/devices/" + device.ID},
		{http.MethodDelete, "/api/v1/devices/" + device.ID},
	}
	for _, tt := range tests {
		rec := f.send(t, tt.method, tt.path, "", nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tt.method, tt.path, rec.Code)
		}
	}
}

func TestDeviceMutationsRequireACSRFToken(t *testing.T) {
	f := newSignedIn(t, nil)
	device := f.registerDevice(t, "laptop", f.cookies, f.csrf)

	for _, tt := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/devices", `{"name":"x","public_key":"` + wgKey(t) + `"}`},
		{http.MethodDelete, "/api/v1/devices/" + device.ID, ""},
	} {
		rec := f.send(t, tt.method, tt.path, tt.body, f.cookies, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF = %d, want 403", tt.method, tt.path, rec.Code)
		}
	}

	// Reads are exempt, or ordinary navigation would break.
	if rec := f.send(t, http.MethodGet, "/api/v1/devices", "", f.cookies, ""); rec.Code != http.StatusOK {
		t.Errorf("GET without CSRF = %d, want 200", rec.Code)
	}
}

func TestAMemberCannotSeeAnotherUsersDevice(t *testing.T) {
	// The scope goes into the store query, so an endpoint cannot forget it.
	f := newSignedIn(t, nil)
	adminDevice := f.registerDevice(t, "admin-laptop", f.cookies, f.csrf)

	memberCookies, memberCSRF := f.addMember(t, "member@example.com")
	memberDevice := f.registerDevice(t, "member-laptop", memberCookies, memberCSRF)

	// The member's list holds only their own.
	rec := f.send(t, http.MethodGet, "/api/v1/devices", "", memberCookies, "")
	var list api.DeviceList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if len(list.Devices) != 1 || list.Devices[0].ID != memberDevice.ID {
		t.Fatalf("a member saw %d devices, want only their own", len(list.Devices))
	}

	// Fetching another user's device is a 404, not a 403: "you may not see
	// this" would confirm it exists.
	if rec := f.send(t, http.MethodGet, "/api/v1/devices/"+adminDevice.ID, "", memberCookies, ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET another user's device = %d, want 404", rec.Code)
	}
	if rec := f.send(t, http.MethodDelete, "/api/v1/devices/"+adminDevice.ID, "", memberCookies, memberCSRF); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE another user's device = %d, want 404", rec.Code)
	}

	// And it really was not revoked.
	rec = f.send(t, http.MethodGet, "/api/v1/devices/"+adminDevice.ID, "", f.cookies, "")
	var reloaded api.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &reloaded); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if reloaded.RevokedAt != nil {
		t.Fatal("a member revoked another user's device")
	}
}

func TestAnAdminSeesEveryDevice(t *testing.T) {
	f := newSignedIn(t, nil)
	f.registerDevice(t, "admin-laptop", f.cookies, f.csrf)

	memberCookies, memberCSRF := f.addMember(t, "member@example.com")
	f.registerDevice(t, "member-laptop", memberCookies, memberCSRF)

	rec := f.send(t, http.MethodGet, "/api/v1/devices", "", f.cookies, "")
	var list api.DeviceList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if len(list.Devices) != 2 {
		t.Fatalf("the admin saw %d devices, want 2", len(list.Devices))
	}
}

func TestRevokeDeviceOverTheAPI(t *testing.T) {
	f := newSignedIn(t, nil)
	device := f.registerDevice(t, "laptop", f.cookies, f.csrf)

	rec := f.send(t, http.MethodDelete, "/api/v1/devices/"+device.ID, "", f.cookies, f.csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	rec = f.send(t, http.MethodGet, "/api/v1/devices/"+device.ID, "", f.cookies, "")
	var reloaded api.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &reloaded); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if reloaded.RevokedAt == nil {
		t.Fatal("the device is not marked revoked")
	}
	if reloaded.IPv4 != "" {
		t.Errorf("the revoked device still shows %s; the address should be back in the pool", reloaded.IPv4)
	}

	// Revoking again is a conflict, not a silent success.
	if rec := f.send(t, http.MethodDelete, "/api/v1/devices/"+device.ID, "", f.cookies, f.csrf); rec.Code != http.StatusConflict {
		t.Errorf("the second revoke = %d, want 409", rec.Code)
	}
}

func TestUnknownDeviceIsNotFound(t *testing.T) {
	f := newSignedIn(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		csrf := ""
		if method == http.MethodDelete {
			csrf = f.csrf
		}
		rec := f.send(t, method, "/api/v1/devices/dev_NOSUCHTHING", "", f.cookies, csrf)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", method, rec.Code)
		}
	}
}

// --- setup keys -------------------------------------------------------------

// createSetupKey issues a key and returns the response.
func (s *signedIn) createSetupKey(t *testing.T, body string) api.CreateSetupKeyResponse {
	t.Helper()
	rec := s.send(t, http.MethodPost, "/api/v1/setup-keys", body, s.cookies, s.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating a setup key failed: %d %s", rec.Code, rec.Body.String())
	}
	var out api.CreateSetupKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	return out
}

func TestCreateSetupKeyReturnsItOnce(t *testing.T) {
	f := newSignedIn(t, nil)
	created := f.createSetupKey(t, `{"description":"build agents","max_uses":1}`)

	if created.SetupKey == "" {
		t.Fatal("no setup key was returned")
	}
	if created.Key.MaxUses != 1 {
		t.Errorf("MaxUses = %d, want 1", created.Key.MaxUses)
	}
	if created.Key.ExpiresAt == nil {
		t.Error("the key got no expiry; expiry is supposed to be the default")
	}
	if !created.Key.Usable {
		t.Error("a freshly created key is not usable")
	}

	// Listing must never hand the key back.
	rec := f.send(t, http.MethodGet, "/api/v1/setup-keys", "", f.cookies, "")
	if strings.Contains(rec.Body.String(), created.SetupKey) {
		t.Fatalf("the key list contains the raw key:\n%s", rec.Body.String())
	}

	var list api.SetupKeyList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if len(list.Keys) != 1 {
		t.Fatalf("listed %d keys, want 1", len(list.Keys))
	}
	if !strings.HasPrefix(created.SetupKey, list.Keys[0].DisplayHint) {
		t.Error("the display hint does not match the key it identifies")
	}
}

func TestSetupKeysAreAdministrative(t *testing.T) {
	// A setup key mints a credential that puts a machine on the network, so
	// issuing one is an administrator's decision.
	f := newSignedIn(t, nil)
	memberCookies, memberCSRF := f.addMember(t, "member@example.com")

	tests := []struct {
		method string
		path   string
		body   string
		csrf   string
	}{
		{http.MethodPost, "/api/v1/setup-keys", `{"max_uses":1}`, memberCSRF},
		{http.MethodGet, "/api/v1/setup-keys", "", ""},
		{http.MethodDelete, "/api/v1/setup-keys/sk_ANY", "", memberCSRF},
	}
	for _, tt := range tests {
		rec := f.send(t, tt.method, tt.path, tt.body, memberCookies, tt.csrf)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a member = %d, want 403", tt.method, tt.path, rec.Code)
		}
	}
}

func TestEnrolWithASetupKeyOverTheAPI(t *testing.T) {
	f := newSignedIn(t, nil)
	created := f.createSetupKey(t, `{"description":"agents","max_uses":1}`)

	body := `{"setup_key":"` + created.SetupKey + `","name":"agent-1","public_key":"` + wgKey(t) + `"}`
	rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", body, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var device api.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &device); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if device.IPv4 == "" {
		t.Error("the enrolled device got no address")
	}
	if device.EnrolledWith != created.Key.ID {
		t.Errorf("EnrolledWith = %q, want %q", device.EnrolledWith, created.Key.ID)
	}

	// Single use means single use.
	second := `{"setup_key":"` + created.SetupKey + `","name":"agent-2","public_key":"` + wgKey(t) + `"}`
	if rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", second, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the second enrolment = %d, want 401", rec.Code)
	}
}

func TestEnrolmentNeedsNoSession(t *testing.T) {
	// The setup key is the credential; that is the whole point of a headless
	// enrolment.
	f := newSignedIn(t, nil)
	created := f.createSetupKey(t, `{}`)

	body := `{"setup_key":"` + created.SetupKey + `","name":"agent","public_key":"` + wgKey(t) + `"}`
	if rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", body, nil, ""); rec.Code != http.StatusCreated {
		t.Fatalf("unauthenticated enrolment = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestEveryRejectedSetupKeyLooksTheSame(t *testing.T) {
	// Distinguishing "revoked" from "expired" from "no such key" tells whoever
	// is holding a rejected key whether it ever existed.
	f := newSignedIn(t, nil)

	revoked := f.createSetupKey(t, `{}`)
	if rec := f.send(t, http.MethodDelete, "/api/v1/setup-keys/"+revoked.Key.ID, "", f.cookies, f.csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("revoking failed: %d", rec.Code)
	}
	exhausted := f.createSetupKey(t, `{"max_uses":1}`)
	spend := `{"setup_key":"` + exhausted.SetupKey + `","name":"first","public_key":"` + wgKey(t) + `"}`
	if rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", spend, nil, ""); rec.Code != http.StatusCreated {
		t.Fatalf("spending the key failed: %d", rec.Code)
	}

	bodies := map[string]string{
		"revoked":   revoked.SetupKey,
		"exhausted": exhausted.SetupKey,
		"unknown":   "not-a-real-setup-key",
		"empty":     "",
	}

	var seen []string
	for name, key := range bodies {
		body := `{"setup_key":"` + key + `","name":"agent","public_key":"` + wgKey(t) + `"}`
		rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", body, nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s key = %d, want 401: %s", name, rec.Code, rec.Body.String())
		}

		var resp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decoding failed: %v", err)
		}
		resp.Error.RequestID = ""
		normalised, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("re-encoding failed: %v", err)
		}
		seen = append(seen, string(normalised))
	}

	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[0] {
			t.Fatalf("rejected setup keys are distinguishable:\n  %s\n  %s", seen[0], seen[i])
		}
	}
}

func TestRevokedSetupKeyCannotEnrol(t *testing.T) {
	f := newSignedIn(t, nil)
	created := f.createSetupKey(t, `{}`)

	if rec := f.send(t, http.MethodDelete, "/api/v1/setup-keys/"+created.Key.ID, "", f.cookies, f.csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("revoking failed: %d %s", rec.Code, rec.Body.String())
	}

	body := `{"setup_key":"` + created.SetupKey + `","name":"agent","public_key":"` + wgKey(t) + `"}`
	if rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", body, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key enrolled a device: %d", rec.Code)
	}
}

func TestNeverExpiringRequiresAnExplicitFlag(t *testing.T) {
	f := newSignedIn(t, nil)

	if created := f.createSetupKey(t, `{}`); created.Key.ExpiresAt == nil {
		t.Error("a key created without an expiry got none")
	}
	if created := f.createSetupKey(t, `{"never_expires":true}`); created.Key.ExpiresAt != nil {
		t.Error("never_expires still produced an expiry")
	}
}

func TestCreateSetupKeyValidatesItsInput(t *testing.T) {
	f := newSignedIn(t, nil)
	for _, body := range []string{
		`{"max_uses":-1}`,
		`{"expires_in_hours":-1}`,
		`{"max_uss":1}`, // misspelled field
		`not json`,
	} {
		rec := f.send(t, http.MethodPost, "/api/v1/setup-keys", body, f.cookies, f.csrf)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q = %d, want 400", body, rec.Code)
		}
	}
}

func TestRevokingAnUnknownSetupKey(t *testing.T) {
	f := newSignedIn(t, nil)
	rec := f.send(t, http.MethodDelete, "/api/v1/setup-keys/sk_NOSUCHTHING", "", f.cookies, f.csrf)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEnrolmentIsRateLimitedLikeLogin(t *testing.T) {
	// It is unauthenticated and a setup key is guessable in principle, so it
	// gets the tighter budget rather than the general one.
	f := newSignedIn(t, func(c *config.Config) {
		c.RateLimit.Enabled = true
		c.RateLimit.RequestsPerMinute = 120
		c.RateLimit.Burst = 60
		c.RateLimit.AuthRequestsPerMinute = 10
		c.RateLimit.AuthBurst = 3
	})

	var limited bool
	for range 12 {
		body := `{"setup_key":"guess","name":"agent","public_key":"` + wgKey(t) + `"}`
		if rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", body, nil, ""); rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("enrolment was never rate limited; a setup key could be guessed at full speed")
	}
}
