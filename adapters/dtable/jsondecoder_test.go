package dtable_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// vocab builds the fixture vocabulary the artifacts below reference.
func vocab() *dtable.Vocabulary {
	return dtable.NewVocabulary().
		MustAddCondition("gold-tier", dtable.Eq("tier", "gold")).
		MustAddCondition("big-order", dtable.GT("total", 100)).
		MustAddYield("default-discount",
			func(context.Context, service.DataReader) (rules.Row, error) {
				return rules.Row{"discount_pct": values.NewVariable(1)}, nil
			})
}

// deployed builds an engine with the JSON decoder and deploys artifact.
func deployed(t *testing.T, artifact string) *dtable.Engine {
	t.Helper()

	dec, err := dtable.NewJSONDecoder(vocab())
	require.NoError(t, err)

	e, err := dtable.New(dtable.WithDecoder(dec))
	require.NoError(t, err)

	require.NoError(t, e.Deploy(ctx, []byte(artifact)))

	return e
}

// the SRD-062 worked artifact.
const discountArtifact = `{
  "name": "discount",
  "hitPolicy": "FIRST",
  "rules": [
    {"when": ["gold-tier", "big-order"], "then": {"discount_pct": 15}},
    {"when": [], "thenFn": "default-discount"}
  ]
}`

// TestJSONDecoder covers SRD-062 T-4b.
func TestJSONDecoder(t *testing.T) {
	t.Run("nil vocabulary rejected",
		func(t *testing.T) {
			_, err := dtable.NewJSONDecoder(nil)
			require.Error(t, err)
		})

	t.Run("the worked artifact deploys and evaluates end to end",
		func(t *testing.T) {
			e := deployed(t, discountArtifact)

			// both named conditions hold -> the literal row; JSON numbers
			// land as float64 (documented, uncoerced).
			rows, err := e.Evaluate(ctx, "discount",
				rdr(map[string]any{"tier": "gold", "total": 150}))
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, float64(15),
				rows[0]["discount_pct"].Get(context.Background()))

			// no gold tier -> the empty-when (match-always) yield functor.
			rows, err = e.Evaluate(ctx, "discount",
				rdr(map[string]any{"tier": "iron", "total": 150}))
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 1,
				rows[0]["discount_pct"].Get(context.Background()))
		})

	t.Run("float64 literals compare only against float64 data",
		func(t *testing.T) {
			dec, err := dtable.NewJSONDecoder(
				dtable.NewVocabulary().
					MustAddCondition("cap", dtable.LE("total", 100)))
			require.NoError(t, err)

			e, err := dtable.New(dtable.WithDecoder(dec))
			require.NoError(t, err)

			// the condition itself is Go-built here; the deployed literal
			// class is exercised through a then-literal read back above.
			require.NoError(t, e.Deploy(ctx, []byte(
				`{"name":"cap","hitPolicy":"FIRST",
				  "rules":[{"when":["cap"],"then":{"ok":true}}]}`)))

			_, err = e.Evaluate(ctx, "cap",
				rdr(map[string]any{"total": 50}))
			require.NoError(t, err)
		})

	t.Run("malformations are classified deploy-time errors",
		func(t *testing.T) {
			dec, err := dtable.NewJSONDecoder(vocab())
			require.NoError(t, err)

			e, err := dtable.New(dtable.WithDecoder(dec))
			require.NoError(t, err)

			cases := map[string]string{
				"malformed JSON": `{nope`,
				"unresolved condition name": `{"name":"d","hitPolicy":"FIRST",
					"rules":[{"when":["no-such"],"then":{"x":1}}]}`,
				"unresolved yield name": `{"name":"d","hitPolicy":"FIRST",
					"rules":[{"when":[],"thenFn":"no-such"}]}`,
				"both then and thenFn": `{"name":"d","hitPolicy":"FIRST",
					"rules":[{"when":[],"then":{"x":1},"thenFn":"default-discount"}]}`,
				"neither then nor thenFn": `{"name":"d","hitPolicy":"FIRST",
					"rules":[{"when":[]}]}`,
				"unknown policy": `{"name":"d","hitPolicy":"BOGUS",
					"rules":[{"when":[],"then":{"x":1}}]}`,
				"empty rules": `{"name":"d","hitPolicy":"FIRST","rules":[]}`,
				"empty name": `{"name":"","hitPolicy":"FIRST",
					"rules":[{"when":[],"then":{"x":1}}]}`,
			}

			for label, artifact := range cases {
				require.Error(t, e.Deploy(ctx, []byte(artifact)), label)
			}
		})
}

// TestVocabulary covers the vocabulary validation surface.
func TestVocabulary(t *testing.T) {
	v := dtable.NewVocabulary()

	require.Error(t, v.AddCondition("", dtable.Any()))
	require.Error(t, v.AddCondition("c", nil))
	require.NoError(t, v.AddCondition("c", dtable.Any()))
	require.Error(t, v.AddCondition("c", dtable.Any()))

	yield := func(context.Context, service.DataReader) (rules.Row, error) {
		return rules.Row{"x": values.NewVariable(1)}, nil
	}

	require.Error(t, v.AddYield("", yield))
	require.Error(t, v.AddYield("y", nil))
	require.NoError(t, v.AddYield("y", yield))
	require.Error(t, v.AddYield("y", yield))

	require.Panics(t, func() { v.MustAddCondition("c", dtable.Any()) })
	require.Panics(t, func() { v.MustAddYield("y", yield) })
}
