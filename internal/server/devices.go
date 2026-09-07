package server

import (
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/devices"
	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/internal/network"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// scopeFor builds the device scope for the signed-in caller: their own
// devices, or all of them for an administrator.
func scopeFor(user *auth.User) devices.Scope {
	return devices.ScopeFor(user.ID, user.IsAdmin())
}

// handleListDevices returns the caller's devices, or every device for an
// administrator.
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
		return
	}

	list, err := s.devices.List(r.Context(), scopeFor(user))
	if err != nil {
		s.logger.ErrorContext(r.Context(), "listing devices failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	out := make([]api.Device, 0, len(list))
	for i := range list {
		out = append(out, deviceResponse(&list[i]))
	}
	httpapi.WriteJSON(w, r, http.StatusOK, api.DeviceList{Devices: out})
}

// handleGetDevice returns one device.
func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
		return
	}

	device, err := s.devices.ByID(r.Context(), r.PathValue("id"), scopeFor(user))
	if err != nil {
		s.writeDeviceError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, r, http.StatusOK, deviceResponse(device))
}

// handleRegisterDevice adds a device owned by the signed-in user.
func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
		return
	}

	var req api.RegisterDeviceRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		return
	}

	device, err := s.devices.Register(r.Context(), user.ID, devices.NewDevice{
		Name:      req.Name,
		PublicKey: req.PublicKey,
		OS:        req.OS,
		Hostname:  req.Hostname,
	}, s.clock.Now())
	if err != nil {
		s.writeDeviceError(w, r, err)
		return
	}

	s.logger.InfoContext(r.Context(), "device registered",
		"device_id", device.ID, "user_id", user.ID,
		"ipv4", device.IPv4.String(), "remote_ip", httpapi.ClientIP(r))

	httpapi.WriteJSON(w, r, http.StatusCreated, deviceResponse(device))
}

// handleEnrollDevice redeems a setup key and registers the device it
// authorises.
//
// This endpoint is unauthenticated: the setup key *is* the credential, which
// is the whole point of a headless enrolment. It therefore sits behind the
// tighter authentication rate limit, alongside login.
func (s *Server) handleEnrollDevice(w http.ResponseWriter, r *http.Request) {
	var req api.EnrollDeviceRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		return
	}

	device, token, err := s.devices.Enroll(r.Context(), req.SetupKey, devices.NewDevice{
		Name:      req.Name,
		PublicKey: req.PublicKey,
		OS:        req.OS,
		Hostname:  req.Hostname,
	}, s.clock.Now())
	if err != nil {
		// Log precisely why; tell the enroller only that the key was rejected.
		// Distinguishing "revoked" from "expired" from "no such key" confirms
		// to whoever is holding it that it once existed.
		if isSetupKeyRejection(err) {
			s.logger.WarnContext(r.Context(), "setup key rejected",
				"reason", err.Error(), "remote_ip", httpapi.ClientIP(r))
			httpapi.WriteError(w, r, api.CodeUnauthorized, "that setup key is not valid")
			return
		}
		s.writeDeviceError(w, r, err)
		return
	}

	s.logger.InfoContext(r.Context(), "device enrolled with a setup key",
		"device_id", device.ID, "user_id", device.UserID,
		"setup_key_id", device.EnrolledWith, "remote_ip", httpapi.ClientIP(r))

	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, r, http.StatusCreated, api.EnrollDeviceResponse{
		Device: deviceResponse(device), DeviceToken: token, InstanceID: s.instanceID,
	})
}

// handleRevokeDevice removes a device from the network.
func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
		return
	}

	id := r.PathValue("id")
	if err := s.devices.Revoke(r.Context(), id, scopeFor(user), s.clock.Now()); err != nil {
		s.writeDeviceError(w, r, err)
		return
	}

	s.logger.InfoContext(r.Context(), "device revoked",
		"device_id", id, "revoked_by", user.ID, "remote_ip", httpapi.ClientIP(r))

	w.WriteHeader(http.StatusNoContent)
}

// isSetupKeyRejection reports whether the error is a key the enroller may not
// use, as opposed to a problem with the device they described.
func isSetupKeyRejection(err error) bool {
	return errors.Is(err, devices.ErrKeyNotFound) ||
		errors.Is(err, devices.ErrKeyRevoked) ||
		errors.Is(err, devices.ErrKeyExpired) ||
		errors.Is(err, devices.ErrKeyExhausted)
}

// writeDeviceError maps a store error onto the API error contract.
func (s *Server) writeDeviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, devices.ErrDeviceNotFound):
		// A device belonging to someone else is reported as missing rather
		// than forbidden: "you may not see this" confirms it exists.
		httpapi.WriteError(w, r, api.CodeNotFound, "no such device")

	case errors.Is(err, devices.ErrPublicKeyTaken):
		httpapi.WriteErrorDetail(w, r, api.CodeConflict,
			"that public key is already registered to a device", "field", "public_key")

	case errors.Is(err, devices.ErrInvalidPublicKey):
		httpapi.WriteErrorDetail(w, r, api.CodeBadRequest, err.Error(), "field", "public_key")

	case errors.Is(err, devices.ErrInvalidName):
		httpapi.WriteErrorDetail(w, r, api.CodeBadRequest, err.Error(), "field", "name")

	case errors.Is(err, devices.ErrDeviceRevoked):
		httpapi.WriteError(w, r, api.CodeConflict, "that device has already been revoked")

	case errors.Is(err, network.ErrPoolExhausted):
		// An operator problem, not a caller problem, and one that needs
		// saying clearly because the fix is a re-addressing exercise.
		s.logger.ErrorContext(r.Context(), "the address pool is exhausted", "error", err)
		httpapi.WriteError(w, r, api.CodeUnavailable,
			"there are no addresses left in this network's pool; an administrator must widen it")

	default:
		s.logger.ErrorContext(r.Context(), "a device operation failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
	}
}

