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
func resolveLanguage(declared, docDefault string) (exprLang, string) {
	lang := strings.TrimSpace(declared)
	if lang == "" {
		lang = strings.TrimSpace(docDefault)
	}

	if lang == "" {
		// Nothing declared anywhere. The syntactic tell that recognizes
		// JUEL here arrives with the translator; until then an undeclared
		// expression is refused by the same path as an unknown one.
		return langRefused, ""
	}

	return languages[lang], lang
}

// newCondition builds a sequence flow's condition, or reports why it
// cannot be built. docLang is the document's expressionLanguage, which an
// expression declaring none inherits.
func newCondition(fs flowSpec, docLang string) (data.FormalExpression, error) {
	kind, lang := resolveLanguage(fs.condLang, docLang)

	if kind != langLite {
		return nil, unsupportedLanguage(fs.id, lang)
	}

	id := fs.condID
	if id == "" {
		id = fs.id + ":condition"
	}

	// lite.Cond, not a hand-rolled TextExpression: it is the library's own
	// way to mint a condition, and it already declares the bool result the
	// condition paths require before evaluating. Duplicating that here
	// would be a second place for the requirement to drift from.
	return lite.Cond(fs.condBody, foundation.WithID(id))
}

// unsupportedLanguage reports an expression the converter cannot make
// runnable, naming the language so the modeler knows what to change.
func unsupportedLanguage(flowID, lang string) error {
	named := lang
	if named == "" {
		named = "(none declared, and none inferable)"
	}

	return errs.New(
		errs.M("bpmn: sequenceFlow %q carries a condition in %s, which this "+
			"converter cannot evaluate; %s is the supported language",
			flowID, named, lite.Language),
		errs.C(errorClass, errs.InvalidParameter))
}
