package flow

// GatewayType represents different types of BPMN gateways.
type GatewayType string

const (
	// Exclusive represents a BPMN exclusive gateway.
	Exclusive  GatewayType = "Exclusive"
	// Inclusive represents a BPMN inclusive gateway.
	Inclusive  GatewayType = "Inclusive"
	// Parallel represents a BPMN parallel gateway.
	Parallel   GatewayType = "Parallel"
	// EventBased represents a BPMN event-based gateway.
	EventBased GatewayType = "EventBased"
)

// GatewayNode represents a BPMN gateway node interface.
type GatewayNode interface {
	Node

	GatewayType() GatewayType
}
