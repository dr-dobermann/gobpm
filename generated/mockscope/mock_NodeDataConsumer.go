// Code generated by mockery v2.43.1. DO NOT EDIT.

package mockscope

import (
	context "context"

	data "github.com/dr-dobermann/gobpm/pkg/model/data"
	flow "github.com/dr-dobermann/gobpm/pkg/model/flow"

	foundation "github.com/dr-dobermann/gobpm/pkg/model/foundation"

	mock "github.com/stretchr/testify/mock"
)

// MockNodeDataConsumer is an autogenerated mock type for the NodeDataConsumer type
type MockNodeDataConsumer struct {
	mock.Mock
}

type MockNodeDataConsumer_Expecter struct {
	mock *mock.Mock
}

func (_m *MockNodeDataConsumer) EXPECT() *MockNodeDataConsumer_Expecter {
	return &MockNodeDataConsumer_Expecter{mock: &_m.Mock}
}

// AddFlow provides a mock function with given fields: _a0, _a1
func (_m *MockNodeDataConsumer) AddFlow(_a0 *flow.SequenceFlow, _a1 data.Direction) error {
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

// MockNodeDataConsumer_AddFlow_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddFlow'
type MockNodeDataConsumer_AddFlow_Call struct {
	*mock.Call
}

// AddFlow is a helper method to define mock.On call
//   - _a0 *flow.SequenceFlow
//   - _a1 data.Direction
func (_e *MockNodeDataConsumer_Expecter) AddFlow(_a0 interface{}, _a1 interface{}) *MockNodeDataConsumer_AddFlow_Call {
	return &MockNodeDataConsumer_AddFlow_Call{Call: _e.mock.On("AddFlow", _a0, _a1)}
}

func (_c *MockNodeDataConsumer_AddFlow_Call) Run(run func(_a0 *flow.SequenceFlow, _a1 data.Direction)) *MockNodeDataConsumer_AddFlow_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*flow.SequenceFlow), args[1].(data.Direction))
	})
	return _c
}

func (_c *MockNodeDataConsumer_AddFlow_Call) Return(_a0 error) *MockNodeDataConsumer_AddFlow_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_AddFlow_Call) RunAndReturn(run func(*flow.SequenceFlow, data.Direction) error) *MockNodeDataConsumer_AddFlow_Call {
	_c.Call.Return(run)
	return _c
}

// BindTo provides a mock function with given fields: _a0
func (_m *MockNodeDataConsumer) BindTo(_a0 flow.Container) error {
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

// MockNodeDataConsumer_BindTo_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BindTo'
type MockNodeDataConsumer_BindTo_Call struct {
	*mock.Call
}

// BindTo is a helper method to define mock.On call
//   - _a0 flow.Container
func (_e *MockNodeDataConsumer_Expecter) BindTo(_a0 interface{}) *MockNodeDataConsumer_BindTo_Call {
	return &MockNodeDataConsumer_BindTo_Call{Call: _e.mock.On("BindTo", _a0)}
}

func (_c *MockNodeDataConsumer_BindTo_Call) Run(run func(_a0 flow.Container)) *MockNodeDataConsumer_BindTo_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(flow.Container))
	})
	return _c
}

func (_c *MockNodeDataConsumer_BindTo_Call) Return(_a0 error) *MockNodeDataConsumer_BindTo_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_BindTo_Call) RunAndReturn(run func(flow.Container) error) *MockNodeDataConsumer_BindTo_Call {
	_c.Call.Return(run)
	return _c
}

// Container provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Container() flow.Container {
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

// MockNodeDataConsumer_Container_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Container'
type MockNodeDataConsumer_Container_Call struct {
	*mock.Call
}

// Container is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Container() *MockNodeDataConsumer_Container_Call {
	return &MockNodeDataConsumer_Container_Call{Call: _e.mock.On("Container")}
}

func (_c *MockNodeDataConsumer_Container_Call) Run(run func()) *MockNodeDataConsumer_Container_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Container_Call) Return(_a0 flow.Container) *MockNodeDataConsumer_Container_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Container_Call) RunAndReturn(run func() flow.Container) *MockNodeDataConsumer_Container_Call {
	_c.Call.Return(run)
	return _c
}

