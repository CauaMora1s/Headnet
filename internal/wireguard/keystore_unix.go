//go:build !windows

package wireguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// File modes for the key and the directory holding it.
//
// 0600 and 0700 are what docs/security/key-management.md promises. The
// directory matters as much as the file: a directory others can write to lets
// them rename the key away and drop in one they chose, which the file's own
// mode does nothing to prevent.
const (
	keyFileMode  os.FileMode = 0o600
	stateDirMode os.FileMode = 0o700
)

// createStateDir creates the directory tree with restrictive permissions.
//
// MkdirAll applies the umask, so the mode is set explicitly afterwards on the
// final directory. A umask of 0022 would otherwise leave a 0755 directory,
// which is the default on most systems and would silently weaken this.
func createStateDir(dir string) error {
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return err
	}
	return os.Chmod(dir, stateDirMode)
}

// createPrivateFile opens the key file for writing, failing if it exists.
//
// The mode is passed to open rather than applied afterwards so the file is
// never, even briefly, readable by anyone else. A create-then-chmod sequence
// has a window in which another process can open it.
//
// O_NOFOLLOW is here as well as on the read path: without it, an attacker who
// can create the key file first, as a symlink, redirects the write.
func createPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, keyFileMode)
}

// restrictKeyFile re-applies the mode.
//
// Redundant after createPrivateFile in the normal case, and deliberately so:
// the umask is subtractive and cannot loosen a mode, but a filesystem mounted
// with unusual options can, and this is where that would be caught.
func restrictKeyFile(path string) error {
	if err := os.Chmod(path, keyFileMode); err != nil {
		return fmt.Errorf("restrict permissions on %s: %w", path, err)
	}
	return nil
}

// openKeyFile opens the key file for reading and refuses if anything about it,
// or about the directory it sits in, would let another account choose its
// contents.
//
// The order matters and so does the fact that it returns the open file. An
// earlier version validated the path with os.Stat and then called os.ReadFile,
// which reopens by path: two different objects can answer the same path if
// something changes in between, so the file that was checked was not
// necessarily the file that was read. Everything below is checked against the
// handle that is returned, so there is nothing left to swap.
//
// Found in review.
func openKeyFile(path string) (*os.File, error) {
	if err := verifyStateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}

	// O_NOFOLLOW makes this fail rather than follow a symlink planted in place
	// of the key file. Without it, the ownership check below would be applied
	// to whatever the link pointed at.
	// O_NONBLOCK matters for the same reason the regular-file check below
	// does: opening a fifo without it blocks until a writer appears, so a fifo
	// planted at the key path would hang the daemon's start rather than being
	// rejected. On a regular file it does nothing.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf(
				"%w: %s is a symbolic link, so its contents are chosen by whoever "+
					"controls the link target; remove it and re-enrol",
				ErrInsecureKeyFile, path)
		}
		return nil, err
	}

	if err := verifyOpenKeyFile(f, path); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// verifyOpenKeyFile checks the already-open file. Everything here comes from
// fstat on the descriptor, not from the path.
func verifyOpenKeyFile(f *os.File, path string) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: cannot inspect %s: %w", ErrInsecureKeyFile, path, err)
	}

	if !info.Mode().IsRegular() {
		// A fifo or device node in place of the key file turns a read into
		// something the attacker controls the timing and contents of.
		return fmt.Errorf("%w: %s is not a regular file", ErrInsecureKeyFile, path)
	}
	if mode := info.Mode(); mode&0o077 != 0 {
		return fmt.Errorf(
			"%w: %s has mode %#o, which lets other accounts read it; "+
				"treat the key as compromised, then run: chmod 600 %s",
			ErrInsecureKeyFile, path, mode.Perm(), path)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Not reachable on any platform this file builds for. Refusing beats
		// assuming, because the alternative is silently skipping the check.
		return fmt.Errorf("%w: cannot determine the owner of %s on this platform",
			ErrInsecureKeyFile, path)
	}
	if uid := os.Getuid(); int(stat.Uid) != uid {
		return fmt.Errorf(
			"%w: %s is owned by uid %d but this process runs as uid %d, "+
				"so another account can replace the key; treat it as compromised",
			ErrInsecureKeyFile, path, stat.Uid, uid)
	}
	return nil
}

// verifyStateDir refuses a directory another account could use to substitute
// the key file.
//
// Only the write bits are checked, not the read bits. Being able to *list* the
// directory reveals that a key exists, which an attacker on the machine can
// infer anyway; being able to *write* to it means renaming the real key away
// and putting an attacker-chosen one in its place, which defeats every check
// on the file itself. The sticky bit is not treated as sufficient: it prevents
// deleting another user's file but not creating one where none exists.
func verifyStateDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		// A missing directory means no key yet, and Load turns that into
		// ErrNoKey. Anything else is passed through as-is.
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrInsecureKeyFile, dir)
	}
	if mode := info.Mode(); mode&0o022 != 0 {
		return fmt.Errorf(
			"%w: %s has mode %#o, so other accounts can replace the key file "+
				"inside it whatever the file's own permissions say; "+
				"treat the key as compromised, then run: chmod 700 %s",
			ErrInsecureKeyFile, dir, mode.Perm(), dir)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: cannot determine the owner of %s on this platform",
			ErrInsecureKeyFile, dir)
	}
	if uid := os.Getuid(); int(stat.Uid) != uid {
		return fmt.Errorf(
			"%w: %s is owned by uid %d but this process runs as uid %d, "+
				"so another account can replace the key file inside it",
			ErrInsecureKeyFile, dir, stat.Uid, uid)
	}
	return nil
}
