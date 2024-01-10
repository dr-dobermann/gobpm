package service

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
)

const (
	INVALID_ERROR_CODE = "INVALID_ERROR_CODE"
	NO_INTERFACE       = "NO_INTERFACE"
	INVALID_INTERFACE  = "INVALID_INTERFACE"
	OP_EXEC_ERR        = "OPERATION_EXECUTION_ERROR"
)

var OpErrDescr = map[string]string{
	INVALID_ERROR_CODE: "Code is not found",
	NO_INTERFACE:       "Operation is not binded to Interface",
	INVALID_INTERFACE:  "Interface isn't provided desired operation",
	OP_EXEC_ERR:        "Operation execution error",
}

func GenerateOpErrs(names ...string) []*common.Error {

	errs := []*common.Error{}

	for _, n := range names {
		d, ok := OpErrDescr[n]
		if !ok {
			panic(fmt.Sprint("Invalid operation error name:", n))
		}

		errs = append(errs, common.MustError(n, d,
			&common.ItemDefinition{
				Kind:   common.IkInformaion,
				Item:   dataprovider.NewSimpleDataItem(""),
				Import: nil,
			}))
	}

	return errs
}

// ============== OperationExecutor interface and its functor ==================
type OperationExecutor interface {
	Exec(op *Operation) *common.Error
}

type OperationFunctor func(op *Operation) *common.Error

func (of OperationFunctor) Exec(op *Operation) *common.Error {
	return of(op)
}

// ========================== Operation ========================================
type Operation struct {
	common.NamedElement

	// to run operation input parameters are in
	// inMessage
	inMessage *common.Message

	// operation could provide results in
	// outMessages
	outMessages map[string]*common.Message

	// Operation could generate errors
	// indexed by Error code
	errors map[string]common.Error

	// operation execution object
	// it should provide OperationExecutor interface
	executor OperationExecutor

	// Binded interface. If nil then Operation
	// could execute by itself without external resources
	iface *Interface
}

func NewOperation(id identity.Id, name string,
	inM *common.Message, errors []*common.Error,
	opEx OperationExecutor, inf *Interface) (*Operation, error) {

	return nil, errs.NotImplementedYet
}

func (op *Operation) GetInterface() *Interface {

	return op.iface
}

func (op *Operation) GetError(code string) *common.Error {

	e, ok := op.errors[code]
	if !ok {
		return common.MustError(OpErrDescr[INVALID_ERROR_CODE],
			INVALID_ERROR_CODE,
			&common.ItemDefinition{
				Kind:   common.IkInformaion,
				Item:   dataprovider.NewSimpleDataItem(code),
				Import: nil})
	}

	return &e
}

func (op *Operation) GetInMessage() *common.Message {

	return op.inMessage
}

func (op *Operation) AddOutMessage(m *common.Message) error {

	if m == nil || m.Name() == "" {
		return model.NewModelError(op.Name(), op.ID(), nil,
			"registered output message shouldn't be empty")
	}

	op.outMessages[m.Name()] = m

	return nil
}
