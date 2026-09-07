// These tests are in the internal package deliberately, against the usual
// preference for external test packages in docs/development/testing.md.
//
// The central property here is that a private key cannot be turned into a
// string by any supported route. Proving that requires the string the key
// *would* have produced, and privateKeyBase64 is unexported precisely so no
// caller outside this package can obtain it. An external test could only
// assert that some output does not contain some other value it guessed at,
// which is not the same claim.
package wireguard

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestGeneratedKeysAreUnique(t *testing.T) {
	t.Parallel()

	const count = 2048
	// Keyed on the derived public key: it is unique per private key, and a map
	// of secrets is a thing not to build even in a test.
	seen := make(map[PublicKey]struct{}, count)
	for i := range count {
		key, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey() failed on iteration %d: %v", i, err)
		}
		if _, duplicate := seen[key.Public()]; duplicate {
			t.Fatalf("GenerateKey() returned a duplicate key after %d draws, "+
				"which means it is not drawing from the CSPRNG", i)
		}
		seen[key.Public()] = struct{}{}
	}
}

func TestGeneratedKeysAreClampedForX25519(t *testing.T) {
	// An unclamped scalar leaks information through small-subgroup attacks and
	// is not what WireGuard produces. If this fails, keys generated here are
	// incompatible with every other WireGuard implementation.
	t.Parallel()

	for i := range 256 {
		key, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey() failed: %v", err)
		}
		if !clamped(key) {
			t.Fatalf("key %d is not clamped", i)
		}
	}
}

func TestPublicKeyMatchesTheRFC7748TestVector(t *testing.T) {
	// RFC 7748 section 6.1, Alice's key pair. Deriving her public key from her
	// private one is the check that this package is doing X25519 and not
	// something that merely looks like it. A home-grown derivation would
	// produce keys no other WireGuard peer could talk to.
	t.Parallel()

	// The RFC states Alice's scalar before clamping, and curve25519's
	// ScalarBaseMult does not clamp for you — a fact this test established
	// and which is why ParsePrivateKey refuses an unclamped key rather than
	// quietly fixing it. An unclamped key derives a *different* public key, so
	// a device would enrol under one identity and hold another.
	const (
		aliceRawHex = "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a"
		alicePublic = "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo="
	)

	raw, err := hex.DecodeString(aliceRawHex)
	if err != nil {
		t.Fatalf("decoding the test vector failed: %v", err)
	}
	scalar := new([KeyLength]byte)
	copy(scalar[:], raw)
	clamp(scalar)
	key := newPrivateKey(scalar)

	if got := key.Public().String(); got != alicePublic {
		t.Fatalf("Public() = %q, want the RFC 7748 vector %q", got, alicePublic)
	}

	// The same key, unclamped, must be refused rather than silently corrected.
	unclampedScalar := new([KeyLength]byte)
	copy(unclampedScalar[:], raw)
	unclampedKey := newPrivateKey(unclampedScalar)
	if _, err := ParsePrivateKey(privateKeyBase64(unclampedKey)); err == nil {
		t.Fatal("ParsePrivateKey accepted an unclamped key, which would derive " +
			"a different public key than the one the device enrolled with")
	}
}

func TestPublicKeyDerivationIsDeterministic(t *testing.T) {
	t.Parallel()

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() failed: %v", err)
	}
	first := key.Public()
	for range 8 {
		if !key.Public().Equal(first) {
			t.Fatal("Public() returned different keys for the same private key")
		}
	}
	if first.IsZero() {
		t.Fatal("Public() returned an all-zero key, which is not a usable point")
	}
}

