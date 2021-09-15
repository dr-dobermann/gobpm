package model

type Variable struct {
	BaseElement
	value interface{}
}

type VPack struct {
	BaseElement
<<<<<<< HEAD
	vars map[string]*Variable
=======
	vars []Variable
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
}

type Expression struct {
	BaseElement
	language string // Formal Expression language (FEEL) in URI format
	body     string // in future it could be changed to another specialized type or
	// realized by interface
<<<<<<< HEAD
	retType string // TODO: should be changed to standard go type in the future
=======
	retType string // should be changed to standard go type in the future
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
}
