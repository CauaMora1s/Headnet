package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/auth"
)

// A password long enough to satisfy the minimum in every case below.
const goodPassword = "correct horse battery staple"

// cheapParams are used wherever the *cost* of hashing is not what the test is
// about. Argon2id at production settings takes tens of milliseconds by design,
// and a suite that runs it several hundred times becomes slow enough that
// people stop running it. Tests that assert on cost use DefaultParams.
func cheapParams() auth.Params {
	return auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword(goodPassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	ok, needsRehash, err := auth.VerifyPassword(goodPassword, hash)
	if err != nil {
		t.Fatalf("VerifyPassword failed: %v", err)
	}
	if !ok {
		t.Fatal("the correct password did not verify")
	}
	if needsRehash {
		t.Error("a hash made with the current parameters was flagged for rehashing")
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword(goodPassword, cheapParams())
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	for _, wrong := range []string{
		"correct horse battery stapl",  // one character short
		"correct horse battery staplf", // one character different
		"Correct horse battery staple", // different case
		"",
		strings.Repeat("x", 32),
	} {
		ok, _, err := auth.VerifyPassword(wrong, hash)
		if err != nil {
			t.Fatalf("VerifyPassword(%q) errored: %v", wrong, err)
		}
		if ok {
			t.Fatalf("the wrong password %q verified", wrong)
		}
	}
}

func TestTheHashIsNotThePassword(t *testing.T) {
	t.Parallel()
	// A hash that contained the password would defeat the entire point of
	// hashing it.
	hash, err := auth.HashPassword(goodPassword, cheapParams())
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if strings.Contains(hash, goodPassword) {
		t.Fatalf("the encoded hash contains the plaintext password: %s", hash)
	}
}

func TestEveryHashUsesADistinctSalt(t *testing.T) {
	t.Parallel()
	// Identical passwords must not produce identical hashes, or one stolen
	// database reveals which users share a password and makes a single
	// rainbow table cover all of them.
	seen := make(map[string]struct{}, 20)
	for range 20 {
		hash, err := auth.HashPassword(goodPassword, cheapParams())
		if err != nil {
			t.Fatalf("HashPassword failed: %v", err)
		}
		if _, dup := seen[hash]; dup {
			t.Fatal("two hashes of the same password were identical; the salt is not random")
		}
		seen[hash] = struct{}{}
	}
}

func TestEncodedFormCarriesItsParameters(t *testing.T) {
	t.Parallel()
	// The parameters travel with the hash so they can be raised later without
	// invalidating existing passwords.
	params := auth.Params{Memory: 8192, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32}

	hash, err := auth.HashPassword(goodPassword, params)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=8192,t=3,p=2$") {
		t.Fatalf("encoded hash = %q, want it to carry m=8192,t=3,p=2", hash)
	}

	// And it still verifies under those parameters, not the defaults.
	ok, _, err := auth.VerifyPassword(goodPassword, hash)
	if err != nil || !ok {
		t.Fatalf("a hash with non-default parameters did not verify: ok=%v err=%v", ok, err)
	}
}

func TestWeakerParametersTriggerARehash(t *testing.T) {
	t.Parallel()
	weak := auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

	hash, err := auth.HashPassword(goodPassword, weak)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	ok, needsRehash, err := auth.VerifyPassword(goodPassword, hash)
	if err != nil || !ok {
		t.Fatalf("verification failed: ok=%v err=%v", ok, err)
	}
	if !needsRehash {
		t.Fatal("a hash made with weaker parameters was not flagged for rehashing")
	}
}

func TestStrongerParametersDoNotTriggerARehash(t *testing.T) {
	t.Parallel()
	// An operator who deliberately lowered the cost — to run on a Raspberry
	// Pi, say — should not have every login rehashing back upwards.
	strong := auth.DefaultParams()
	strong.Iterations++

	hash, err := auth.HashPassword(goodPassword, strong)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	ok, needsRehash, err := auth.VerifyPassword(goodPassword, hash)
	if err != nil || !ok {
		t.Fatalf("verification failed: ok=%v err=%v", ok, err)
	}
	if needsRehash {
		t.Fatal("a hash stronger than the defaults was flagged for rehashing")
	}
}

func TestMalformedHashesAreRejectedAsErrorsNotAsWrongPasswords(t *testing.T) {
	t.Parallel()
	// A corrupted hash is an operator problem. Reporting it as a failed login
	// would send someone chasing a forgotten password instead of a damaged
	// database.
	tests := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"not a hash", "hunter2"},
		{"too few fields", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA"},
		{"wrong algorithm", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYQ"},
		{"unparseable parameters", "$argon2id$v=19$m=x,t=y,p=z$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNo"},
		{"salt is not base64", "$argon2id$v=19$m=19456,t=2,p=1$!!!!$aGFzaGhhc2hoYXNo"},
		{"key is not base64", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$!!!!"},
		{"no leading separator", "argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ok, _, err := auth.VerifyPassword(goodPassword, tt.hash)
			if err == nil {
				t.Fatalf("VerifyPassword accepted a malformed hash %q without error", tt.hash)
			}
			if ok {
				t.Fatal("a malformed hash reported a successful verification")
			}
		})
	}
}

func TestTamperedHashDoesNotVerify(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword(goodPassword, cheapParams())
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Flip the last character of the derived key.
	runes := []rune(hash)
	if runes[len(runes)-1] == 'A' {
		runes[len(runes)-1] = 'B'
	} else {
		runes[len(runes)-1] = 'A'
	}

	ok, _, err := auth.VerifyPassword(goodPassword, string(runes))
	if err != nil {
		// A tampered key may or may not still decode; either way it must not
		// verify.
		return
	}
	if ok {
		t.Fatal("a tampered hash verified")
	}
}

func TestAFutureArgonVersionIsRefused(t *testing.T) {
	t.Parallel()
	hash := "$argon2id$v=99$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYQ"
	_, _, err := auth.VerifyPassword(goodPassword, hash)
	if !errors.Is(err, auth.ErrIncompatibleVersion) {
		t.Fatalf("error = %v, want it to wrap ErrIncompatibleVersion", err)
	}
}

func TestPasswordLengthBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		password string
		minimum  int
		wantErr  error
	}{
		{"too short", strings.Repeat("a", auth.MinPasswordLength-1), 0, auth.ErrPasswordTooShort},
		{"exactly the minimum", strings.Repeat("a", auth.MinPasswordLength), 0, nil},
		{"empty", "", 0, auth.ErrPasswordTooShort},
		{"too long", strings.Repeat("a", auth.MaxPasswordLength+1), 0, auth.ErrPasswordTooLong},
		{"exactly the maximum", strings.Repeat("a", auth.MaxPasswordLength), 0, nil},
		{"below a raised minimum", strings.Repeat("a", 15), 20, auth.ErrPasswordTooShort},
		{"meets a raised minimum", strings.Repeat("a", 20), 20, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := auth.CheckPasswordLength(tt.password, tt.minimum)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckPasswordLength errored unexpectedly: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want it to wrap %v", err, tt.wantErr)
			}
		})
	}
}

