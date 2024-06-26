// Code generated by mockery v2.43.1. DO NOT EDIT.

package mockscope

import (
	context "context"

	data "github.com/dr-dobermann/gobpm/pkg/model/data"
	flow "github.com/dr-dobermann/gobpm/pkg/model/flow"

	foundation "github.com/dr-dobermann/gobpm/pkg/model/foundation"

	mock "github.com/stretchr/testify/mock"

	scope "github.com/dr-dobermann/gobpm/internal/scope"
)

// MockNodeDataProducer is an autogenerated mock type for the NodeDataProducer type
type MockNodeDataProducer struct {
	mock.Mock
}

type MockNodeDataProducer_Expecter struct {
	mock *mock.Mock
}

func (_m *MockNodeDataProducer) EXPECT() *MockNodeDataProducer_Expecter {
	return &MockNodeDataProducer_Expecter{mock: &_m.Mock}
}

// AddFlow provides a mock function with given fields: _a0, _a1
func (_m *MockNodeDataProducer) AddFlow(_a0 *flow.SequenceFlow, _a1 data.Direction) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for AddFlow")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*flow.SequenceFlow, data.Direction) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockNodeDataProducer_AddFlow_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddFlow'
type MockNodeDataProducer_AddFlow_Call struct {
	*mock.Call
}

// AddFlow is a helper method to define mock.On call
//   - _a0 *flow.SequenceFlow
//   - _a1 data.Direction
func (_e *MockNodeDataProducer_Expecter) AddFlow(_a0 interface{}, _a1 interface{}) *MockNodeDataProducer_AddFlow_Call {
	return &MockNodeDataProducer_AddFlow_Call{Call: _e.mock.On("AddFlow", _a0, _a1)}
}

func (_c *MockNodeDataProducer_AddFlow_Call) Run(run func(_a0 *flow.SequenceFlow, _a1 data.Direction)) *MockNodeDataProducer_AddFlow_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*flow.SequenceFlow), args[1].(data.Direction))
	})
	return _c
}

func (_c *MockNodeDataProducer_AddFlow_Call) Return(_a0 error) *MockNodeDataProducer_AddFlow_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_AddFlow_Call) RunAndReturn(run func(*flow.SequenceFlow, data.Direction) error) *MockNodeDataProducer_AddFlow_Call {
	_c.Call.Return(run)
	return _c
}

// BindTo provides a mock function with given fields: _a0
func (_m *MockNodeDataProducer) BindTo(_a0 flow.Container) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for BindTo")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(flow.Container) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockNodeDataProducer_BindTo_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BindTo'
type MockNodeDataProducer_BindTo_Call struct {
	*mock.Call
}

// BindTo is a helper method to define mock.On call
//   - _a0 flow.Container
func (_e *MockNodeDataProducer_Expecter) BindTo(_a0 interface{}) *MockNodeDataProducer_BindTo_Call {
	return &MockNodeDataProducer_BindTo_Call{Call: _e.mock.On("BindTo", _a0)}
}

func (_c *MockNodeDataProducer_BindTo_Call) Run(run func(_a0 flow.Container)) *MockNodeDataProducer_BindTo_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(flow.Container))
	})
	return _c
}

func (_c *MockNodeDataProducer_BindTo_Call) Return(_a0 error) *MockNodeDataProducer_BindTo_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_BindTo_Call) RunAndReturn(run func(flow.Container) error) *MockNodeDataProducer_BindTo_Call {
	_c.Call.Return(run)
	return _c
}

// Container provides a mock function with given fields:
func (_m *MockNodeDataProducer) Container() flow.Container {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Container")
	}

	var r0 flow.Container
	if rf, ok := ret.Get(0).(func() flow.Container); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(flow.Container)
		}
	}

	return r0
}

// MockNodeDataProducer_Container_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Container'
type MockNodeDataProducer_Container_Call struct {
	*mock.Call
}

// Container is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Container() *MockNodeDataProducer_Container_Call {
	return &MockNodeDataProducer_Container_Call{Call: _e.mock.On("Container")}
}

func (_c *MockNodeDataProducer_Container_Call) Run(run func()) *MockNodeDataProducer_Container_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Container_Call) Return(_a0 flow.Container) *MockNodeDataProducer_Container_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Container_Call) RunAndReturn(run func() flow.Container) *MockNodeDataProducer_Container_Call {
	_c.Call.Return(run)
	return _c
}

// Docs provides a mock function with given fields:
func (_m *MockNodeDataProducer) Docs() []*foundation.Documentation {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Docs")
	}

	var r0 []*foundation.Documentation
	if rf, ok := ret.Get(0).(func() []*foundation.Documentation); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*foundation.Documentation)
		}
	}

	return r0
}

// MockNodeDataProducer_Docs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Docs'
type MockNodeDataProducer_Docs_Call struct {
	*mock.Call
}

