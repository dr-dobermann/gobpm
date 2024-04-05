package flow

type GatewayNode interface {
	Node

	GatewayType() string
}
