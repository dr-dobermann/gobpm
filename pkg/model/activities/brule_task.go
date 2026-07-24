package activities

import (
	"context"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// BusinessRuleTask is a BPMN business rule task (§13.3.3): on activation it
// calls the decision named by its decision reference on the configured
// Business Rule Engine and completes on the call's return, committing the
// result to process data (ADR-027 v.1). The reference is opaque to the task —
// the engine wired at thresher construction resolves it (a registered name
// for the in-core gorules registry, a DMN decision id/key for an external
// engine), so the same model runs under whichever engine the embedder chose.
type BusinessRuleTask struct {
	decisionRef string

	task
}

// NewBusinessRuleTask creates a BusinessRuleTask evaluating decisionRef, with
// name and foundation/activity options.
func NewBusinessRuleTask(
	name, decisionRef string,
	opts ...options.Option,
) (*BusinessRuleTask, error) {
	decisionRef = strings.TrimSpace(decisionRef)
	if decisionRef == "" {
		return nil, errs.New(
			errs.M("NewBusinessRuleTask: an empty decision reference isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t, err := newTask(strings.TrimSpace(name), opts...)
	if err != nil {
		return nil, errs.New(
			errs.M("business rule task building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &BusinessRuleTask{
		decisionRef: decisionRef,
		task:        *t,
	}, nil
}

// DecisionRef returns the decision reference the task evaluates.
func (bt *BusinessRuleTask) DecisionRef() string {
	return bt.decisionRef
}

// ----------------------- flow.Node interface --------------------------------

// Node returns the BusinessRuleTask as a flow node.
func (bt *BusinessRuleTask) Node() flow.Node {
	return bt
}

// Clone returns a per-instance copy of the BusinessRuleTask (a fresh activity
// shell over the shared config).
func (bt *BusinessRuleTask) Clone() (flow.Node, error) {
	t, err := bt.clone()
	if err != nil {
		return nil, err
	}

	return &BusinessRuleTask{
		decisionRef: bt.decisionRef,
		task:        t,
	}, nil
}

// ------------------------ flow.Task interface -------------------------------

// TaskType returns the task type for BusinessRuleTask.
func (bt *BusinessRuleTask) TaskType() flow.TaskType {
	return flow.BusinessRuleTask
}

// ----------------------exec.NodeExecutor interface --------------------------

// Exec calls the configured Business Rule Engine with the task's decision
// reference and commits the returned result rows to process data (the
// ADR-027 v.1 §2.3 semantics: call, complete, commit). re satisfies the
// narrow service.DataReader structurally (the ServiceTask precedent), so the
// decision reads exactly what an in-process Go operation reads. An
// evaluation error fails the task through the ordinary fault path; an empty
// result commits nothing.
func (bt *BusinessRuleTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if re == nil {
		return nil, errs.New(
			errs.M("BusinessRuleTask.Exec: a nil RuntimeEnvironment isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("business_rule_task_name", bt.Name()),
			errs.D("business_rule_task_id", bt.ID()))
	}

	eng := re.RuleEngine()

	rows, err := eng.Evaluate(ctx, bt.decisionRef, re)
	if err != nil {
		bt.reportDecision(re, observability.PhaseFailed, map[string]string{
			observability.AttrDecisionRef:    bt.decisionRef,
			observability.AttrImplementation: eng.Type(),
			observability.AttrError:          err.Error(),
		})

		return nil, err
	}

	resultVar := ""

	if len(rows) > 0 {
		resultVar, err = bt.commitResult(rows, re)
		if err != nil {
			return nil, err
		}
	}

	bt.reportDecision(re, observability.PhaseEvaluated, map[string]string{
		observability.AttrDecisionRef:    bt.decisionRef,
		observability.AttrImplementation: eng.Type(),
		observability.AttrRowCount:       strconv.Itoa(len(rows)),
		observability.AttrResultVariable: resultVar,
	})

	return bt.selectOutgoing(ctx, re)
}

// reportDecision announces a KindRules fact (SRD-060 FR-6): the decision-level
// audit record — reference, engine kind, result shape — names and counts only,
// never payload values (the masking rule).
func (bt *BusinessRuleTask) reportDecision(
	re renv.RuntimeEnvironment,
	phase observability.Phase,
	details map[string]string,
) {
	re.Reporter().Report(observability.Fact{
		Kind:     observability.KindRules,
		Phase:    phase,
		NodeID:   bt.ID(),
		NodeName: bt.Name(),
		Details:  details,
	})
}

// commitResult commits the decision result rows to process data through the
// execution frame with the ADR-027 §2.3 fold: exactly one row with exactly
// one output commits as a scalar named by that output; anything else commits
// as an array of row-maps named by the decision reference. It returns the
// committed variable's name for the Evaluated fact.
func (bt *BusinessRuleTask) commitResult(
	rows []rules.Row,
	re renv.RuntimeEnvironment,
) (string, error) {
	wrap := func(msg string, err error) error {
		return errs.New(
			errs.M(msg),
			errs.C(errorClass),
			errs.E(err),
			errs.D("business_rule_task_name", bt.Name()),
			errs.D("business_rule_task_id", bt.ID()),
			errs.D("decision_ref", bt.decisionRef))
	}

	name, value, err := foldResult(bt.decisionRef, rows)
	if err != nil {
		return "", wrap("couldn't fold decision result", err)
	}

	item, err := data.NewItemDefinition(value, foundation.WithID(name))
	if err != nil {
		return "", wrap("couldn't build decision result item", err)
	}

	iae, err := data.NewItemAwareElement(item, data.ReadyDataState)
	if err != nil {
		return "", wrap("couldn't wrap decision result", err)
	}

	res, err := data.NewParameter(name, iae)
	if err != nil {
		return "", wrap("couldn't build decision result parameter", err)
	}

	if err := re.Put(res); err != nil {
		return "", wrap("couldn't commit decision result", err)
	}

	return name, nil
}

// foldResult picks the committed variable's name and value: a 1-row/1-output
// result folds to the scalar under its output name; any other non-empty
// result becomes an array of row-maps under the decision reference.
func foldResult(
	decisionRef string,
	rows []rules.Row,
) (string, data.Value, error) {
	if len(rows) == 1 && len(rows[0]) == 1 {
		for name, v := range rows[0] {
			return name, v, nil
		}
	}

	rowVals := make([]data.Value, 0, len(rows))

	for _, row := range rows {
		m, err := values.NewMap[data.Value](row)
		if err != nil {
			return "", nil, err
		}

		rowVals = append(rowVals, m)
	}

	return decisionRef, values.NewArray[data.Value](rowVals...), nil
}

// ----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node         = (*BusinessRuleTask)(nil)
	_ flow.Task         = (*BusinessRuleTask)(nil)
	_ exec.NodeExecutor = (*BusinessRuleTask)(nil)
)
