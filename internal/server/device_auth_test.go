package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/packages/api"
)

func (f *signedIn) enrolDeviceToken(t *testing.T) api.EnrollDeviceResponse {
	t.Helper()
	key := f.createSetupKey(t, `{"max_uses":1}`)
	body := `{"setup_key":"` + key.SetupKey + `","name":"agent","public_key":"` + wgKey(t) + `"}`
	rec := f.send(t, http.MethodPost, "/api/v1/devices/enroll", body, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("enrolment: %d %s", rec.Code, rec.Body.String())
	}
	var out api.EnrollDeviceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID == "" || out.DeviceToken == "" || out.InstanceID != "net_TEST" {
		t.Fatal("missing enrolment identity or credential")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("one-time credential response is cacheable")
	}
	return out
}

func (f *signedIn) deviceRequest(method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDeviceEndpointsEnrolHeartbeatAndRevoke(t *testing.T) {
	// A real enrolment must grant only self access; revocation must deny every
	// device endpoint immediately. Neither a heartbeat nor config means VPN up.
	f := newSignedIn(t, nil)
	d := f.enrolDeviceToken(t)
	other := f.enrolDeviceToken(t)
	rec := f.deviceRequest(http.MethodGet, "/api/v1/devices/me", d.DeviceToken)
	var me api.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || rec.Code != http.StatusOK || me.ID != d.ID {
		t.Fatalf("self lookup failed: status %d, error %v", rec.Code, err)
	}
	if rec := f.deviceRequest(http.MethodPost, "/api/v1/devices/me/heartbeat", d.DeviceToken); rec.Code != http.StatusNoContent {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.deviceRequest(http.MethodGet, "/api/v1/devices/me", d.DeviceToken)
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.LastSeenAt == nil {
		t.Fatalf("heartbeat not visible: %v", err)
	}
	rec = f.deviceRequest(http.MethodGet, "/api/v1/network/config", d.DeviceToken)
	var cfg api.DeviceNetworkConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("network config: status %d, error %v", rec.Code, err)
	}
	if cfg.DeviceID != d.ID || cfg.InstanceID != d.InstanceID || len(cfg.Addresses) != 2 ||
		cfg.Addresses[0] != d.IPv4 || cfg.Addresses[1] != d.IPv6 || cfg.Peers == nil || len(cfg.Peers) != 0 ||
		cfg.DNS.Enabled || cfg.DNS.Servers == nil || len(cfg.DNS.Servers) != 0 {
		t.Fatalf("config misrepresents inventory: %+v", cfg)
	}
	for _, path := range []string{"/api/v1/devices/me", "/api/v1/network/config"} {
		rec := f.deviceRequest(http.MethodGet, path, d.DeviceToken)
		for _, secret := range []string{d.DeviceToken, auth.HashToken(d.DeviceToken), other.ID} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s disclosed credential or another device", path)
			}
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s is cacheable", path)
		}
	}
	for _, path := range []string{"/api/v1/devices", "/api/v1/devices/" + other.ID, "/api/v1/setup-keys"} {
		if rec := f.deviceRequest(http.MethodGet, path, d.DeviceToken); rec.Code != http.StatusUnauthorized {
			t.Errorf("device credential granted user access to %s: %d", path, rec.Code)
		}
	}
	if rec := f.send(t, http.MethodDelete, "/api/v1/devices/"+d.ID, "", f.cookies, f.csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("revocation: %d %s", rec.Code, rec.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/devices/me"},
		{http.MethodPost, "/api/v1/devices/me/heartbeat"},
		{http.MethodGet, "/api/v1/network/config"},
	} {
		if rec := f.deviceRequest(route.method, route.path, d.DeviceToken); rec.Code != http.StatusUnauthorized {
			t.Errorf("revoked device accessed %s: %d", route.path, rec.Code)
		}
	}
	if rec := f.deviceRequest(http.MethodGet, "/api/v1/devices/me", other.DeviceToken); rec.Code != http.StatusOK {
		t.Fatalf("revocation affected another device: %d", rec.Code)
	}
}

func TestDeviceAuthenticationRejectsBrowserAndAmbiguousCredentials(t *testing.T) {
	// Browser cookies, query parameters and other credential classes cannot be
	// substituted for explicit device authorization, including on POST.
	f := newSignedIn(t, nil)
	d := f.enrolDeviceToken(t)
	session := cookieNamed(f.cookies, auth.SessionCookieName).Value
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/devices/me"},
		{http.MethodPost, "/api/v1/devices/me/heartbeat"},
		{http.MethodGet, "/api/v1/network/config"},
	} {
		for _, headers := range [][]string{
			nil, {"Basic " + d.DeviceToken}, {"Bearer " + session}, {"Bearer invalid"},
			{"Bearer  " + d.DeviceToken}, {"Bearer " + d.DeviceToken, "Bearer " + d.DeviceToken},
		} {
			req := httptest.NewRequest(route.method, route.path+"?token="+d.DeviceToken, nil)
			for _, c := range f.cookies {
				req.AddCookie(c)
			}
			for _, header := range headers {
				req.Header.Add("Authorization", header)
			}
			rec := httptest.NewRecorder()
			f.srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
				t.Errorf("%s accepted a missing/ambiguous/wrong credential: %d", route.path, rec.Code)
			}
		}
	}
}

func TestDeviceHeartbeatRejectsPayloadWithoutChangingLastSeen(t *testing.T) {
	// Device-supplied state must not be mistaken for an observed connection
	// or a trusted timestamp; this endpoint deliberately accepts no payload.
	f := newSignedIn(t, nil)
	d := f.enrolDeviceToken(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/me/heartbeat",
		strings.NewReader(`{"connected":true}`))
	req.Header.Set("Authorization", "Bearer "+d.DeviceToken)
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("heartbeat accepted a payload: %d", rec.Code)
	}
	rec = f.deviceRequest(http.MethodGet, "/api/v1/devices/me", d.DeviceToken)
	var me api.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.LastSeenAt != nil {
		t.Fatalf("rejected heartbeat updated last_seen: %v", err)
	}
}
