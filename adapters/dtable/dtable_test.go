package dtable_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// stubReader hands conditions a fixed set of named values; a missing name
// errors (the runtime reader's posture).
type stubReader struct {
	data map[string]any
}

func (s stubReader) GetData(name string) (data.Data, error) {
	v, ok := s.data[name]
	if !ok {
		return nil, errs.New(
			errs.M("no datum %q", name),
			errs.C(errs.ObjectNotFound))
	}

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v)),
			data.ReadyDataState)), nil
}

func (s stubReader) GetDataByID(id string) (data.Data, error) {
	return s.GetData(id)
}

func (stubReader) GetSources() []string { return nil }

func (stubReader) List(string) ([]string, error) { return nil, nil }

func TestMain(m *testing.M) {
	if err := data.CreateDefaultStates(); err != nil {
		panic(err)
	}

	m.Run()
}

var ctx = context.Background()

// rdr builds a stub reader over the given values.
func rdr(kv map[string]any) service.DataReader {
	return stubReader{data: kv}
}

// row builds a rules.Row over plain values.
func row(kv map[string]any) rules.Row {
	out := rules.Row{}
	for k, v := range kv {
		out[k] = values.NewVariable(v)
	}

	return out
}

// mustRule builds a functor rule or fails the test.
func mustRule(
	t *testing.T, out map[string]any, conds ...dtable.Condition,
) dtable.Rule {
	t.Helper()

	r, err := dtable.R(conds...).Then(row(out))
	require.NoError(t, err)

	return r
}
