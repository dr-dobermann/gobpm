package dtable_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// stubDecoder decodes any definition into a fixed table (or fails).
type stubDecoder struct {
	tbl *dtable.Table
	err error
}

func (sd stubDecoder) Decode([]byte) (*dtable.Table, error) {
	return sd.tbl, sd.err
}

// switchDecoder decodes into whatever table is currently set — the
// redeploy fixture.
type switchDecoder struct {
	tbl *dtable.Table
}

func (sd *switchDecoder) Decode([]byte) (*dtable.Table, error) {
	return sd.tbl, nil
}

// oneRowTable builds a single always-matching rule table named name whose
// row is {out: v}.
func oneRowTable(t *testing.T, name string, v any) *dtable.Table {
	t.Helper()

	tbl, err := dtable.NewTable(name, dtable.First,
		mustRule(t, map[string]any{"out": v}))
	require.NoError(t, err)

	return tbl
}

// TestDeploy covers SRD-062 T-4a: the mechanics half of the component
// contract.
func TestDeploy(t *testing.T) {
	t.Run("no decoder configured fails loud",
		func(t *testing.T) {
			e, err := dtable.New()
			require.NoError(t, err)

			err = e.Deploy(ctx, []byte(`{}`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "WithDecoder")
		})

	t.Run("WithDecoder(nil) rejected",
		func(t *testing.T) {
			_, err := dtable.New(dtable.WithDecoder(nil))
			require.Error(t, err)
		})

	t.Run("empty definition rejected",
		func(t *testing.T) {
			e, err := dtable.New(
				dtable.WithDecoder(stubDecoder{tbl: oneRowTable(t, "d", 1)}))
			require.NoError(t, err)

			require.Error(t, e.Deploy(ctx, nil))
		})

	t.Run("decode failure is wrapped",
		func(t *testing.T) {
			e, err := dtable.New(dtable.WithDecoder(
				stubDecoder{err: errs.New(errs.M("bad grid"))}))
			require.NoError(t, err)

			err = e.Deploy(ctx, []byte(`x`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "bad grid")
		})

	t.Run("deploy registers and redeploy REPLACES",
		func(t *testing.T) {
			// a switchable decoder: each deployment decodes the next table.
			dec := &switchDecoder{tbl: oneRowTable(t, "d", 1)}

			e, err := dtable.New(dtable.WithDecoder(dec))
			require.NoError(t, err)

			require.NoError(t, e.Deploy(ctx, []byte(`v1`)))

			rows, err := e.Evaluate(ctx, "d", rdr(nil))
			require.NoError(t, err)
			require.Equal(t, 1, rows[0]["out"].Get(context.Background()))

			// the second deployment carries an updated table for the SAME
			// decision — it must supersede, not error.
			dec.tbl = oneRowTable(t, "d", 2)
			require.NoError(t, e.Deploy(ctx, []byte(`v2`)))

			rows, err = e.Evaluate(ctx, "d", rdr(nil))
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 2, rows[0]["out"].Get(context.Background()))
		})

	t.Run("deploy replaces a programmatically registered table too, "+
		"while Register keeps rejecting duplicates",
		func(t *testing.T) {
			e, err := dtable.New(
				dtable.WithDecoder(stubDecoder{tbl: oneRowTable(t, "d", 2)}))
			require.NoError(t, err)

			require.NoError(t, e.Register(oneRowTable(t, "d", 1)))
			require.Error(t, e.Register(oneRowTable(t, "d", 3)))

			require.NoError(t, e.Deploy(ctx, []byte(`v2`)))

			rows, err := e.Evaluate(ctx, "d", rdr(nil))
			require.NoError(t, err)
			require.Equal(t, 2, rows[0]["out"].Get(context.Background()))
		})
}

// the engine is a rules.Deployer (the component-contract assert).
var _ rules.Deployer = (*dtable.Engine)(nil)
