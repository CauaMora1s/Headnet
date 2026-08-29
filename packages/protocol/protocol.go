// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

// Package protocol defines the versioning and capability negotiation contract
// spoken between a Headnet control-plane server and the clients that connect
// to it.
//
// Servers and clients are upgraded independently: a fleet of laptops will not
// restart the moment an administrator upgrades the control plane, and a
// long-lived subnet router may lag by months. The rules in this package exist
// so that a version mismatch produces a clear, actionable error instead of a
// mysterious runtime failure.
//
// The contract is:
//
//   - Version is a single monotonically increasing integer. There are no minor
//     or patch protocol versions; additive changes are expressed as
//     capabilities instead.
//   - A peer advertises the newest version it speaks (Version) and the oldest
//     it still accepts (MinVersion). Two peers are compatible when their
//     supported ranges overlap.
//   - Optional behaviour is gated on named capabilities, never on a version
//     comparison. Adding a capability is always backwards compatible.
//   - Unknown capabilities are ignored, so an old peer talking to a new one
//     degrades gracefully rather than failing.
package protocol

import (
	"fmt"
	"slices"
	"strings"
)

// Version is the newest protocol version this build speaks.
//
// Increment it only for a breaking change to the wire contract that cannot be
// expressed as a capability. Every increment requires a migration note in
// docs/upgrade.md.
const Version = 1

// MinVersion is the oldest protocol version this build still accepts from a
// peer. Raising it drops support for older clients and is a breaking change
// for operators, so it may only happen in a major release.
const MinVersion = 1

// Capability names an optional, independently negotiable behaviour.
//
// Capabilities are lower-case, dot-separated and namespaced by subsystem so
// that they read sensibly in logs: "relay.fallback", "dns.magic".
type Capability string

// Capabilities implemented by this build.
//
// This list grows as roadmap phases land. A capability is only added here once
// the behaviour behind it actually works end to end; advertising a capability
// the peer cannot use is a protocol bug, not a placeholder.
const (
	// CapabilityProtocolNegotiation is the ability to perform an explicit
	// version and capability handshake before any other request. Every build
	// that speaks version 1 supports it.
	CapabilityProtocolNegotiation Capability = "protocol.negotiation"
)

// Supported returns the capabilities this build implements. The result is a
// fresh slice, so callers may sort or filter it without corrupting the
// package-level set.
func Supported() []Capability {
	return []Capability{
		CapabilityProtocolNegotiation,
	}
}

// Set is an unordered collection of capabilities with set semantics.
type Set map[Capability]struct{}

// NewSet builds a Set from a list, discarding duplicates and empty names.
func NewSet(caps ...Capability) Set {
	s := make(Set, len(caps))
	for _, c := range caps {
		if c == "" {
			continue
		}
		s[c] = struct{}{}
	}
	return s
}

// Has reports whether the capability is present. The zero Set reports false
// for everything, which is the safe default: absent means "do not use".
func (s Set) Has(c Capability) bool {
	_, ok := s[c]
	return ok
}

// Sorted returns the capabilities in lexical order. Deterministic ordering
// keeps API responses, logs and test fixtures stable.
func (s Set) Sorted() []Capability {
	out := make([]Capability, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

// Intersect returns the capabilities present in both sets. This is the whole
// of capability negotiation: a feature may only be used when both ends
// independently claim to implement it.
func (s Set) Intersect(other Set) Set {
	out := make(Set)
	// Iterate the smaller set so the cost is bounded by the shorter list.
	small, large := s, other
	if len(large) < len(small) {
		small, large = large, small
	}
	for c := range small {
		if large.Has(c) {
			out[c] = struct{}{}
		}
	}
	return out
}

// Range describes the span of protocol versions a peer accepts.
type Range struct {
	// Version is the newest version the peer speaks.
	Version int `json:"version"`
	// Min is the oldest version the peer still accepts.
	Min int `json:"min_version"`
}

// Local returns the version range of this build.
func Local() Range {
	return Range{Version: Version, Min: MinVersion}
}

// Valid reports whether the range is internally coherent.
func (r Range) Valid() bool {
	return r.Min >= 1 && r.Version >= r.Min
}

// IncompatibleError explains why two peers cannot talk to each other. It is
// deliberately verbose: this error is shown directly to an operator who has to
// decide which side to upgrade.
type IncompatibleError struct {
	// Local is this build's supported range.
	Local Range
	// Remote is the peer's advertised range.
	Remote Range
	// RemoteTooOld is true when the peer must be upgraded, false when this
	// side is the one lagging behind.
	RemoteTooOld bool
}

// Error implements error.
func (e *IncompatibleError) Error() string {
	side := "this build is too old"
	action := "upgrade this component"
	if e.RemoteTooOld {
		side = "the peer is too old"
		action = "upgrade the peer"
	}
	return fmt.Sprintf(
		"incompatible protocol version: %s (local supports %d..%d, peer supports %d..%d); %s",
		side, e.Local.Min, e.Local.Version, e.Remote.Min, e.Remote.Version, action,
	)
}

// Negotiate selects the protocol version and capability set two peers will
// use.
//
// The chosen version is the highest both sides accept, which lets a new client
// use new behaviour against a new server while still falling back cleanly
// against an old one. The chosen capabilities are the intersection of what the
// two sides advertise.
func Negotiate(local, remote Range, localCaps, remoteCaps Set) (version int, caps Set, err error) {
	if !local.Valid() {
		return 0, nil, fmt.Errorf("invalid local protocol range %d..%d", local.Min, local.Version)
	}
	if !remote.Valid() {
		return 0, nil, fmt.Errorf("invalid peer protocol range %d..%d", remote.Min, remote.Version)
	}

	// The usable window is the overlap of the two ranges.
	high := min(local.Version, remote.Version)
	low := max(local.Min, remote.Min)
	if high < low {
		return 0, nil, &IncompatibleError{
			Local:  local,
			Remote: remote,
			// If the peer's newest version is below our oldest accepted
			// version, the peer is the stale one.
			RemoteTooOld: remote.Version < local.Min,
		}
	}

	return high, localCaps.Intersect(remoteCaps), nil
}

// ParseCapabilities converts wire-format strings into a Set, normalising case
// and whitespace. Unknown names are preserved rather than rejected: a newer
// peer may advertise capabilities this build has never heard of, and the
// intersection in Negotiate will discard them harmlessly.
func ParseCapabilities(values []string) Set {
	s := make(Set, len(values))
	for _, v := range values {
		name := Capability(strings.ToLower(strings.TrimSpace(v)))
		if name == "" {
			continue
		}
		s[name] = struct{}{}
	}
	return s
}

// Strings renders a set as sorted wire-format strings.
func (s Set) Strings() []string {
	sorted := s.Sorted()
	out := make([]string, len(sorted))
	for i, c := range sorted {
		out[i] = string(c)
	}
	return out
}