// deviceResponse converts a device to its wire form.
//
// It exists so that adding a field to devices.Device cannot silently publish
// it — the same reason userResponse exists.
func deviceResponse(d *devices.Device) api.Device {
	return api.Device{
		ID:           d.ID,
		UserID:       d.UserID,
		Name:         d.Name,
		PublicKey:    d.PublicKey,
		OS:           d.OS,
		Hostname:     d.Hostname,
		IPv4:         addrString(d.IPv4),
		IPv6:         addrString(d.IPv6),
		EnrolledWith: d.EnrolledWith,
		CreatedAt:    d.CreatedAt,
		LastSeenAt:   d.LastSeenAt,
		RevokedAt:    d.RevokedAt,
	}
}

func addrString(addr netip.Addr) string {
	if !addr.IsValid() {
		return ""
	}
	return addr.String()
}

// --- setup keys -------------------------------------------------------------

// handleCreateSetupKey issues an enrolment credential.
func (s *Server) handleCreateSetupKey(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
		return
	}

	var req api.CreateSetupKeyRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		return
	}
	if req.ExpiresInHours < 0 {
		httpapi.WriteErrorDetail(w, r, api.CodeBadRequest,
			"expires_in_hours must not be negative", "field", "expires_in_hours")
		return
	}
	if req.MaxUses < 0 {
		httpapi.WriteErrorDetail(w, r, api.CodeBadRequest,
			"max_uses must not be negative", "field", "max_uses")
		return
	}

	issued, err := s.devices.CreateSetupKey(r.Context(), user.ID, devices.NewSetupKey{
		Description:  req.Description,
		Tags:         req.Tags,
		ExpiresIn:    time.Duration(req.ExpiresInHours) * time.Hour,
		NeverExpires: req.NeverExpires,
		MaxUses:      req.MaxUses,
	}, s.clock.Now())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "creating a setup key failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	// The key itself is never logged — only its identifier and hint. It is in
	// the response body once and nowhere else, ever.
	s.logger.InfoContext(r.Context(), "setup key created",
		"setup_key_id", issued.Key.ID, "created_by", user.ID,
		"max_uses", issued.Key.MaxUses, "never_expires", issued.Key.ExpiresAt == nil,
		"remote_ip", httpapi.ClientIP(r))

	httpapi.WriteJSON(w, r, http.StatusCreated, api.CreateSetupKeyResponse{
		Key:      setupKeyResponse(&issued.Key, s.clock.Now()),
		SetupKey: issued.Token,
	})
}

// handleListSetupKeys returns every setup key, without any of them being
// usable.
func (s *Server) handleListSetupKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.devices.ListSetupKeys(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "listing setup keys failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	now := s.clock.Now()
	out := make([]api.SetupKey, 0, len(keys))
	for i := range keys {
		out = append(out, setupKeyResponse(&keys[i], now))
	}
	httpapi.WriteJSON(w, r, http.StatusOK, api.SetupKeyList{Keys: out})
}

// handleRevokeSetupKey ends a setup key immediately.
func (s *Server) handleRevokeSetupKey(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	id := r.PathValue("id")

	if err := s.devices.RevokeSetupKey(r.Context(), id, s.clock.Now()); err != nil {
		if errors.Is(err, devices.ErrKeyNotFound) {
			httpapi.WriteError(w, r, api.CodeNotFound, "no such setup key")
			return
		}
		s.logger.ErrorContext(r.Context(), "revoking a setup key failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	revokedBy := ""
	if user != nil {
		revokedBy = user.ID
	}
	s.logger.InfoContext(r.Context(), "setup key revoked",
		"setup_key_id", id, "revoked_by", revokedBy, "remote_ip", httpapi.ClientIP(r))

	w.WriteHeader(http.StatusNoContent)
}

// setupKeyResponse converts a setup key to its wire form. The key material is
// structurally absent: there is no field for it here.
func setupKeyResponse(k *devices.SetupKey, now time.Time) api.SetupKey {
	return api.SetupKey{
		ID:          k.ID,
		DisplayHint: k.DisplayHint,
		Description: k.Description,
		Tags:        k.Tags,
		CreatedBy:   k.CreatedBy,
		CreatedAt:   k.CreatedAt,
		ExpiresAt:   k.ExpiresAt,
		MaxUses:     k.MaxUses,
		Uses:        k.Uses,
		RevokedAt:   k.RevokedAt,
		Usable:      k.Redeemable(now) == nil,
	}
}
