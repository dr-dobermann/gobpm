package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// reviewers are the board members the Multi-Instance fans out over, and
// scoreFor is the score each one gives — derived from its loop index so the
// scoring task and the example's own expectation are the same rule, and can
// never drift apart.
var reviewers = []string{"Ann", "Bob", "Cara", "Dan"}

func scoreFor(i int) int { return 70 + i*5 }

// reviewerList returns the reviewers as the `any` slice the array value wants.
func reviewerList() []any {
	l := make([]any, 0, len(reviewers))
	for _, r := range reviewers {
		l = append(l, r)
	}

	return l
}

// wantScores is what the output collection must hold once every iteration has
// contributed: one score per reviewer, in reviewer order.
func wantScores() []int {
	w := make([]int, len(reviewers))
	for i := range reviewers {
		w[i] = scoreFor(i)
	}

	return w
}

// sameInts reports an error unless got holds exactly the ints in want, in
// order. The values arrive as `any` from the collection, so a wrong TYPE is a
// mismatch too, not a silent zero.
func sameInts(got []any, want []int) error {
	if len(got) != len(want) {
		return fmt.Errorf("got %d values %v, want %d %v",
			len(got), got, len(want), want)
	}

	for i, w := range want {
		if got[i] != w {
			return fmt.Errorf("got %v, want %v", got, want)
		}
	}

	return nil
}

// reportScores prints the `scores` collection assembled once every reviewer's
// iteration completed (the visibility barrier), and their average.
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

	// The output collection is the demonstration: every reviewer's iteration
	// must have contributed its score, and the collection must be assembled
	// at the visibility barrier rather than partially. A run that lost one
	// reviewer's output — or ran fewer instances than there are reviewers —
	// would print a shorter list and still exit 0.
	if err := sameInts(scores, wantScores()); err != nil {
		return fmt.Errorf("output collection: %w", err)
	}

	fmt.Printf("\n  completed — scores: %v (average %d)\n",
		scores, sum/len(scores))

	return nil
}
