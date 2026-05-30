package aws

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const callerIdentityAssumedRole = `{
  "UserId": "AROAEXAMPLE:triagent-session",
  "Account": "111122223333",
  "Arn": "arn:aws:sts::111122223333:assumed-role/triagent-readonly/triagent-session"
}`

const callerIdentityPlainUser = `{
  "UserId": "AIDAEXAMPLE",
  "Account": "111122223333",
  "Arn": "arn:aws:iam::111122223333:user/operator"
}`

func TestIdentityBuildsCallerIdentityArgv(t *testing.T) {
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"sts get-caller-identity": {Stdout: callerIdentityAssumedRole},
	}}
	p, err := New()
	require.NoError(t, err)

	_, err = p.Identity(context.Background(), f.run)
	require.NoError(t, err)

	require.Len(t, f.calls, 1)
	assert.Equal(t, []string{"sts", "get-caller-identity", "--output", "json"}, f.calls[0])
}

func TestIdentityValidWhenAssumedRole(t *testing.T) {
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"sts get-caller-identity": {Stdout: callerIdentityAssumedRole},
	}}
	p, err := New()
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), f.run)
	require.NoError(t, err)

	assert.Equal(t, "aws", st.Provider)
	assert.Equal(t, "arn:aws:sts::111122223333:assumed-role/triagent-readonly/triagent-session", st.AssumedIdentity)
	assert.True(t, st.Valid, "an assumed-role ARN proves the pinned profile took effect")
}

func TestIdentityInvalidWhenNotAssumedRole(t *testing.T) {
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"sts get-caller-identity": {Stdout: callerIdentityPlainUser},
	}}
	p, err := New()
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), f.run)
	require.NoError(t, err)

	assert.Equal(t, "arn:aws:iam::111122223333:user/operator", st.AssumedIdentity)
	assert.False(t, st.Valid, "a plain user ARN means the assume-role pin did not take effect")
	assert.NotEmpty(t, st.Hint)
}

func TestIdentityMatchesExpectedRoleArnWhenPinned(t *testing.T) {
	t.Setenv(envExpectedRoleARN, "arn:aws:iam::111122223333:role/triagent-readonly")
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"sts get-caller-identity": {Stdout: callerIdentityAssumedRole},
	}}
	p, err := New()
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), f.run)
	require.NoError(t, err)
	assert.True(t, st.Valid, "assumed-role ARN whose role matches the pinned expectation is valid")
}

func TestIdentityRejectsMismatchedExpectedRoleArn(t *testing.T) {
	t.Setenv(envExpectedRoleARN, "arn:aws:iam::111122223333:role/some-other-role")
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"sts get-caller-identity": {Stdout: callerIdentityAssumedRole},
	}}
	p, err := New()
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), f.run)
	require.NoError(t, err)
	assert.False(t, st.Valid, "assumed role not matching the pinned expectation is invalid")
	assert.NotEmpty(t, st.Hint)
}

func TestIdentityInvalidOnNonZeroExit(t *testing.T) {
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"sts get-caller-identity": {ExitCode: 255, Stdout: ""},
	}}
	p, err := New()
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), f.run)
	require.NoError(t, err)
	assert.False(t, st.Valid)
	assert.NotEmpty(t, st.Hint)
}
