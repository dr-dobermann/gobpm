package main

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// tableJSON is the deployed decision artifact: structure only — the rule
// grid, the policy and the names of the Go behavior below.
//
//go:embed table.json
var tableJSON []byte

// buildEngine assembles the Decision Table component: a vocabulary of named
// Go functors + the embedded artifact deployed through the Decoder seam.
func buildEngine() (*dtable.Engine, error) {
	vocab := dtable.NewVocabulary()

	if err := vocab.AddCondition("vip",
		dtable.Eq("tier", "vip")); err != nil {
		return nil, fmt.Errorf("vocabulary: %w", err)
	}

	if err := vocab.AddCondition("big-order",
		dtable.GT("total", 100)); err != nil {
		return nil, fmt.Errorf("vocabulary: %w", err)
	}

	if err := vocab.AddYield("default-discount",
		func(context.Context, service.DataReader) (rules.Row, error) {
			fmt.Println("  [decision] fallthrough rule -> retail rate")

			return rules.Row{
				"discount_pct": values.NewVariable(float64(5)),
			}, nil
		}); err != nil {
		return nil, fmt.Errorf("vocabulary: %w", err)
	}

	dec, err := dtable.NewJSONDecoder(vocab)
	if err != nil {
		return nil, fmt.Errorf("decoder: %w", err)
	}

	engine, err := dtable.New(dtable.WithDecoder(dec))
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}

	if err := engine.Deploy(context.Background(), tableJSON); err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}

	return engine, nil
}