// Docs provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Docs() []*foundation.Documentation {
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

// MockNodeDataConsumer_Docs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Docs'
type MockNodeDataConsumer_Docs_Call struct {
	*mock.Call
}

// Docs is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Docs() *MockNodeDataConsumer_Docs_Call {
	return &MockNodeDataConsumer_Docs_Call{Call: _e.mock.On("Docs")}
}

func (_c *MockNodeDataConsumer_Docs_Call) Run(run func()) *MockNodeDataConsumer_Docs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Docs_Call) Return(_a0 []*foundation.Documentation) *MockNodeDataConsumer_Docs_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Docs_Call) RunAndReturn(run func() []*foundation.Documentation) *MockNodeDataConsumer_Docs_Call {
	_c.Call.Return(run)
	return _c
}

// Id provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Id() string {
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

// MockNodeDataConsumer_Id_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Id'
type MockNodeDataConsumer_Id_Call struct {
	*mock.Call
}

// Id is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Id() *MockNodeDataConsumer_Id_Call {
	return &MockNodeDataConsumer_Id_Call{Call: _e.mock.On("Id")}
}

func (_c *MockNodeDataConsumer_Id_Call) Run(run func()) *MockNodeDataConsumer_Id_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Id_Call) Return(_a0 string) *MockNodeDataConsumer_Id_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Id_Call) RunAndReturn(run func() string) *MockNodeDataConsumer_Id_Call {
	_c.Call.Return(run)
	return _c
}

// Incoming provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Incoming() []*flow.SequenceFlow {
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

// MockNodeDataConsumer_Incoming_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Incoming'
type MockNodeDataConsumer_Incoming_Call struct {
	*mock.Call
}

// Incoming is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Incoming() *MockNodeDataConsumer_Incoming_Call {
	return &MockNodeDataConsumer_Incoming_Call{Call: _e.mock.On("Incoming")}
}

func (_c *MockNodeDataConsumer_Incoming_Call) Run(run func()) *MockNodeDataConsumer_Incoming_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Incoming_Call) Return(_a0 []*flow.SequenceFlow) *MockNodeDataConsumer_Incoming_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Incoming_Call) RunAndReturn(run func() []*flow.SequenceFlow) *MockNodeDataConsumer_Incoming_Call {
	_c.Call.Return(run)
	return _c
}

// LoadData provides a mock function with given fields: _a0
func (_m *MockNodeDataConsumer) LoadData(_a0 context.Context) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for LoadData")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockNodeDataConsumer_LoadData_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'LoadData'
type MockNodeDataConsumer_LoadData_Call struct {
	*mock.Call
}

// LoadData is a helper method to define mock.On call
//   - _a0 context.Context
func (_e *MockNodeDataConsumer_Expecter) LoadData(_a0 interface{}) *MockNodeDataConsumer_LoadData_Call {
	return &MockNodeDataConsumer_LoadData_Call{Call: _e.mock.On("LoadData", _a0)}
}

func (_c *MockNodeDataConsumer_LoadData_Call) Run(run func(_a0 context.Context)) *MockNodeDataConsumer_LoadData_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *MockNodeDataConsumer_LoadData_Call) Return(_a0 error) *MockNodeDataConsumer_LoadData_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_LoadData_Call) RunAndReturn(run func(context.Context) error) *MockNodeDataConsumer_LoadData_Call {
	_c.Call.Return(run)
	return _c
}

// Name provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Name() string {
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

// MockNodeDataConsumer_Name_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Name'
type MockNodeDataConsumer_Name_Call struct {
	*mock.Call
}

// Name is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Name() *MockNodeDataConsumer_Name_Call {
	return &MockNodeDataConsumer_Name_Call{Call: _e.mock.On("Name")}
}

func (_c *MockNodeDataConsumer_Name_Call) Run(run func()) *MockNodeDataConsumer_Name_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Name_Call) Return(_a0 string) *MockNodeDataConsumer_Name_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Name_Call) RunAndReturn(run func() string) *MockNodeDataConsumer_Name_Call {
	_c.Call.Return(run)
	return _c
}

// Node provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Node() flow.Node {
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

// MockNodeDataConsumer_Node_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Node'
type MockNodeDataConsumer_Node_Call struct {
	*mock.Call
}

