package activities

// ScriptTask is executed by a business process engine. The modeler or
// implementer defines a script in a language that the engine can interpret.
// When the Task is ready to start, the engine will execute the script. When
// the script is completed, the Task will also be completed.
type ScriptTask struct {
	ScriptFormat string
	Script       string
	task
}
