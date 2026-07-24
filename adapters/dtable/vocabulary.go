package dtable

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// Vocabulary is the Go-registered behavior a deployed artifact wires by
// name (ADR-029 §2.6): named Conditions for the "when" cells and named
// YieldFuncs for computed "thenFn" outputs. Artifacts carry structure only;
// every predicate stays compiled, reviewable Go.
type Vocabulary struct {
	conds  map[string]Condition
	yields map[string]YieldFunc
}

// NewVocabulary creates an empty vocabulary.
func NewVocabulary() *Vocabulary {
	return &Vocabulary{
		conds:  map[string]Condition{},
		yields: map[string]YieldFunc{},
	}
}

// AddCondition registers a named condition. The name must be non-empty and
// unique; the condition must be non-nil.
func (v *Vocabulary) AddCondition(name string, c Condition) error {
	name = strings.TrimSpace(name)

	switch {
	case name == "":
		return errs.New(
			errs.M("AddCondition: an empty name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case c == nil:
		return errs.New(
			errs.M("AddCondition: a nil Condition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("name", name))
	}

	if _, ok := v.conds[name]; ok {
		return errs.New(
			errs.M("AddCondition: name is already registered"),
			errs.C(errorClass, errs.DuplicateObject),
			errs.D("name", name))
	}

	v.conds[name] = c

	return nil
}

// MustAddCondition is the panic-on-error AddCondition twin for tests and
// static construction; it returns the vocabulary for chaining.
func (v *Vocabulary) MustAddCondition(name string, c Condition) *Vocabulary {
	if err := v.AddCondition(name, c); err != nil {
		errs.Panic(err)

		return nil
	}

	return v
}

// AddYield registers a named computed-output functor. The name must be
// non-empty and unique; the functor must be non-nil.
func (v *Vocabulary) AddYield(name string, f YieldFunc) error {
	name = strings.TrimSpace(name)

	switch {
	case name == "":
		return errs.New(
			errs.M("AddYield: an empty name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case f == nil:
		return errs.New(
			errs.M("AddYield: a nil YieldFunc isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("name", name))
	}

	if _, ok := v.yields[name]; ok {
		return errs.New(
			errs.M("AddYield: name is already registered"),
			errs.C(errorClass, errs.DuplicateObject),
			errs.D("name", name))
	}

	v.yields[name] = f

	return nil
}

// MustAddYield is the panic-on-error AddYield twin for tests and static
// construction; it returns the vocabulary for chaining.
func (v *Vocabulary) MustAddYield(name string, f YieldFunc) *Vocabulary {
	if err := v.AddYield(name, f); err != nil {
		errs.Panic(err)

		return nil
	}

	return v
}

// condition resolves a named condition.
func (v *Vocabulary) condition(name string) (Condition, bool) {
	c, ok := v.conds[name]

	return c, ok
}

// yield resolves a named yield functor.
func (v *Vocabulary) yield(name string) (YieldFunc, bool) {
	f, ok := v.yields[name]

	return f, ok
}
