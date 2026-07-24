package gorules_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
)

// stubReader is a minimal service.DataReader handing the decisions a single
// Ready datum for any lookup.
type stubReader struct {
	d data.Data
}

func (s stubReader) GetData(string) (data.Data, error) {
	if s.d == nil {
		return nil, errs.New(errs.M("no data"))
	}

	return s.d, nil
}

func (s stubReader) GetDataByID(string) (data.Data, error) {
	return s.GetData("")
}

func (stubReader) GetSources() []string { return nil }

func (stubReader) List(string) ([]string, error) { return nil, nil }

func TestMain(m *testing.M) {
	if err := data.CreateDefaultStates(); err != nil {
		panic(err)
	}

	m.Run()
}

func readyData(name string, v any) data.Data {
	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v)),
			data.ReadyDataState))
}

// doubler reads the single datum and returns twice its int value as "result".
func doubler(
	_ context.Context,
	r service.DataReader,
) (rules.Row, error) {
	d, err := r.GetData("in")
	if err != nil {
		return nil, err
	}

	v, ok := d.Value().Get(context.Background()).(int)
	if !ok {
		return nil, errs.New(errs.M("not an int"))
	}

	return rules.Row{"result": values.NewVariable(v * 2)}, nil
}

func TestRegister(t *testing.T) {
	t.Run("ok and duplicate rejected",
		func(t *testing.T) {
			reg := gorules.New()

			require.NoError(t, reg.Register("discount", doubler))

			err := reg.Register("discount", doubler)
			require.Error(t, err)
			require.Contains(t, err.Error(), "already registered")
		})

	t.Run("empty name rejected",
		func(t *testing.T) {
			require.Error(t, gorules.New().Register("", doubler))
		})

	t.Run("nil decision rejected",
		func(t *testing.T) {
			require.Error(t, gorules.New().Register("discount", nil))
		})
}

func TestMustRegister(t *testing.T) {
	t.Run("chains on success",
		func(t *testing.T) {
			reg := gorules.New().
				MustRegister("a", doubler).
				MustRegister("b", doubler)

			_, err := reg.Evaluate(
				context.Background(), "a", stubReader{readyData("in", 3)})
			require.NoError(t, err)
		})

	t.Run("panics on invalid registration",
		func(t *testing.T) {
			require.Panics(t,
				func() {
					gorules.New().MustRegister("", doubler)
				})
		})
}

func TestType(t *testing.T) {
	require.Equal(t, gorules.GoRulesType, gorules.New().Type())
	require.Equal(t, "##GoRules", gorules.GoRulesType)
}

func TestEvaluate(t *testing.T) {
	reg := gorules.New().MustRegister("double", doubler)

	t.Run("roundtrip yields a one-row result",
		func(t *testing.T) {
			rows, err := reg.Evaluate(
				context.Background(), "double",
				stubReader{readyData("in", 21)})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Len(t, rows[0], 1)
			require.Equal(t, 42,
				rows[0]["result"].Get(context.Background()))
		})

	t.Run("empty reference rejected",
		func(t *testing.T) {
			_, err := reg.Evaluate(
				context.Background(), "", stubReader{readyData("in", 1)})
			require.Error(t, err)
		})

	t.Run("nil reader rejected",
		func(t *testing.T) {
			_, err := reg.Evaluate(context.Background(), "double", nil)
			require.Error(t, err)
		})

	t.Run("unregistered reference is a classified error",
		func(t *testing.T) {
			_, err := reg.Evaluate(
				context.Background(), "no-such-decision",
				stubReader{readyData("in", 1)})
			require.Error(t, err)
			require.Contains(t, err.Error(), "no-such-decision")
			require.Contains(t, err.Error(), "isn't registered")
		})

	t.Run("decision error is wrapped with its reference",
		func(t *testing.T) {
			failing := gorules.New().
				MustRegister("boom",
					func(
						_ context.Context,
						_ service.DataReader,
					) (rules.Row, error) {
						return nil, errs.New(errs.M("inner failure"))
					})

			_, err := failing.Evaluate(
				context.Background(), "boom",
				stubReader{readyData("in", 1)})
			require.Error(t, err)
			require.Contains(t, err.Error(), "inner failure")
			require.Contains(t, err.Error(), "boom")
		})

	t.Run("nil row with nil error is an empty result",
		func(t *testing.T) {
			silent := gorules.New().
				MustRegister("silent",
					func(
						_ context.Context,
						_ service.DataReader,
					) (rules.Row, error) {
						return nil, nil
					})

			rows, err := silent.Evaluate(
				context.Background(), "silent",
				stubReader{readyData("in", 1)})
			require.NoError(t, err)
			require.Empty(t, rows)
		})
}

// compile-time seam check: the registry is a rules.Engine.
var _ rules.Engine = (*gorules.Registry)(nil)
