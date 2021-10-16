package model

import "fmt"

type Model struct {
	name    string
	version string
	nodes   []Node
}

func (m Model) Name() string {
	return m.name
}

func (m Model) Version() string {
	return m.version
}

type ModelError struct {
	m   *Model
	msg string
}

func (me ModelError) Error() string {
	if me.m == nil {
		return me.msg
	}

	return fmt.Sprintf("M[%s V:%s]: %s",
		me.m.Name(), me.m.version, me.msg)
}

func NewModelError(m *Model, msg string) error {
	return ModelError{m, msg}
}

func (m *Model) AddNode(n Node) error {
	if n == nil {
		return NewModelError(m, "Couldn't add en empty Node")
	}

	for _, nd := range m.nodes {
		if nd.IsEqual(n) {
			return NewModelError(m, "This task already exists in the model")
		}
	}

	return nil
}
