package activities

// BusinessRuleTask provides a mechanism for the Process to provide input to a
// Business Rules Engine and to get the output of calculations that the Business
// Rules Engine might provide. The InputOutputSpecification of the Task will allow
// the Process to send data to and receive data from the Business Rules Engine.
type BusinessRuleTask struct {
	Implementation string
	task
}
