package wireguard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// KeyFileName is the file a device's private key lives in.
const KeyFileName = "device.key"

// maxKeyFileBytes caps how much of the key file is read.
//
// A key file is 45 bytes: 44 of base64 and a newline. The cap is generous
// enough to tolerate a stray carriage return or trailing whitespace, and small
// enough that a device node or an enormous file substituted for the key is
// rejected by shape rather than consumed.
const maxKeyFileBytes = 128

// Key store errors.
var (
	// ErrNoKey means this machine has no device key yet. It is an expected
	// condition on a machine that has never enrolled, and callers should
	// treat it as "generate one", not as a failure.
	ErrNoKey = errors.New("no device key on this machine")

	// ErrKeyExists means a key is already present. Overwriting it would take
	// the device off the network with no way back except re-enrolment, so it
	// requires an explicit act rather than happening as a side effect.
	ErrKeyExists = errors.New("a device key already exists")

	// ErrInsecureKeyFile means the key file can be read by someone other than
	// the account running the daemon. The key must be treated as compromised.
	ErrInsecureKeyFile = errors.New("device key file is not private")
)

// Store persists a device's WireGuard private key on the machine that
// generated it.
//
// The key is written once, at enrolment, and read at every start. It is the
// device's identity on the network: lose it and the device must re-enrol;
// leak it and an attacker can impersonate the device until it is revoked.
//
// Everything this type does is in service of two rules from
// docs/security/key-management.md:
//
//   - Permissions are verified on read, not merely set on write. A key file
//     that has become readable by others is a compromised key, and the daemon
//     refuses to use it rather than continuing quietly.
//   - The key never enters a log, a diagnostic bundle or an error message.
//     None of the errors below carry key material, including the ones that
//     describe a malformed file.
type Store struct {
	path string
}

// NewStore returns a store for the key file at path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path is where the key lives. Safe to log: it is a path, not a secret.
func (s *Store) Path() string {
	return s.path
}

// DefaultKeyPath is where the daemon keeps the device key on this platform.
//
// These are system-wide, privileged locations, because the daemon is a system
// service and the key must not be readable by the desktop user's browser, or
// by any other account on a shared machine.
func DefaultKeyPath() string {
	return filepath.Join(DefaultStateDir(), KeyFileName)
}

// DefaultStateDir is the directory the daemon keeps its state in.
func DefaultStateDir() string {
	switch runtime.GOOS {
	case "windows":
		// ProgramData is the conventional location for machine-wide service
		// state. The fallback is only reached if the variable is missing,
		// which on a sane Windows installation it is not.
		if dir := os.Getenv("ProgramData"); dir != "" {
			return filepath.Join(dir, "Headnet")
		}
		return filepath.Join(`C:\ProgramData`, "Headnet")
	default:
		// Linux and macOS both use this. macOS would conventionally prefer
		// /Library/Application Support, and the Keychain is under evaluation
		// for the key itself; until that is built, one path is one thing to
		// get right rather than two.
		return "/var/lib/headnet"
	}
}

// Load reads the device key.
//
// It returns ErrNoKey if the machine has not enrolled, and ErrInsecureKeyFile
// if the file is readable beyond the account running the daemon — refusing in
// that case rather than continuing, because a key others can read is a key
// others may already have.
func (s *Store) Load() (PrivateKey, error) {
	f, err := openKeyFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PrivateKey{}, fmt.Errorf("%w: %s does not exist", ErrNoKey, s.path)
		}
		return PrivateKey{}, err
	}
	defer f.Close()

	// The limit is not about memory. It means a device node or a very large
	// file substituted for the key is refused by shape rather than read in
	// full, and a key file is 45 bytes.
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes+1))
	if err != nil {
		return PrivateKey{}, fmt.Errorf("read device key %s: %w", s.path, err)
	}
	defer zero(raw)

	if len(raw) > maxKeyFileBytes {
		return PrivateKey{}, fmt.Errorf(
			"device key %s is unusable: it is larger than %d bytes, so it is not a key file",
			s.path, maxKeyFileBytes)
	}

	key, err := ParsePrivateKey(strings.TrimSpace(string(raw)))
	if err != nil {
		// ParsePrivateKey's errors describe the shape of the input and never
		// echo it, so wrapping is safe. Naming the path tells an operator
		// which file to replace.
		return PrivateKey{}, fmt.Errorf("device key %s is unusable: %w", s.path, err)
	}
	return key, nil
}

