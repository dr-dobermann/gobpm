package data_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPropertyCloneIsDeepCopy covers FIX-016 3.2.1: Property.Clone returns a
// distinct object (its own ItemAwareElement) under the same name, so mutating
// the clone doesn't touch the source.
func TestPropertyCloneIsDeepCopy(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p := data.MustProperty("counter",
		data.MustItemDefinition(values.NewVariable(1),
			foundation.WithID("counter")),
		data.ReadyDataState)

	clone, err := p.Clone()
	require.NoError(t, err)

	require.NotSame(t, p, clone)
	require.Equal(t, p.Name(), clone.Name())

	ctx := context.Background()
	require.NoError(t, clone.Value().Update(ctx, 42))
	require.Equal(t, 1, p.Value().Get(ctx),
		"mutating the clone must not affect the source")
	require.Equal(t, 42, clone.Value().Get(ctx))

	// a zero-value Property (no ItemDefinition) can't be cloned.
	var empty data.Property
	_, err = empty.Clone()
	require.Error(t, err)
}

type invldPA struct{}

func (ipa *invldPA) Validate() error {
	return nil
}

func TestProperty(t *testing.T) {
	t.Run("errors",
		func(t *testing.T) {
			// empty name
			_, err := data.NewProperty("", nil, nil)
			require.Error(t, err)
			require.Panics(t,
				func() {
					_ = data.MustProperty("", nil, nil)
				})

			_, err = data.NewProp("", nil)
			require.Error(t, err)

			// name carrying the reserved path separator
			_, err = data.NewProperty("a/b", nil, data.ReadyDataState)
			require.Error(t, err)

			// empty parameters
			_, err = data.NewProperty("empty item", nil, data.ReadyDataState)
			require.Error(t, err)

			_, err = data.NewProp("empty iae", nil)
			require.Error(t, err)

			// invalid option
			_, err = data.NewProperty(
				"invalid option",
				data.MustItemDefinition(nil),
				data.UnavailableDataState,
				options.WithName("extra name"))
			require.Error(t, err)

			// invalid params
			mpac := mockdata.NewMockPropertyAdder(t)
			po := data.WithProperty("", nil)
			require.Error(t, po.Apply(mpac))
			po = data.WithProperty("no iae", nil)
			require.Error(t, po.Apply(mpac))

			var ipac invldPA
			require.Error(t, po.Apply(&ipac))
		})

	t.Run("normal",
		func(t *testing.T) {
			var pNames []string

			mpac := mockdata.NewMockPropertyAdder(t)
			mpac.EXPECT().AddProperty(mock.Anything).
				RunAndReturn(
					func(p *data.Property) error {
						t.Log("  ->> mock PropertyAdder adds a new Property: ",
							p.Name())
						pNames = append(pNames, p.Name())
						return nil
					})

			pa := data.WithProperties(
				data.MustProp("name",
					data.WithIAE(
						data.WithIDefinition(values.NewVariable("John Doe")),
						data.WithState(data.ReadyDataState))),
				data.MustProperty("age",
					data.MustItemDefinition(values.NewVariable(52)),
					data.ReadyDataState),
			)

			err := pa.Apply(mpac)
			require.NoError(t, err)
			require.Contains(t, pNames, "name")
			require.Contains(t, pNames, "age")

			pNames = []string{}
			pa = data.WithProperty("sex", data.WithIAE(
				data.WithIDefinition(values.NewVariable("male"))))

			err = pa.Apply(mpac)
			require.NoError(t, err)
			require.Contains(t, pNames, "sex")
		})
}
