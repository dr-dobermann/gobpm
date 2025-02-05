package errs_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

func TestJson(t *testing.T) {
	testJson := `{"error":"test error","message":"test message","classes":["INVALID_OBJECT","BUILDING_FAILED"],"details":{"name":"value"}}`

	ae := errs.New(
		errs.M("test message"),
		errs.C(errs.InvalidObject, errs.BulidingFailed),
		errs.E(fmt.Errorf("test error")),
		errs.D("name", "value"))

	js := string(ae.JSON())
	require.Equal(t, testJson, js)
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

func TestErrWrapping(t *testing.T) {
	testErr := errors.New("test error")

	myErr := errs.New(
		errs.M("application error wrap testing"),
		errs.C(errs.OperationFailed),
		errs.D("name", "value"),
		errs.E(testErr))

	var asTestErr *errs.ApplicationError
	require.True(t, errors.Is(myErr, testErr))
	require.True(t, errors.As(myErr, &asTestErr))
}
