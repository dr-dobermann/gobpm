package model

type Model struct {
	name    string
	version string
}

func (m Model) Name() string {
	return m.name
}

