package flow

type GatewayType string

const (
	Exclusive  GatewayType = "Exclusive"
	Inclusive  GatewayType = "Inclusive"
	Parallel   GatewayType = "Parallel"
	EventBased GatewayType = "EventBased"
)

type GatewayNode interface {
	Node

	GatewayType() GatewayType
}
