package bpmn

import (
	"encoding/xml"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// nsCamunda is the one vendor dialect this converter recognizes (ADR-024
// v.4 §2.14). Recognizing it is what lets the importer MAP the parts the
// model can hold and REPORT the parts it cannot; an unrecognized
// namespace stays silent, because a converter cannot report on a
// vocabulary it does not know.
const nsCamunda = "http://camunda.org/schema/1.0/bpmn"

// Camunda attribute names the converter maps. Anything else in the
// dialect is reported — including an attribute added after this was
// written, because the reporter works by DISCOVERY over the element's
// actual attributes rather than from a list of names to look for. A list
// would silently drop whatever it had not heard of, which is the failure
// this whole mechanism exists to prevent.
const (
	camundaAssignee        = "assignee"
	camundaCandidateUsers  = "candidateUsers"
	camundaCandidateGroups = "candidateGroups"
	camundaType            = "type"
	// The name comes from the observability vocabulary because the two
	// collide by spelling and the repo enforces the constant wherever it
	// appears (internal/lintcfg TestNoLiteralAttrKeys). They are NOT the
	// same thing — Camunda fixes this attribute name, a log key can be
	// renamed — so TestCamundaTopicMatchesTheDialect pins the equality.
	camundaTopic       = observability.AttrTopic
	camundaDecisionRef = "decisionRef"
)

// camundaExternal is the camunda:type value that makes a service task an
// external-worker task, which the model expresses as a topic.
const camundaExternal = "external"

// dialectReasons explains, per construct, why a recognized Camunda
// attribute is not mapped. The reason travels to the host in the import
// report, so it has to say something a modeler can act on rather than
// "unsupported".
var dialectReasons = map[string]string{
	"class":              "names a JVM class; a Go engine has no way to load it — model the behavior as a ServiceTask Operation",
	"delegateExpression": "resolves a JVM bean; a Go engine has no bean container — model the behavior as a ServiceTask Operation",
	"expression":         "invokes a JVM expression; use a gobpm:lite condition or a Go Operation",
	"resultVariable":     "names where a JVM delegate's result lands; a Go Operation returns its result directly",
	"executionListener":  "runs host code at a lifecycle point the engine does not expose to documents",
	"taskListener":       "runs host code at a task lifecycle point the engine does not expose to documents",
	"asyncBefore":        "is a job-executor boundary; this engine schedules differently and has no equivalent",
	"asyncAfter":         "is a job-executor boundary; this engine schedules differently and has no equivalent",
	"exclusive":          "is a job-executor scheduling hint with no equivalent here",
	"jobPriority":        "orders another engine's job executor; this engine has none",
	"failedJobRetryTimeCycle": "is expressible as a retry policy (R3/PT5M is 3 attempts 5 minutes apart), " +
		"but reading it needs an ISO-8601 recurrence parser that today exists only unexported in the model layer; " +
		"set the policy on the task in Go until that parser is shared",
	"formKey":                "names a form the engine does not render; supply a Renderer on the UserTask",
	"formData":               "declares form fields the engine does not render; supply a Renderer on the UserTask",
	"properties":             "carries vendor key/value pairs the model has nowhere to put",
	"property":               "carries a vendor key/value pair the model has nowhere to put",
	"inputOutput":            "maps variables in the vendor's own dialect; use the standard's data associations",
	"inputParameter":         "maps a variable in the vendor's own dialect; use the standard's data associations",
	"outputParameter":        "maps a variable in the vendor's own dialect; use the standard's data associations",
	"versionTag":             "labels a deployment; this engine versions a definition by its process id",
	"historyTimeToLive":      "configures another engine's history cleanup",
	"candidateStarterUsers":  "authorizes who may start a definition, which is the host's concern here",
	"candidateStarterGroups": "authorizes who may start a definition, which is the host's concern here",
}

// dialectReason returns why construct is not mapped, falling back to a
// truthful generic when the construct is one this converter has never
// heard of — which is exactly when reporting matters most.
func dialectReason(construct string) string {
	if why, ok := dialectReasons[construct]; ok {
		return why
	}

	return "is a vendor construct this converter does not map"
}

// nsAttrValue returns the value of a namespaced attribute.
//
// attrValue deliberately matches only UNPREFIXED attributes, and every
// caller of it depends on that — a BPMN `id` must never be satisfied by
// some vendor's `id`. So the dialect gets its own reader rather than a
// loosened shared one.
func nsAttrValue(se xml.StartElement, ns, local string) string {
	for _, a := range se.Attr {
		if a.Name.Space == ns && a.Name.Local == local {
			return a.Value
		}
	}

	return ""
}

// camundaOptions maps the dialect attributes of one element onto model
// options, and reports every other Camunda attribute it finds.
//
// The mapped set is small on purpose (ADR-024 v.4 §2.14 rule 1): the
// dialect never motivates a new model type, so what has no home is
// reported rather than accommodated.
func (p *parser) camundaOptions(se xml.StartElement, id string) []options.Option {
	var opts []options.Option

	mapped := map[string]bool{}

	claim := func(name string) string {
		v := strings.TrimSpace(nsAttrValue(se, nsCamunda, name))
		if v != "" {
			mapped[name] = true
		}

		return v
	}

	if v := claim(camundaAssignee); v != "" {
		opts = append(opts, activities.WithAssignee(v))
	}

	if v := claim(camundaCandidateUsers); v != "" {
		opts = append(opts, activities.WithCandidateUsers(splitList(v)...))
	}

	if v := claim(camundaCandidateGroups); v != "" {
		opts = append(opts, activities.WithCandidateGroups(splitList(v)...))
	}

	// An external-worker service task is a topic in the model. The two
	// attributes are one fact, so both are claimed together.
	if strings.EqualFold(claim(camundaType), camundaExternal) {
		if topic := claim(camundaTopic); topic != "" {
			opts = append(opts, activities.WithWorker(topic))
		}
	}

	p.reportUnmappedAttrs(se, id, mapped)

	return opts
}

// reportUnmappedAttrs reports every Camunda attribute on se that nothing
// claimed. It walks the element's ACTUAL attributes, so a construct added
// to the dialect after this code was written is reported rather than
// dropped.
func (p *parser) reportUnmappedAttrs(
	se xml.StartElement, id string, mapped map[string]bool,
) {
	for _, a := range se.Attr {
		if a.Name.Space != nsCamunda || mapped[a.Name.Local] {
			continue
		}

		p.report(id, "camunda:"+a.Name.Local, dialectReason(a.Name.Local))
	}
}

// report records one construct the converter recognized and did not map.
func (p *parser) report(element, construct, reason string) {
	p.dropped = append(p.dropped, convert.Dropped{
		Element:   element,
		Construct: construct,
		Reason:    reason,
	})
}

// splitList splits Camunda's comma-separated id lists.
func splitList(v string) []string {
	parts := strings.Split(v, ",")

	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
