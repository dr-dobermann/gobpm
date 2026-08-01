package data

import (
	"context"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// BodyHolder is the capability a text-carrying FormalExpression implements
// (ADR-032 §2.2): engines that interpret source text assert it to reach
// the body. The functor kind deliberately does not implement it — its
// logic is the closure, not a text.
type BodyHolder interface {
	// Body returns the expression's source text.
	Body() string
}

// TextExpression is the text FormalExpression kind (ADR-032 §2.2): it
// carries a required language URI and source body for the routed
// expression engine to interpret, and refuses self-evaluation — with the
// engine registry owning interpretation, a text expression evaluated
// outside it would silently bypass language routing.
type TextExpression struct {
	language   string
	body       string
	resultType string

	foundation.BaseElement
}

// interface checks
var (
	_ FormalExpression = (*TextExpression)(nil)
	_ BodyHolder       = (*TextExpression)(nil)
)

// textExprConfig collects the construction options.
type textExprConfig struct {
	resultType string
	baseOpts   []options.Option
}

// textExprOption is a TextExpression-specific construction option (the
// house marker-interface idiom: dispatched by the constructor's
// type-switch, called directly).
type textExprOption func(*textExprConfig) error

// Option marks textExprOption as an options.Option.
func (textExprOption) Option() {}

// WithResultType declares the expression's result-type name (the
// programmatic analog of evaluatesToTypeRef).
func WithResultType(rt string) options.Option {
	return textExprOption(func(c *textExprConfig) error {
		rt = strings.TrimSpace(rt)
		if rt == "" {
			return errs.New(
				errs.M("WithResultType: an empty result type isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.resultType = rt

		return nil
	})
}

// NewTextExpression creates a text FormalExpression in the given
// expression language. Both the language and the body are required — the
// metamodel carries language as 0..1 for interchange inheritance, but a
// programmatic text expression always knows its language (fail-fast,
// ADR-032 §2.2).
func NewTextExpression(
	language, body string,
	opts ...options.Option,
) (*TextExpression, error) {
	language = strings.TrimSpace(language)
	if language == "" {
		return nil, errs.New(
			errs.M("NewTextExpression: an empty language isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errs.New(
			errs.M("NewTextExpression: an empty body isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	cfg := textExprConfig{}

	for _, opt := range opts {
		switch o := opt.(type) {
		case textExprOption:
			if err := o(&cfg); err != nil {
				return nil, err
			}

		case foundation.BaseOption:
			cfg.baseOpts = append(cfg.baseOpts, opt)

		default:
			return nil, errs.New(
				errs.M("NewTextExpression: invalid option type"),
				errs.C(errorClass, errs.InvalidObject))
		}
	}

	be, err := foundation.NewBaseElement(cfg.baseOpts...)
	if err != nil {
		return nil, err
	}

	return &TextExpression{
		language:    language,
		body:        body,
		resultType:  cfg.resultType,
		BaseElement: *be,
	}, nil
}

// Language returns the expression-language URI.
func (te *TextExpression) Language() string {
	return te.language
}

// Body returns the expression's source text.
func (te *TextExpression) Body() string {
	return te.body
}

// ResultType returns the declared result-type name (empty when
// undeclared).
func (te *TextExpression) ResultType() string {
	return te.resultType
}

// Evaluate refuses self-evaluation: a text expression is interpreted by
// the routed expression engine (ADR-032 §2.2) — evaluating it directly
// would bypass language routing.
func (te *TextExpression) Evaluate(context.Context, Source) (Value, error) {
	return nil, errs.New(
		errs.M("a text expression evaluates through the engine registry "+
			"(language %q)", te.language),
		errs.C(errorClass, errs.InvalidState),
		errs.D(observability.AttrExpressionID, te.ID()))
}

// Result refuses like Evaluate — the text kind holds no evaluation state.
func (te *TextExpression) Result() (Value, error) {
	return nil, errs.New(
		errs.M("a text expression holds no result — it evaluates through "+
			"the engine registry"),
		errs.C(errorClass, errs.InvalidState),
		errs.D(observability.AttrExpressionID, te.ID()))
}

// IsEvaluated always reports false — see Result.
func (te *TextExpression) IsEvaluated() bool {
	return false
}
