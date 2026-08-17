package bpmn

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// nsXPath is the expressionLanguage the BPMN schema defaults to
// (elements/foundation.md:19). The converter refuses it — see
// resolveLanguage.
const nsXPath = "http://www.w3.org/1999/XPath"

// exprLang is what the converter does with an expression written in a
// given language.
type exprLang uint8

const (
	// langRefused is the zero value: a language the converter has no
	// engine for is reported, never carried inert. An expression carried
	// inert imports fine and faults at the first decision that needs it,
	// which is the failure this policy exists to move earlier.
	langRefused exprLang = iota

	// langLite passes through as the model's text-expression kind, for
	// the engine that already interprets it.
	langLite

	// langJUEL is rewritten into the text language (see juel.go). It is
	// reached by the syntactic tell as often as by a declaration, because
	// a Camunda file declares no expression language at all.
	langJUEL
)

// languages maps a declared expression language to its policy. A language
// absent from the table is refused, so a new language cannot be accepted
// by omission.
//
// XPath is listed explicitly because it is the SCHEMA DEFAULT: a document
// that declares nothing is, by the letter of the standard, written in
// XPath. Honoring that literally would refuse nearly every real file, so
// resolveLanguage treats "declared nothing" and "declared XPath"
// differently — the entry here refuses only an EXPLICIT declaration.
var languages = map[string]exprLang{
	lite.Language: langLite,
	nsXPath:       langRefused,
}

// resolveLanguage decides which language an expression is written in:
// the expression's own `language`, else the document's
// `expressionLanguage`, else nothing declared at all.
//
// It returns the effective language name for diagnostics alongside the
// policy, so a refusal can name what it refused rather than saying
// "unsupported".
func resolveLanguage(declared, docDefault, body string) (exprLang, string) {
	lang := strings.TrimSpace(declared)
	if lang == "" {
		lang = strings.TrimSpace(docDefault)
	}

	if lang == "" {
		// Nothing declared anywhere — so the expression's own shape is the
		// only evidence left. ${…} means JUEL, which is what a Camunda file
		// carries and never labels.
		if isJUEL(body) {
			return langJUEL, "JUEL (by its ${…} delimiters)"
		}

		return langRefused, ""
	}

	// A declared language still yields to the delimiters: a document that
	// says XPath and writes ${…} is a Camunda file with a schema default
	// nobody edited, and refusing it on its own mislabelling helps no one.
	if isJUEL(body) {
		return langJUEL, "JUEL (by its ${…} delimiters)"
	}

	return languages[lang], lang
}

// exprSpec is one expression as a document wrote it, detached from
// whatever element carried it.
//
// A sequence flow's <conditionExpression> is one; so is the condition of
// a <conditionalEventDefinition> and the instant of a <timeDate>, neither
// of which belongs to a flow. The expression layer is about a body and a
// language, and taking a flowSpec would make every non-flow caller
// invent one.
type exprSpec struct {
	// ownerKind and ownerID name the element carrying the expression, so
	// a refusal points at something a reader can find in the file.
	ownerKind string
	ownerID   string
	// role is what that element calls this expression — "condition",
	// "timeDate". It names the expression in a refusal and supplies the
	// id when the expression declared none.
	role string
	// id is the expression's own id, empty when it declared none.
	id   string
	lang string
	body string
}

// exprID is the id to mint the expression under: its own, or one derived
// from its owner when BPMN's optional id is absent.
func (s exprSpec) exprID() string {
	if s.id != "" {
		return s.id
	}

	return s.ownerID + ":" + s.role
}

// runnableBody resolves which language an expression is written in and
// returns the body to mint, translating JUEL on the way. docLang is the
// document's expressionLanguage, which an expression declaring none
// inherits.
func runnableBody(s exprSpec, docLang string) (string, error) {
	kind, lang := resolveLanguage(s.lang, docLang, s.body)

	switch kind {
	case langLite:
		return s.body, nil

	case langJUEL:
		return translateJUEL(s.body)
	}

	return "", unsupportedLanguage(s, lang)
}

// newBoolExpression mints an expression that must evaluate to a boolean:
// a sequence flow's condition, or a conditional event definition's.
//
// lite.Cond, not a hand-rolled TextExpression: it is the library's own
// way to mint a condition, and it already declares the bool result the
// condition paths require before evaluating. Duplicating that here would
// be a second place for the requirement to drift from.
func newBoolExpression(s exprSpec, docLang string) (data.FormalExpression, error) {
	body, err := runnableBody(s, docLang)
	if err != nil {
		return nil, err
	}

	return lite.Cond(body, foundation.WithID(s.exprID()))
}

// newCondition builds a sequence flow's condition, or reports why it
// cannot be built.
func newCondition(fs flowSpec, docLang string) (data.FormalExpression, error) {
	return newBoolExpression(exprSpec{
		ownerKind: tagSequenceFlow,
		ownerID:   fs.id,
		role:      "condition",
		id:        fs.condID,
		lang:      fs.condLang,
		body:      fs.condBody,
	}, docLang)
}

// unsupportedLanguage reports an expression the converter cannot make
// runnable, naming the language so the modeler knows what to change.
func unsupportedLanguage(s exprSpec, lang string) error {
	named := lang
	if named == "" {
		named = "(none declared, and none inferable)"
	}

	return errs.New(
		errs.M("bpmn: %s %q carries a %s in %s, which this converter cannot "+
			"evaluate; %s is the supported language",
			s.ownerKind, s.ownerID, s.role, named, lite.Language),
		errs.C(errorClass, errs.InvalidParameter))
}
