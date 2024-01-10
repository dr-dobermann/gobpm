package common_test

import (
	"encoding/json"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/request"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/matryer/is"
)

func TestMessage(t *testing.T) {

	is := is.New(t)

	m, err := common.NewMessage("test", request.New(10, "request id"))
	is.NoErr(err)

	it := m.GetItem()
	r, ok := it.(*request.Request)
	if !ok {
		t.Fatal("couldn't get request data")
	}
	is.Equal(r.ID(), 10)
	is.Equal(r.Descr(), "request id")
}

func TestMsgMarshalling(t *testing.T) {

	is := is.New(t)

	msrc, err := common.NewMessage("msg-test", request.New(100, "test request"))
	is.NoErr(err)

	buf, err := json.Marshal(msrc)
	is.NoErr(err)
	t.Log(string(buf))

	mdest, err := common.NewMessage("dst", request.New(0, ""))
	is.NoErr(err)
	is.NoErr(json.Unmarshal(buf, &mdest))

	it := mdest.GetItem()
	r, ok := it.(*request.Request)
	if !ok {
		t.Fatal("couldn't get request data")
	}
	is.Equal(r.ID(), 100)
	is.Equal(r.Descr(), "test request")
}
