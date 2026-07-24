package activities

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// TestServiceTaskCommitErr covers FIX-026: the commit-failure classifier
// carries the task identity.
func TestServiceTaskCommitErr(t *testing.T) {
	op, err := service.NewOperation("op", nil, nil, nil)
	require.NoError(t, err)

	st, err := NewServiceTask("svc", op, WithoutParams())
	require.NoError(t, err)

	e := st.commitErr("commit operation result", errs.New(errs.M("inner")))
	require.Contains(t, e.Error(), "svc")
	require.Contains(t, e.Error(), "commit operation result")
}
