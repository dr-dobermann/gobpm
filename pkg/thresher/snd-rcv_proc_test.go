package thresher

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"

	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
	"github.com/dr-dobermann/srvbus"
	"github.com/google/uuid"
	"github.com/matryer/is"
	"go.uber.org/zap"
)

var (
	test_queue = "xchQ"
)

func TestSelectorExecutor(t *testing.T) {
	t.Run("SendTaskExecutor", func(t *testing.T) {
		p := model.NewProcess(mid.NewID(), "SendTask process test", "0.1.0")
		std := model.NewSendTask(p, "Send X", "letter_X", test_queue)

		ste, err := executor.GetTaskExecutor(std)
		if err != nil {
			t.Error("couldn't get SendTaskExecutor", err)
		}

		if ste != nil {
			fmt.Println(ste.Name())
		}
	})

	t.Run("ReceiveTaskExecutor", func(t *testing.T) {
		p := model.NewProcess(mid.NewID(), "ReceiveTask process test", "0.1.0")
		rtd := model.NewReceiveTask(p, "Receive X", "letter_X", test_queue)

		rte, err := executor.GetTaskExecutor(rtd)
		if err != nil {
			t.Error("couldn't get ReceiveTaskExecutor", err)
		}

		if rte != nil {
			fmt.Println(rte.Name())
		}
	})
}

func TestSendReceiveProcesses(t *testing.T) {
	is := is.New(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	thr, err := getRunnedThresher(ctx, t)
	is.NoErr(err)

	xch_queue := "xch_queue"
	buf := bytes.NewBuffer([]byte{})
	out := model.OutputDescr{Locker: new(sync.Mutex), To: buf}

	createReceivingInstance(ctx, thr, xch_queue, &out, t)

	createSendingInstance(ctx, thr, xch_queue, t)

	time.Sleep(2 * time.Second)

	out.Locker.Lock()
	defer out.Locker.Unlock()
	t.Log(buf.String())
	is.True(bytes.Contains(buf.Bytes(), []byte("x = 42")))
}

func getRunnedThresher(ctx context.Context, t *testing.T) (*Thresher, error) {
	is := is.New(t)
	log, err := zap.NewDevelopment()
	is.NoErr(err)

	sBus, err := srvbus.New(uuid.Nil, log.Sugar())
	is.NoErr(err)

	is.NoErr(sBus.Run(ctx))

	thr, err := New(sBus, log.Sugar())
	is.NoErr(err)

	is.NoErr(thr.Run(ctx))

	return thr, nil
}

func createReceivingInstance(
	ctx context.Context,
	thr *Thresher,
	qName string,
	output *model.OutputDescr,
	t *testing.T) {

	is := is.New(t)

	p := model.NewProcess(mid.EmptyID(), "Receiver", "0.1.0")
	is.True(p != nil)

	x := vars.V("x", vars.Int, 0)

	_, err := p.AddMessage(
		"letter_X",
		model.Incoming,
		*model.NewMVar(x, model.Required))
	is.NoErr(err)

	lane := "Receiver"
	tn1 := "Receive letter_X"
	tn2 := "Print X"
	is.NoErr(p.NewLane(lane))

	is.NoErr(
		p.AddTask(
			model.NewReceiveTask(
				p,
				tn1,
				"letter_X",
				qName),
			lane))

	is.NoErr(
		p.AddTask(
			model.NewOutputTask(
				p,
				tn2,
				*output,
				*x),
			lane))

	is.NoErr(p.LinkNamedNodes(tn1, tn2, nil))

	_, err = thr.NewInstance(p)
	is.NoErr(err)
}

func createSendingInstance(
	ctx context.Context,
	thr *Thresher,
	qName string,
	t *testing.T) {

	is := is.New(t)

	p := model.NewProcess(mid.EmptyID(), "Sender", "0.1.0")
	is.True(p != nil)

	x := vars.V("x", vars.Int, 42)

	msgName := "letter_X"
	_, err := p.AddMessage(
		msgName,
		model.Outgoing,
		*model.NewMVar(x, model.Required))
	is.NoErr(err)

	lane := "Sender"
	tn1 := "Store X"
	tn2 := "Send letter_X"
	is.NoErr(p.NewLane(lane))

	is.NoErr(
		p.AddTask(
			model.NewStoreTask(
				p,
				tn1,
				*x),
			lane))

	is.NoErr(
		p.AddTask(
			model.NewSendTask(
				p,
				tn2,
				msgName,
				qName),
			lane))

	is.NoErr(p.LinkNamedNodes(tn1, tn2, nil))

	_, err = thr.NewInstance(p)
	is.NoErr(err)

}
