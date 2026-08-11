package gorules_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
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

// factSink collects reported facts for the registrar-audit assertions.
type factSink struct {
	facts []observability.Fact
}

func (fs *factSink) Report(f observability.Fact) {
	fs.facts = append(fs.facts, f)
}

// TestRegistrarFacts covers SRD-069 T-3: bound registration emits the
// Registered audit fact; unbound and nil-sink registries stay silent; a
// rejected duplicate emits nothing.
func TestRegistrarFacts(t *testing.T) {
	noop := func(
		_ context.Context, _ service.DataReader,
	) (rules.Row, error) {
		return nil, nil
	}

	t.Run("unbound registration is silent",
		func(t *testing.T) {
			reg := gorules.New()
			require.NoError(t, reg.Register("quiet", noop))
		})

	t.Run("a nil sink is ignored",
		func(t *testing.T) {
			reg := gorules.New()
			reg.BindReporter(nil)
			require.NoError(t, reg.Register("still-quiet", noop))
		})

	t.Run("bound registration emits Registered with names only",
		func(t *testing.T) {
			sink := &factSink{}

			reg := gorules.New()
			reg.BindReporter(sink)
			require.NoError(t, reg.Register("discount", noop))

			require.Len(t, sink.facts, 1)
			f := sink.facts[0]
			require.Equal(t, observability.KindRules, f.Kind)
			require.Equal(t, observability.PhaseRegistered, f.Phase)
			require.Equal(t, "discount",
				f.Details[observability.AttrDecisionRef])
			require.Equal(t, gorules.GoRulesType,
				f.Details[observability.AttrImplementation])
		})

	t.Run("a rejected duplicate emits nothing",
		func(t *testing.T) {
			sink := &factSink{}

			reg := gorules.New()
			reg.BindReporter(sink)
			require.NoError(t, reg.Register("once", noop))
			require.Error(t, reg.Register("once", noop))

			require.Len(t, sink.facts, 1, "only the success is audited")
		})
}

// blockingSink holds its caller inside Report until released — the only way to
// observe that the registry is not talking to the host with its lock held.
type blockingSink struct {
	entered chan struct{}
	release chan struct{}
}

func (bs *blockingSink) Report(_ observability.Fact) {
	close(bs.entered)
	<-bs.release
}

// TestRegisterDoesNotHoldTheRegistryLock pins the foreignness rule here.
//
// The reporter is whatever the embedding application passed to BindReporter, so
// Report is a call into the HOST. While reg.mu was held across it, every other
// registration and every Evaluate lookup queued behind a latency this engine
// does not control (FIX-038 §1.1). Evaluate had it right all along — read under
// the lock, call outside it — which is what made Register the outlier.
//
// The decision must already be registered by the time the report goes out: the
// audit fact says a registration happened, so a concurrent Evaluate that sees
// the fact and then misses the decision would be reading a lie.
func TestRegisterDoesNotHoldTheRegistryLock(t *testing.T) {
	noop := func(_ context.Context, _ service.DataReader) (rules.Row, error) {
		return nil, nil
	}

	sink := &blockingSink{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	reg := gorules.New()
	reg.BindReporter(sink)

	registered := make(chan error, 1)

	go func() { registered <- reg.Register("slow", noop) }()

	select {
	case <-sink.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Register never reached the reporter")
	}

	// Register is pinned inside the host call. Evaluate takes reg.mu, so it
	// answers only if Register let the lock go.
	evaluated := make(chan error, 1)

	go func() {
		_, err := reg.Evaluate(context.Background(), "slow", stubReader{})
		evaluated <- err
	}()

	select {
	case err := <-evaluated:
		require.NoError(t, err,
			"the decision must be in the registry before its audit fact is "+
				"reported: a fact for a registration a lookup cannot find is a lie")
	case <-time.After(2 * time.Second):
		close(sink.release)
		t.Fatal("Evaluate blocked while Register was inside the host's " +
			"reporter: the registry is holding reg.mu across a host call " +
			"(FIX-038 §1.1)")
	}

	close(sink.release)
	require.NoError(t, <-registered)
}