// Node is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Node() *MockNodeDataConsumer_Node_Call {
	return &MockNodeDataConsumer_Node_Call{Call: _e.mock.On("Node")}
}

func (_c *MockNodeDataConsumer_Node_Call) Run(run func()) *MockNodeDataConsumer_Node_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Node_Call) Return(_a0 flow.Node) *MockNodeDataConsumer_Node_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Node_Call) RunAndReturn(run func() flow.Node) *MockNodeDataConsumer_Node_Call {
	_c.Call.Return(run)
	return _c
}

// NodeType provides a mock function with given fields:
func (_m *MockNodeDataConsumer) NodeType() flow.NodeType {
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

// MockNodeDataConsumer_NodeType_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'NodeType'
type MockNodeDataConsumer_NodeType_Call struct {
	*mock.Call
}

// NodeType is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) NodeType() *MockNodeDataConsumer_NodeType_Call {
	return &MockNodeDataConsumer_NodeType_Call{Call: _e.mock.On("NodeType")}
}

func (_c *MockNodeDataConsumer_NodeType_Call) Run(run func()) *MockNodeDataConsumer_NodeType_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_NodeType_Call) Return(_a0 flow.NodeType) *MockNodeDataConsumer_NodeType_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_NodeType_Call) RunAndReturn(run func() flow.NodeType) *MockNodeDataConsumer_NodeType_Call {
	_c.Call.Return(run)
	return _c
}

// Outgoing provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Outgoing() []*flow.SequenceFlow {
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

// MockNodeDataConsumer_Outgoing_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Outgoing'
type MockNodeDataConsumer_Outgoing_Call struct {
	*mock.Call
}

// Outgoing is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Outgoing() *MockNodeDataConsumer_Outgoing_Call {
	return &MockNodeDataConsumer_Outgoing_Call{Call: _e.mock.On("Outgoing")}
}

func (_c *MockNodeDataConsumer_Outgoing_Call) Run(run func()) *MockNodeDataConsumer_Outgoing_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Outgoing_Call) Return(_a0 []*flow.SequenceFlow) *MockNodeDataConsumer_Outgoing_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Outgoing_Call) RunAndReturn(run func() []*flow.SequenceFlow) *MockNodeDataConsumer_Outgoing_Call {
	_c.Call.Return(run)
	return _c
}

// Type provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Type() flow.ElementType {
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

// MockNodeDataConsumer_Type_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Type'
type MockNodeDataConsumer_Type_Call struct {
	*mock.Call
}

// Type is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Type() *MockNodeDataConsumer_Type_Call {
	return &MockNodeDataConsumer_Type_Call{Call: _e.mock.On("Type")}
}

func (_c *MockNodeDataConsumer_Type_Call) Run(run func()) *MockNodeDataConsumer_Type_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Type_Call) Return(_a0 flow.ElementType) *MockNodeDataConsumer_Type_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Type_Call) RunAndReturn(run func() flow.ElementType) *MockNodeDataConsumer_Type_Call {
	_c.Call.Return(run)
	return _c
}

// Unbind provides a mock function with given fields:
func (_m *MockNodeDataConsumer) Unbind() error {
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

// MockNodeDataConsumer_Unbind_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Unbind'
type MockNodeDataConsumer_Unbind_Call struct {
	*mock.Call
}

// Unbind is a helper method to define mock.On call
func (_e *MockNodeDataConsumer_Expecter) Unbind() *MockNodeDataConsumer_Unbind_Call {
	return &MockNodeDataConsumer_Unbind_Call{Call: _e.mock.On("Unbind")}
}

func (_c *MockNodeDataConsumer_Unbind_Call) Run(run func()) *MockNodeDataConsumer_Unbind_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *MockNodeDataConsumer_Unbind_Call) Return(_a0 error) *MockNodeDataConsumer_Unbind_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockNodeDataConsumer_Unbind_Call) RunAndReturn(run func() error) *MockNodeDataConsumer_Unbind_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockNodeDataConsumer creates a new instance of MockNodeDataConsumer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockNodeDataConsumer(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockNodeDataConsumer {
	mock := &MockNodeDataConsumer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}