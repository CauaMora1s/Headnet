//go:build windows

package wireguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Regression tests for the Windows findings raised in review.
// Each names the finding it covers, so that removing one is a visible act.

func TestLoadRefusesAKeyFileOwnedByAnUntrustedAccount(t *testing.T) {
	// Review finding: the original permission check asked only for the DACL. An
	// account that owns the file can rewrite its DACL whenever it likes, so a
	// restrictive DACL on a file owned by someone else is a promise that
	// account can withdraw between this check and the read.
	//
	// The refusal cannot be triggered by changing the file's owner — a test
	// cannot create another account, and Windows will not let it assign an
	// owner it does not hold. So the trusted set is narrowed instead, which
	// exercises the same branch from the other side.

	store := newTestStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("Load() failed before the trusted set was narrowed: %v", err)
	}

	// Not parallel: this replaces package state.
	original := trustedSIDs
	t.Cleanup(func() { trustedSIDs = original })

	// A set that cannot contain the real owner. Everyone is never a file
	// owner, so this stands in for "owned by an account we do not trust".
	trustedSIDs = func() ([]*windows.SID, error) {
		everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
		if err != nil {
			return nil, err
		}
		return []*windows.SID{everyone}, nil
	}

	_, err := store.Load()
	if !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("Load() error = %v, want ErrInsecureKeyFile", err)
	}
	if !strings.Contains(err.Error(), "owned by") {
		t.Errorf("error = %q, want it to name the owner as the problem — the DACL "+
			"check would otherwise report the same file as merely over-shared", err)
	}
	if !strings.Contains(err.Error(), "rewrite its permissions") {
		t.Errorf("error = %q, want it to say why an untrusted owner matters", err)
	}
}

func TestLoadRefusesAKeyFileInADirectoryOthersCanWrite(t *testing.T) {
	// Review finding, the directory half. A key file with a perfect DACL
	// inside a directory anyone can write to can be renamed away and replaced
	// with a key the attacker chose. Nothing about the file itself would show
	// it.
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("Load() failed before the directory was widened: %v", err)
	}

	grantEveryone(t, filepath.Dir(store.Path()))

	_, err := store.Load()
	if !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("Load() with a writable state directory error = %v, want ErrInsecureKeyFile", err)
	}
	if !strings.Contains(err.Error(), filepath.Dir(store.Path())) {
		t.Errorf("error = %q, want it to name the directory, not the file", err)
	}
}

func TestUnreadableACEsFailClosed(t *testing.T) {
	// Review finding. The first version of the ACE loop skipped every type
	// that was not a plain allow entry, on the assumption that the rest denied
	// access. Object and compound entries are allow entries with a different
	// layout: skipping them let an untrusted grant through unread, and reading
	// their SID at SidStart would have produced garbage rather than an answer.
	t.Parallel()

	tests := []struct {
		name    string
		aceType byte
		want    aceDisposition
	}{
		{"plain allow", aceAccessAllowed, aceGrants},
		{"callback allow", aceAccessAllowedCallback, aceGrants},
		{"plain deny", aceAccessDenied, aceDenies},
		{"object deny", aceAccessDeniedObject, aceDenies},
		{"callback deny", aceAccessDeniedCallback, aceDenies},
		{"callback object deny", aceAccessDeniedCallbackObject, aceDenies},

		// The three that must not be mistaken for denials.
		{"object allow", aceAccessAllowedObject, aceUnknown},
		{"callback object allow", aceAccessAllowedCallbackObj, aceUnknown},
		{"compound allow", aceAccessAllowedCompound, aceUnknown},

		// Audit and alarm entries belong in a SACL. Seeing one in a DACL means
		// something is not as expected, which is a reason to stop.
		{"system audit", 0x2, aceUnknown},
		{"mandatory label", 0x11, aceUnknown},
		{"unallocated", 0x7f, aceUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := dispositionOf(tt.aceType); got != tt.want {
				t.Errorf("dispositionOf(%#x) = %v, want %v — an allow entry read "+
					"as a denial is a grant nobody checked", tt.aceType, got, tt.want)
			}
		})
	}
}

func TestEveryAllowDispositionCanActuallyBeParsed(t *testing.T) {
	// The two dispositions are not interchangeable: aceGrants promises the
	// SID sits at SidStart, which is only true for the plain and callback
	// layouts. If a future edit adds an object type to that branch, the loop
	// will read whatever happens to be at that offset and compare it to the
	// trusted set — a check that cannot fail and therefore protects nothing.
	t.Parallel()

	for _, objectType := range []byte{
		aceAccessAllowedObject, aceAccessAllowedCallbackObj, aceAccessAllowedCompound,
	} {
		if dispositionOf(objectType) == aceGrants {
			t.Errorf("ACE type %#x is treated as parseable, but its SID is not at "+
				"SidStart, so the SID comparison would read the wrong bytes", objectType)
		}
	}
}

func TestLoadRefusesAKeyFileThatIsADirectory(t *testing.T) {
	// Windows refuses to open a directory for reading without
	// FILE_FLAG_BACKUP_SEMANTICS, which openKeyFile deliberately does not pass
	// for the key file, so this is normally refused at the open rather than by
	// the attribute check inside verifyOpenKeyFile. The attribute check stays
	// as the second line: it is what catches the case where a future edit adds
	// that flag for some unrelated reason.
	//
	// The assertion is therefore that Load fails and hands back no key, not
	// that it fails with a particular error.
	t.Parallel()

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "headnet", KeyFileName))
	if err := createStateDir(filepath.Dir(store.Path())); err != nil {
		t.Fatalf("createStateDir() failed: %v", err)
	}
	if err := os.Mkdir(store.Path(), 0o700); err != nil {
		t.Fatalf("creating a directory where the key belongs failed: %v", err)
	}

	key, err := store.Load()
	if err == nil {
		t.Fatal("Load() read a directory as though it were a key file")
	}
	if errors.Is(err, ErrNoKey) {
		t.Error("a directory reported as ErrNoKey would make the daemon try to " +
			"create a key on top of it")
	}
	if !key.IsZero() {
		t.Error("Load() returned key material alongside the refusal")
	}
}
