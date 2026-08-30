// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package protocol_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/CauaMora1s/Headnet/packages/protocol"
)

func TestLocalRangeIsValid(t *testing.T) {
	if got := protocol.Local(); !got.Valid() {
		t.Fatalf("Local() = %+v, want a valid range", got)
	}
	if protocol.MinVersion > protocol.Version {
		t.Fatalf("MinVersion (%d) must not exceed Version (%d)", protocol.MinVersion, protocol.Version)
	}
}

func TestSupportedIsNotAliased(t *testing.T) {
	a := protocol.Supported()
	if len(a) == 0 {
		t.Fatal("Supported() returned no capabilities")
	}
	a[0] = "mutated"
	if b := protocol.Supported(); b[0] == "mutated" {
		t.Fatal("Supported() returns a shared backing array; callers can corrupt it")
	}
}

func TestSetHasAndSorted(t *testing.T) {
	s := protocol.NewSet("b.two", "a.one", "b.two", "")
	if len(s) != 2 {
		t.Fatalf("NewSet deduplication failed: %v", s.Sorted())
	}
	if !s.Has("a.one") || !s.Has("b.two") {
		t.Fatalf("Has() missed a member: %v", s.Sorted())
	}
	if s.Has("absent") {
		t.Fatal("Has() reported a capability that was never added")
	}
	want := []protocol.Capability{"a.one", "b.two"}
	if got := s.Sorted(); !slices.Equal(got, want) {
		t.Fatalf("Sorted() = %v, want %v", got, want)
	}
}

func TestZeroSetHasNothing(t *testing.T) {
	var s protocol.Set
	if s.Has(protocol.CapabilityProtocolNegotiation) {
		t.Fatal("the zero Set must report false for every capability")
	}
	if got := s.Sorted(); len(got) != 0 {
		t.Fatalf("zero Set Sorted() = %v, want empty", got)
	}
}

