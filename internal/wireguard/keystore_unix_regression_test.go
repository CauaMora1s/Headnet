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

// Regression tests for the Unix half of a review finding:
// validating one object and then reading another, and never looking at the
// directory at all.

func TestLoadRefusesAKeyFileInADirectoryOthersCanWrite(t *testing.T) {
	// A key file with a perfect 0600 mode inside a 0777 directory can be
	// renamed away and replaced with a key the attacker chose. Nothing about
	// the file itself would show it, which is exactly why checking only the
	// file was not enough.
	t.Parallel()

	tests := []struct {
		name string
		mode os.FileMode
	}{
		{"group writable", 0o770},
		{"world writable", 0o707},
		{"wide open", 0o777},
		// The sticky bit stops others deleting a file they do not own, but not
		// creating one where none exists, so it is not a substitute.
		{"world writable and sticky", os.ModeSticky | 0o777},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newTestStore(t)
			if _, err := store.Create(); err != nil {
				t.Fatalf("Create() failed: %v", err)
			}
			if _, err := store.Load(); err != nil {
				t.Fatalf("Load() failed before the directory was widened: %v", err)
			}

			dir := filepath.Dir(store.Path())
			if err := os.Chmod(dir, tt.mode); err != nil {
				t.Fatalf("chmod on the directory failed: %v", err)
			}

			_, err := store.Load()
			if !errors.Is(err, ErrInsecureKeyFile) {
				t.Fatalf("Load() with a %v state directory error = %v, want ErrInsecureKeyFile",
					tt.mode, err)
			}
			if !strings.Contains(err.Error(), dir) {
				t.Errorf("error = %q, want it to name the directory, not the file", err)
			}
			if !strings.Contains(err.Error(), "chmod 700") {
				t.Errorf("error = %q, want it to tell an operator how to fix it", err)
			}
		})
	}
}

func TestADirectoryOthersCanOnlyReadIsAccepted(t *testing.T) {
	// The mirror of the test above, and the reason the directory check looks
	// at the write bits rather than demanding 0700 exactly. Being able to list
	// the directory reveals that a key exists, which an attacker on the
	// machine can infer anyway. Being able to write to it is what lets them
	// choose the key. Refusing on the read bits would fail deployments that
	// are not actually unsafe.
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	if err := os.Chmod(filepath.Dir(store.Path()), 0o755); err != nil {
		t.Fatalf("chmod on the directory failed: %v", err)
	}

	if _, err := store.Load(); err != nil {
		t.Fatalf("Load() refused a readable but not writable directory: %v", err)
	}
}

func TestLoadRefusesASymlinkedKeyFile(t *testing.T) {
	// The path-then-reopen pattern this replaced would follow a symlink
	// planted at the key path, and apply its ownership and mode checks to
	// whatever the link pointed at rather than to what it read.
	t.Parallel()

	store := newTestStore(t)
	target := filepath.Join(t.TempDir(), "elsewhere.key")

	real, err := NewStore(target).Create()
	if err != nil {
		t.Fatalf("creating the link target failed: %v", err)
	}
	if err := createStateDir(filepath.Dir(store.Path())); err != nil {
		t.Fatalf("createStateDir() failed: %v", err)
	}
	if err := os.Symlink(target, store.Path()); err != nil {
		t.Skipf("this platform will not create a symlink here: %v", err)
	}

	loaded, err := store.Load()
	if err == nil {
		t.Fatal("Load() followed a symbolic link at the key path")
	}
	if !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("Load() on a symlinked key error = %v, want ErrInsecureKeyFile", err)
	}
	if !loaded.IsZero() {
		t.Error("Load() returned key material alongside the refusal")
	}
	if strings.Contains(err.Error(), privateKeyBase64(real)) {
		t.Error("the error message contains the key it refused to load")
	}
}

func TestCreateRefusesToWriteThroughASymlink(t *testing.T) {
	// The write half of the same problem: an attacker who can create the key
	// file first, as a link, redirects where the new key lands.
	t.Parallel()

	store := newTestStore(t)
	target := filepath.Join(t.TempDir(), "attacker-chosen.key")

	if err := createStateDir(filepath.Dir(store.Path())); err != nil {
		t.Fatalf("createStateDir() failed: %v", err)
	}
	if err := os.Symlink(target, store.Path()); err != nil {
		t.Skipf("this platform will not create a symlink here: %v", err)
	}

	if _, err := store.Create(); err == nil {
		t.Fatal("Create() wrote through a symbolic link")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("Create() created the file the link pointed at")
	}
}

func TestLoadRefusesAKeyFileThatIsNotARegularFile(t *testing.T) {
	// A fifo in place of the key file turns the read into something the
	// attacker controls the contents and timing of, and would otherwise block
	// the daemon's start indefinitely.
	t.Parallel()

	store := newTestStore(t)
	if err := createStateDir(filepath.Dir(store.Path())); err != nil {
		t.Fatalf("createStateDir() failed: %v", err)
	}
	if err := makeFifo(store.Path()); err != nil {
		t.Skipf("this platform will not create a fifo here: %v", err)
	}

	_, err := store.Load()
	if !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("Load() on a fifo error = %v, want ErrInsecureKeyFile", err)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %q, want it to say what is wrong with the file", err)
	}
}

// makeFifo creates a named pipe, which is the cheapest non-regular file a test
// can put where the key belongs.
func makeFifo(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
