package bpmn

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
)

// JUEL is what a Camunda file's expressions are written in, and it is not
// a language gobpm interprets. It is close enough to the engine's own text
// language to be REWRITTEN into it — the grammars share comparison, member
// access, index access, parentheses and literals, and differ in delimiters,
// in how booleans are spelled, and in one variable-access idiom.
//
// Rewriting is not interpreting. Everything this file cannot express in
// the target grammar is REFUSED by name; nothing is approximated. A
// translator that quietly dropped what it could not express would produce
// a condition that parses, evaluates, and routes the token the wrong way —
// a mis-executed process, discovered far from the import that admitted it.

// juelBuiltins are the calls that survive translation, because the target
// grammar has them too (lite's has/len/time). Any other call is a host
// function or a bean method: it lives in code the document cannot see, and
// there is nothing to rewrite it into.
var juelBuiltins = map[string]bool{
	"has":  true,
	"len":  true,
	"time": true,
}

// juelRefusedWords are JUEL spellings with no counterpart in the target
// grammar. They are listed so the refusal can name the construct rather
// than failing as a generic parse error somewhere downstream.
var juelRefusedWords = map[string]string{
	"empty": "the empty operator",
	"div":   "the word form of /",
	"mod":   "the word form of %",
	"eq":    "the word form of ==",
	"ne":    "the word form of !=",
	"lt":    "the word form of <",
	"gt":    "the word form of >",
	"le":    "the word form of <=",
	"ge":    "the word form of >=",
}

// juelVariableAccessor is the one implicit-object idiom that translates:
// the engine reads process data by name, so execution.getVariable("x")
// IS x. Every other execution.* member reaches into a runtime object this
// engine does not model.
const juelVariableAccessor = "execution.getVariable"

// isJUEL reports whether body is a JUEL expression by its delimiters —
// the syntactic tell ADR-024 §2.10 resolves by when a document
// declares no language, which is what nearly every modeler emits.
func isJUEL(body string) bool {
	b := strings.TrimSpace(body)

	return (strings.HasPrefix(b, "${") || strings.HasPrefix(b, "#{")) &&
		strings.HasSuffix(b, "}")
}

// translateJUEL rewrites a JUEL expression into the engine's text
// language, or reports the construct that stopped it.
func translateJUEL(body string) (string, error) {
	inner, err := juelBody(body)
	if err != nil {
		return "", err
	}

	var out strings.Builder

	for i := 0; i < len(inner); {
		text, n, err := juelToken(body, inner[i:])
		if err != nil {
			return "", err
		}

		out.WriteString(text)

		i += n
	}

	return strings.TrimSpace(collapseSpaces(out.String())), nil
}

// juelToken translates one lexeme at the head of s, returning its
// replacement and how many bytes it consumed. Splitting it out of the scan
// loop keeps each construct's rule readable on its own.
func juelToken(body, s string) (string, int, error) {
	r := s[0]

	switch {
	case r == '\'' || r == '"':
		// A string literal is copied verbatim. Operators and keywords
		// INSIDE one are data, not syntax: ${name == "a && b"} must keep
		// its "a && b" intact.
		return juelString(body, s)

	case r == '&' || r == '|':
		return juelBoolOp(s)

	case r == '!':
		// "!=" is a comparison the target grammar shares; a lone "!" is
		// negation, which it spells "not".
		if len(s) > 1 && s[1] == '=' {
			return "!=", 2, nil
		}

		return "not ", 1, nil

	case r == '?' || r == ':':
		return "", 0, juelRefusal(body,
			"a conditional (ternary) operator, which the target grammar has no form of")

	case isIdentStart(rune(r)):
		word, n := juelIdent(s)

		rendered, err := juelWord(body, word, s[n:])
		if err != nil {
			return "", 0, err
		}

		return rendered.text, n + rendered.skip, nil
	}

	return string(r), 1, nil
}