func TestIntersect(t *testing.T) {
	tests := []struct {
		name string
		a, b protocol.Set
		want []protocol.Capability
	}{
		{
			name: "overlap",
			a:    protocol.NewSet("one", "two", "three"),
			b:    protocol.NewSet("two", "three", "four"),
			want: []protocol.Capability{"three", "two"},
		},
		{
			name: "disjoint",
			a:    protocol.NewSet("one"),
			b:    protocol.NewSet("two"),
			want: nil,
		},
		{
			name: "empty operand",
			a:    protocol.NewSet(),
			b:    protocol.NewSet("one"),
			want: nil,
		},
		{
			name: "nil operand",
			a:    nil,
			b:    protocol.NewSet("one"),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Intersect(tt.b).Sorted()
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Intersect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIntersectIsCommutative(t *testing.T) {
	a := protocol.NewSet("one", "two", "three", "four")
	b := protocol.NewSet("two", "four")
	if !slices.Equal(a.Intersect(b).Sorted(), b.Intersect(a).Sorted()) {
		t.Fatal("Intersect is not commutative")
	}
}

func TestRangeValid(t *testing.T) {
	tests := []struct {
		name string
		r    protocol.Range
		want bool
	}{
		{"normal", protocol.Range{Version: 3, Min: 1}, true},
		{"single version", protocol.Range{Version: 1, Min: 1}, true},
		{"inverted", protocol.Range{Version: 1, Min: 2}, false},
		{"zero", protocol.Range{}, false},
		{"negative min", protocol.Range{Version: 2, Min: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.Valid(); got != tt.want {
				t.Fatalf("Range%+v.Valid() = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

func TestNegotiatePicksHighestCommonVersion(t *testing.T) {
	local := protocol.Range{Version: 3, Min: 1}
	remote := protocol.Range{Version: 2, Min: 1}

	version, caps, err := protocol.Negotiate(local, remote,
		protocol.NewSet("a", "b"), protocol.NewSet("b", "c"))
	if err != nil {
		t.Fatalf("Negotiate returned an unexpected error: %v", err)
	}
	if version != 2 {
		t.Fatalf("negotiated version = %d, want 2 (the highest both sides accept)", version)
	}
	if want := []protocol.Capability{"b"}; !slices.Equal(caps.Sorted(), want) {
		t.Fatalf("negotiated capabilities = %v, want %v", caps.Sorted(), want)
	}
}

func TestNegotiateRejectsStalePeer(t *testing.T) {
	local := protocol.Range{Version: 5, Min: 4}
	remote := protocol.Range{Version: 2, Min: 1}

	_, _, err := protocol.Negotiate(local, remote, nil, nil)
	var incompat *protocol.IncompatibleError
	if !errors.As(err, &incompat) {
		t.Fatalf("Negotiate error = %v, want *IncompatibleError", err)
	}
	if !incompat.RemoteTooOld {
		t.Fatal("RemoteTooOld = false, want true when the peer's newest version is below our minimum")
	}
	if incompat.Error() == "" {
		t.Fatal("IncompatibleError produced an empty message")
	}
}

func TestNegotiateRejectsStaleLocal(t *testing.T) {
	local := protocol.Range{Version: 2, Min: 1}
	remote := protocol.Range{Version: 5, Min: 4}

	_, _, err := protocol.Negotiate(local, remote, nil, nil)
	var incompat *protocol.IncompatibleError
	if !errors.As(err, &incompat) {
		t.Fatalf("Negotiate error = %v, want *IncompatibleError", err)
	}
	if incompat.RemoteTooOld {
		t.Fatal("RemoteTooOld = true, want false when this build is the stale side")
	}
}

func TestNegotiateRejectsInvalidRanges(t *testing.T) {
	valid := protocol.Range{Version: 1, Min: 1}
	if _, _, err := protocol.Negotiate(protocol.Range{}, valid, nil, nil); err == nil {
		t.Fatal("Negotiate accepted an invalid local range")
	}
	if _, _, err := protocol.Negotiate(valid, protocol.Range{}, nil, nil); err == nil {
		t.Fatal("Negotiate accepted an invalid peer range")
	}
}

func TestNegotiateWithIdenticalBuilds(t *testing.T) {
	local := protocol.Local()
	caps := protocol.NewSet(protocol.Supported()...)

	version, negotiated, err := protocol.Negotiate(local, local, caps, caps)
	if err != nil {
		t.Fatalf("two identical builds failed to negotiate: %v", err)
	}
	if version != protocol.Version {
		t.Fatalf("negotiated version = %d, want %d", version, protocol.Version)
	}
	if !negotiated.Has(protocol.CapabilityProtocolNegotiation) {
		t.Fatal("identical builds failed to agree on protocol.negotiation")
	}
}

func TestParseCapabilitiesNormalises(t *testing.T) {
	got := protocol.ParseCapabilities([]string{"  Relay.Fallback ", "DNS.MAGIC", "", "   "})
	want := []protocol.Capability{"dns.magic", "relay.fallback"}
	if !slices.Equal(got.Sorted(), want) {
		t.Fatalf("ParseCapabilities() = %v, want %v", got.Sorted(), want)
	}
}

func TestUnknownCapabilitiesAreIgnoredNotRejected(t *testing.T) {
	// A newer peer may advertise capabilities this build has never heard of.
	// They must survive parsing and then be dropped by the intersection.
	remote := protocol.ParseCapabilities([]string{"protocol.negotiation", "future.feature"})
	if !remote.Has("future.feature") {
		t.Fatal("ParseCapabilities dropped an unknown capability; it should be preserved")
	}
	local := protocol.NewSet(protocol.Supported()...)
	negotiated := local.Intersect(remote)
	if negotiated.Has("future.feature") {
		t.Fatal("negotiation kept a capability this build does not implement")
	}
	if !negotiated.Has(protocol.CapabilityProtocolNegotiation) {
		t.Fatal("negotiation dropped a capability both sides implement")
	}
}

func TestStrings(t *testing.T) {
	got := protocol.NewSet("b", "a").Strings()
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Strings() = %v, want [a b]", got)
	}
}
