package errs_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

// TestCheckStr covers both branches of CheckStr plus the class trimming: a
// non-empty string passes, an empty one fails with the message and only the
// non-blank classes.
func TestCheckStr(t *testing.T) {
	require.NoError(t, errs.CheckStr("non-empty", "should not fail"))

	err := errs.CheckStr("", "value is required", "MY_CLASS", "  ", "")
	require.Error(t, err)

	var ae *errs.ApplicationError
	require.ErrorAs(t, err, &ae)
	require.Equal(t, "value is required", ae.Message)
	require.Equal(t, []string{"MY_CLASS"}, ae.Classes)
}

// TestHasClass covers the present and absent branches of HasClass.
func TestHasClass(t *testing.T) {
	ae := errs.New(errs.M("boom"), errs.C("ALPHA", "BETA"))

	require.True(t, ae.HasClass("ALPHA"))
	require.True(t, ae.HasClass("BETA"))
	require.False(t, ae.HasClass("GAMMA"))
}

// TestHasPanicHandler covers both states of HasPanicHandler.
func TestHasPanicHandler(t *testing.T) {
	errs.DropPanicHandler()
	require.False(t, errs.HasPanicHandler())

	require.NoError(t, errs.RegisterPanicHandler(func(any) bool { return false }))
	require.True(t, errs.HasPanicHandler())

	errs.DropPanicHandler()
	require.False(t, errs.HasPanicHandler())
}

// TestUnwrapNil covers ApplicationError.Unwrap when no error is wrapped.
func TestUnwrapNil(t *testing.T) {
	require.Nil(t, errs.New(errs.M("no wrapped error")).Unwrap())
}
