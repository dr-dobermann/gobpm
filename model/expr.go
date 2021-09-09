package model

type Variable struct {
	ID    uint64
	Doc   Documentation
	Value interface{}
}

type VarsPack struct {
	ID   uint64
	Vars []Variable
}

type Expression struct {
}
