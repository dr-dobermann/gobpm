package model

type MessageFlow struct {
	ID       uint64
	Doc      Documentation
	StartRef uint64
	EndRef   uint64
	Dir      FlowDirection
}

type Message struct {
	ID    uint64
	Doc   Documentation
	VPack VarsPack
	Flow  *MessageFlow
}
