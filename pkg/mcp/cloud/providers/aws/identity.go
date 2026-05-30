package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

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
// Validity has two modes. With expected set to a role ARN, the caller's
// underlying role must match it exactly. Without it, the structural check
// applies: the caller must be an assumed-role ARN, which proves the AWS_PROFILE
// pin took effect — a plain user/root ARN means base credentials leaked through
// unimpersonated, so the session is not valid.
func (p *Provider) Identity(ctx context.Context, run cloud.RunFunc, expected string) (cloud.IdentityStatus, error) {
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
	st.Valid, st.Hint = evaluateIdentity(caller.Arn, expected)
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
// shape arn:<partition>:sts::<account>:assumed-role/<role-path-and-name>/<session>,
// across the aws, aws-us-gov, and aws-cn partitions. The role keeps any IAM
// path: the role-path-and-name is everything between "assumed-role/" and the
// final "/<session>" segment, so a path-prefixed role like
// assumed-role/team/sub/Role/session resolves to role/team/sub/Role. The IAM
// role it stands for is arn:<partition>:iam::<account>:role/<role-path-and-name>.
func assumedRoleARN(arn string) (string, bool) {
	const stsInfix = ":sts::"
	const marker = ":assumed-role/"
	partition, ok := arnPartition(arn)
	if !ok {
		return "", false
	}
	if !strings.HasPrefix(arn, "arn:"+partition+stsInfix) {
		return "", false
	}
	idx := strings.Index(arn, marker)
	if idx < 0 {
		return "", false
	}
	account := arn[len("arn:"+partition+stsInfix):idx]
	rest := arn[idx+len(marker):]
	// The session name is the final slash-delimited segment; the role path and
	// name is everything before it.
	rolePath, _, found := lastCut(rest, "/")
	if !found || rolePath == "" || account == "" {
		return "", false
	}
	return fmt.Sprintf("arn:%s:iam::%s:role/%s", partition, account, rolePath), true
}

// arnPartition returns the partition segment of an ARN (the field between the
// first two colons of "arn:<partition>:...").
func arnPartition(arn string) (string, bool) {
	rest, ok := strings.CutPrefix(arn, "arn:")
	if !ok {
		return "", false
	}
	partition, _, found := strings.Cut(rest, ":")
	if !found || partition == "" {
		return "", false
	}
	return partition, true
}

// lastCut splits s around the last instance of sep, returning the text before
// and after it. found reports whether sep appears in s.
func lastCut(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