// TestThePrivateKeyCannotBeSerialised is the test this package exists for.
//
// docs/security/key-management.md promises that no private key enters a log, a
// diagnostic bundle or an error message. That promise is only as good as the
// types, because fmt and encoding/json reach into structs without asking. An
// equivalent claim about auth.User turned out to be false when it was finally
// tested; this is the same test written before the same bug.
func TestThePrivateKeyCannotBeSerialised(t *testing.T) {
	t.Parallel()

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() failed: %v", err)
	}
	secret := privateKeyBase64(key)

	// A realistic mistake: the key sitting in a struct somebody logs whole.
	type deviceState struct {
		Name       string
		PrivateKey PrivateKey
		PublicKey  PublicKey
	}
	state := deviceState{Name: "laptop", PrivateKey: key, PublicKey: key.Public()}

	t.Run("fmt verbs do not render it", func(t *testing.T) {
		for _, format := range []string{"%v", "%s", "%d", "%+v", "%#v", "%q"} {
			for _, value := range []any{key, &key, state, &state} {
				rendered := fmt.Sprintf(format, value)
				if strings.Contains(rendered, secret) {
					t.Errorf("fmt.Sprintf(%q, %T) rendered the private key", format, value)
				}
			}
		}
	})

	t.Run("the byte values do not leak through %d", func(t *testing.T) {
		// %d on a [32]byte prints the raw scalar as decimal numbers, which is
		// the key in another costume.
		//
		// This assertion was written expecting String() to cover it, and it
		// failed: fmt consults Stringer only for the string-shaped verbs.
		// PrivateKey implements fmt.Formatter because of this test, which is
		// the whole argument for writing the failure-mode assertion even when
		// you are confident of the answer.
		rendered := fmt.Sprintf("%d", key)
		if rendered != redactedPrivateKey {
			t.Errorf("%%d rendered the raw key bytes: %s", rendered)
		}
		if got := fmt.Sprintf("%T", key); got != "wireguard.PrivateKey" {
			t.Errorf("%%T = %q, want the type name — redacting it would make a "+
				"suppressed log line impossible to trace", got)
		}
	})

	t.Run("JSON encoding fails rather than succeeding quietly", func(t *testing.T) {
		if _, err := json.Marshal(key); !errors.Is(err, ErrKeyNotSerialisable) {
			t.Errorf("json.Marshal(key) error = %v, want ErrKeyNotSerialisable", err)
		}
		out, err := json.Marshal(state)
		if !errors.Is(err, ErrKeyNotSerialisable) {
			t.Errorf("json.Marshal(struct) error = %v, want ErrKeyNotSerialisable", err)
		}
		if strings.Contains(string(out), secret) {
			t.Error("json.Marshal produced output containing the private key")
		}
	})

	t.Run("gob encoding fails", func(t *testing.T) {
		var buf bytes.Buffer
		err := gob.NewEncoder(&buf).Encode(state)
		if err == nil {
			t.Error("gob encoded a struct containing a private key without complaint")
		}
		if strings.Contains(buf.String(), secret) {
			t.Error("gob output contains the private key")
		}
	})

	t.Run("slog does not write it", func(t *testing.T) {
		for _, handler := range []string{"json", "text"} {
			var buf bytes.Buffer
			var h slog.Handler
			if handler == "json" {
				h = slog.NewJSONHandler(&buf, nil)
			} else {
				h = slog.NewTextHandler(&buf, nil)
			}
			logger := slog.New(h)

			logger.Info("enrolled", "key", key, "state", state,
				"pointer", &key, "group", slog.GroupValue(slog.Any("k", key)))

			if strings.Contains(buf.String(), secret) {
				t.Errorf("the %s handler wrote the private key: %s", handler, buf.String())
			}
			if !strings.Contains(buf.String(), "REDACTED") {
				t.Errorf("the %s handler did not mark the key as redacted, so a "+
					"reader cannot tell a key was suppressed: %s", handler, buf.String())
			}
		}
	})

	t.Run("an unexported field does not leak it", func(t *testing.T) {
		// This was found in review, with a reproduction, and it was
		// the most important of the four findings: the claim above this test
		// was simply false for the old representation.
		//
		// fmt renders unexported struct fields through reflection and cannot
		// call methods on them, because reflect will not hand out an interface
		// for an unexported field. Format, String and every other escape hatch
		// is skipped, and fmt prints the underlying value instead. When
		// PrivateKey was a [32]byte, %v on a struct holding one in an
		// unexported field printed the raw scalar.
		//
		// A daemon keeping its key in an unexported field and logging its own
		// state is the obvious way to write a daemon, not a contrived case.
		type daemonState struct {
			name string
			key  PrivateKey
			pub  PublicKey
		}
		hidden := daemonState{name: "laptop", key: key, pub: key.Public()}

		// The scalar rendered as decimal and as hex, which is what %v, %d and
		// %#v would produce respectively.
		var decimal, hexed strings.Builder
		for i, b := range keyBytesForTest(key) {
			if i > 0 {
				decimal.WriteByte(' ')
				hexed.WriteString(", ")
			}
			fmt.Fprintf(&decimal, "%d", b)
			fmt.Fprintf(&hexed, "%#x", b)
		}

		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%d", "%q"} {
			rendered := fmt.Sprintf(format, hidden)
			for _, leak := range []struct{ what, value string }{
				{"base64", secret},
				{"decimal bytes", decimal.String()},
				{"hex bytes", hexed.String()},
			} {
				if strings.Contains(rendered, leak.value) {
					t.Errorf("fmt.Sprintf(%q, ...) leaked the key as %s through an "+
						"unexported field: %s", format, leak.what, rendered)
				}
			}
		}

		// And through slog, which is how it would actually reach a log file.
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("daemon state", "state", hidden)
		if strings.Contains(buf.String(), secret) ||
			strings.Contains(buf.String(), decimal.String()) {
			t.Errorf("slog wrote the key held in an unexported field: %s", buf.String())
		}
	})

	t.Run("the public key still renders, because it must", func(t *testing.T) {
		// Suppressing the public key too would be the easy way to pass every
		// assertion above and would break enrolment, which has to send it.
		if got := fmt.Sprintf("%v", key.Public()); got != key.Public().String() {
			t.Errorf("public key rendered as %q, want its base64 form", got)
		}
	})
}

func TestParsePrivateKeyRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	// An unclamped but otherwise valid key: 32 bytes with the low bits set.
	unclamped := make([]byte, KeyLength)
	for i := range unclamped {
		unclamped[i] = 0xff
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "it is empty"},
		{"whitespace only", "   \n", "it is empty"},
		{"not base64", "this is not base64!!", "not valid standard base64"},
		{"url-safe base64", "-_" + strings.Repeat("A", 42) + "=", "not valid standard base64"},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16)), "decodes to 16 bytes"},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 64)), "decodes to 64 bytes"},
		{"all zeroes", base64.StdEncoding.EncodeToString(make([]byte, KeyLength)), "key generation failed"},
		{"unclamped", base64.StdEncoding.EncodeToString(unclamped), "not clamped"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParsePrivateKey(tt.in)
			if err == nil {
				t.Fatalf("ParsePrivateKey(%q) succeeded, want an error", tt.in)
			}
			if !errors.Is(err, ErrInvalidKey) {
				t.Errorf("error does not wrap ErrInvalidKey: %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
			if trimmed := strings.TrimSpace(tt.in); trimmed != "" && strings.Contains(err.Error(), trimmed) {
				// For a private key the input *is* the secret, so an error
				// that echoes it would put a key in the log of whatever
				// reported the failure.
				t.Errorf("error echoes the rejected input, which may be key material: %v", err)
			}
		})
	}
}

func TestPrivateKeyRoundTripsThroughItsTextForm(t *testing.T) {
	t.Parallel()

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() failed: %v", err)
	}

	parsed, err := ParsePrivateKey(privateKeyBase64(key))
	if err != nil {
		t.Fatalf("ParsePrivateKey() rejected a key this package generated: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("the key did not survive a round trip through its text form")
	}
	if !parsed.Public().Equal(key.Public()) {
		t.Fatal("the round-tripped key derives a different public key")
	}
}

func TestPublicKeyRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() failed: %v", err)
	}
	want := key.Public()

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal(PublicKey) failed: %v", err)
	}
	if string(encoded) != `"`+want.String()+`"` {
		t.Fatalf("PublicKey encoded as %s, want the bare base64 string the API uses", encoded)
	}

	var got PublicKey
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal(PublicKey) failed: %v", err)
	}
	if !got.Equal(want) {
		t.Fatal("the public key did not survive a JSON round trip")
	}
}

func TestParsePublicKeyRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "nonsense", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := ParsePublicKey(in); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("ParsePublicKey(%q) error = %v, want ErrInvalidKey", in, err)
		}
	}
}

func TestPublicKeyAcceptsAllZeroes(t *testing.T) {
	// Deliberate asymmetry with ParsePrivateKey, and worth stating so nobody
	// "fixes" it: an all-zero *private* key means generation failed locally,
	// which this package can and does refuse. An all-zero *public* key is
	// attacker-controlled input arriving over the network, and rejecting it is
	// the control plane's job — devices.ValidatePublicKey already does exactly
	// that, with an error message aimed at the enroller rather than at a
	// daemon operator.
	t.Parallel()

	key, err := ParsePublicKey(base64.StdEncoding.EncodeToString(make([]byte, KeyLength)))
	if err != nil {
		t.Fatalf("ParsePublicKey() rejected a zero key here, splitting the "+
			"validation rule across two packages: %v", err)
	}
	if !key.IsZero() {
		t.Fatal("IsZero() did not recognise an all-zero public key")
	}
}

// keyBytesForTest exposes the raw scalar so the leak assertions can look for
// it. It exists only in tests, which is the point: there is no exported way to
// get these bytes back out, and privateKeyBase64 is unexported for the same
// reason.
func keyBytesForTest(k PrivateKey) [KeyLength]byte {
	scalar := k.bytes()
	if scalar == nil {
		return [KeyLength]byte{}
	}
	return *scalar
}
