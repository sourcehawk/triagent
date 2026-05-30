package cloud

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// defaultOutputLimit caps run_cli stdout so a raw provider response cannot blow
// the agent's context budget. Output beyond it is dropped and flagged.
const defaultOutputLimit = 64 * 1024

// execCLI runs binPath with argv via execve — no shell, ever. The argv tokens
// reach the binary as literal arguments, so shell metacharacters are inert. The
// subprocess runs with exactly the supplied env (never the parent environment,
// so a poisoned PATH cannot redirect the binary and ambient secrets do not
// leak), closed stdin (no interactive prompt), and stdout capped at limit. A
// non-zero exit is a normal result carried in ExitCode, not a Go error; a Go
// error means the process could not be run at all. Stderr — where gcloud/aws
// write their error context — is captured alongside stdout and capped at the
// same limit, so a non-zero exit carries an explanation instead of an empty
// result.
func execCLI(ctx context.Context, binPath string, argv []string, env []string, limit int) (CLIResult, error) {
	cmd := exec.CommandContext(ctx, binPath, argv...)
	cmd.Env = env
	cmd.Stdin = nil

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	res := CLIResult{}
	out := stdout.Bytes()
	if len(out) > limit {
		out = out[:limit]
		res.Truncated = true
	}
	res.Stdout = string(out)

	errOut := stderr.Bytes()
	if len(errOut) > limit {
		errOut = errOut[:limit]
		res.Truncated = true
	}
	res.Stderr = string(errOut)

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
