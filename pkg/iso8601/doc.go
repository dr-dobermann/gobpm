// Package iso8601 parses the ISO 8601 forms BPMN uses for timer event
// definitions: a date-time for timeDate, a duration for timeDuration, and a
// bounded repeating interval for timeCycle (BPMN 2.0.2 §10.5.5, Table 10.101).
//
// It exists because the standard library covers only one of the three:
// time.Parse handles the RFC 3339 profile, but time.ParseDuration reads Go's
// own "10h" syntax and rejects every ISO form (P10D, PT10H, P2W). The parser is
// hand-written over the standard library so the core module keeps its
// stdlib-only dependency rule (SAD-001 G2).
//
// # Why it is public
//
// It sits under pkg/ rather than internal/ because its consumers do: the
// timer constructors live in pkg/model/events, which MUST NOT import internal
// (ADR-003 §4.4, enforced by depguard), and the BPMN converter will need the
// same grammars to read timer attributes out of XML. A parser of a stable
// international standard is a safe thing to expose.
//
// # Strictness
//
// Every function REJECTS rather than approximates or normalizes. A parser that
// silently accepts an ambiguous or meaningless value merely moves the failure
// somewhere harder to diagnose, so:
//
//   - Zero and negative values are refused. A timer that fires after no time
//     is not a timer.
//   - The year and month designators are refused: they are not fixed-length,
//     and Go's calendar arithmetic normalizes overflow rather than clamping
//     (2026-01-31 plus one month is March 3), so honoring them would require
//     inventing a truncation policy the standard does not specify.
//   - The week designator does not combine with other fields, per ISO.
//   - Fractional components are refused rather than truncated.
//   - Designators are uppercase, as ISO specifies.
//   - An unbounded recurrence (R/PT10H) is refused: nothing in the engine can
//     consume one safely (SRD-077 §4.6).
//
// Each rejection names what it refused, so the caller can repair the input
// rather than guess at it.
package iso8601
