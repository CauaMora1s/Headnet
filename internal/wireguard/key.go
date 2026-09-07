package wireguard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// KeyLength is the size of a WireGuard key in bytes. Curve25519 keys are
// always 32 bytes; this is not configurable and never will be.
const KeyLength = 32

// redactedPrivateKey is what a private key renders as anywhere it is rendered
// at all. It names the type so that a log line containing it reads as a bug
// that was caught rather than as a mysterious blank.
const redactedPrivateKey = "wireguard.PrivateKey(REDACTED)"

// Key errors.
var (
	// ErrInvalidKey means the input was not a well-formed WireGuard key.
	ErrInvalidKey = errors.New("invalid WireGuard key")

	// ErrKeyNotSerialisable is returned by every marshalling method on
	// PrivateKey. It is an error rather than a redacted placeholder on
	// purpose: a placeholder would let a struct containing a private key
	// encode successfully, and nobody would notice the field that silently
	// did nothing. An error fails the encode and names the type.
	ErrKeyNotSerialisable = errors.New("a WireGuard private key cannot be serialised")
)

// PublicKey is a device's WireGuard public key. It is not secret: the control
// plane stores it, the API returns it, and every peer authorised to reach the
// device receives it.
type PublicKey [KeyLength]byte

// PrivateKey is a device's WireGuard private key.
//
// It never leaves the device that generated it. There is no API field, no
// configuration key and no log call anywhere in Headnet that can carry one,
// and the methods below keep that true by construction rather than by
// discipline: String, GoString, MarshalText, MarshalJSON and MarshalBinary
// all refuse.
//
// This matters more than it looks. fmt reaches unexported fields quite
// happily, so a single logger.Info("...", "device", d) on a struct holding a
// private key would otherwise write the key to disk in production. Exactly
// that bug was found in auth.User by a test, which is why this type has the
// treatment from its first line.
type PrivateKey [KeyLength]byte

// GenerateKey returns a new WireGuard private key from the platform CSPRNG.
//
// Headnet invents no cryptography here: this is WireGuard's own key
// generation — 32 random bytes, clamped for X25519. See
// docs/architecture/decisions/ADR-0002-use-wireguard.md.
func GenerateKey() (PrivateKey, error) {
	var key PrivateKey
	if _, err := rand.Read(key[:]); err != nil {
		// crypto/rand failing is not a recoverable condition, and returning a
		// partially filled buffer would be catastrophic. Return a zero key
		// with the error so a caller that ignores the error still gets
		// something IsZero rejects rather than something weak it accepts.
		return PrivateKey{}, fmt.Errorf("generate WireGuard key: %w", err)
	}
	key.clamp()
	return key, nil
}

// clamp applies the X25519 scalar clamping WireGuard uses: clear the three low
// bits, clear the top bit, set the second-highest. That forces the scalar into
// the correct subgroup and to a fixed bit length, which is what makes the
// scalar multiplication constant-time and free of small-subgroup leakage.
func (k *PrivateKey) clamp() {
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
}

// clamped reports whether a scalar already satisfies the clamping rules.
func clamped(k PrivateKey) bool {
	return k[0]&7 == 0 && k[31]&128 == 0 && k[31]&64 == 64
}

// Public derives the public key. The derivation is one-way: holding the result
// tells an attacker nothing about the private key.
func (k PrivateKey) Public() PublicKey {
	var pub PublicKey
	// ScalarBaseMult is deprecated in favour of X25519 for Diffie-Hellman, but
	// it is the right call for deriving a public key from a scalar and it
	// cannot fail, whereas X25519 returns an error this call could never
	// produce and which a caller would then have to invent handling for.
	curve25519.ScalarBaseMult((*[KeyLength]byte)(&pub), (*[KeyLength]byte)(&k))
	return pub
}

// IsZero reports whether the key is entirely zero bytes.
//
// A zero key is what an uninitialised buffer looks like, and it is not a
// usable Curve25519 scalar. Treating it as "no key" rather than as a valid one
// is what stops a failed generation being enrolled as though it had worked.
func (k PrivateKey) IsZero() bool {
	var zero PrivateKey
	return subtle.ConstantTimeCompare(k[:], zero[:]) == 1
}

// IsZero reports whether the public key is entirely zero bytes.
func (k PublicKey) IsZero() bool {
	var zero PublicKey
	return subtle.ConstantTimeCompare(k[:], zero[:]) == 1
}

// Equal compares two public keys in constant time.
//
// Public keys are not secret, so this is not strictly required. But a peer
// lookup written with == is one refactor away from being pointed at something
// that is secret, and the cost of doing it properly here is nothing.
func (k PublicKey) Equal(other PublicKey) bool {
	return subtle.ConstantTimeCompare(k[:], other[:]) == 1
}

