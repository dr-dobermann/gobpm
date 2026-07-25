package lite

// node is one parsed AST node; pos anchors evaluation errors to the body.
type node interface {
	pos() int
}

// base carries the node's byte offset — embedded by every node kind.
type base struct {
	off int
}

func (b base) pos() int { return b.off }

// at builds the embedded base.
func at(off int) base { return base{off: off} }

// litNode carries a literal: float64, string, bool or nil.
type litNode struct {
	val any

	base
}

// refNode carries a data reference — a bare name or a structural path,
// resolved verbatim through src.Find (SRD-067 §4.1).
type refNode struct {
	path string

	base
}

// callNode carries a builtin call (has, len, time — all unary).
type callNode struct {
	arg  node
	name string

	base
}

// unaryNode carries 'not' or the numeric negation.
type unaryNode struct {
	operand node
	op      string

	base
}

// binNode carries a binary operation.
type binNode struct {
	left  node
	right node
	op    string

	base
}

// builtins is the whole builtin set (SRD-067 FR-3) — anything richer
// belongs to a FEEL adapter.
var builtins = map[string]struct{}{
	"has":  {},
	"len":  {},
	"time": {},
}

// comparisonOps marks the comparison lexemes for the parser's single,
// non-associative comparison level.
var comparisonOps = map[string]struct{}{
	"==": {}, "!=": {}, "<": {}, "<=": {}, ">": {}, ">=": {},
}

// parser walks the token stream with one token of lookahead.
type parser struct {
	tt []token
	i  int
}

// parse tokenizes and parses body into its AST.
func parse(body string) (node, error) {
	tt, err := lex(body)
	if err != nil {
		return nil, err
	}

	p := &parser{tt: tt}

	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if p.cur().kind != tkEOF {
		return nil, syntaxErr(
			"unexpected input after the expression", p.cur().off)
	}

	return n, nil
}

// cur returns the current token; next consumes it.
func (p *parser) cur() token { return p.tt[p.i] }

func (p *parser) next() token {
	t := p.tt[p.i]
	p.i++

	return t
}

// parseOr: orExpr := andExpr { "or" andExpr }.
func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.cur().kind == tkOr {
		t := p.next()

		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}

		left = binNode{op: "or", left: left, right: right, base: at(t.off)}
	}

	return left, nil
}

// parseAnd: andExpr := notExpr { "and" notExpr }.
func (p *parser) parseAnd() (node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.cur().kind == tkAnd {
		t := p.next()

		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		left = binNode{op: "and", left: left, right: right, base: at(t.off)}
	}

	return left, nil
}

// parseNot: notExpr := "not" notExpr | cmpExpr.
func (p *parser) parseNot() (node, error) {
	if p.cur().kind == tkNot {
		t := p.next()

		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return unaryNode{op: "not", operand: operand, base: at(t.off)}, nil
	}

	return p.parseComparison()
}

// parseComparison: cmpExpr := addExpr [ cmpOp addExpr ] — a single,
// non-associative comparison (chaining "a < b < c" is a syntax error).
func (p *parser) parseComparison() (node, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}

	c := p.cur()
	if c.kind != tkOp {
		return left, nil
	}

	if _, ok := comparisonOps[c.text]; !ok {
		return left, nil
	}

	p.next()

	right, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}

	return binNode{op: c.text, left: left, right: right, base: at(c.off)}, nil
}

// parseAdditive: addExpr := mulExpr { ("+"|"-") mulExpr }.
func (p *parser) parseAdditive() (node, error) {
	return p.parseBinaryChain(
		p.parseMultiplicative, map[string]struct{}{"+": {}, "-": {}})
}

// parseMultiplicative: mulExpr := unaryExpr { ("*"|"/"|"%") unaryExpr }.
func (p *parser) parseMultiplicative() (node, error) {
	return p.parseBinaryChain(
		p.parseUnary, map[string]struct{}{"*": {}, "/": {}, "%": {}})
}

// parseBinaryChain folds a left-associative chain of same-level binary
// operators over the next-tighter level.
func (p *parser) parseBinaryChain(
	sub func() (node, error),
	ops map[string]struct{},
) (node, error) {
	left, err := sub()
	if err != nil {
		return nil, err
	}

	for {
		c := p.cur()
		if c.kind != tkOp {
			return left, nil
		}

		if _, ok := ops[c.text]; !ok {
			return left, nil
		}

		p.next()

		right, err := sub()
		if err != nil {
			return nil, err
		}

		left = binNode{op: c.text, left: left, right: right, base: at(c.off)}
	}
}

// parseUnary: unaryExpr := "-" unaryExpr | primary.
func (p *parser) parseUnary() (node, error) {
	if c := p.cur(); c.kind == tkOp && c.text == "-" {
		p.next()

		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return unaryNode{op: "-", operand: operand, base: at(c.off)}, nil
	}

	return p.parsePrimary()
}

// parsePrimary: a literal, a data reference, a builtin call or a
// parenthesized expression.
func (p *parser) parsePrimary() (node, error) {
	switch t := p.next(); t.kind {
	case tkNumber:
		return litNode{val: t.num, base: at(t.off)}, nil

	case tkString:
		return litNode{val: t.text, base: at(t.off)}, nil

	case tkTrue:
		return litNode{val: true, base: at(t.off)}, nil

	case tkFalse:
		return litNode{val: false, base: at(t.off)}, nil

	case tkNil:
		return litNode{val: nil, base: at(t.off)}, nil

	case tkLParen:
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if p.cur().kind != tkRParen {
			return nil, syntaxErr("expected ')'", p.cur().off)
		}

		p.next()

		return n, nil

	case tkRef:
		return p.parseRefOrCall(t)

	default:
		return nil, syntaxErr("expected an expression", t.off)
	}
}

// parseRefOrCall finishes a tkRef primary: followed by '(' it is a
// builtin call (unknown names are loud at parse time, FR-3); otherwise a
// data reference.
func (p *parser) parseRefOrCall(t token) (node, error) {
	if p.cur().kind != tkLParen {
		return refNode{path: t.text, base: at(t.off)}, nil
	}

	if _, ok := builtins[t.text]; !ok {
		return nil, syntaxErr(
			"unknown function "+t.text+" (builtins: has, len, time)",
			t.off)
	}

	p.next() // '('

	arg, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if p.cur().kind != tkRParen {
		return nil, syntaxErr("expected ')' after the argument",
			p.cur().off)
	}

	p.next()

	return callNode{name: t.text, arg: arg, base: at(t.off)}, nil
}
