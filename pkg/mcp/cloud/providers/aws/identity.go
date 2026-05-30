package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// EnvExpectedRoleARN optionally pins the IAM role ARN the assumed-role caller
// must resolve to. When set, Identity rejects any caller whose underlying role
// does not match it, the strict check. When unset, Identity falls back to the
// structural check (the caller must be an assumed-role ARN at all, proving the
// AWS_PROFILE assume-role pin took effect rather than the operator's plain base
// identity leaking through).
const EnvExpectedRoleARN = "TRIAGENT_CLOUD_AWS_EXPECTED_ROLE_ARN"

// callerIdentity is the projection of `aws sts get-caller-identity --output
// json`. Only the fields the probe and inventory fallback use are decoded.
type callerIdentity struct {
	UserID  string `json:"UserId"`
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
}

// Identity is the read-only whoami over the assumed role. It runs `aws sts
// get-caller-identity` through the injected run core (unvalidated under Probe;
// the command is also allowlisted so it works under the validated core), parses
// the caller ARN, and reports whether the pinned assume-role identity is active.
//
// Validity has two modes. With TRIAGENT_CLOUD_AWS_EXPECTED_ROLE_ARN set, the
// caller's underlying role must match it exactly. Without it, the structural
// check applies: the caller must be an assumed-role ARN, which proves the
// AWS_PROFILE pin took effect — a plain user/root ARN means base credentials
// leaked through unimpersonated, so the session is not valid.
func (p *Provider) Identity(ctx context.Context, run cloud.RunFunc) (cloud.IdentityStatus, error) {
	res, err := run(ctx, []string{"sts", "get-caller-identity", "--output", "json"})
	if err != nil {
		return cloud.IdentityStatus{Provider: "aws", Valid: false, Hint: err.Error()}, nil
	}
	if res.ExitCode != 0 {
		return cloud.IdentityStatus{
			Provider: "aws",
			Valid:    false,
			Hint:     "aws sts get-caller-identity failed; re-authenticate your base credentials (e.g. aws sso login)",
		}, nil
	}

	var caller callerIdentity
	if err := json.Unmarshal([]byte(res.Stdout), &caller); err != nil {
		return cloud.IdentityStatus{
			Provider: "aws",
			Valid:    false,
			Hint:     fmt.Sprintf("parse caller identity: %v", err),
		}, nil
	}

	st := cloud.IdentityStatus{Provider: "aws", AssumedIdentity: caller.Arn}
	st.Valid, st.Hint = evaluateIdentity(caller.Arn, os.Getenv(EnvExpectedRoleARN))
	return st, nil
}

// evaluateIdentity decides whether a resolved caller ARN represents the pinned
// read-only assume-role identity. It returns validity plus a hint explaining a
// degrade.
func evaluateIdentity(arn, expectedRoleARN string) (bool, string) {
	role, ok := assumedRoleARN(arn)
	if !ok {
		return false, "active identity is not an assumed role; the AWS_PROFILE assume-role pin did not take effect — re-authenticate your base credentials (e.g. aws sso login)"
	}
	if expectedRoleARN != "" && role != expectedRoleARN {
		return false, fmt.Sprintf("assumed role %q does not match the pinned read-only role %q", role, expectedRoleARN)
	}
	return true, ""
}

// assumedRoleARN reports whether arn is an STS assumed-role ARN and, if so,
// returns the canonical IAM role ARN behind it. An assumed-role ARN has the
// shape arn:aws:sts::<account>:assumed-role/<role-name>/<session>; the IAM role
// it stands for is arn:aws:iam::<account>:role/<role-name>.
func assumedRoleARN(arn string) (string, bool) {
	const prefix = "arn:aws:sts::"
	const marker = ":assumed-role/"
	if !strings.HasPrefix(arn, prefix) {
		return "", false
	}
	idx := strings.Index(arn, marker)
	if idx < 0 {
		return "", false
	}
	account := arn[len(prefix):idx]
	rest := arn[idx+len(marker):]
	roleName, _, found := strings.Cut(rest, "/")
	if !found || roleName == "" || account == "" {
		return "", false
	}
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", account, roleName), true
}
