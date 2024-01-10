package service

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
)

type Interface struct {
	common.NamedElement

	// Operations supported by the Interface
	// identifyed by name
	operations map[string]*Operation

	// callables consists of the references to CallableElements which are
	// use the Interface.
	// This link is a bad design, becouse it makes hard-link between
	// the Interface and its users.
	// callables  []common.CallableElement

	// Reference on implementation control structure
	implementor dataprovider.DataItem
}

func NewInterface(id identity.Id,
	name string, impl dataprovider.DataItem) (*Interface, error) {

	if impl == nil {
		return nil, model.NewModelError(name, id, nil,
			"couldn't create Interface with an empty implementation object")
	}

	name = strings.Trim(name, " ")
	if name == "" {
		return nil, model.NewModelError(name, id, nil,
			"couldn't create Interface with an empty name")
	}

	return &Interface{
		NamedElement: *common.NewNamedElement(id, name),
		operations:   map[string]*Operation{},
		implementor:  impl.Copy(),
	}, nil
}

func MustInterface(id identity.Id, name string,
	di dataprovider.DataItem) *Interface {

	inf, err := NewInterface(id, name, di)
	if err != nil {
		panic(err.Error())
	}

	return inf
}

func (inf *Interface) GetOperation(name string) (*Operation, error) {
	op, ok := inf.operations[name]
	if !ok {
		return nil,
			model.NewModelError(inf.Name(), inf.ID(), nil,
				"there is no operation %q", name)
	}

	return op, nil
}

func (inf *Interface) GetImplementor() dataprovider.DataItem {

	return inf.implementor
}
