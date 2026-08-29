// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package api

import "time"

// BasePath is the versioned prefix under which every API endpoint lives.
//
// The version in the URL changes only for a breaking API change. Additive
// changes — new fields, new endpoints — happen in place, and clients are
// required to ignore fields they do not recognise.
const BasePath = "/api/v1"

// HealthStatus summarises whether a component is usable.
type HealthStatus string

// The health states, ordered from best to worst.
const (
	// StatusOK means the component is fully functional.
	StatusOK HealthStatus = "ok"
	// StatusDegraded means the component works but something is wrong that an
	// operator should look at.
	StatusDegraded HealthStatus = "degraded"
	// StatusDown means the component is not usable.
	StatusDown HealthStatus = "down"
)

// Worse returns whichever of the two statuses is more severe. Aggregating a
// health response is a fold of this function over the individual checks.
func (s HealthStatus) Worse(other HealthStatus) HealthStatus {
	rank := func(v HealthStatus) int {
		switch v {
		case StatusOK:
			return 0
		case StatusDegraded:
			return 1
		case StatusDown:
			return 2
		default:
			// An unrecognised status is treated as the worst case rather than
			// being silently ignored.
			return 3
		}
	}
	if rank(other) > rank(s) {
		return other
	}
	return s
}

// Check is the result of probing one dependency.
type Check struct {
	// Name identifies the dependency, e.g. "database".
	Name string `json:"name"`
	// Status is the outcome of the probe.
	Status HealthStatus `json:"status"`
	// Detail explains a non-OK status in operator-facing terms. It must never
	// leak a connection string, credential or internal path.
	Detail string `json:"detail,omitempty"`
	// DurationMS is how long the probe took, which is often the first hint
	// that a dependency is about to fail outright.
	DurationMS int64 `json:"duration_ms"`
}

// HealthResponse is returned by GET /health.
//
// It is intentionally unauthenticated so that load balancers and container
// orchestrators can consume it, and therefore contains no configuration
// values, no hostnames and no version detail beyond the release string.
type HealthResponse struct {
	// Status is the aggregate of every check.
	Status HealthStatus `json:"status"`
	// Version is the server release, e.g. "0.1.0-dev".
	Version string `json:"version"`
	// UptimeSeconds is how long this process has been serving.
	UptimeSeconds int64 `json:"uptime_seconds"`
	// Checks lists the individual dependency probes.
	Checks []Check `json:"checks"`
}

// VersionResponse is returned by GET /api/v1/version. It tells a client
// whether it can talk to this server at all, and is the first call the daemon
// and the CLI make.
type VersionResponse struct {
	// Version is the human-readable server release.
	Version string `json:"version"`
	// Commit is the git revision the binary was built from.
	Commit string `json:"commit"`
	// BuildDate is the RFC 3339 build timestamp.
	BuildDate string `json:"build_date"`
	// ProtocolVersion is the newest protocol version the server speaks.
	ProtocolVersion int `json:"protocol_version"`
	// MinProtocolVersion is the oldest protocol version the server accepts.
	MinProtocolVersion int `json:"min_protocol_version"`
	// Capabilities lists the optional behaviours this server implements.
	Capabilities []string `json:"capabilities"`
}

// NegotiateRequest is the body of POST /api/v1/protocol/negotiate. A client
// sends the range and capabilities it supports before doing anything else.
type NegotiateRequest struct {
	// ProtocolVersion is the newest version the client speaks.
	ProtocolVersion int `json:"protocol_version"`
	// MinProtocolVersion is the oldest version the client accepts. Zero is
	// treated as "same as ProtocolVersion".
	MinProtocolVersion int `json:"min_protocol_version,omitempty"`
	// Capabilities lists the optional behaviours the client implements.
	// Unknown names are ignored rather than rejected.
	Capabilities []string `json:"capabilities,omitempty"`
	// ClientVersion is the client's release string. It is recorded for
	// diagnostics and fleet-visibility only; it never affects the outcome.
	ClientVersion string `json:"client_version,omitempty"`
}

// NegotiateResponse reports the agreed protocol version and capability set.
type NegotiateResponse struct {
	// ProtocolVersion is the version both sides will use.
	ProtocolVersion int `json:"protocol_version"`
	// Capabilities are the behaviours both sides implement. A client must not
	// use anything absent from this list.
	Capabilities []string `json:"capabilities"`
	// ServerVersion is the server release string.
	ServerVersion string `json:"server_version"`
}

// InstanceID is the stable identity of one control-plane deployment. It is
// generated on first start and persisted, so it survives restarts but changes
// if the database is recreated — which is exactly the signal a client needs to
// notice that it is talking to a different network than it enrolled into.
type InstanceID string

// Timestamp is the wire representation of a point in time: RFC 3339 in UTC.
// Every timestamp the API emits uses this format.
type Timestamp = time.Time
