package errs_test

import (
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

func TestPanic(t *testing.T) {
	t.Run("settings",
		func(t *testing.T) {
			require.False(t, errs.DontPanic())
			require.Panics(t, func() { errs.Panic("should panic") })

			errs.SetDontPanic(true)
			require.True(t, errs.DontPanic())
			require.NotPanics(t, func() { errs.Panic("don't panic") })
		})

	t.Run("errors",
		func(t *testing.T) {

			detalils := map[string]string{
				"hello": "world",
				"test":  "detail"}

			classes := []string{
				"test",
				"classes",
				"   "}

			e := errs.New(
				errs.E(fmt.Errorf("test err")),
				errs.M("hello %s!", "world"),
				errs.C(classes...),
				errs.D(detalils))

			require.NotEmpty(t, e)
			require.Equal(t, "hello world!", e.Message)
			require.Equal(t, detalils, e.Details)
			require.Equal(t, classes[:2], e.Classes)
			require.Error(t, e.Err)

			t.Log(e.Error())
		})
}
