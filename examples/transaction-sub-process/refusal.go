package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// showRefusal registers a second booking whose Transaction names the BPMN
// method "##Store" — resource-manager coordination this engine has no
// coordinator for (ADR-028 §2.7). The model carries any method a document
// names; it is registration that refuses one nothing can perform, while the
// caller still holds an error return and nothing has run. The refusal is the
// modeler-facing message, so the example prints it once.
func showRefusal(engine *thresher.Thresher) error {
	fmt.Println("  registering a booking with method=\"##Store\" …")

	proc, err := buildProcess(newRunLog(),
		activities.WithTransactionMethod("##Store"))
	if err != nil {
		return fmt.Errorf("build store booking: %w", err)
	}

	_, err = engine.RegisterProcess(proc)
	if err == nil {
		return fmt.Errorf("a method with no coordinator registered — " +
			"registration must refuse it")
	}

	fmt.Printf("  ✗ refused: %v\n\n", err)

	return nil
}
