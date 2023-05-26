// request package consists Request struct which implements common.DataItem interface.
// Request struct is only used for tests with common.DataItem usage.
package request

import (
	"fmt"
	"math/rand"

	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
)

// ------------------------------------------------------------------------------
type Request struct {
	id    int
	descr string
}

//------------------------------------------------------------------------------

func New(id int, descr string) *Request {

	if id == 0 {
		id = rand.Int()
	}

	return &Request{
		id:    id,
		descr: descr,
	}
}

func (r *Request) ID() int {
	return r.id
}

func (r *Request) Descr() string {
	return r.descr
}

// common.DataItem interface implementation
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
