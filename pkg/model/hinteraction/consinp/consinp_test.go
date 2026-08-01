package consinp_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestCRender(t *testing.T) {
	data.CreateDefaultStates()

	ctx := context.Background()

	t.Run("invalid parameters",
		func(t *testing.T) {
			// empty input list
			_, err := consinp.NewRenderer()
			require.Error(t, err)

			// invalid option
			_, err = consinp.NewRenderer(
				consinp.WithIntInput("test", ""),
				options.WithName("extra_name"))
			require.Error(t, err)

			_, err = consinp.NewRenderer(
				consinp.WithSource(nil))
			require.Error(t, err)

			// duplicate option
			_, err = consinp.NewRenderer(
				consinp.WithIntInput("int_option", ""),
				consinp.WithIntInput("int_option", ""))
			require.Error(t, err)

			// invalid input
			_, err = consinp.NewRenderer(
				consinp.WithStringInput("", "say something"))
			require.Error(t, err)

			_, err = consinp.NewRenderer(
				consinp.WithIntInput("", "say something"))
			require.Error(t, err)

			_, err = consinp.NewRenderer(
				consinp.WithMessager("", ""))
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			r, err := consinp.NewRenderer(
				foundation.WithID("my console renderer"),
				consinp.WithSource(
					bytes.NewBufferString("dr.Dobermann\n52\n")),
				consinp.WithMessager("wellcomer", "This is a wellcome message"),
				consinp.WithStringInput("name", "Enter your name: "),
				consinp.WithIntInput("age", "Enter your age: "))
			require.NoError(t, err)

			dd, err := r.Render(nil)
			require.NoError(t, err)
			require.Len(t, dd, 2)
			require.Equal(t, "name", dd[0].Name())
			require.Equal(t, "dr.Dobermann", dd[0].Value().Get(ctx).(string))
			require.Equal(t, "age", dd[1].Name())
			require.Equal(t, 52, dd[1].Value().Get(ctx).(int))

			rr, err := consinp.NewRenderer(
				consinp.WithIntInput("couldn't read", ""))
			require.NoError(t, err)

			_, err = rr.Render(nil)
			require.Error(t, err)
		})
}

// TestDuplicateStringInputRejected: WithStringInput's duplicate-name guard had
// no test, while WithIntInput's did — the two options carry separate checks, so
// covering one says nothing about the other. A duplicate prompt name would
// otherwise silently shadow the first input at render time.
func TestDuplicateStringInputRejected(t *testing.T) {
	_, err := consinp.NewRenderer(
		consinp.WithStringInput("who", "Enter your name: "),
		consinp.WithStringInput("who", "Enter it again: "))

	require.Error(t, err)
	require.ErrorContains(t, err, "duplicate input name")
}
