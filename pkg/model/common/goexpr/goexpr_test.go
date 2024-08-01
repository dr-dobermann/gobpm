package goexpr_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

func CheckPositive(ds data.Source) (data.Value, error) {
	xv, err := ds.Find(context.Background(), "x")
	if err != nil {
		return nil, fmt.Errorf("couldn't find x value: %w", err)
	}
}

func TestGoBpmExpression(t *testing.T) {
}
