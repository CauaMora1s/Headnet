package wireguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore returns a store under a temporary directory. t.TempDir is
// cleaned up automatically, and on every platform it is already private to the
// user running the test, which is what the permission assertions rely on.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "headnet", KeyFileName))
}

func TestLoadReportsNoKeyBeforeEnrolment(t *testing.T) {
	// A machine that has never enrolled is the normal starting state, not a
	// failure. If this returned a generic error the daemon could not tell
	// "generate one" from "something is wrong, stop".
	t.Parallel()

	store := newTestStore(t)

	_, err := store.Load()
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load() on a fresh machine error = %v, want ErrNoKey", err)
	}
	if !strings.Contains(err.Error(), store.Path()) {
		t.Errorf("error = %q, want it to name the path an operator should look at", err)
	}
}

func TestCreateWritesAKeyThatOnlyTheOwnerCanRead(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	key, err := store.Create()
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	if key.IsZero() {
		t.Fatal("Create() returned an all-zero key")
	}

	// The check that matters, and the one that runs on every load in
	// production. Its platform-specific half lives in keystore_unix.go and
	// keystore_windows.go.
	if err := verifyKeyFilePermissions(store.Path()); err != nil {
		t.Fatalf("the key file this package just wrote fails its own "+
			"permission check: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() failed on a key Create() wrote: %v", err)
	}
	if loaded != key {
		t.Fatal("Load() returned a different key than Create() produced")
	}
}

func TestTheKeyFileIsInterchangeableWithWireGuardTooling(t *testing.T) {
	// `wg genkey` writes base64 and a newline, and `wg pubkey` reads the same.
	// Matching that format means an operator can inspect or replace the key
	// with the standard tools rather than only with Headnet's own.
	t.Parallel()

	store := newTestStore(t)
	key, err := store.Create()
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("reading the key file failed: %v", err)
	}
	contents := string(raw)

	if !strings.HasSuffix(contents, "\n") {
		t.Error("the key file has no trailing newline, which `wg` writes and " +
			"a shell pipeline will expect")
	}
	if strings.Count(contents, "\n") != 1 {
		t.Errorf("the key file has %d newlines, want exactly one",
			strings.Count(contents, "\n"))
	}

	parsed, err := ParsePrivateKey(contents)
	if err != nil {
		t.Fatalf("the key file does not parse as a WireGuard key: %v", err)
	}
	if parsed != key {
		t.Fatal("the key on disk is not the key Create() returned")
	}
}

func TestCreateRefusesToOverwriteAnExistingKey(t *testing.T) {
	// Overwriting takes the device off the network with no way back except
	// re-enrolment, and the control plane cannot distinguish that from the
	// machine being replaced. It has to be a deliberate act.
	t.Parallel()

	store := newTestStore(t)
	first, err := store.Create()
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	_, err = store.Create()
	if !errors.Is(err, ErrKeyExists) {
		t.Fatalf("a second Create() error = %v, want ErrKeyExists", err)
	}

	after, err := store.Load()
	if err != nil {
		t.Fatalf("Load() failed after the refused overwrite: %v", err)
	}
	if after != first {
		t.Fatal("the refused Create() changed the key on disk anyway")
	}
}

func TestLoadOrCreateKeepsTheSameIdentityAcrossRestarts(t *testing.T) {
	// A daemon restart must not re-enrol the machine. This is the property the
	// roadmap's "daemon restart preserves identity" line refers to.
	t.Parallel()

	store := newTestStore(t)

	first, created, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("first LoadOrCreate() failed: %v", err)
	}
	if !created {
		t.Error("created = false on a machine with no key, want true")
	}

	for i := range 3 {
		again, created, err := store.LoadOrCreate()
		if err != nil {
			t.Fatalf("LoadOrCreate() failed on restart %d: %v", i, err)
		}
		if created {
			t.Fatalf("restart %d generated a new key, which would silently "+
				"re-enrol the machine under a new identity", i)
		}
		if again != first {
			t.Fatalf("restart %d loaded a different key", i)
		}
	}
}

func TestLoadRejectsACorruptKeyFileWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{"empty file", "", "it is empty"},
		{"not base64", "clearly not a key\n", "not valid standard base64"},
		{"truncated", "AAAA\n", "decodes to"},
		{"all zeroes", strings.Repeat("A", 43) + "=\n", "key generation failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newTestStore(t)
			if err := createStateDir(filepath.Dir(store.Path())); err != nil {
				t.Fatalf("createStateDir() failed: %v", err)
			}
			writeKeyFileForTest(t, store.Path(), tt.contents)

			_, err := store.Load()
			if err == nil {
				t.Fatal("Load() accepted a corrupt key file")
			}
			if errors.Is(err, ErrNoKey) {
				t.Error("a corrupt key reported as ErrNoKey would make the daemon " +
					"generate a replacement and silently re-enrol the machine")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), store.Path()) {
				t.Errorf("error = %q, want it to name the file to fix", err)
			}
			// The contents of a key file are secret even when unparseable —
			// a truncated key is still most of a key.
			if trimmed := strings.TrimSpace(tt.contents); trimmed != "" &&
				strings.Contains(err.Error(), trimmed) {
				t.Errorf("error echoes the file contents, which may be key material: %v", err)
			}
		})
	}
}

func TestLoadOrCreateDoesNotPaperOverAnUnreadableKey(t *testing.T) {
	// The dangerous shortcut would be "if Load fails for any reason, make a new
	// one". That turns a permissions problem or a corrupt file into a silent
	// re-enrolment, destroying the evidence on the way.
	t.Parallel()

	store := newTestStore(t)
	if err := createStateDir(filepath.Dir(store.Path())); err != nil {
		t.Fatalf("createStateDir() failed: %v", err)
	}
	writeKeyFileForTest(t, store.Path(), "not a key at all\n")

	_, created, err := store.LoadOrCreate()
	if err == nil {
		t.Fatal("LoadOrCreate() accepted a corrupt key file")
	}
	if created {
		t.Fatal("LoadOrCreate() replaced a corrupt key instead of reporting it")
	}

	raw, readErr := os.ReadFile(store.Path())
	if readErr != nil {
		t.Fatalf("reading the key file failed: %v", readErr)
	}
	if string(raw) != "not a key at all\n" {
		t.Fatal("LoadOrCreate() modified the existing key file")
	}
}

func TestSaveRefusesAnAllZeroKey(t *testing.T) {
	// Reachable only if GenerateKey's error were ignored, which is exactly the
	// kind of mistake that should fail loudly rather than enrol a device with
	// no usable identity.
	t.Parallel()

	store := newTestStore(t)
	if err := store.save(PrivateKey{}); err == nil {
		t.Fatal("save() stored an all-zero key")
	}
	if _, err := os.Stat(store.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("save() left a file behind after refusing")
	}
}

func TestDefaultKeyPathIsSystemWide(t *testing.T) {
	// The key must not live somewhere the desktop user's browser, or another
	// account on a shared machine, can reach. This asserts the shape rather
	// than the exact string, which differs per platform.
	t.Parallel()

	path := DefaultKeyPath()
	if !filepath.IsAbs(path) {
		t.Errorf("DefaultKeyPath() = %q, want an absolute path", path)
	}
	if filepath.Base(path) != KeyFileName {
		t.Errorf("DefaultKeyPath() = %q, want it to end in %q", path, KeyFileName)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" &&
		strings.HasPrefix(path, home) {
		t.Errorf("DefaultKeyPath() = %q is inside the user's home directory, "+
			"where an unprivileged process can read it", path)
	}
}

// writeKeyFileForTest writes contents to path with the same protection the
// store itself applies, so a permissions failure cannot masquerade as the
// parse failure under test.
func writeKeyFileForTest(t *testing.T, path, contents string) {
	t.Helper()

	f, err := createPrivateFile(path)
	if err != nil {
		t.Fatalf("createPrivateFile(%s) failed: %v", path, err)
	}
	if _, err := f.WriteString(contents); err != nil {
		f.Close()
		t.Fatalf("writing the test key file failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing the test key file failed: %v", err)
	}
	if err := restrictKeyFile(path); err != nil {
		t.Fatalf("restrictKeyFile(%s) failed: %v", path, err)
	}
}
