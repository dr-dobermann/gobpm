package activities

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// TestBusinessRuleTaskCloneError covers Clone's error branch (white-box): a
// zero-value Property has no value, so the property deep-copy inside
// task.clone fails before anything else runs (the snapshot forge precedent).
func TestBusinessRuleTaskCloneError(t *testing.T) {
	bt := &BusinessRuleTask{
		decisionRef: "d",
		task: task{
			activity: activity{
				properties: map[string]*data.Property{"bad": {}},
			},
		},
	}

	_, err := bt.Clone()
	require.Error(t, err)
}
