package allowall

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/auth"
)

func TestAllowsEveryAction(t *testing.T) {
	p := New()
	ctx := context.Background()

	for _, a := range []auth.Action{
		auth.ActionStartProcess,
		auth.ActionClaimUserTask,
		auth.ActionCancelInstance,
	} {
		req := auth.Request{Subject: "u", Resource: "r", Action: a}
		if err := p.Authorize(ctx, req); err != nil {
			t.Fatalf("allow-all denied %q: %v", a, err)
		}
	}
}
