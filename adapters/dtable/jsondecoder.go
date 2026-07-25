package dtable

import (
	"encoding/json"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// JSONDecoder is the batteries Decoder (ADR-029 §2.6): a structure-only
// JSON artifact wiring named Go behavior from a Vocabulary. The artifact
// carries the table name, the hit policy and the rule grid; every "when"
// cell references a registered Condition by name and outputs are either
// JSON literals ("then") or a registered YieldFunc ("thenFn"). No condition
// language enters — an unresolved name is a classified deploy-time error.
//
// JSON number literals land as float64 (encoding/json's untyped-number
// default) and are deliberately NOT coerced: a table comparing a deployed
// literal against an int datum hits the loud type-mismatch error, steering
// authors to matching datum types or yield functors.
type JSONDecoder struct {
	vocab *Vocabulary
}

// interface check
var _ Decoder = (*JSONDecoder)(nil)

// NewJSONDecoder creates the decoder over the vocabulary its artifacts
// reference.
func NewJSONDecoder(v *Vocabulary) (*JSONDecoder, error) {
	if v == nil {
		return nil, errs.New(
			errs.M("NewJSONDecoder: a nil Vocabulary isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &JSONDecoder{vocab: v}, nil
}

// jsonTable is the artifact shape.
type jsonTable struct {
	Name      string     `json:"name"`
	HitPolicy string     `json:"hitPolicy"`
	Rules     []jsonRule `json:"rules"`
}

// jsonRule is one grid row: named conditions plus literal XOR named
// outputs.
type jsonRule struct {
	Then   map[string]any `json:"then,omitempty"`
	ThenFn string         `json:"thenFn,omitempty"`
	When   []string       `json:"when"`
}

// Decode translates the artifact into an executable Table.
func (jd *JSONDecoder) Decode(definition []byte) (*Table, error) {
	var jt jsonTable

	if err := json.Unmarshal(definition, &jt); err != nil {
		return nil, errs.New(
			errs.M("JSONDecoder: malformed definition"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	rr := make([]Rule, 0, len(jt.Rules))

	for i, jr := range jt.Rules {
		r, err := jd.decodeRule(i, jr)
		if err != nil {
			return nil, err
		}

		rr = append(rr, r)
	}

	// NewTable single-sources the structural validation (name, known
	// policy, non-empty rules).
	return NewTable(jt.Name, HitPolicy(jt.HitPolicy), rr...)
}

// decodeRule translates one grid row, resolving names against the
// vocabulary.
func (jd *JSONDecoder) decodeRule(ordinal int, jr jsonRule) (Rule, error) {
	conds := make([]Condition, 0, len(jr.When))

	for _, name := range jr.When {
		c, ok := jd.vocab.condition(name)
		if !ok {
			return nil, decodeErr(ordinal, "unresolved condition name", name)
		}

		conds = append(conds, c)
	}

	switch {
	case len(jr.Then) > 0 && jr.ThenFn != "":
		return nil, decodeErr(ordinal,
			"a rule carries either then or thenFn, not both", jr.ThenFn)

	case jr.ThenFn != "":
		f, ok := jd.vocab.yield(jr.ThenFn)
		if !ok {
			return nil, decodeErr(ordinal, "unresolved yield name", jr.ThenFn)
		}

		return R(conds...).ThenF(f)

	case len(jr.Then) > 0:
		out := rules.Row{}
		for k, v := range jr.Then {
			out[k] = values.NewVariable(v)
		}

		return R(conds...).Then(out)
	}

	return nil, decodeErr(ordinal, "a rule needs then or thenFn", "")
}

// decodeErr classifies a grid-row decoding failure.
func decodeErr(ordinal int, msg, name string) error {
	return errs.New(
		errs.M("JSONDecoder: "+msg),
		errs.C(errorClass, errs.InvalidObject),
		errs.D("rule", strconv.Itoa(ordinal)),
		errs.D("name", name))
}
