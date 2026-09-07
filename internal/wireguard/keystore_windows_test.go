//go:build windows

package wireguard

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// grantEveryone widens the file's DACL so that every account can read it.
//
// This is the Windows equivalent of `chmod 644`, and writing it out is the
// only way to prove the verification does anything. A permission check that is
// never shown refusing is indistinguishable from one that always returns nil —
// which is exactly what a stubbed Windows implementation would look like.
func grantEveryone(t *testing.T, path string) {
	t.Helper()

	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(WinWorldSid) failed: %v", err)
	}

	self, err := currentUserSID()
	if err != nil {
		t.Fatalf("currentUserSID() failed: %v", err)
	}

	// The owner keeps access, so a failure to load is attributable to the
	// Everyone entry rather than to the test locking itself out.
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(self),
			},
		},
		{
			AccessPermissions: windows.GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(everyone),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("ACLFromEntries failed: %v", err)
	}

	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
	if err != nil {
		t.Fatalf("SetNamedSecurityInfo failed: %v", err)
	}
}

func TestLoadRefusesAKeyFileEveryAccountCanRead(t *testing.T) {
	// Windows has no file mode, so the 0600 promise in
	// docs/security/key-management.md is kept with an explicit DACL. This is
	// the test that the DACL is checked and not merely written.
	t.Parallel()

	store := newTestStore(t)
	key, err := store.Create()
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// It must load before the ACL is widened, or the assertion below would
	// pass even if Load refused everything.
	if _, err := store.Load(); err != nil {
		t.Fatalf("Load() failed on a freshly written key: %v", err)
	}

	grantEveryone(t, store.Path())

	loaded, err := store.Load()
	if !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("Load() on a world-readable key file error = %v, want ErrInsecureKeyFile", err)
	}
	if !loaded.IsZero() {
		t.Error("Load() returned key material alongside the refusal")
	}
	if !strings.Contains(err.Error(), store.Path()) {
		t.Errorf("error = %q, want it to name the file", err)
	}
	if strings.Contains(err.Error(), privateKeyBase64(key)) {
		t.Error("the error message contains the key it is refusing to load")
	}
}

func TestTheKeyFileDACLIsProtectedFromInheritance(t *testing.T) {
	// Setting a DACL without PROTECTED_DACL_SECURITY_INFORMATION adds to the
	// inherited entries instead of replacing them, which would leave whatever
	// the parent directory grants in force. Verification would then pass while
	// the file remained readable.
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	sd, err := windows.GetNamedSecurityInfo(store.Path(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo failed: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL() failed: %v", err)
	}
	if dacl == nil {
		t.Fatal("the key file has a NULL DACL, which grants every account full access")
	}
	if dacl.AceCount != 1 {
		t.Errorf("the key file's DACL has %d entries, want exactly 1 — more than "+
			"one means inherited entries were not replaced", dacl.AceCount)
	}
}
