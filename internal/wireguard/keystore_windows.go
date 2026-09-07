//go:build windows

package wireguard

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no file mode, so the 0600 promise in
// docs/security/key-management.md is kept a different way: an explicit DACL
// with a single entry granting the account that runs the daemon, and
// inheritance switched off so nothing the parent directory permits leaks in.
//
// This file is the reason the Unix implementation is not simply reused with a
// no-op here. A stub that returned nil would make Windows *look* protected
// while leaving the key readable by every account on the machine — the exact
// kind of quiet lie this project refuses elsewhere.

// createStateDir creates the directory and restricts it to the current user.
//
// The directory matters independently of the file: an account that can write
// to the directory can replace the key file wholesale, whatever the file's own
// ACL says.
func createStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Objects created later inherit this, which is what keeps a future state
	// file from being created world-readable by default.
	return restrictToCurrentUser(dir, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

// createPrivateFile opens the key file for writing, failing if it exists.
//
// The mode argument is nearly meaningless on Windows — it controls only the
// read-only attribute — so the file is briefly protected by nothing but the
// inherited directory ACL. restrictKeyFile closes that window immediately
// after, and the directory created above is already restricted, so the window
// is not open to anyone who was not already able to read the directory.
func createPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// restrictKeyFile replaces the file's DACL with one entry for the current
// user, and marks it protected so no inherited entry applies.
func restrictKeyFile(path string) error {
	return restrictToCurrentUser(path, windows.NO_INHERITANCE)
}

func restrictToCurrentUser(path string, inheritance uint32) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build an access control list for %s: %w", path, err)
	}

	// PROTECTED_DACL_SECURITY_INFORMATION is the load-bearing half. Without
	// it the entries inherited from the parent directory remain in force and
	// the single entry above would be an addition rather than a replacement.
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
	if err != nil {
		return fmt.Errorf("restrict access to %s: %w", path, err)
	}
	return nil
}

// verifyKeyFilePermissions refuses a key file that any account beyond the
// expected ones can reach.
//
// The accepted set is the current user, LocalSystem and the Administrators
// group. The last two are not a weakening: on Windows both can take ownership
// of any object regardless of its DACL, so refusing to run because they appear
// in an ACL would produce a false alarm rather than any additional protection.
// Everything else is refused.
func verifyKeyFilePermissions(path string) error {
	// Stat first, so a missing file surfaces as os.ErrNotExist and the caller
	// can tell "not enrolled yet" from "cannot be trusted".
	if _, err := os.Stat(path); err != nil {
		return err
	}

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: cannot read the access control list of %s: %w",
			ErrInsecureKeyFile, path, err)
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("%w: cannot read the access control list of %s: %w",
			ErrInsecureKeyFile, path, err)
	}
	if dacl == nil {
		// A NULL DACL grants everyone full access. It is the worst possible
		// state and is easy to mistake for "no entries", so it gets its own
		// message.
		return fmt.Errorf("%w: %s has no access control list at all, which grants every account full access",
			ErrInsecureKeyFile, path)
	}

	allowed, err := allowedSIDs()
	if err != nil {
		return err
	}

	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%w: cannot read entry %d of the access control list of %s: %w",
				ErrInsecureKeyFile, i, path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			// Deny entries only ever reduce access, so they cannot make the
			// file more readable than the allow entries already do.
			continue
		}

		// The SID is stored inline at the end of the ACE, which is why the
		// Win32 header declares SidStart as the first DWORD of it. This
		// pointer conversion is the documented way to reach it and there is
		// no safe alternative in x/sys/windows.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))

		if !containsSID(allowed, sid) {
			return fmt.Errorf(
				"%w: %s grants access to %s; treat the key as compromised, "+
					"then remove that entry or delete the file and re-enrol",
				ErrInsecureKeyFile, path, sid.String())
		}
	}
	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("determine the account this process runs as: %w", err)
	}
	return user.User.Sid, nil
}

// allowedSIDs is the set that may appear in the key file's DACL.
func allowedSIDs() ([]*windows.SID, error) {
	self, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	sids := []*windows.SID{self}

	// These two can take ownership of any object anyway. A failure to look
	// one up is not fatal: it only narrows what is accepted, which errs
	// towards refusing rather than towards trusting.
	for _, wellKnown := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid,
		windows.WinBuiltinAdministratorsSid,
	} {
		if sid, err := windows.CreateWellKnownSid(wellKnown); err == nil {
			sids = append(sids, sid)
		}
	}
	return sids, nil
}

func containsSID(set []*windows.SID, sid *windows.SID) bool {
	for _, candidate := range set {
		if candidate.Equals(sid) {
			return true
		}
	}
	return false
}