func TestTheMinimumLengthFloorCannotBeLowered(t *testing.T) {
	t.Parallel()
	// Configuration may raise the minimum but never drop below the built-in
	// floor, so a misconfiguration cannot permit a two-character password.
	short := strings.Repeat("a", 4)
	if err := auth.CheckPasswordLength(short, 1); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("a minimum of 1 accepted a 4-character password: %v", err)
	}
}

func TestPasswordLengthIsCountedInRunes(t *testing.T) {
	t.Parallel()
	// Thirteen characters, but far more bytes. Counting bytes would let a
	// short non-Latin passphrase pass, and penalise a legitimate one.
	password := strings.Repeat("パスワード", 3) // 15 runes
	if err := auth.CheckPasswordLength(password, auth.MinPasswordLength); err != nil {
		t.Fatalf("a 15-rune passphrase was rejected: %v", err)
	}
}

func TestHashPasswordRejectsUnusableParameters(t *testing.T) {
	t.Parallel()
	for _, p := range []auth.Params{
		{Memory: 0, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		{Memory: 1024, Iterations: 0, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		{Memory: 1024, Iterations: 1, Parallelism: 0, SaltLength: 16, KeyLength: 32},
		{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 4, KeyLength: 32},
		{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 8},
	} {
		if _, err := auth.HashPassword(goodPassword, p); err == nil {
			t.Errorf("HashPassword accepted unusable parameters %+v", p)
		}
	}
}

func TestZeroParamsMeansDefaults(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword(goodPassword, auth.Params{})
	if err != nil {
		t.Fatalf("HashPassword with the zero Params failed: %v", err)
	}
	ok, needsRehash, err := auth.VerifyPassword(goodPassword, hash)
	if err != nil || !ok || needsRehash {
		t.Fatalf("the zero Params did not produce a current-cost hash: ok=%v rehash=%v err=%v",
			ok, needsRehash, err)
	}
}

func TestDummyHashIsUsableAndUnguessable(t *testing.T) {
	t.Parallel()
	// The login path verifies against this when no account exists, so that the
	// timing of "no such user" matches "wrong password". It has to be a real
	// hash that real work can be done against, and must never match anything.
	first, err := auth.DummyHash()
	if err != nil {
		t.Fatalf("DummyHash failed: %v", err)
	}

	ok, _, err := auth.VerifyPassword(goodPassword, first)
	if err != nil {
		t.Fatalf("verifying against the dummy hash errored: %v", err)
	}
	if ok {
		t.Fatal("a password verified against the dummy hash")
	}

	second, err := auth.DummyHash()
	if err != nil {
		t.Fatalf("DummyHash failed: %v", err)
	}
	if first == second {
		t.Fatal("DummyHash returned the same value twice")
	}
}
