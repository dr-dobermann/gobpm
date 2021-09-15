package model

type Variable struct {
	BaseElement
	value interface{}
}

type VPack struct {
	BaseElement
	vars []Variable
}

type Expression struct {
	BaseElement
	language string // Formal Expression language (FEEL) in URI format
	body     string // in future it could be changed to another specialized type or
	// realized by interface
	retType string // should be changed to standard go type in the future
}
