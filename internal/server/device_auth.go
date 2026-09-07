package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CauaMora1s/Headnet/internal/devices"
	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// deviceHandler receives a database-authenticated device, never a user session.
type deviceHandler func(http.ResponseWriter, *http.Request, *devices.Device)

// bearerToken rejects ambiguous/multiple authorization fields. Tokens in
// cookies or query parameters are never considered for device authentication.
func bearerToken(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}

func (s *Server) requireDevice(next deviceHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		device, err := s.devices.Authenticate(r.Context(), bearerToken(r))
		if err != nil {
			s.writeDeviceAuthError(w, r, err)
			return
		}
		next(w, r, device)
	})
}

func (s *Server) writeDeviceAuthError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, devices.ErrDeviceTokenInvalid) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="headnet-device"`)
		httpapi.WriteError(w, r, api.CodeUnauthorized,
			"a valid device bearer token is required; enrol the device with a setup key")
		return
	}
	s.logger.ErrorContext(r.Context(), "device authentication or check-in failed", "error", err)
	httpapi.WriteError(w, r, api.CodeInternal, "internal error")
}

func (s *Server) handleDeviceMe(w http.ResponseWriter, r *http.Request, device *devices.Device) {
	httpapi.WriteJSON(w, r, http.StatusOK, deviceResponse(device))
}

func (s *Server) handleDeviceHeartbeat(w http.ResponseWriter, r *http.Request, device *devices.Device) {
	// No client timestamp or metadata is accepted: this records receipt only.
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		httpapi.WriteError(w, r, api.CodeBadRequest, "heartbeat requests must have an empty body")
		return
	}
	if err := s.devices.Heartbeat(r.Context(), bearerToken(r), s.clock.Now()); err != nil {
		s.writeDeviceAuthError(w, r, err)
		return
	}
	s.logger.DebugContext(r.Context(), "device checked in", "device_id", device.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeviceNetworkConfig(w http.ResponseWriter, r *http.Request, device *devices.Device) {
	addresses := make([]string, 0, 2)
	for _, addr := range device.Addrs() {
		addresses = append(addresses, addr.String())
	}
	httpapi.WriteJSON(w, r, http.StatusOK, api.DeviceNetworkConfig{
		InstanceID: s.instanceID, DeviceID: device.ID, Addresses: addresses,
		DNS:   api.DeviceDNSConfig{Enabled: false, Servers: []string{}},
		Peers: []api.NetworkPeer{},
	})
}
