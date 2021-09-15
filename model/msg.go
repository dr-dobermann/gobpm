package model

type MessageFlow struct {
	FlowElement
	startRef id
	endRef   id
	dir      FlowDirection
	message  id
}

type Message struct {
	FlowElement
	vPack VPack
	flow  id
	event id // Message event processor
}
