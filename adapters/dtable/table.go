package dtable

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// HitPolicy decides how multiple matching rules resolve into the decision
// result (ADR-029 §2.4 — the DMN table-notation names).
type HitPolicy string

const (
	// Unique expects at most one matching rule; two or more is a
	// contradiction error.
	Unique HitPolicy = "UNIQUE"
	// First returns the first matching rule in rule order.
	First HitPolicy = "FIRST"
	// AnyMatch allows several matching rules but requires them to agree;
	// one row is returned. (Named AnyMatch so the Any() condition
	// constructor keeps the DMN "-" name.)
	AnyMatch HitPolicy = "ANY"
	// RuleOrder returns every matching rule's row, in rule order.
	RuleOrder HitPolicy = "RULE ORDER"
	// Collect returns every matching rule's row (bare Collect — no
	// aggregation operators; the row set equals RuleOrder's).
	Collect HitPolicy = "COLLECT"
)

// Table is the data declaration of one decision (ADR-029 §2.3): a name (the
// decision reference it answers to), a hit policy, and an ordered rule
// list. It carries no evaluation logic of its own.
type Table struct {
	name   string
	policy HitPolicy
	rules  []Rule
}

// NewTable creates a decision table. The name must be non-empty, the policy
// known, and the rule list non-empty with no nil entries.
func NewTable(name string, policy HitPolicy, rr ...Rule) (*Table, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errs.New(
			errs.M("NewTable: an empty table name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := resolvers[policy]; !ok {
		return nil, errs.New(
			errs.M("NewTable: unknown hit policy"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("table", name),
			errs.D("hit_policy", string(policy)))
	}

	if len(rr) == 0 {
		return nil, errs.New(
			errs.M("NewTable: a table needs at least one rule"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("table", name))
	}

	for i, r := range rr {
		if r == nil {
			return nil, errs.New(
				errs.M("NewTable: a nil Rule isn't allowed (rule %d)", i),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("table", name))
		}
	}

	return &Table{
		name:   name,
		policy: policy,
		rules:  append([]Rule{}, rr...),
	}, nil
}

// Name returns the decision reference the table answers to.
func (t *Table) Name() string {
	return t.name
}

// Policy returns the table's hit policy.
func (t *Table) Policy() HitPolicy {
	return t.policy
}
