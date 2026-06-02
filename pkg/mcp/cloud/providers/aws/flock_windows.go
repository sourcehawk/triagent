//go:build windows

package aws

import "os"

// Windows has no flock. Same-process managed-profile writes are serialized by
// the package mutex; cross-process AWS-config generation is not a supported
// concurrency mode on Windows, so these are no-ops that keep the write path
// compiling and running.
func lockExclusive(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }
