package server

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// runCmdWithETXTBSYRetry calls fn (which must Run a freshly-built
// *exec.Cmd) with up to a few short retries on ETXTBSY ("text file
// busy"). The error surfaces transiently when a sibling goroutine
// forked while holding an open-for-write FD on the spawned binary —
// child forks inherit FDs, and O_CLOEXEC only takes effect at exec
// time, so the kernel blocks exec of the binary until the sibling's
// exec closes the inherited FD. Each retry is given a fresh Cmd via
// fn so callers don't have to reason about exec.Cmd's post-failure
// pipe state.
//
// Production realistically never hits this — the binaries we spawn
// (triagent-mcp, gh, git, claude) aren't being written concurrently.
// It surfaces under parallel test load that stubs binaries via
// os.WriteFile + exec; the retry covers that without forcing tests
// to drop t.Parallel.
func runCmdWithETXTBSYRetry(fn func() *exec.Cmd) error {
	const maxAttempts = 5
	backoff := 10 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := fn().Run()
		if err == nil || !errors.Is(err, syscall.ETXTBSY) || attempt >= maxAttempts {
			return err
		}
		time.Sleep(backoff)
		backoff *= 2
	}
}
