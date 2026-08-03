package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/iso8601"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// TimerForm names which BPMN timer attribute an ISO 8601 expression feeds
// (§10.5.5, Table 10.101). A literal string carries its own form — R… is a
// cycle, P… a duration, anything else a date — but an EXPRESSION does not:
// its value does not exist until the timer arms, while the attribute it fills
// is fixed when the process is built. BPMN settles this the same way, by
// making the element name static and the expression inside it dynamic.
type TimerForm string

const (
	// Time fills timeDate: an absolute instant, fired once.
	Time TimerForm = "time"

	// Duration fills timeDuration: a relative delay, fired once.
	Duration TimerForm = "duration"

	// Cycle fills timeCycle AND timeDuration together, from one recurrence
	// expression — the engine carries a recurrence as (count, interval).
	Cycle TimerForm = "cycle"
)

// NewISO8601Timer builds a TimerEventDefinition from ONE ISO 8601 string,
// disassembling it into the timer attributes the engine carries.
//
//	"2011-03-11T12:13:14Z" → timeDate
//	"P10D"                 → timeDuration
//	"R3/PT10H"             → timeCycle (3) + timeDuration (10h)
//
// The three grammars are disjoint, so the form is detected from the text. The
// result is an ordinary TimerEventDefinition: it passes the same validation and
// drives the same waiter as one built positionally with
// NewTimerEventDefinition.
//
// For a timer whose value depends on instance data, use NewISO8601TimerExpr.
func NewISO8601Timer(
	s string,
	baseOpts ...options.Option,
) (*TimerEventDefinition, error) {
	switch form := formOf(s); form {
	case Cycle:
		r, err := iso8601.ParseRepeat(s)
		if err != nil {
			return nil, isoErr(s, err)
		}

		return NewTimerEventDefinition(nil,
			constExpr(s+"-count", r.Count),
			constExpr(s+"-interval", r.Interval), baseOpts...)

	case Duration:
		d, err := iso8601.ParseDuration(s)
		if err != nil {
			return nil, isoErr(s, err)
		}

		return NewTimerEventDefinition(nil, nil,
			constExpr(s+"-after", d), baseOpts...)

	default:
		t, err := iso8601.ParseDateTime(s)
		if err != nil {
			return nil, isoErr(s, err)
		}

		return NewTimerEventDefinition(
			constExpr(s+"-at", t), nil, nil, baseOpts...)
	}
}

// MustISO8601Timer is the panic-on-error twin of NewISO8601Timer, for static
// wiring in tests and examples.
func MustISO8601Timer(
	s string,
	baseOpts ...options.Option,
) *TimerEventDefinition {
	ted, err := NewISO8601Timer(s, baseOpts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return ted
}

// NewISO8601TimerExpr builds a TimerEventDefinition whose timing is decided
// PER INSTANCE: e evaluates to an ISO 8601 string when the timer arms, and form
// says which attribute that string feeds.
//
// The expression is evaluated against the instance's data, so a deadline can
// come from the process itself — an SLA read off the order, a due date carried
// on the case. Because the value is unknown until then, a malformed string
// fails at ARM time rather than at construction, reported as an ordinary
// expression failure naming the offending value.
func NewISO8601TimerExpr(
	form TimerForm,
	e data.FormalExpression,
	baseOpts ...options.Option,
) (*TimerEventDefinition, error) {
	if e == nil {
		return nil, errs.New(
			errs.M("NewISO8601TimerExpr: a nil expression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	switch form {
	case Time:
		return NewTimerEventDefinition(
			isoAdapter(e, Time, dateOf), nil, nil, baseOpts...)

	case Duration:
		return NewTimerEventDefinition(nil, nil,
			isoAdapter(e, Duration, durationOf), baseOpts...)

	case Cycle:
		// One recurrence string feeds BOTH attributes, so each gets its own
		// adapter over the same expression. Re-parsing per attribute keeps the
		// two honest — no state is shared between them, and arming happens
		// once per activation.
		return NewTimerEventDefinition(nil,
			isoAdapter(e, Cycle, countOf),
			isoAdapter(e, Cycle, intervalOf), baseOpts...)
	}

	return nil, errs.New(
		errs.M("NewISO8601TimerExpr: unknown timer form %q — use Time, "+
			"Duration or Cycle", string(form)),
		errs.C(errorClass, errs.InvalidParameter))
}

// MustISO8601TimerExpr is the panic-on-error twin of NewISO8601TimerExpr.
func MustISO8601TimerExpr(
	form TimerForm,
	e data.FormalExpression,
	baseOpts ...options.Option,
) *TimerEventDefinition {
	ted, err := NewISO8601TimerExpr(form, e, baseOpts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return ted
}
