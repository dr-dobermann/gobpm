package tasks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestFaultDatumErr covers FIX-026: the fault-source failure classifier
// names the datum (the build itself is data.ReadyParameter /
// ReadyValueParameter, tested in pkg/model/data).
func TestFaultDatumErr(t *testing.T) {
	err := faultDatumErr("body", errs.New(errs.M("inner")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "body")
	require.Contains(t, err.Error(), "inner")
}
