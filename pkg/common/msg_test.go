package common_test

import (
	"encoding/json"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/matryer/is"
)

// ==============================================================================
type TestStruct struct {
	Str string
	Int int
}

// ==============================================================================
func TestMessage(t *testing.T) {

	is := is.New(t)

	m, err := common.NewMessage("test", TestStruct{"str", 100})
	is.NoErr(err)
	is.Equal(m.GetItem().Int, 100)
	is.Equal(m.GetItem().Str, "str")
	item := m.GetItem()
	m.UpdateItem(TestStruct{item.Str, item.Int + 200})
	is.Equal(m.GetItem().Int, 300)

	m1, err := common.NewMessage("test-int", 555)
	is.NoErr(err)
	is.Equal(m1.GetItem(), 555)
}

func TestMsgMarshalling(t *testing.T) {

	is := is.New(t)

	msrc, err := common.NewMessage("msg-test", TestStruct{"str-test", 111})
	is.NoErr(err)

	buf, err := json.Marshal(msrc)
	is.NoErr(err)

	mdest := new(common.Message[TestStruct])
	is.NoErr(json.Unmarshal(buf, &mdest))
	is.Equal(mdest.GetItem().Int, msrc.GetItem().Int)
	is.Equal(mdest.GetItem().Str, msrc.GetItem().Str)
}
