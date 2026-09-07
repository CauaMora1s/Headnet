//go:build windows

package wireguard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// ACE types, from winnt.h. x/sys/windows declares only the first two, and the
// rest are needed to decide which entries this package is able to reason about
// rather than guessing.
//
// The distinction that matters is layout, not intent: a plain ACE stores the
// SID at SidStart, whereas an object ACE has Flags, ObjectType and
// InheritedObjectType in between. Reading SidStart on an object ACE yields
// garbage, so those cannot be checked with the code below and must not be
// waved through.
const (
	aceAccessAllowed              = 0x0
	aceAccessDenied               = 0x1
	aceAccessAllowedCompound      = 0x4
	aceAccessAllowedObject        = 0x5
	aceAccessDeniedObject         = 0x6
	aceAccessAllowedCallback      = 0x9
	aceAccessDeniedCallback       = 0xA
	aceAccessAllowedCallbackObj   = 0xB
	aceAccessDeniedCallbackObject = 0xC
)

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
// is not open to anyone who was not already able to write to the directory.
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
	//
	// The owner is set explicitly too. A file this process created is already
	// owned by it in the normal case, but a file created inside a directory
	// carrying the "creator owner" default can be owned by someone else, and
	// the owner can rewrite the DACL at will — so leaving it unset would make
	// the DACL above advisory.
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|
			windows.OWNER_SECURITY_INFORMATION,
		sid, nil, acl, nil)
	if err != nil {
		return fmt.Errorf("restrict access to %s: %w", path, err)
	}
	return nil
}

// openKeyFile opens the key file for reading and refuses if anything about it,
// or about the directory it sits in, would let another account choose its
// contents.
//
// As on Unix, every check below is made against the returned handle rather
// than against the path. An earlier version validated with GetNamedSecurityInfo
// and then read with os.ReadFile, so the object that was checked was not
// necessarily the object that was read.
//
// Found in review.
func openKeyFile(path string) (*os.File, error) {
	if err := verifyStateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}

	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("open device key %s: %w", path, err)
	}

	// FILE_FLAG_OPEN_REPARSE_POINT opens the link itself rather than following
	// it. Without it, a junction or symlink planted at this path would send
	// the read somewhere an attacker controls, and every check below would be
	// applied to the wrong object.
	handle, err := windows.CreateFile(name,
		windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, mapOpenError(path, err)
	}

	f := os.NewFile(uintptr(handle), path)
	if err := verifyOpenKeyFile(f, path); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// mapOpenError turns the Win32 "not there" errors into fs.ErrNotExist, so Load
// can tell a machine that has not enrolled from one whose key it cannot trust.
func mapOpenError(path string, err error) error {
	switch err {
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND:
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	default:
		return &fs.PathError{Op: "open", Path: path, Err: err}
	}
}

func verifyOpenKeyFile(f *os.File, path string) error {
	handle := windows.Handle(f.Fd())

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("%w: cannot inspect %s: %w", ErrInsecureKeyFile, path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf(
			"%w: %s is a reparse point, so its contents are chosen by whoever "+
				"controls the target; remove it and re-enrol",
			ErrInsecureKeyFile, path)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fmt.Errorf("%w: %s is a directory", ErrInsecureKeyFile, path)
	}

	return verifyHandleSecurity(handle, path)
}

// verifyStateDir applies the same checks to the directory holding the key.
//
// An account that can write to the directory can rename the key away and drop
// in one it chose, which no check on the file itself would catch.
func verifyStateDir(dir string) error {
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	// FILE_FLAG_BACKUP_SEMANTICS is what allows a directory handle at all.
	handle, err := windows.CreateFile(name,
		windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		// A missing directory means no key yet, and Load turns that into
		// ErrNoKey.
		return mapOpenError(dir, err)
	}
	defer windows.CloseHandle(handle)

	return verifyHandleSecurity(handle, dir)
}

