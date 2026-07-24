package msgflow

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestErrBuilders covers FIX-026: the datum-failure classifiers carry the
// item id (the builds themselves are data.Ready*Parameter, tested in
// pkg/model/data).
func TestErrBuilders(t *testing.T) {
	err := deriveErr("order_in", errs.New(errs.M("inner")))
	require.Contains(t, err.Error(), "order_in")
	require.Contains(t, err.Error(), "DeriveKey")

	err = bindDatumErr("order_in", errs.New(errs.M("inner")))
	require.Contains(t, err.Error(), "order_in")
	require.Contains(t, err.Error(), "Bind")
}
