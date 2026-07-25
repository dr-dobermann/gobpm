package lite

import (
	"context"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// evaluator walks a parsed body against the process data Source.
type evaluator struct {
	src data.Source
}

// evalErr builds the classified evaluation error naming the byte offset.
func evalErr(msg string, off int) error {
	return errs.New(
		errs.M(msg),
		errs.C(errorClass, errs.InvalidParameter),
		errs.D("offset", strconv.Itoa(off)))
}

// eval evaluates one AST node into an operand: float64, string, bool,
// time.Time or nil.
func (e *evaluator) eval(ctx context.Context, n node) (any, error) {
	switch t := n.(type) {
	case litNode:
		return t.val, nil

	case refNode:
		return e.readRef(ctx, t)

	case unaryNode:
		return e.evalUnary(ctx, t)

	case binNode:
		return e.evalBinary(ctx, t)

	case callNode:
		return e.evalCall(ctx, t)

	default:
		return nil, evalErr("unsupported AST node", n.pos())
	}
}

// readRef reads a datum (or a structural path) through the Source — a
// missing datum or a dead path fails loud with the resolver's own error
// attached (SRD-067 FR-2).
func (e *evaluator) readRef(ctx context.Context, r refNode) (any, error) {
	d, err := e.src.Find(ctx, r.path)
	if err != nil {
		return nil, errs.New(
			errs.M("reading %q failed", r.path),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("offset", strconv.Itoa(r.pos())),
			errs.E(err))
	}

	return normalize(d.Value().Get(ctx), r.path, r.pos())
}

// normalize folds a raw datum value into an operand: the numeric family
// unifies to float64 (ADR-032 §2.3); string, bool, time.Time and nil pass
// through; anything else (a whole collection, a struct) is loud.
func normalize(v any, path string, off int) (any, error) {
	switch x := v.(type) {
	case nil, bool, string, float64, time.Time:
		return x, nil

	case int:
		return float64(x), nil
	case int8:
		return float64(x), nil
	case int16:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case uint:
		return float64(x), nil
	case uint8:
		return float64(x), nil
	case uint16:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case float32:
		return float64(x), nil

	default:
		return nil, evalErr(
			"\""+path+"\" isn't a lite operand (number, string, bool,"+
				" time) — navigate into it or use len()",
			off)
	}
}

// evalUnary evaluates 'not' and the numeric negation.
func (e *evaluator) evalUnary(ctx context.Context, n unaryNode) (any, error) {
	v, err := e.eval(ctx, n.operand)
	if err != nil {
		return nil, err
	}

	switch n.op {
	case "not":
		b, ok := v.(bool)
		if !ok {
			return nil, evalErr("'not' needs a bool operand", n.pos())
		}

		return !b, nil

	default: // "-"
		f, ok := v.(float64)
		if !ok {
			return nil, evalErr("unary '-' needs a number", n.pos())
		}

		return -f, nil
	}
}

// evalBinary evaluates the boolean, comparison and arithmetic operators.
func (e *evaluator) evalBinary(ctx context.Context, n binNode) (any, error) {
	if n.op == "and" || n.op == "or" {
		return e.evalLogical(ctx, n)
	}

	l, err := e.eval(ctx, n.left)
	if err != nil {
		return nil, err
	}

	r, err := e.eval(ctx, n.right)
	if err != nil {
		return nil, err
	}

	if _, ok := comparisonOps[n.op]; ok {
		return compare(n.op, l, r, n.pos())
	}

	return arithmetic(n.op, l, r, n.pos())
}

// evalLogical evaluates 'and'/'or' with short-circuiting — the right side
// is never touched when the left decides (SRD-067 FR-2).
func (e *evaluator) evalLogical(ctx context.Context, n binNode) (any, error) {
	l, err := e.eval(ctx, n.left)
	if err != nil {
		return nil, err
	}

	lb, ok := l.(bool)
	if !ok {
		return nil, evalErr("'"+n.op+"' needs bool operands", n.pos())
	}

	if (n.op == "and" && !lb) || (n.op == "or" && lb) {
		return lb, nil
	}

	r, err := e.eval(ctx, n.right)
	if err != nil {
		return nil, err
	}

	rb, ok := r.(bool)
	if !ok {
		return nil, evalErr("'"+n.op+"' needs bool operands", n.pos())
	}

	return rb, nil
}

// compare evaluates one comparison: numbers, strings and times order and
// equate within their own kind; bools and nil equate only; every
// cross-kind comparison is loud — never a silent false (SRD-067 FR-2).
func compare(op string, l, r any, off int) (any, error) {
	if l == nil || r == nil {
		switch op {
		case "==":
			return l == nil && r == nil, nil
		case "!=":
			return (l == nil) != (r == nil), nil
		default:
			return nil, evalErr("nil doesn't order — only == and !=", off)
		}
	}

	switch lv := l.(type) {
	case float64:
		rv, ok := r.(float64)
		if !ok {
			return nil, crossKind(op, off)
		}

		return ordered(op, lv == rv, lv < rv), nil

	case string:
		rv, ok := r.(string)
		if !ok {
			return nil, crossKind(op, off)
		}

		return ordered(op, lv == rv, lv < rv), nil

	case time.Time:
		rv, ok := r.(time.Time)
		if !ok {
			return nil, crossKind(op, off)
		}

		return ordered(op, lv.Equal(rv), lv.Before(rv)), nil

	default: // bool
		rv, ok := r.(bool)
		if !ok {
			return nil, crossKind(op, off)
		}

		switch op {
		case "==":
			return l.(bool) == rv, nil
		case "!=":
			return l.(bool) != rv, nil
		default:
			return nil, evalErr("bools don't order — only == and !=", off)
		}
	}
}

// crossKind is the loud cross-kind comparison error.
func crossKind(op string, off int) error {
	return evalErr(
		"'"+op+"' can't compare operands of different kinds", off)
}

// ordered folds an eq/lt pair into the comparison result — one table for
// numbers, strings and times.
func ordered(op string, eq, lt bool) bool {
	switch op {
	case "==":
		return eq
	case "!=":
		return !eq
	case "<":
		return lt
	case "<=":
		return lt || eq
	case ">":
		return !lt && !eq
	default: // ">="
		return !lt
	}
}

// arithmetic evaluates + - * / %: '+' adds numbers or concatenates
// strings; the rest need numbers; division by zero is loud.
func arithmetic(op string, l, r any, off int) (any, error) {
	if op == "+" {
		if ls, ok := l.(string); ok {
			rs, ok := r.(string)
			if !ok {
				return nil, crossKind(op, off)
			}

			return ls + rs, nil
		}
	}

	lf, ok := l.(float64)
	if !ok {
		return nil, evalErr("'"+op+"' needs number operands", off)
	}

	rf, ok := r.(float64)
	if !ok {
		return nil, evalErr("'"+op+"' needs number operands", off)
	}

	switch op {
	case "+":
		return lf + rf, nil
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, evalErr("division by zero", off)
		}

		return lf / rf, nil
	default: // "%"
		if rf == 0 {
			return nil, evalErr("division by zero in '%'", off)
		}

		return math.Mod(lf, rf), nil
	}
}

