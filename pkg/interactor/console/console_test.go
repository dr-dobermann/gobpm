package console_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/interactor/console"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/stretchr/testify/require"
)

type actor struct{}

func (actor) UserID() string   { return "op" }
func (actor) Groups() []string { return nil }

// fakeEngine records the Complete outputs and returns preset Take results.
type fakeEngine struct {
	view        interactor.TaskView
	takeErr     error
	completeErr error
	mu          sync.Mutex
	completed   []data.Data
	completeHit bool
}

func (f *fakeEngine) Take(
	context.Context, string, hi.Actor,
) (interactor.TaskView, error) {
	return f.view, f.takeErr
}

func (f *fakeEngine) Complete(
	_ context.Context, _ string, _ hi.Actor, out []data.Data,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = out
	f.completeHit = true

	return f.completeErr
}

func (f *fakeEngine) hit() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.completeHit
}

// errRenderer is a hi.Renderer whose Render fails.
type errRenderer struct{ foundation.BaseElement }

func (errRenderer) Implementation() string { return "err" }
func (errRenderer) Render(data.Source) ([]data.Data, error) {
	return nil, errors.New("render boom")
}

func consForm(t *testing.T) hi.Renderer {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	r, err := consinp.NewRenderer(
		consinp.WithStringInput("decision", "?"),
		consinp.WithSource(bytes.NewBufferString("approved\n")))
	require.NoError(t, err)

	return r
}

func TestDriverDrivesToComplete(t *testing.T) {
	eng := &fakeEngine{
		view: interactor.TaskView{Renderers: []hi.Renderer{consForm(t)}},
	}
	d := console.New(actor{}, &bytes.Buffer{})
	d.Bind(eng)

	require.NoError(t,
		d.Distribute(context.Background(), interactor.TaskInfo{}))

	require.Eventually(t, eng.hit, 2*time.Second, 10*time.Millisecond)
	require.Len(t, eng.completed, 1)
	require.Equal(t, "decision", eng.completed[0].Name())
}

func TestDriverNoRendererCompletesEmpty(t *testing.T) {
	eng := &fakeEngine{} // empty view — no renderers
	d := console.New(actor{}, &bytes.Buffer{})
	d.Bind(eng)

	require.NoError(t,
		d.Distribute(context.Background(), interactor.TaskInfo{}))

	require.Eventually(t, eng.hit, 2*time.Second, 10*time.Millisecond)
	require.Empty(t, eng.completed)
}

// safeBuf is a concurrency-safe writer: drive writes on a goroutine while the
// test reads.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *safeBuf) contains(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return bytes.Contains(s.b.Bytes(), []byte(sub))
}

func TestDriverErrorPaths(t *testing.T) {
	waitFor := func(t *testing.T, buf *safeBuf, sub string) {
		t.Helper()
		require.Eventually(t, func() bool { return buf.contains(sub) },
			2*time.Second, 10*time.Millisecond)
	}

	t.Run("take fails", func(t *testing.T) {
		buf := &safeBuf{}
		d := console.New(actor{}, buf)
		d.Bind(&fakeEngine{takeErr: errors.New("nope")})
		require.NoError(t, d.Distribute(context.Background(), interactor.TaskInfo{}))
		waitFor(t, buf, "take failed")
	})

	t.Run("render fails", func(t *testing.T) {
		buf := &safeBuf{}
		d := console.New(actor{}, buf)
		d.Bind(&fakeEngine{
			view: interactor.TaskView{Renderers: []hi.Renderer{&errRenderer{}}},
		})
		require.NoError(t, d.Distribute(context.Background(), interactor.TaskInfo{}))
		waitFor(t, buf, "render failed")
	})

	t.Run("complete fails", func(t *testing.T) {
		buf := &safeBuf{}
		d := console.New(actor{}, buf)
		d.Bind(&fakeEngine{
			view:        interactor.TaskView{Renderers: []hi.Renderer{consForm(t)}},
			completeErr: errors.New("bad"),
		})
		require.NoError(t, d.Distribute(context.Background(), interactor.TaskInfo{}))
		waitFor(t, buf, "complete failed")
	})
}

func TestDriverWithdrawPrints(t *testing.T) {
	buf := &safeBuf{}
	d := console.New(actor{}, buf)
	require.NoError(t, d.Withdraw(context.Background(), "t1"))
	require.True(t, buf.contains("t1"))
}

func TestDriverNewDefaultsWriter(t *testing.T) {
	require.NotNil(t, console.New(actor{}, nil)) // nil writer defaults to stdout
}