// String returns the standard base64 encoding, which is how WireGuard itself
// renders keys and how the Headnet API carries them.
func (k PublicKey) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// MarshalText makes PublicKey encode as base64 in JSON, YAML and anywhere else
// honouring encoding.TextMarshaler.
func (k PublicKey) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// UnmarshalText parses the base64 form.
func (k *PublicKey) UnmarshalText(text []byte) error {
	parsed, err := ParsePublicKey(string(text))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// ParsePublicKey decodes the base64 form of a public key.
func ParsePublicKey(s string) (PublicKey, error) {
	raw, err := parseKeyBytes(s)
	if err != nil {
		return PublicKey{}, err
	}
	return PublicKey(raw), nil
}

// ParsePrivateKey decodes the base64 form of a private key.
//
// It exists to read a key back off disk, and to accept one generated by
// `wg genkey`. It deliberately does not clamp: a key that arrives unclamped
// did not come from a correct implementation, and silently fixing it would
// hide that while changing which public key the device presents — so the
// device would enrol under one key and hold another.
func ParsePrivateKey(s string) (PrivateKey, error) {
	raw, err := parseKeyBytes(s)
	if err != nil {
		return PrivateKey{}, err
	}
	key := PrivateKey(raw)
	if key.IsZero() {
		return PrivateKey{}, fmt.Errorf(
			"%w: it is all zeroes, which means key generation failed", ErrInvalidKey)
	}
	if !clamped(key) {
		return PrivateKey{}, fmt.Errorf(
			"%w: it is not clamped for X25519, so it was not produced by a correct implementation",
			ErrInvalidKey)
	}
	return key, nil
}

// parseKeyBytes is the shared decoding path. Its error messages describe the
// shape of the input and never echo it, because for a private key the input is
// the secret.
func parseKeyBytes(s string) ([KeyLength]byte, error) {
	var out [KeyLength]byte

	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return out, fmt.Errorf("%w: it is empty", ErrInvalidKey)
	}

	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return out, fmt.Errorf("%w: it is not valid standard base64", ErrInvalidKey)
	}
	if len(raw) != KeyLength {
		return out, fmt.Errorf("%w: it decodes to %d bytes, want %d",
			ErrInvalidKey, len(raw), KeyLength)
	}

	copy(out[:], raw)
	return out, nil
}

// privateKeyBase64 renders the key in WireGuard's own base64 form.
//
// It is unexported, and there is exactly one caller: writing the key to the
// device's own key file. Exporting it would create the thing this package is
// built to prevent — a supported way to turn a private key into a string that
// can then be logged, sent or embedded in a struct.
func privateKeyBase64(k PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// String refuses to render the key.
func (k PrivateKey) String() string {
	return redactedPrivateKey
}

// GoString covers %#v, which does not go through String.
func (k PrivateKey) GoString() string {
	return redactedPrivateKey
}

// Format renders the key as its redacted form for every verb.
//
// String and GoString are not enough on their own, and a test proved it: fmt
// consults Stringer only for the string-shaped verbs, so `%d` on a PrivateKey
// printed the raw scalar as a list of 32 decimal numbers — the key in another
// costume. fmt.Formatter takes precedence over every other interface and over
// the default array rendering, so implementing it is the only way to cover
// verbs nobody thought to test.
//
// Numeric verbs are the plausible accident here: a struct printed with %d, or
// a %v that a later edit turned into something else.
func (k PrivateKey) Format(f fmt.State, verb rune) {
	// %T must still name the type. It is not derived from the value and
	// suppressing it would make a redacted log line harder to trace back.
	if verb == 'T' {
		_, _ = io.WriteString(f, "wireguard.PrivateKey")
		return
	}
	_, _ = io.WriteString(f, redactedPrivateKey)
}

// LogValue makes log/slog render the key as its type name. slog consults this
// interface before falling back to fmt, so it covers the case this type exists
// to prevent: a key passed as a structured log attribute.
func (k PrivateKey) LogValue() slog.Value {
	return slog.StringValue(redactedPrivateKey)
}

// MarshalText refuses. It covers JSON, YAML and anything else honouring
// encoding.TextMarshaler, including struct fields whose author forgot what
// they were holding.
func (k PrivateKey) MarshalText() ([]byte, error) {
	return nil, ErrKeyNotSerialisable
}

// MarshalJSON refuses.
//
// MarshalText alone would already cover encoding/json. Having both means a
// later change that drops MarshalText cannot silently make private keys
// JSON-encodable, which is the kind of regression nobody reviews for.
func (k PrivateKey) MarshalJSON() ([]byte, error) {
	return nil, ErrKeyNotSerialisable
}

// MarshalBinary refuses, so gob and anything built on it cannot carry the key
// off the machine either.
func (k PrivateKey) MarshalBinary() ([]byte, error) {
	return nil, ErrKeyNotSerialisable
}
