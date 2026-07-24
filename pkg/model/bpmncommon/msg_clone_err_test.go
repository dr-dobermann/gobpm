package bpmncommon

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestMsgCloneErr covers FIX-026: the message-clone failure classifier
// carries the message name.
func TestMsgCloneErr(t *testing.T) {
	err := msgCloneErr("order placed", errs.New(errs.M("inner")))
	require.Contains(t, err.Error(), "order placed")
	require.Contains(t, err.Error(), "cloned message item")
}
