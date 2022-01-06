package thresher

import (
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/model"
)

var test_queue = "test_intrch_queue"

func TestSendTaskExecutor(t *testing.T) {
	p := model.NewProcess(model.NewID(), "SendTask process test", "0.1.0")
	std := model.NewSendTask(p, "Send X", "letter_X", test_queue)

	ste, err := GetTaskExecutor(std)
	if err != nil {
		t.Error("couldn't get SendTaskExecutor", err)
	}

	if ste != nil {
		fmt.Println(ste.Name())
	}

}

func TestReceiveTaskExecutor(t *testing.T) {
	p := model.NewProcess(model.NewID(), "ReceiveTask process test", "0.1.0")
	rtd := model.NewReceiveTask(p, "Receive X", "letter_X", test_queue)

	rte, err := GetTaskExecutor(rtd)
	if err != nil {
		t.Error("couldn't get ReceiveTaskExecutor", err)
	}

	if rte != nil {
		fmt.Println(rte.Name())
	}

}