// juelBody strips the ${…} / #{…} delimiters, refusing a composite
// expression: "${a} and ${b}" is a string TEMPLATE, and a template is not
// a boolean condition however much it looks like one.
func juelBody(body string) (string, error) {
	b := strings.TrimSpace(body)

	if !isJUEL(b) {
		return "", juelRefusal(body, "no ${…} or #{…} delimiters")
	}

	inner := b[2 : len(b)-1]

	if strings.Contains(inner, "${") || strings.Contains(inner, "#{") {
		return "", juelRefusal(body,
			"more than one ${…} section, which is a string template rather than an expression")
	}

	return inner, nil
}

// juelString copies one quoted literal, returning it and its length.
//
// An unterminated literal is an ERROR rather than a zero-length read: the
// caller advances by the returned length, so answering 0 would spin
// forever on a malformed expression.
func juelString(body, s string) (string, int, error) {
	quote := s[0]

	for i := 1; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++

			continue
		}

		if s[i] == quote {
			return s[:i+1], i + 1, nil
		}
	}

	return "", 0, juelRefusal(body, "an unterminated string literal")
}

// juelBoolOp rewrites && and || into the target grammar's word forms.
func juelBoolOp(s string) (string, int, error) {
	switch {
	case strings.HasPrefix(s, "&&"):
		return " and ", 2, nil

	case strings.HasPrefix(s, "||"):
		return " or ", 2, nil
	}

	return "", 0, juelRefusal(s,
		fmt.Sprintf("a bitwise operator %q", s[:1]))
}

// juelIdent reads one identifier, including dotted member access, so
// "order.customer.tier" arrives whole.
func juelIdent(s string) (string, int) {
	i := 0
	for i < len(s) && (isIdentStart(rune(s[i])) || unicode.IsDigit(rune(s[i])) ||
		s[i] == '.') {
		i++
	}

	return s[:i], i
}

// rendering is one identifier's translation plus how much of what follows
// it consumed.
type rendering struct {
	text string
	skip int
}

// juelWord translates one identifier in context: a refused spelling, a
// call, the variable accessor, or a plain reference.
func juelWord(body, word, rest string) (rendering, error) {
	if why, refused := juelRefusedWords[word]; refused {
		return rendering{}, juelRefusal(body, why+" ("+word+")")
	}

	if word == "null" {
		return rendering{text: "nil"}, nil
	}

	if strings.HasPrefix(word, juelVariableAccessor) {
		name, n, err := juelVariable(body, rest)
		if err != nil {
			return rendering{}, err
		}

		return rendering{text: name, skip: n}, nil
	}

	// A call the target grammar does not have is host code the document
	// cannot see — a bean method or a function namespace.
	if strings.HasPrefix(strings.TrimLeft(rest, " "), "(") && !juelBuiltins[word] {
		return rendering{}, juelRefusal(body,
			fmt.Sprintf("a call to %q, which lives in host code rather than in the process's data", word))
	}

	return rendering{text: word}, nil
}

// juelVariable reads the ("name") of execution.getVariable and returns the
// bare name, which is how the engine addresses process data.
func juelVariable(body, rest string) (string, int, error) {
	open := strings.Index(rest, "(")
	closing := strings.Index(rest, ")")

	if open != 0 || closing < 0 {
		return "", 0, juelRefusal(body,
			"an execution.* member other than getVariable(\"name\")")
	}

	arg := strings.TrimSpace(rest[1:closing])
	if len(arg) < 2 || (arg[0] != '\'' && arg[0] != '"') {
		return "", 0, juelRefusal(body,
			"execution.getVariable with a non-literal name, which cannot be resolved at import")
	}

	return arg[1 : len(arg)-1], closing + 1, nil
}

// isIdentStart reports whether r can begin an identifier.
func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

// collapseSpaces squeezes the runs of spaces the rewrites introduce, so
// the output reads like something a person wrote.
func collapseSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}

	return s
}

// juelRefusal reports a construct the translator will not guess at,
// naming it and the expression it came from.
//
// Refusing is the whole design: an approximation here becomes a condition
// that evaluates to the wrong branch, and the process misbehaves somewhere
// with no trace back to the file that caused it.
func juelRefusal(body, construct string) error {
	return errs.New(
		errs.M("bpmn: cannot translate the JUEL expression %q into %s: it uses %s",
			strings.TrimSpace(body), lite.Language, construct),
		errs.C(errorClass, errs.InvalidParameter))
}