// verifyHandleSecurity refuses an object any account beyond the expected ones
// can reach, or whose security descriptor this package cannot fully read.
//
// The accepted set is the current user, LocalSystem and the Administrators
// group. The last two are not a weakening: both can take ownership of any
// object regardless of its DACL, so refusing to run because they appear would
// produce a false alarm rather than any additional protection. Everything else
// is refused.
func verifyHandleSecurity(handle windows.Handle, subject string) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: cannot read the security descriptor of %s: %w",
			ErrInsecureKeyFile, subject, err)
	}

	trusted, err := trustedSIDs()
	if err != nil {
		return err
	}

	// The owner is checked before the DACL, because the owner can rewrite the
	// DACL whenever it likes. A file owned by an untrusted account can present
	// a perfectly restrictive DACL right up to the moment it is read.
	//
	// Found in review; the Unix side had the equivalent check from
	// the start and Windows did not.
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("%w: cannot read the owner of %s: %w",
			ErrInsecureKeyFile, subject, err)
	}
	if owner == nil {
		return fmt.Errorf("%w: %s has no owner recorded", ErrInsecureKeyFile, subject)
	}
	if !containsSID(trusted, owner) {
		return fmt.Errorf(
			"%w: %s is owned by %s, which can rewrite its permissions at any time; "+
				"treat the key as compromised",
			ErrInsecureKeyFile, subject, owner.String())
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("%w: cannot read the access control list of %s: %w",
			ErrInsecureKeyFile, subject, err)
	}
	if dacl == nil {
		// A NULL DACL grants everyone full access. It is the worst possible
		// state and is easy to mistake for "no entries", so it gets its own
		// message.
		return fmt.Errorf("%w: %s has no access control list at all, which grants every account full access",
			ErrInsecureKeyFile, subject)
	}

	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%w: cannot read entry %d of the access control list of %s: %w",
				ErrInsecureKeyFile, i, subject, err)
		}

		switch dispositionOf(ace.Header.AceType) {
		case aceDenies:
			continue

		case aceGrants:
			// Both store the SID at SidStart. A callback entry grants
			// conditionally, on a condition this package does not evaluate;
			// treating it as an unconditional grant is the conservative
			// reading, and an untrusted principal is refused either way.
			//
			// The SID is stored inline at the end of the ACE, which is why the
			// Win32 header declares SidStart as its first DWORD. This pointer
			// conversion is the documented way to reach it and there is no
			// safe alternative in x/sys/windows.
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !containsSID(trusted, sid) {
				return fmt.Errorf(
					"%w: %s grants access to %s; treat the key as compromised, "+
						"then remove that entry or delete the file and re-enrol",
					ErrInsecureKeyFile, subject, sid.String())
			}

		default:
			// An earlier version of this loop skipped everything that was not a
			// plain allow entry, on the assumption that the rest denied access,
			// which quietly waved through object and compound *allow* entries.
			//
			// Failing closed here costs a deployment that uses conditional or
			// object ACEs an explicit error telling it what happened. Failing
			// open costs it the key.
			//
			// Found in review.
			return fmt.Errorf(
				"%w: entry %d of the access control list of %s is of type %#x, "+
					"which this check cannot evaluate; refusing rather than assuming "+
					"it denies access. Reset the permissions so only the account "+
					"running the daemon is granted access",
				ErrInsecureKeyFile, i, subject, ace.Header.AceType)
		}
	}
	return nil
}

// trustedSIDs is allowedSIDs behind a variable so that a test can narrow the
// trusted set and watch the refusal paths fire.
//
// Without it the owner check is untestable on a single-account machine: to see
// it refuse, the key file would have to be owned by an account the test cannot
// create, and a security check nobody has watched refuse is indistinguishable
// from one that always returns nil.
var trustedSIDs = allowedSIDs

// aceDisposition says what this package is able to conclude from an ACE type.
//
// It is a separate function so the mapping can be tested directly. The
// mapping is the part that was wrong: the first version treated every type
// other than a plain allow entry as a denial, which waved through object and
// compound *allow* entries.
type aceDisposition int

const (
	// aceGrants is an allow entry storing its SID at SidStart, so the SID can
	// be read and checked.
	aceGrants aceDisposition = iota
	// aceDenies only ever reduces access, so it cannot make an object more
	// reachable than the allow entries already do.
	aceDenies
	// aceUnknown is anything this package cannot read confidently — object and
	// compound layouts put other fields where the SID would be, and an
	// unrecognised type could be anything. These fail closed.
	aceUnknown
)

func dispositionOf(aceType byte) aceDisposition {
	switch aceType {
	case aceAccessAllowed, aceAccessAllowedCallback:
		return aceGrants
	case aceAccessDenied, aceAccessDeniedObject,
		aceAccessDeniedCallback, aceAccessDeniedCallbackObject:
		return aceDenies
	default:
		return aceUnknown
	}
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("determine the account this process runs as: %w", err)
	}
	return user.User.Sid, nil
}

// allowedSIDs is the set that may own, or appear in the DACL of, the key file.
func allowedSIDs() ([]*windows.SID, error) {
	self, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	sids := []*windows.SID{self}

	// These two can take ownership of any object anyway. A failure to look one
	// up is not fatal: it only narrows what is accepted, which errs towards
	// refusing rather than towards trusting.
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
