// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package api

import "time"

// Device is a machine on the network, as the API represents it.
//
// There is deliberately no private key field of any kind — not the value, not
// a placeholder, not an omitempty string. A device generates its own key pair
// and sends only the public half, and a schema with somewhere to put the
// private one is a schema that will eventually carry it.
//
// This is the property the whole control-plane security argument rests on: a
// compromised server, database or backup yields nothing that can decrypt any
// traffic.
type Device struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	// PublicKey is the device's WireGuard public key, base64-encoded.
	PublicKey string `json:"public_key"`
	OS        string `json:"os,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	// IPv4 and IPv6 are the addresses allocated to this device. Both are
	// cleared when it is revoked, because the addresses go back to the pool.
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
	// EnrolledWith is the setup key this device was enrolled with, if any.
	EnrolledWith string     `json:"enrolled_with,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	// RevokedAt is set once the device has been removed from the network.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// RegisterDeviceRequest is the body of POST /api/v1/devices.
type RegisterDeviceRequest struct {
	Name string `json:"name"`
	// PublicKey must be a base64-encoded 32-byte WireGuard public key.
	PublicKey string `json:"public_key"`
	OS        string `json:"os,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
}

// EnrollDeviceRequest is the body of POST /api/v1/devices/enroll. The setup
// key is the credential, so this endpoint needs no session.
type EnrollDeviceRequest struct {
	SetupKey  string `json:"setup_key"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	OS        string `json:"os,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
}

// EnrollDeviceResponse preserves the device fields and adds a one-time bearer
// credential. Store it locally as a secret; subsequent reads never return it.
type EnrollDeviceResponse struct {
	Device
	DeviceToken string `json:"device_token"`
	// InstanceID lets the client pin the deployment identity at enrolment.
	InstanceID string `json:"instance_id"`
}

// DeviceNetworkConfig describes allocated inventory, not a functioning tunnel.
type DeviceNetworkConfig struct {
	InstanceID string          `json:"instance_id"`
	DeviceID   string          `json:"device_id"`
	Addresses  []string        `json:"addresses"`
	DNS        DeviceDNSConfig `json:"dns"`
	// Peers is always empty until Phase 3 implements peer distribution.
	Peers []NetworkPeer `json:"peers"`
}

// DeviceDNSConfig explicitly reports DNS as disabled until Phase 7.
type DeviceDNSConfig struct {
	Enabled bool     `json:"enabled"`
	Servers []string `json:"servers"`
}

// NetworkPeer reserves the public-only peer shape; no peers are distributed yet.
type NetworkPeer struct {
	PublicKey string   `json:"public_key"`
	Addresses []string `json:"addresses"`
}

// DeviceList is the body of GET /api/v1/devices.
type DeviceList struct {
	Devices []Device `json:"devices"`
}

// SetupKey is an enrolment credential, as the API represents it.
//
// The key itself is absent: it is returned exactly once, by
// CreateSetupKeyResponse, and only its hash is stored. DisplayHint carries
// enough to recognise a key in a list and far too little to use one.
type SetupKey struct {
	ID          string     `json:"id"`
	DisplayHint string     `json:"display_hint"`
	Description string     `json:"description,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// MaxUses of 0 means unlimited.
	MaxUses int `json:"max_uses"`
	Uses    int `json:"uses"`
	// RevokedAt is set once the key has been revoked.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// Usable reports whether the key would be accepted right now, so a client
	// does not have to re-derive expiry and use-count logic.
	Usable bool `json:"usable"`
}

// CreateSetupKeyRequest is the body of POST /api/v1/setup-keys.
type CreateSetupKeyRequest struct {
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// ExpiresInHours is how long the key stays valid. Omitted or zero means
	// the server default; a key that never expires requires never_expires.
	ExpiresInHours int `json:"expires_in_hours,omitempty"`
	// NeverExpires creates a key with no expiry. It exists so that choosing
	// one is a deliberate act rather than an omission.
	NeverExpires bool `json:"never_expires,omitempty"`
	// MaxUses caps redemptions. Zero means unlimited; one is single-use.
	MaxUses int `json:"max_uses,omitempty"`
}

// CreateSetupKeyResponse carries the newly created key.
type CreateSetupKeyResponse struct {
	Key SetupKey `json:"key"`
	// SetupKey is the secret, returned **exactly once**. It cannot be
	// recovered afterwards: the server stores only its hash. Show it to the
	// operator, then forget it.
	SetupKey string `json:"setup_key"`
}

// SetupKeyList is the body of GET /api/v1/setup-keys.
type SetupKeyList struct {
	Keys []SetupKey `json:"keys"`
}
