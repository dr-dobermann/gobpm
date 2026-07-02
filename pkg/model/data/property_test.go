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

// TestCloneProperties covers FIX-017 CloneProperties: a nil set clones to nil,
// valued properties are deep-copied (distinct objects, names preserved), and a
// value-less member makes the clone fail.
func TestCloneProperties(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	got, err := data.CloneProperties(nil)
	require.NoError(t, err)
	require.Nil(t, got)

	src := []*data.Property{
		data.MustProperty("a",
			data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState),
		data.MustProperty("b",
			data.MustItemDefinition(values.NewVariable(1)), data.ReadyDataState),
	}

	cloned, err := data.CloneProperties(src)
	require.NoError(t, err)
	require.Len(t, cloned, 2)
	require.NotSame(t, src[0], cloned[0])
	require.Equal(t, src[0].Name(), cloned[0].Name())

	// a value-less property can no longer be constructed (FIX-018); a bare
	// zero-value struct is the only remaining value-less source, and CloneProperties
	// still rejects it (the data-layer clone precondition).
	_, err = data.CloneProperties([]*data.Property{{}})
	require.Error(t, err)
}

// TestNewPropertyRejectsValueLess covers FIX-018 3.2.4: a value-less property
// (its item has no structure, Value()==nil) is rejected at construction —
// NewProperty / NewProp return an error, MustProperty / MustProp panic — while a
// valued property still constructs.
func TestNewPropertyRejectsValueLess(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := data.NewProperty("p",
		data.MustItemDefinition(nil), data.ReadyDataState)
	require.Error(t, err)

	_, err = data.NewProp("p",
		data.WithIAE(data.WithIDefinition(nil)))
	require.Error(t, err)

	require.Panics(t, func() {
		_ = data.MustProperty("p",
			data.MustItemDefinition(nil), data.ReadyDataState)
	})

	// a valued property still constructs.
	_, err = data.NewProperty("ok",
		data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState)
	require.NoError(t, err)
}
