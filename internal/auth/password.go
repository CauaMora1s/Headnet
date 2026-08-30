package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password length bounds.
//
// The minimum is a floor, not a policy: a passphrase is far stronger than a
// short string with a digit and a symbol bolted on, so Headnet asks for length
// rather than composition rules that mostly produce "Password1!".
//
// The maximum is a denial-of-service control. Argon2 cost is bounded by its
// parameters, but hashing an unbounded input still means reading and copying
// it, and an endpoint that accepts a megabyte password is an easy way to burn
// a server's memory bandwidth.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 1024
)

// Errors from hashing and verification.
var (
	// ErrPasswordTooShort and ErrPasswordTooLong are user-correctable. They
	// are sentinels so a handler can branch on them; the wrapped message
	// carries the actual limit, which is configurable.
	ErrPasswordTooShort = errors.New("the password is too short")
	ErrPasswordTooLong  = errors.New("the password is too long")

	// ErrInvalidHash means the stored hash could not be parsed. This is a
	// server-side data problem, not a wrong password, and must never be
	// reported to the user as a failed login — it needs an operator.
	ErrInvalidHash = errors.New("the stored password hash is malformed")

	// ErrIncompatibleVersion means the hash was produced by a newer Argon2
	// than this build understands.
	ErrIncompatibleVersion = errors.New("the stored password hash uses an unsupported Argon2 version")
)

// Params are the Argon2id cost parameters.
//
// They are stored alongside every hash rather than assumed, which is what
// allows them to be raised later without invalidating existing passwords: an
// old hash still verifies under its own parameters, and VerifyPassword reports
// that it should be upgraded.
type Params struct {
	// Memory is the memory cost in KiB. This is the parameter that actually
	// resists GPU and ASIC attack, so it is the one to raise first.
	Memory uint32
	// Iterations is the time cost.
	Iterations uint32
	// Parallelism is the number of lanes.
	Parallelism uint8
	// SaltLength is the salt size in bytes.
	SaltLength uint32
	// KeyLength is the derived key size in bytes.
	KeyLength uint32
}

// DefaultParams returns the current cost settings, following OWASP guidance
// for Argon2id: 19 MiB of memory, two iterations, one lane.
//
// Raising these is a one-line change; existing hashes keep working and are
// upgraded the next time their owner signs in.
func DefaultParams() Params {
	return Params{
		Memory:      19 * 1024,
		Iterations:  2,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// valid reports whether the parameters can produce a usable hash.
func (p Params) valid() bool {
	return p.Memory > 0 && p.Iterations > 0 && p.Parallelism > 0 &&
		p.SaltLength >= 8 && p.KeyLength >= 16
}

// weakerThan reports whether p is cheaper to attack than other. It drives the
// rehash-on-login upgrade, and deliberately ignores parameters that were
// *lowered* — an operator who reduced the cost on purpose, perhaps to run on a
// Raspberry Pi, should not have every login rehashing back upwards.
func (p Params) weakerThan(other Params) bool {
	return p.Memory < other.Memory ||
		p.Iterations < other.Iterations ||
		p.KeyLength < other.KeyLength
}

// HashPassword derives an Argon2id hash in PHC string format:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// The parameters and salt travel with the hash, so nothing outside the string
// needs to be remembered in order to verify it later.
func HashPassword(password string, p Params) (string, error) {
	if p == (Params{}) {
		p = DefaultParams()
	}
	if !p.valid() {
		return "", fmt.Errorf("auth: invalid Argon2id parameters %+v", p)
	}
	if err := CheckPasswordLength(password, MinPasswordLength); err != nil {
		return "", err
	}

	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating a salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// CheckPasswordLength validates a password against the configured minimum.
// A minimum below MinPasswordLength is raised to it: the floor is not
// negotiable by configuration.
func CheckPasswordLength(password string, minimum int) error {
	if minimum < MinPasswordLength {
		minimum = MinPasswordLength
	}
	// Counted in runes, so a passphrase in a non-Latin script is not penalised
	// for encoding to more bytes.
	length := len([]rune(password))
	switch {
	case length < minimum:
		return fmt.Errorf("%w: it must be at least %d characters", ErrPasswordTooShort, minimum)
	case length > MaxPasswordLength:
		return fmt.Errorf("%w: it must be at most %d characters", ErrPasswordTooLong, MaxPasswordLength)
	default:
		return nil
	}
}

// VerifyPassword checks a password against an encoded hash.
//
// It returns whether the password matched, and whether the hash should be
// recomputed because it was made with weaker parameters than are current. A
// caller that ignores needsRehash is still correct, just never upgrades.
//
// An error means the stored hash is unusable, which is an operator problem and
// is distinct from the password simply being wrong.
func VerifyPassword(password, encoded string) (ok bool, needsRehash bool, err error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}

	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	// Constant time: a byte-by-byte comparison would leak how much of the
	// hash matched, and with it a path to forging one.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	return true, p.weakerThan(DefaultParams()), nil
}

// decodeHash parses a PHC-format Argon2id string.
func decodeHash(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" {
		return p, nil, nil, ErrInvalidHash
	}
	if parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("%w: algorithm is %q, want argon2id", ErrInvalidHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: found version %d, this build understands %d",
			ErrIncompatibleVersion, version, argon2.Version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, ErrInvalidHash
	}

	if salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil {
		return p, nil, nil, ErrInvalidHash
	}

	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	if !p.valid() {
		return p, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}

// DummyHash returns a hash of a random password.
//
// It exists so that a login attempt for an account that does not exist can do
// the same Argon2 work as one for an account that does. Without it, the
// difference between "no such user" and "wrong password" is measurable in the
// response time, and the login endpoint becomes an oracle for which email
// addresses are registered.
func DummyHash() (string, error) {
	filler := make([]byte, 32)
	if _, err := rand.Read(filler); err != nil {
		return "", fmt.Errorf("auth: generating a dummy hash: %w", err)
	}
	return HashPassword(base64.RawStdEncoding.EncodeToString(filler), DefaultParams())
}
