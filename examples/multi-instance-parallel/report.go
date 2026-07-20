package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// reportScores prints the `scores` collection assembled once every reviewer's
// instance completed (the visibility barrier), and their average.
func reportScores(ctx context.Context, r service.DataReader) error {
	d, err := r.GetData("scores")
	if err != nil {
		return fmt.Errorf("read scores collection: %w", err)
	}

	col, ok := d.Value().(data.Collection)
	if !ok {
		return fmt.Errorf("scores is not a collection")
	}

	scores := col.GetAll(ctx)

	sum := 0
	for _, s := range scores {
		if v, ok := s.(int); ok {
			sum += v
		}
	}

	fmt.Printf("\n  completed — scores: %v (average %d)\n",
		scores, sum/len(scores))

	return nil
}
