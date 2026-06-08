package localdispatcher

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

func TestRegisterAndDispatch(t *testing.T) {
	d := New(0) // default pool size
	want := "done"

	if err := d.Register("greet", func(_ context.Context, _ tasks.Job) (any, error) {
		return want, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := d.Dispatch(context.Background(), tasks.Job{Type: "greet"})
	if err != nil || got != want {
		t.Fatalf("Dispatch = %v, %v; want %q, nil", got, err, want)
	}
}

func TestDispatchNoHandler(t *testing.T) {
	d := New(1)

	if _, err := d.Dispatch(context.Background(), tasks.Job{Type: "missing"}); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("err = %v, want ErrNoHandler", err)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	d := New(1)
	h := func(context.Context, tasks.Job) (any, error) { return nil, nil }

	_ = d.Register("t", h)
	if err := d.Register("t", h); !errors.Is(err, ErrDuplicateHandler) {
		t.Fatalf("err = %v, want ErrDuplicateHandler", err)
	}
}

func TestBoundedPoolBlocksAndCtxCancels(t *testing.T) {
	d := New(1) // pool of one
	release := make(chan struct{})
	started := make(chan struct{}, 1)

	_ = d.Register("block", func(_ context.Context, _ tasks.Job) (any, error) {
		started <- struct{}{}
		<-release

		return nil, nil
	})

	go func() { _, _ = d.Dispatch(context.Background(), tasks.Job{Type: "block"}) }()
	<-started // the only pool slot is now held

	cctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := d.Dispatch(cctx, tasks.Job{Type: "block"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	close(release)
}
