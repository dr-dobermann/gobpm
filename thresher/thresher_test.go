package thresher

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"

	mid "github.com/dr-dobermann/gobpm/internal/identity"
	vars "github.com/dr-dobermann/gobpm/model/variables"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/srvbus"
	"github.com/dr-dobermann/srvbus/es"
	"github.com/google/uuid"
	"github.com/matryer/is"
	"go.uber.org/zap"
)

func TestTracks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	buf := bytes.NewBuffer([]byte{})
	out := model.OutputDescr{
		To:     buf,
		Locker: new(sync.Mutex)}

	thr, inst := getTestInstance(ctx, getTestProcess(t, &out), t)

	// subscribe to instance finished
	eSrv, err := thr.SrvBus().GetEventServer()
	if err != nil {
		t.Fatal("couldn't get event server: ", err)
	}

	// event channel
	insCh := make(chan es.EventEnvelope)

	err = eSrv.Subscribe(uuid.UUID(thr.id), es.SubscrReq{
		Topic:     thr.esTopic,
		SubCh:     insCh,
		Recursive: false,
		Depth:     0,
		StartPos:  0,
		Filters: []es.Filter{
			es.WithName(instance.InstEndEvt),
			es.WithSubData([]byte(inst.ID().String())),
		},
	})
	if err != nil {
		t.Fatal("couldn't subscribe on event:", err)
	}

	// wait until instance finished
	<-insCh
	close(insCh)

	// check results
	testStr := "x = 2"

	out.Locker.Lock()
	defer out.Locker.Unlock()

	if !bytes.Contains(buf.Bytes(), []byte(testStr)) {
		t.Fatalf("unexpected process execution results: '%s' doesn't contain '%s'",
			buf.String(), testStr)
	}

}

func getTestInstance(
	ctx context.Context,
	p *model.Process,
	t *testing.T) (*Thresher, *instance.Instance) {

	if p == nil {
		p = getTestProcess(t, nil)
	}

	is := is.New(t)

	log, err := zap.NewDevelopment()
	is.NoErr(err)

	sBus, err := srvbus.New(uuid.New(), log.Sugar())
	is.NoErr(err)

	is.NoErr(sBus.Run(ctx))

	thr, err := New(sBus, log.Sugar())
	is.NoErr(err)

	is.NoErr(thr.Run(ctx))

	pi, err := thr.NewInstance(p)
	is.NoErr(err)

	return thr, thr.instances[pi]
}

// test process creates a process of two tasks (if buf is nil):
//
//  ------------       -------------
// | Store Task | --> | Output Task |
//  ------------       -------------
//
// or three tasks (if buf is not nil)
//  ------------       -------------
// | Store Task | --> | Output Task |
//  ------------       -------------
//       |
//       |             -------------
//        ----------> | Check Task  |
//                     -------------
//
// Store Task puts X == 2 into instance storage
//
// Output Task prints X from internal storage onto io.Stdout.
//
// Check Task output the X into the given buffer so it's possible to
// check task execution results
//
func getTestProcess(t *testing.T, buf *model.OutputDescr) *model.Process {

	p := model.NewProcess(mid.EmptyID(), "Test Process", "0.1.0")

	t1 := model.NewStoreTask(p, "Store Task", *vars.V("x", vars.Int, 2))

	t2 := model.NewOutputTask(p, "Output Task",
		model.OutputDescr{nil, os.Stdout}, *vars.V("x", vars.Int, 0))

	if t1 == nil || t2 == nil {
		t.Fatal("Couldn't create tasks for test process")
	}

	if err := p.NewLane("Lane 1"); err != nil {
		t.Fatal("Couldn't add Lane 1 to tosting process : ", err)
	}

	if err := p.AddTask(t1, "Lane 1"); err != nil {
		t.Fatal("Couldn't add Task 1 on Lane 1 : ", err)
	}

	if err := p.AddTask(t2, "Lane 1"); err != nil {
		t.Fatal("Couldn't add Task 2 on Lane 1 : ", err)
	}

	if err := p.LinkNodes(t1, t2, nil); err != nil {
		t.Fatal("couldn't link tasks in test process : ", err)
	}

	if buf != nil {
		t3 := model.NewOutputTask(p, "Check Task",
			*buf, *vars.V("x", vars.Int, 0))

		if err := p.AddTask(t3, "Lane 1"); err != nil {
			t.Fatal("Couldn't add Task 3 on Lane 1 : ", err)
		}

		if err := p.LinkNodes(t1, t3, nil); err != nil {
			t.Fatal("couldn't link tasks in test process : ", err)
		}
	}

	return p
}