// Create generates a new key and writes it.
//
// It fails with ErrKeyExists rather than overwriting. A device that loses its
// key loses its identity on the network, and "the daemon silently made a new
// one" is indistinguishable at the control plane from "someone replaced this
// machine" — which is a distinction an operator needs.
func (s *Store) Create() (PrivateKey, error) {
	key, err := GenerateKey()
	if err != nil {
		return PrivateKey{}, err
	}
	if err := s.save(key); err != nil {
		return PrivateKey{}, err
	}
	return key, nil
}

// LoadOrCreate returns the existing key, or generates and stores a new one.
// The bool reports whether a key was created.
//
// An insecure or corrupt existing key is an error, not a reason to replace it:
// overwriting would destroy the evidence and re-enrol the machine under a new
// identity, when what an operator needs is to be told.
func (s *Store) LoadOrCreate() (PrivateKey, bool, error) {
	key, err := s.Load()
	switch {
	case err == nil:
		return key, false, nil
	case errors.Is(err, ErrNoKey):
		created, err := s.Create()
		if err != nil {
			return PrivateKey{}, false, err
		}
		return created, true, nil
	default:
		return PrivateKey{}, false, err
	}
}

// save writes the key with the tightest protection the platform offers.
func (s *Store) save(key PrivateKey) error {
	if key.IsZero() {
		// Defensive: a zero key means generation failed and the error was
		// dropped. Persisting it would enrol a device with no usable identity.
		return fmt.Errorf("refusing to store an all-zero device key")
	}

	dir := filepath.Dir(s.path)
	if err := createStateDir(dir); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	// Base64 with a trailing newline, which is the format `wg` reads and
	// writes, so the file interoperates with the standard tooling.
	contents := []byte(privateKeyBase64(key) + "\n")
	defer zero(contents)

	// O_EXCL is what makes this refuse to overwrite, and it does so
	// atomically — a check-then-write would race with a second daemon.
	f, err := createPrivateFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w at %s", ErrKeyExists, s.path)
		}
		return fmt.Errorf("create device key %s: %w", s.path, err)
	}

	if _, err := f.Write(contents); err != nil {
		f.Close()
		// Leaving a truncated key file behind would make the next start fail
		// with a parse error instead of enrolling, so clear it away.
		_ = os.Remove(s.path)
		return fmt.Errorf("write device key %s: %w", s.path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(s.path)
		return fmt.Errorf("sync device key %s: %w", s.path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(s.path)
		return fmt.Errorf("close device key %s: %w", s.path, err)
	}

	// Windows cannot express the restriction at open time, so it is applied
	// here and then verified. On Unix the mode came from the open call and
	// this is a cheap confirmation that no umask or filesystem surprised us.
	if err := restrictKeyFile(s.path); err != nil {
		_ = os.Remove(s.path)
		return err
	}
	// Verified through the same path Load uses, rather than through a separate
	// check that could drift away from it. If the key cannot be read back
	// safely, it must not be left on disk claiming to be usable.
	check, err := openKeyFile(s.path)
	if err != nil {
		_ = os.Remove(s.path)
		return fmt.Errorf("device key %s was written but could not be protected: %w", s.path, err)
	}
	return check.Close()
}

// zero overwrites a buffer that held key material.
//
// This is worth doing and worth being honest about: Go's garbage collector may
// have copied the slice already, and there is no mlock here, so this reduces
// the window rather than closing it. It costs nothing and removes the obvious
// case — a buffer sitting in memory for the life of the process.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
