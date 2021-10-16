package model

type MessageFlow struct {
	FlowElement
	startRef Id
	endRef   Id
	message  Id
}

type Message struct {
	FlowElement
	vPack VPack
	flow  Id
	event Id // Message event processor
}
