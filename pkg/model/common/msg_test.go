package common_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
	"github.com/matryer/is"
)

// ==============================================================================
type Request struct {
	id    int
	descr string
}

func (r *Request) ID() int {
	return r.id
}

func (r *Request) Descr() string {
	return r.descr
}

// implement DataItem interface for Request
func (r *Request) IsCollection() bool {
	return false
}

func (r *Request) Len() int {
	return 1
}

func (r *Request) Copy() dataprovider.DataItem {

	return &Request{
		id:    r.id,
		descr: r.descr,
	}
}

func (r *Request) GetValue() map[string]interface{} {

	v := map[string]interface{}{}

	v["id"] = r.id
	v["descr"] = r.descr

	return v
}

func (r *Request) UpdateValue(nv map[string]interface{}) error {

	id, descr := 0, ""

	v, ok := nv["id"]
	if !ok {
		return fmt.Errorf("couldn't find id field")
	}

	switch vv := v.(type) {
	case int:
		id = vv

	case float64:
		id = int(vv)

	default:
		return fmt.Errorf("couldn't get id")
	}

	v, ok = nv["descr"]
	if !ok {
		return fmt.Errorf("couldn't find descr field")
	}

	if descr, ok = v.(string); !ok {
		return fmt.Errorf("couldn't get descr")
	}

	r.id = id
	r.descr = descr

	return nil
}

// =============================================================================
func TestMessage(t *testing.T) {

	is := is.New(t)

	m, err := common.NewMessage("test", &Request{10, "request id"})
	is.NoErr(err)

	it := m.GetItem()
	r, ok := it.(*Request)
	if !ok {
		t.Fatal("couldn't get request data")
	}
	is.Equal(r.id, 10)
	is.Equal(r.descr, "request id")
}

func TestMsgMarshalling(t *testing.T) {

	is := is.New(t)

	msrc, err := common.NewMessage("msg-test", &Request{100, "test request"})
	is.NoErr(err)

	buf, err := json.Marshal(msrc)
	is.NoErr(err)
	t.Log(string(buf))

	mdest, err := common.NewMessage("dst", &Request{0, ""})
	is.NoErr(err)
	is.NoErr(json.Unmarshal(buf, &mdest))

	it := mdest.GetItem()
	r, ok := it.(*Request)
	if !ok {
		t.Fatal("couldn't get request data")
	}
	is.Equal(r.id, 100)
	is.Equal(r.descr, "test request")
}