// evalCall evaluates the builtins: has, len, time (SRD-067 FR-3).
func (e *evaluator) evalCall(ctx context.Context, c callNode) (any, error) {
	switch c.name {
	case "has":
		return e.evalHas(ctx, c)

	case "len":
		return e.evalLen(ctx, c)

	default: // "time"
		return e.evalTime(ctx, c)
	}
}

// evalHas probes a name-or-path: resolution success is true, ANY
// resolution failure is false — the explicit existence opt-out of the
// fail-loud default (SRD-067 §4.4).
func (e *evaluator) evalHas(ctx context.Context, c callNode) (any, error) {
	v, err := e.eval(ctx, c.arg)
	if err != nil {
		return nil, err
	}

	s, ok := v.(string)
	if !ok {
		return nil, evalErr(
			"has() needs a string naming a datum or path", c.pos())
	}

	if _, err := e.src.Find(ctx, s); err != nil {
		return false, nil
	}

	return true, nil
}

// evalLen counts: an Array's elements, a Map's keys, a string's RUNES
// (SRD-067 §4.5); anything else is loud. The result is a number.
func (e *evaluator) evalLen(ctx context.Context, c callNode) (any, error) {
	if ref, ok := c.arg.(refNode); ok {
		return e.lenOfRef(ctx, ref, c.pos())
	}

	v, err := e.eval(ctx, c.arg)
	if err != nil {
		return nil, err
	}

	s, ok := v.(string)
	if !ok {
		return nil, evalErr(
			"len() needs an array, a map or a string", c.pos())
	}

	return float64(utf8.RuneCountInString(s)), nil
}

// lenOfRef counts a referenced datum without normalizing it — the whole
// collection is a legal len() operand even though it is not an operand
// anywhere else.
func (e *evaluator) lenOfRef(
	ctx context.Context, ref refNode, off int,
) (any, error) {
	d, err := e.src.Find(ctx, ref.path)
	if err != nil {
		return nil, errs.New(
			errs.M("reading %q failed", ref.path),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("offset", strconv.Itoa(off)),
			errs.E(err))
	}

	switch v := d.Value().(type) {
	case interface{ Count() int }:
		return float64(v.Count()), nil

	case interface{ Keys() []string }:
		return float64(len(v.Keys())), nil

	default:
		if s, ok := v.Get(ctx).(string); ok {
			return float64(utf8.RuneCountInString(s)), nil
		}

		return nil, evalErr(
			"len() needs an array, a map or a string", off)
	}
}

// evalTime parses an RFC3339 literal into a time operand.
func (e *evaluator) evalTime(ctx context.Context, c callNode) (any, error) {
	v, err := e.eval(ctx, c.arg)
	if err != nil {
		return nil, err
	}

	s, ok := v.(string)
	if !ok {
		return nil, evalErr("time() needs an RFC3339 string", c.pos())
	}

	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, errs.New(
			errs.M("time(): %q isn't an RFC3339 timestamp", s),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("offset", strconv.Itoa(c.pos())),
			errs.E(err))
	}

	return ts, nil
}
