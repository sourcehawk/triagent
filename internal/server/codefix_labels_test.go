package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// programmedGh routes each gh call by inspecting argv. Lets a test
// model the 200-OK / 404 / error paths of the label-existence probe
// independently of the create call.
type programmedCodefixGh struct {
	calls   [][]string
	respond func(args []string) (stdout []byte, stderr []byte, err error)
}

func (p *programmedCodefixGh) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	p.calls = append(p.calls, append([]string(nil), args...))
	return p.respond(args)
}

func TestEnsureCodefixLabelForRepo_NoOpWhenLabelPresent(t *testing.T) {
	t.Parallel()
	gh := &programmedCodefixGh{
		respond: func(args []string) ([]byte, []byte, error) {
			if len(args) >= 2 && args[0] == "api" && strings.HasPrefix(args[1], "repos/") &&
				strings.Contains(args[1], "/labels/") {
				return []byte(`{"name":"triagent-proposal"}`), nil, nil
			}
			return nil, nil, fmt.Errorf("unexpected call: %v", args)
		},
	}
	a := &apiHandlers{codefixGh: gh}
	require.NoError(t, a.ensureCodefixLabelForRepo(context.Background(), "example-org/example-service"))
	require.Len(t, gh.calls, 1, "no create call should fire when label present (200 OK)")
}

func TestEnsureCodefixLabelForRepo_CreatesWhenMissing404(t *testing.T) {
	t.Parallel()
	gh := &programmedCodefixGh{
		respond: func(args []string) ([]byte, []byte, error) {
			if len(args) >= 2 && args[0] == "api" {
				return nil, []byte("gh: HTTP 404: Not Found"), fmt.Errorf("exit 1")
			}
			if len(args) >= 2 && args[0] == "label" && args[1] == "create" {
				return []byte("created"), nil, nil
			}
			return nil, nil, fmt.Errorf("unexpected: %v", args)
		},
	}
	a := &apiHandlers{codefixGh: gh}
	require.NoError(t, a.ensureCodefixLabelForRepo(context.Background(), "example-org/example-service"))
	require.Len(t, gh.calls, 2, "expected probe (404) + create")
	probe := gh.calls[0]
	create := gh.calls[1]
	require.Equal(t, "api", probe[0])
	require.Contains(t, probe[1], "/labels/triagent-proposal")
	require.Equal(t, "label", create[0])
	require.Equal(t, "create", create[1])
	require.Equal(t, codefixLabelName, create[2])
	require.Contains(t, create, "--color")
	require.Contains(t, create, codefixLabelColor)
	require.Contains(t, create, "--description")
	require.Contains(t, create, codefixLabelDescription)
}

func TestEnsureCodefixLabelForRepo_ProbeAuthFailureBubblesUp(t *testing.T) {
	// Any probe error that isn't a 404 (e.g. 401/403 — missing
	// scope) should propagate so the operator sees what's wrong,
	// not silently get treated as "label missing" and trigger a
	// create that will then also fail.
	t.Parallel()
	gh := &programmedCodefixGh{
		respond: func(args []string) ([]byte, []byte, error) {
			return nil, []byte("gh: HTTP 403: Bad credentials"), fmt.Errorf("exit 4")
		},
	}
	a := &apiHandlers{codefixGh: gh}
	err := a.ensureCodefixLabelForRepo(context.Background(), "example-org/example-service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "labels probe")
	require.Len(t, gh.calls, 1, "should not attempt create after non-404 probe failure")
}

func TestEnsureCodefixLabelForRepo_CreateFailurePropagates(t *testing.T) {
	t.Parallel()
	gh := &programmedCodefixGh{
		respond: func(args []string) ([]byte, []byte, error) {
			if len(args) >= 2 && args[0] == "api" {
				return nil, []byte("404"), fmt.Errorf("exit 1")
			}
			if len(args) >= 2 && args[0] == "label" && args[1] == "create" {
				return nil, []byte("forbidden"), fmt.Errorf("exit 4")
			}
			return nil, nil, fmt.Errorf("unexpected")
		},
	}
	a := &apiHandlers{codefixGh: gh}
	err := a.ensureCodefixLabelForRepo(context.Background(), "example-org/example-service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "label create")
}

func TestEnsureCodefixLabelForRepo_NilRunner(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{} // codefixGh nil
	err := a.ensureCodefixLabelForRepo(context.Background(), "example-org/example-service")
	require.Error(t, err)
}
