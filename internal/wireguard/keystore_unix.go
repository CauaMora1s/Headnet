//go:build !windows

package wireguard

import (
	"fmt"
	"os"
	"syscall"
)

// File modes for the key and the directory holding it.
//
// 0600 and 0700 are what docs/security/key-management.md promises. The
// directory matters as much as the file: a directory others can write to lets
// them replace the key file outright, whatever its own mode says.
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
func createPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyFileMode)
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

// verifyKeyFilePermissions refuses a key file that anyone but its owner can
// read.
//
// This runs on every load, not only after a write, which is the point: a key
// file is protected when it is created, and then lives on a real machine
// where a backup restore, a careless chmod -R or an rsync without -p can
// widen it months later. The daemon must notice.
func verifyKeyFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		// Wrapped so the caller can distinguish "no key yet" from "cannot
		// tell", which are very different situations.
		return err
	}

	if mode := info.Mode(); mode&0o077 != 0 {
		return fmt.Errorf(
			"%w: %s has mode %#o, which lets other accounts read it; "+
				"treat the key as compromised, then run: chmod 600 %s",
			ErrInsecureKeyFile, path, mode.Perm(), path)
	}

	// Ownership matters independently of the mode. A file owned by another
	// account is a file that account can rewrite, so a key read from it is a
	// key an attacker may have chosen.
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