// Docs is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Docs() *MockNodeDataProducer_Docs_Call {
	return &MockNodeDataProducer_Docs_Call{Call: _e.mock.On("Docs")}
}

func (_c *MockNodeDataProducer_Docs_Call) Run(run func()) *MockNodeDataProducer_Docs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Docs_Call) Return(_a0 []*foundation.Documentation) *MockNodeDataProducer_Docs_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Docs_Call) RunAndReturn(run func() []*foundation.Documentation) *MockNodeDataProducer_Docs_Call {
	_c.Call.Return(run)
	return _c
}

// Id provides a mock function with given fields:
func (_m *MockNodeDataProducer) Id() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Id")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// MockNodeDataProducer_Id_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Id'
type MockNodeDataProducer_Id_Call struct {
	*mock.Call
}

// Id is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Id() *MockNodeDataProducer_Id_Call {
	return &MockNodeDataProducer_Id_Call{Call: _e.mock.On("Id")}
}

func (_c *MockNodeDataProducer_Id_Call) Run(run func()) *MockNodeDataProducer_Id_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Id_Call) Return(_a0 string) *MockNodeDataProducer_Id_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Id_Call) RunAndReturn(run func() string) *MockNodeDataProducer_Id_Call {
	_c.Call.Return(run)
	return _c
}

// Incoming provides a mock function with given fields:
func (_m *MockNodeDataProducer) Incoming() []*flow.SequenceFlow {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Incoming")
	}

	var r0 []*flow.SequenceFlow
	if rf, ok := ret.Get(0).(func() []*flow.SequenceFlow); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*flow.SequenceFlow)
		}
	}

	return r0
}

// MockNodeDataProducer_Incoming_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Incoming'
type MockNodeDataProducer_Incoming_Call struct {
	*mock.Call
}

// Incoming is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Incoming() *MockNodeDataProducer_Incoming_Call {
	return &MockNodeDataProducer_Incoming_Call{Call: _e.mock.On("Incoming")}
}

func (_c *MockNodeDataProducer_Incoming_Call) Run(run func()) *MockNodeDataProducer_Incoming_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Incoming_Call) Return(_a0 []*flow.SequenceFlow) *MockNodeDataProducer_Incoming_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Incoming_Call) RunAndReturn(run func() []*flow.SequenceFlow) *MockNodeDataProducer_Incoming_Call {
	_c.Call.Return(run)
	return _c
}

// Name provides a mock function with given fields:
func (_m *MockNodeDataProducer) Name() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Name")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// MockNodeDataProducer_Name_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Name'
type MockNodeDataProducer_Name_Call struct {
	*mock.Call
}

// Name is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Name() *MockNodeDataProducer_Name_Call {
	return &MockNodeDataProducer_Name_Call{Call: _e.mock.On("Name")}
}

func (_c *MockNodeDataProducer_Name_Call) Run(run func()) *MockNodeDataProducer_Name_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Name_Call) Return(_a0 string) *MockNodeDataProducer_Name_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Name_Call) RunAndReturn(run func() string) *MockNodeDataProducer_Name_Call {
	_c.Call.Return(run)
	return _c
}

// Node provides a mock function with given fields:
func (_m *MockNodeDataProducer) Node() flow.Node {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Node")
	}

	var r0 flow.Node
	if rf, ok := ret.Get(0).(func() flow.Node); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(flow.Node)
		}
	}

	return r0
}

// MockNodeDataProducer_Node_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Node'
type MockNodeDataProducer_Node_Call struct {
	*mock.Call
}

// Node is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Node() *MockNodeDataProducer_Node_Call {
	return &MockNodeDataProducer_Node_Call{Call: _e.mock.On("Node")}
}

func (_c *MockNodeDataProducer_Node_Call) Run(run func()) *MockNodeDataProducer_Node_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Node_Call) Return(_a0 flow.Node) *MockNodeDataProducer_Node_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Node_Call) RunAndReturn(run func() flow.Node) *MockNodeDataProducer_Node_Call {
	_c.Call.Return(run)
	return _c
}

// NodeType provides a mock function with given fields:
func (_m *MockNodeDataProducer) NodeType() flow.NodeType {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for NodeType")
	}

	var r0 flow.NodeType
	if rf, ok := ret.Get(0).(func() flow.NodeType); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(flow.NodeType)
	}

	return r0
}

// MockNodeDataProducer_NodeType_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'NodeType'
type MockNodeDataProducer_NodeType_Call struct {
	*mock.Call
}

// NodeType is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) NodeType() *MockNodeDataProducer_NodeType_Call {
	return &MockNodeDataProducer_NodeType_Call{Call: _e.mock.On("NodeType")}
}

