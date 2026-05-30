package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyBase inherits cloud sources when the override omits them, mirroring
// linked_repos: a nil slice means "field absent → take base"; an
// empty-but-non-nil slice is a deliberate clear.
func TestApplyBase_InheritsCloudWhenOverrideOmits(t *testing.T) {
	t.Parallel()
	override := &Profile{
		Base: "default",
		Name: "child",
	}
	// default ships no cloud sources, so prime the resolved base in memory
	// via a direct merge against a hand-built base to exercise the field.
	base := &Profile{
		Cloud: []CloudSource{{Alias: "base-gcp", Provider: "gcp", AssumedIdentity: "ro@base.iam.gserviceaccount.com"}},
	}
	mergeCloud(override, base)
	require.Len(t, override.Cloud, 1)
	assert.Equal(t, "base-gcp", override.Cloud[0].Alias)
}

func TestApplyBase_OverrideCloudWins(t *testing.T) {
	t.Parallel()
	override := &Profile{
		Cloud: []CloudSource{{Alias: "child-aws", Provider: "aws", AssumedIdentity: "arn:aws:iam::1:role/ro"}},
	}
	base := &Profile{
		Cloud: []CloudSource{{Alias: "base-gcp", Provider: "gcp"}},
	}
	mergeCloud(override, base)
	require.Len(t, override.Cloud, 1)
	assert.Equal(t, "child-aws", override.Cloud[0].Alias, "override cloud must win over base")
}
