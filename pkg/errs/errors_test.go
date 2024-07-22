package errs_test

import (
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

func TestPanic(t *testing.T) {
	require.Error(t, errs.RegisterPanicHandler(nil))

	handlerCreator := func(unhandled bool) errs.PanicHandler {
		return func(v any) bool {
			fmt.Println("panic handled: ", v)
			return unhandled
		}
	}

	require.NoError(t, errs.RegisterPanicHandler(handlerCreator(true)))
	require.Panics(t, func() { errs.Panic("panic unhandled") })

	errs.DropPanicHandler()

	require.NoError(t, errs.RegisterPanicHandler(handlerCreator(false)))
	require.NotPanics(t, func() { errs.Panic("panic handled") })
}

func TestDontPanic(t *testing.T) {
	errs.DropPanicHandler()

	require.False(t, errs.DontPanic())
	require.Panics(t, func() { errs.Panic("should panic") })

	errs.SetDontPanic(true)
	require.True(t, errs.DontPanic())
	require.NotPanics(t, func() { errs.Panic("don't panic") })
}

func TestErrors(t *testing.T) {
	t.Run("errors",
		func(t *testing.T) {
			detalils := []struct {
				k, v string
			}{
				{"hello", "world"},
				{"test", "detail"},
			}

			classes := []string{
				"test",
				"classes",
				"   ",
			}

			e := errs.New(
				errs.E(fmt.Errorf("test err")),
				errs.M("hello %s!", "world"),
				errs.C(classes...),
				errs.D(detalils[0].k, detalils[0].v),
				errs.D(detalils[1].k, detalils[1].v),
			)

			require.NotEmpty(t, e)
			require.Equal(t, "hello world!", e.Message)
			require.Equal(t, classes[:2], e.Classes)
			require.Error(t, e.Err)

			require.Equal(t, len(detalils), len(e.Details))
			for _, d := range detalils {
				found := false
				ed, ok := e.Details[d.k]
				if ok {
					if d.v == ed {
						found = true
					}
				}

				require.True(t,
					found,
					"value for key %q differs from %s (has %s)",
					d.k, d.v, ed)
			}

			t.Log(e.Error())
		})

	t.Run("invalid error options",
		func(t *testing.T) {
			const defaultMessage = "unspecified error"

			e := errs.New(
				nil,
				errs.E(nil),
				errs.M(""),
			)

			require.NotEmpty(t, e)
			require.NotNil(t, e.Err)
			require.Equal(t, defaultMessage, e.Message)
		})
}