func (_c *MockNodeDataProducer_NodeType_Call) Run(run func()) *MockNodeDataProducer_NodeType_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_NodeType_Call) Return(_a0 flow.NodeType) *MockNodeDataProducer_NodeType_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_NodeType_Call) RunAndReturn(run func() flow.NodeType) *MockNodeDataProducer_NodeType_Call {
	_c.Call.Return(run)
	return _c
}

// Outgoing provides a mock function with given fields:
func (_m *MockNodeDataProducer) Outgoing() []*flow.SequenceFlow {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Outgoing")
	}

	var r0 []*flow.SequenceFlow
	if rf, ok := ret.Get(0).(func() []*flow.SequenceFlow); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*flow.SequenceFlow)
		}
	}

	return r0
}

// MockNodeDataProducer_Outgoing_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Outgoing'
type MockNodeDataProducer_Outgoing_Call struct {
	*mock.Call
}

// Outgoing is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Outgoing() *MockNodeDataProducer_Outgoing_Call {
	return &MockNodeDataProducer_Outgoing_Call{Call: _e.mock.On("Outgoing")}
}

func (_c *MockNodeDataProducer_Outgoing_Call) Run(run func()) *MockNodeDataProducer_Outgoing_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Outgoing_Call) Return(_a0 []*flow.SequenceFlow) *MockNodeDataProducer_Outgoing_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Outgoing_Call) RunAndReturn(run func() []*flow.SequenceFlow) *MockNodeDataProducer_Outgoing_Call {
	_c.Call.Return(run)
	return _c
}

// Type provides a mock function with given fields:
func (_m *MockNodeDataProducer) Type() flow.ElementType {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Type")
	}

	var r0 flow.ElementType
	if rf, ok := ret.Get(0).(func() flow.ElementType); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(flow.ElementType)
	}

	return r0
}

// MockNodeDataProducer_Type_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Type'
type MockNodeDataProducer_Type_Call struct {
	*mock.Call
}

// Type is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Type() *MockNodeDataProducer_Type_Call {
	return &MockNodeDataProducer_Type_Call{Call: _e.mock.On("Type")}
}

func (_c *MockNodeDataProducer_Type_Call) Run(run func()) *MockNodeDataProducer_Type_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Type_Call) Return(_a0 flow.ElementType) *MockNodeDataProducer_Type_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Type_Call) RunAndReturn(run func() flow.ElementType) *MockNodeDataProducer_Type_Call {
	_c.Call.Return(run)
	return _c
}

// Unbind provides a mock function with given fields:
func (_m *MockNodeDataProducer) Unbind() error {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Unbind")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockNodeDataProducer_Unbind_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Unbind'
type MockNodeDataProducer_Unbind_Call struct {
	*mock.Call
}

// Unbind is a helper method to define mock.On call
func (_e *MockNodeDataProducer_Expecter) Unbind() *MockNodeDataProducer_Unbind_Call {
	return &MockNodeDataProducer_Unbind_Call{Call: _e.mock.On("Unbind")}
}

func (_c *MockNodeDataProducer_Unbind_Call) Run(run func()) *MockNodeDataProducer_Unbind_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataProducer_Unbind_Call) Return(_a0 error) *MockNodeDataProducer_Unbind_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_Unbind_Call) RunAndReturn(run func() error) *MockNodeDataProducer_Unbind_Call {
	_c.Call.Return(run)
	return _c
}

// UploadData provides a mock function with given fields: ctx, s
func (_m *MockNodeDataProducer) UploadData(ctx context.Context, s scope.Scope) error {
	ret := _m.Called(ctx, s)

	if len(ret) == 0 {
		panic("no return value specified for UploadData")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, scope.Scope) error); ok {
		r0 = rf(ctx, s)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockNodeDataProducer_UploadData_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'UploadData'
type MockNodeDataProducer_UploadData_Call struct {
	*mock.Call
}

// UploadData is a helper method to define mock.On call
//   - ctx context.Context
//   - s scope.Scope
func (_e *MockNodeDataProducer_Expecter) UploadData(ctx interface{}, s interface{}) *MockNodeDataProducer_UploadData_Call {
	return &MockNodeDataProducer_UploadData_Call{Call: _e.mock.On("UploadData", ctx, s)}
}

func (_c *MockNodeDataProducer_UploadData_Call) Run(run func(ctx context.Context, s scope.Scope)) *MockNodeDataProducer_UploadData_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(scope.Scope))
	})
	return _c
}

func (_c *MockNodeDataProducer_UploadData_Call) Return(_a0 error) *MockNodeDataProducer_UploadData_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataProducer_UploadData_Call) RunAndReturn(run func(context.Context, scope.Scope) error) *MockNodeDataProducer_UploadData_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockNodeDataProducer creates a new instance of MockNodeDataProducer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockNodeDataProducer(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockNodeDataProducer {
	mock := &MockNodeDataProducer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
