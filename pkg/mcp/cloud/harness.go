package cloud

import (
	"context"
	"errors"
	"os/exec"
)

// defaultOutputLimit caps run_cli stdout so a raw provider response cannot blow
// the agent's context budget. Output beyond it is dropped and flagged.
const defaultOutputLimit = 64 * 1024

// limitedWriter retains at most limit bytes of everything written to it and
// records whether any write pushed it past that cap. It never grows past limit,
// so a command emitting an arbitrarily large response cannot consume unbounded
// memory: bytes past the cap are counted for the overflow flag and discarded.
type limitedWriter struct {
	buf      []byte
	limit    int
	overflow bool
}

// Write retains up to the remaining capacity in the buffer and discards the
// rest, flagging overflow whenever a write carries more bytes than the buffer
// can still hold. It always reports the full length written so the child
// process is never blocked on a short write.
func (w *limitedWriter) Write(p []byte) (int, error) {
	room := w.limit - len(w.buf)
	if len(p) > room {
		w.overflow = true
	}
	if room > 0 {
		take := len(p)
		if take > room {
			take = room
		}
		w.buf = append(w.buf, p[:take]...)
	}
	return len(p), nil
}

// execCLI runs binPath with argv via execve — no shell, ever. The argv tokens
// reach the binary as literal arguments, so shell metacharacters are inert. The
// subprocess runs with exactly the supplied env (never the parent environment,
// so a poisoned PATH cannot redirect the binary and ambient secrets do not
// leak), closed stdin (no interactive prompt), and stdout/stderr captured
// through bounded writers that retain at most limit bytes each — the cap is
// effective during the run, so a command emitting a very large response can
// never buffer it all in memory. A non-zero exit is a normal result carried in
// ExitCode, not a Go error; a Go error means the process could not be run at
// all. Stderr — where gcloud/aws write their error context — is captured
// alongside stdout and capped at the same limit, so a non-zero exit carries an
// explanation instead of an empty result.
func execCLI(ctx context.Context, binPath string, argv []string, env []string, limit int) (CLIResult, error) {
	cmd := exec.CommandContext(ctx, binPath, argv...)
	cmd.Env = env
	cmd.Stdin = nil

	stdout := &limitedWriter{limit: limit}
	stderr := &limitedWriter{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()

	res := CLIResult{
		Stdout:    string(stdout.buf),
		Stderr:    string(stderr.buf),
		Truncated: stdout.overflow || stderr.overflow,
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return CLIResult{}, err
	}
	return res, nil
}
