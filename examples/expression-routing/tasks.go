package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// printTask builds a ServiceTask whose Go functor prints msg.
func printTask(
	ran *pathSet, name, msg string,
) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			ran.mark(name)

			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	task, err := activities.NewServiceTask(name, op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create %s task: %w", name, err)
	}

	return task, nil
}

// eurRateOK is the ##GoExpr side of the mix: a functor condition reading
// the rates map through the same structural-path resolver the lite
// engine rides — rates["EUR"] < 1.2, in Go.
func eurRateOK() data.FormalExpression {
	return goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, `rates["EUR"]`)
			if err != nil {
				return nil, err
			}

			rate, _ := d.Value().Get(ctx).(float64)

			return values.NewVariable(rate < 1.2), nil
		})
}

// approveTask builds the UserTask whose ASSIGNEE is computed by a lite
// STRING expression over process data: order.customer.tier + "-manager"
// resolves to "vip-manager" — only that actor may complete the task.
func approveTask() (*activities.UserTask, error) {
	assignee, err := lite.Expr(`order.customer.tier + "-manager"`)
	if err != nil {
		return nil, fmt.Errorf("assignee expression: %w", err)
	}

	form, err := consinp.NewRenderer(
		consinp.WithStringInput("decision", "Approve the urgent order?"),
		consinp.WithSource(bytes.NewBufferString("approved\n")),
	)
	if err != nil {
		return nil, fmt.Errorf("form renderer: %w", err)
	}

	ut, err := activities.NewUserTask("approve",
		activities.WithAssigneeExpr(assignee),
		activities.WithRenderer(form),
		activities.WithOutput("decision", "string", true),
		activities.WithoutParams(),
	)
	if err != nil {
		return nil, fmt.Errorf("approve task: %w", err)
	}

	return ut, nil
}
