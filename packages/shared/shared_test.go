// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package shared_test

import (
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/packages/shared"
)

func TestNewIDIsPrefixedAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := shared.NewID("dev")
		if !strings.HasPrefix(id, "dev_") {
			t.Fatalf("NewID(%q) = %q, want a %q prefix", "dev", id, "dev_")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID produced a duplicate identifier %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewIDWithoutPrefix(t *testing.T) {
	id := shared.NewID("")
	if strings.Contains(id, "_") {
		t.Fatalf("NewID(\"\") = %q, want no separator", id)
	}
	if id == "" {
		t.Fatal("NewID(\"\") returned an empty identifier")
	}
}

func TestNewIDUsesUnambiguousAlphabet(t *testing.T) {
	// Crockford base32 deliberately omits I, L, O and U so that identifiers
	// survive being transcribed by a human.
	const forbidden = "ILOU"
	for range 200 {
		_, value, ok := shared.SplitID(shared.NewID("key"))
		if !ok {
			t.Fatal("SplitID failed on a freshly generated ID")
		}
		if i := strings.IndexAny(value, forbidden); i >= 0 {
			t.Fatalf("ID %q contains ambiguous character %q", value, value[i])
		}
	}
}

func TestSplitID(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantPrefix string
		wantValue  string
		wantOK     bool
	}{
		{"well formed", "dev_ABC123", "dev", "ABC123", true},
		{"no separator", "ABC123", "", "ABC123", false},
		{"empty prefix", "_ABC123", "", "_ABC123", false},
		{"empty value", "dev_", "", "dev_", false},
		{"empty string", "", "", "", false},
		{"only separator", "_", "", "_", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, value, ok := shared.SplitID(tt.in)
			if prefix != tt.wantPrefix || value != tt.wantValue || ok != tt.wantOK {
				t.Fatalf("SplitID(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.in, prefix, value, ok, tt.wantPrefix, tt.wantValue, tt.wantOK)
			}
		})
	}
}

func TestHasPrefix(t *testing.T) {
	id := shared.NewID("usr")
	if !shared.HasPrefix(id, "usr") {
		t.Fatalf("HasPrefix(%q, \"usr\") = false, want true", id)
	}
	if shared.HasPrefix(id, "dev") {
		t.Fatalf("HasPrefix(%q, \"dev\") = true, want false", id)
	}
	if shared.HasPrefix("bare", "dev") {
		t.Fatal("HasPrefix on an unprefixed ID = true, want false")
	}
}

func TestSystemClockIsUTC(t *testing.T) {
	now := shared.SystemClock.Now()
	if now.Location() != time.UTC {
		t.Fatalf("SystemClock.Now() location = %v, want UTC", now.Location())
	}
}

func TestFixedClock(t *testing.T) {
	want := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := shared.FixedClock(want)
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("FixedClock.Now() = %v, want %v", got, want)
	}
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("FixedClock.Now() is not stable across calls: %v", got)
	}
}
