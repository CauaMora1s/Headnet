// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

// Package shared holds small, dependency-free helpers that are used by more
// than one Headnet component (server, client daemon, CLI, relay).
//
// Anything placed here is part of the Apache-2.0 licensed interoperability
// surface, so keep it generic: no control-plane business logic belongs here.
package shared

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// idAlphabet is Crockford base32 without padding: it is case-insensitive,
// excludes the visually ambiguous letters I, L, O and U, and survives being
// copied out of a terminal or read aloud over a support call.
var idAlphabet = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// IDEntropyBytes is the number of random bytes behind every generated ID.
// 16 bytes (128 bits) matches UUIDv4 and makes collisions irrelevant in
// practice.
const IDEntropyBytes = 16

// NewID returns a prefixed, URL-safe, random identifier such as
// "dev_4KQZ9V3B2N7XR8T0YCFHJM". The prefix makes IDs self-describing in logs
// and audit records, which matters when an operator is reading an incident
// timeline months later.
//
// It panics only if the system CSPRNG fails, which Go treats as an
// unrecoverable condition; callers must not attempt to continue with a
// predictable identifier.
func NewID(prefix string) string {
	b := make([]byte, IDEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("shared: crypto/rand unavailable: %v", err))
	}
	encoded := idAlphabet.EncodeToString(b)
	if prefix == "" {
		return encoded
	}
	return prefix + "_" + encoded
}

// SplitID separates a prefixed identifier into its prefix and random part. It
// reports whether the value looked like a prefixed ID at all.
func SplitID(id string) (prefix, value string, ok bool) {
	prefix, value, ok = strings.Cut(id, "_")
	if !ok || prefix == "" || value == "" {
		return "", id, false
	}
	return prefix, value, true
}

// HasPrefix reports whether id carries the given type prefix. Handlers use it
// to reject an ID of the wrong kind before it ever reaches the database.
func HasPrefix(id, prefix string) bool {
	got, _, ok := SplitID(id)
	return ok && got == prefix
}
