package waiters

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestPayloadErr covers FIX-026: the payload-failure classifier carries the
// message name (the datum build itself is data.ReadyValueParameter, tested
// in pkg/model/data).
func TestPayloadErr(t *testing.T) {
	err := payloadErr("order placed", errs.New(errs.M("inner")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "order placed")
	require.Contains(t, err.Error(), "payload datum")
}
