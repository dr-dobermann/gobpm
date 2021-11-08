package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/model"
)

type Task interface {
	model.Node
	Exec(ctx context.Context, tr *track) ([]model.Node, error)
}
