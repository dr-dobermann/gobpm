package dtable

import (
	"context"
	"reflect"
	"strconv"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// Engine is the Decision Table rules.Engine: a registry of tables keyed by
// their decision name. Register write-locks (setup time); Evaluate
// read-locks, so concurrent tracks evaluate freely.
type Engine struct {
	tables map[string]*Table
	mu     sync.RWMutex
}

// interface check
var _ rules.Engine = (*Engine)(nil)

// Option configures the Engine at construction (the Decoder seam arrives
// through it).
type Option func(*Engine) error

// New creates an empty Decision Table engine.
func New(opts ...Option) (*Engine, error) {
	e := &Engine{tables: map[string]*Table{}}

	for _, opt := range opts {
		if opt == nil {
			return nil, errs.New(
				errs.M("New: a nil Option isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if err := opt(e); err != nil {
			return nil, err
		}
	}

	return e, nil
}

// Register adds the table under its decision name. A nil table and a
// duplicate name are rejected — programmatic registration is
// construction-time wiring, where a duplicate is a bug (Deploy, the
// lifecycle operation, replaces instead — ADR-029 §2.6).
func (e *Engine) Register(t *Table) error {
	if t == nil {
		return errs.New(
			errs.M("Register: a nil Table isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.tables[t.name]; ok {
		return errs.New(
			errs.M("Register: table is already registered"),
			errs.C(errorClass, errs.DuplicateObject),
			errs.D("table", t.name))
	}

	e.tables[t.name] = t

	return nil
}

// MustRegister is the panic-on-error Register twin for tests and static
// construction; it returns the engine for chaining.
func (e *Engine) MustRegister(t *Table) *Engine {
	if err := e.Register(t); err != nil {
		errs.Panic(err)

		return nil
	}

	return e
}

// Type returns the engine's implementation kind.
func (e *Engine) Type() string {
	return DTableType
}

// match is one matched rule: its 0-based ordinal and its yielded row.
type match struct {
	row     rules.Row
	ordinal int
}

// resolvers maps every hit policy to its resolution over the matched rules
// (the data-over-code house rule). shortCircuit marks the policies whose
// scan may stop at the first match.
var resolvers = map[HitPolicy]struct {
	resolve      func(ctx context.Context, mm []match) ([]rules.Row, error)
	shortCircuit bool
}{
	Unique:    {resolve: resolveUnique},
	First:     {resolve: resolveAll, shortCircuit: true},
	AnyMatch:  {resolve: resolveAny},
	RuleOrder: {resolve: resolveAll},
	Collect:   {resolve: resolveAll},
}

// Evaluate resolves decisionRef to a table, scans its rules in order and
// resolves the matches through the table's hit policy. An unknown reference
// is a classified error; rule failures carry the decision and rule ordinal.
func (e *Engine) Evaluate(
	ctx context.Context,
	decisionRef string,
	r service.DataReader,
) ([]rules.Row, error) {
	if decisionRef == "" {
		return nil, errs.New(
			errs.M("Evaluate: an empty decision reference isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil, errs.New(
			errs.M("Evaluate: a nil DataReader isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("decision_ref", decisionRef))
	}

	e.mu.RLock()
	t, ok := e.tables[decisionRef]
	e.mu.RUnlock()

	if !ok {
		return nil, errs.New(
			errs.M("Evaluate: decision table isn't registered"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("decision_ref", decisionRef))
	}

	rs := resolvers[t.policy]

	var mm []match

	for i, rule := range t.rules {
		hit, err := rule.Matches(ctx, r)
		if err != nil {
			return nil, ruleErr(decisionRef, i, "match", err)
		}

		if !hit {
			continue
		}

		row, err := rule.Yield(ctx, r)
		if err != nil {
			return nil, ruleErr(decisionRef, i, "yield", err)
		}

		mm = append(mm, match{row: row, ordinal: i})

		if rs.shortCircuit {
			break
		}
	}

	if len(mm) == 0 {
		return nil, nil
	}

	rows, err := rs.resolve(ctx, mm)
	if err != nil {
		return nil, errs.New(
			errs.M("decision resolution failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("decision_ref", decisionRef),
			errs.D("hit_policy", string(t.policy)))
	}

	return rows, nil
}

// ruleErr classifies a rule-level failure with its decision and ordinal.
func ruleErr(decisionRef string, ordinal int, phase string, err error) error {
	return errs.New(
		errs.M("rule %s failed", phase),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err),
		errs.D("decision_ref", decisionRef),
		errs.D("rule", strconv.Itoa(ordinal)))
}

// resolveAll returns every matched row in rule order (First — with its
// short-circuiting scan — RuleOrder and Collect).
func resolveAll(_ context.Context, mm []match) ([]rules.Row, error) {
	rows := make([]rules.Row, 0, len(mm))
	for _, m := range mm {
		rows = append(rows, m.row)
	}

	return rows, nil
}

// resolveUnique returns the single match; two or more is the table
// contradiction Unique exists to catch.
func resolveUnique(_ context.Context, mm []match) ([]rules.Row, error) {
	if len(mm) > 1 {
		return nil, errs.New(
			errs.M("UNIQUE hit policy violated: rules %d and %d both match",
				mm[0].ordinal, mm[1].ordinal),
			errs.C(errorClass, errs.InvalidState))
	}

	return []rules.Row{mm[0].row}, nil
}

// resolveAny returns one row after asserting every matched row agrees
// (compared by extracted values — Row holds data.Value interfaces whose
// identities differ even when the values agree).
func resolveAny(ctx context.Context, mm []match) ([]rules.Row, error) {
	first := rowValues(ctx, mm[0].row)

	for _, m := range mm[1:] {
		if !reflect.DeepEqual(first, rowValues(ctx, m.row)) {
			return nil, errs.New(
				errs.M("ANY hit policy violated: rules %d and %d disagree",
					mm[0].ordinal, m.ordinal),
				errs.C(errorClass, errs.InvalidState))
		}
	}

	return []rules.Row{mm[0].row}, nil
}

// rowValues extracts a row's plain values for comparison.
func rowValues(ctx context.Context, row rules.Row) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = v.Get(ctx)
	}

	return out
}
