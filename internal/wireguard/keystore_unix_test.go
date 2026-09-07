//go:build !windows

package wireguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestTheKeyFileIsWrittenAs0600InA0700Directory(t *testing.T) {
	// This is the literal promise in docs/security/key-management.md.
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("key file mode = %#o, want 0600", got)
	}

	dir, err := os.Stat(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatalf("stat on the directory failed: %v", err)
	}
	if got := dir.Mode().Perm(); got != 0o700 {
		t.Errorf("state directory mode = %#o, want 0700 — a directory others "+
			"can enter or write to undermines the file's own mode", got)
	}
}

func TestTheKeyFileModeSurvivesAPermissiveUmask(t *testing.T) {
	// A umask is subtractive, so it cannot loosen a mode — but the directory
	// is created with MkdirAll, which applies the umask to the *initial* mode.
	// Without the explicit chmod in createStateDir this test fails.
	//
	// Not parallel: the umask is process-wide.
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	store := newTestStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	if err := verifyKeyFilePermissions(store.Path()); err != nil {
		t.Fatalf("a key written under umask 0 fails its own permission check: %v", err)
	}
	dir, err := os.Stat(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatalf("stat on the directory failed: %v", err)
	}
	if got := dir.Mode().Perm(); got != 0o700 {
		t.Errorf("state directory mode = %#o under umask 0, want 0700", got)
	}
}

func TestLoadRefusesAKeyFileOtherAccountsCanRead(t *testing.T) {
	// The scenario this exists for is not a bug in this package. It is a real
	// machine months later: a restored backup, a careless chmod -R, an rsync
	// without -p. The daemon has to notice rather than carry on using a key
	// that other accounts have had the opportunity to copy.
	t.Parallel()

	tests := []struct {
		name string
		mode os.FileMode
	}{
		{"group readable", 0o640},
		{"world readable", 0o604},
		{"world writable", 0o622},
		{"wide open", 0o666},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newTestStore(t)
			key, err := store.Create()
			if err != nil {
				t.Fatalf("Create() failed: %v", err)
			}
			if err := os.Chmod(store.Path(), tt.mode); err != nil {
				t.Fatalf("chmod failed: %v", err)
			}

			loaded, err := store.Load()
			if !errors.Is(err, ErrInsecureKeyFile) {
				t.Fatalf("Load() on a %#o key file error = %v, want ErrInsecureKeyFile",
					tt.mode, err)
			}
			if loaded != (PrivateKey{}) {
				t.Error("Load() returned key material alongside the refusal")
			}
			if !strings.Contains(err.Error(), "chmod 600") {
				t.Errorf("error = %q, want it to tell an operator how to fix it", err)
			}
			if strings.Contains(err.Error(), privateKeyBase64(key)) {
				t.Error("the error message contains the key it is refusing to load")
			}
		})
	}
}
