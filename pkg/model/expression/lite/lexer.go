package lite

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// tokKind enumerates the lexer's token kinds.
type tokKind uint8

const (
	tkNumber tokKind = iota
	tkString
	tkRef // a bare name or a structural path ("order.items[0]")
	tkTrue
	tkFalse
	tkNil
	tkAnd
	tkOr
	tkNot
	tkOp // == != < <= > >= + - * / %
	tkLParen
	tkRParen
	tkEOF
)

// token is one lexeme with its byte offset in the body — every parse and
// evaluation error anchors to it (SRD-067 FR-1).
type token struct {
	text string
	num  float64
	off  int
	kind tokKind
}

// keywords maps bare identifiers onto their keyword kinds.
var keywords = map[string]tokKind{
	"true":  tkTrue,
	"false": tkFalse,
	"nil":   tkNil,
	"and":   tkAnd,
	"or":    tkOr,
	"not":   tkNot,
}

// syntaxErr builds the classified syntax error naming the byte offset.
func syntaxErr(msg string, off int) error {
	return errs.New(
		errs.M("syntax error: "+msg),
		errs.C(errorClass, errs.InvalidParameter),
		errs.D("offset", strconv.Itoa(off)))
}

// lex tokenizes the expression body.
func lex(body string) ([]token, error) {
	var tt []token

	i := 0
	for i < len(body) {
		c := body[i]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++

		case c >= '0' && c <= '9':
			t, j, err := lexNumber(body, i)
			if err != nil {
				return nil, err
			}

			tt = append(tt, t)
			i = j

		case c == '\'' || c == '"':
			t, j, err := lexString(body, i)
			if err != nil {
				return nil, err
			}

			tt = append(tt, t)
			i = j

		case isIdentStart(c):
			t, j, err := lexWord(body, i)
			if err != nil {
				return nil, err
			}

			tt = append(tt, t)
			i = j

		default:
			t, j, err := lexOperator(body, i)
			if err != nil {
				return nil, err
			}

			tt = append(tt, t)
			i = j
		}
	}

	return append(tt, token{kind: tkEOF, off: len(body)}), nil
}

// isIdentStart reports an identifier's first byte (ASCII letter or '_').
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart reports an identifier's continuation byte.
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// lexNumber scans an integer or decimal literal; every number is float64
// (the ADR-032 §2.3 numeric unification).
func lexNumber(body string, i int) (token, int, error) {
	j := i
	for j < len(body) && body[j] >= '0' && body[j] <= '9' {
		j++
	}

	if j < len(body) && body[j] == '.' {
		j++
		if j >= len(body) || body[j] < '0' || body[j] > '9' {
			return token{}, 0,
				syntaxErr("a digit must follow the decimal point", j)
		}

		for j < len(body) && body[j] >= '0' && body[j] <= '9' {
			j++
		}
	}

	n, err := strconv.ParseFloat(body[i:j], 64)
	if err != nil {
		return token{}, 0, syntaxErr("invalid number "+body[i:j], i)
	}

	return token{kind: tkNumber, num: n, text: body[i:j], off: i}, j, nil
}

// lexString scans a single- or double-quoted literal; '\' escapes the
// quote character and itself, nothing else.
func lexString(body string, i int) (token, int, error) {
	q := body[i]

	var sb []byte

	j := i + 1
	for j < len(body) {
		switch body[j] {
		case '\\':
			if j+1 >= len(body) ||
				(body[j+1] != q && body[j+1] != '\\') {
				return token{}, 0, syntaxErr(
					`only the quote and '\' may be escaped`, j)
			}

			sb = append(sb, body[j+1])
			j += 2

		case q:
			return token{kind: tkString, text: string(sb), off: i},
				j + 1, nil

		default:
			sb = append(sb, body[j])
			j++
		}
	}

	return token{}, 0, syntaxErr("unterminated string", i)
}

// lexWord scans an identifier and classifies it: a keyword stays bare; a
// data reference greedily extends through its structural-path segments
// ('.field', '[0]', '["key"]') so the whole path lands in src.Find as one
// name (SRD-067 §4.1).
func lexWord(body string, i int) (token, int, error) {
	j := i + 1
	for j < len(body) && isIdentPart(body[j]) {
		j++
	}

	if k, ok := keywords[body[i:j]]; ok {
		return token{kind: k, text: body[i:j], off: i}, j, nil
	}

	j, err := lexPathSteps(body, j)
	if err != nil {
		return token{}, 0, err
	}

	return token{kind: tkRef, text: body[i:j], off: i}, j, nil
}

// lexPathSteps extends a data reference through '.field' and '[...]'
// segments, returning the end offset.
func lexPathSteps(body string, j int) (int, error) {
	for j < len(body) {
		switch {
		case body[j] == '.' && j+1 < len(body) && isIdentStart(body[j+1]):
			j += 2
			for j < len(body) && isIdentPart(body[j]) {
				j++
			}

		case body[j] == '[':
			end, err := lexBracket(body, j)
			if err != nil {
				return 0, err
			}

			j = end

		default:
			return j, nil
		}
	}

	return j, nil
}

// lexBracket scans one '[index]' or '["key"]' path segment.
func lexBracket(body string, j int) (int, error) {
	k := j + 1
	if k >= len(body) {
		return 0, syntaxErr("unterminated '[' path segment", j)
	}

	switch c := body[k]; {
	case c >= '0' && c <= '9':
		for k < len(body) && body[k] >= '0' && body[k] <= '9' {
			k++
		}

	case c == '\'' || c == '"':
		q := c
		k++
		for k < len(body) && body[k] != q {
			k++
		}

		if k >= len(body) {
			return 0, syntaxErr("unterminated key in a path segment", j)
		}

		k++

	default:
		return 0, syntaxErr(
			"a path segment needs an index or a quoted key", k)
	}

	if k >= len(body) || body[k] != ']' {
		return 0, syntaxErr("a path segment must close with ']'", k)
	}

	return k + 1, nil
}

// lexOperator scans punctuation and operator lexemes.
func lexOperator(body string, i int) (token, int, error) {
	two := func(text string) (token, int, error) {
		return token{kind: tkOp, text: text, off: i}, i + 2, nil
	}
	one := func(kind tokKind, text string) (token, int, error) {
		return token{kind: kind, text: text, off: i}, i + 1, nil
	}

	next := byte(0)
	if i+1 < len(body) {
		next = body[i+1]
	}

	switch c := body[i]; c {
	case '=':
		if next != '=' {
			return token{}, 0, syntaxErr("'=' must be '=='", i)
		}

		return two("==")

	case '!':
		if next != '=' {
			return token{}, 0, syntaxErr("'!' must be '!='", i)
		}

		return two("!=")

	case '<', '>':
		if next == '=' {
			return two(string(c) + "=")
		}

		return one(tkOp, string(c))

	case '+', '-', '*', '/', '%':
		return one(tkOp, string(c))

	case '(':
		return one(tkLParen, "(")

	case ')':
		return one(tkRParen, ")")

	default:
		return token{}, 0,
			syntaxErr("unexpected character "+strconv.QuoteRune(rune(c)), i)
	}
}
