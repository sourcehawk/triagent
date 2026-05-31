//go:build !windows

package aws

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockExclusive takes an exclusive advisory lock on f, blocking until it is
// granted. Paired with unlockFile, it serializes the managed-profile
// read-modify-write across processes (the launcher probe and each serve
// subprocess all generate into the same ~/.aws/config).
func lockExclusive(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }

// unlockFile releases the advisory lock held on f.
func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
