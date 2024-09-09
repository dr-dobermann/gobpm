package consinp

import (
	"fmt"
	"io"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

type (
	// inputBase provides base input functionality for all
	// input types.
	inputBase struct {
		inputName, prompt string
	}

	// intInput provides input functionality for integers.
	intInput struct {
		inputBase
	}

	// stringInput provides input functionality for strings.
	stringInput struct {
		inputBase
	}

	// messager just prints its prompt and doesn't expect
	// any user inputs.
	messager struct {
		inputBase
	}
)

// ------------------ Input interface -----------------------------------------

// name implements input interface for all inputs through inputBase.
func (ib *inputBase) name() string {
	return ib.inputName
}

// read implements input for integers.
func (ii *intInput) read(src io.Reader) (data.Data, error) {
	v := 0

	fmt.Print(ii.prompt)

	if _, err := fmt.Fscanln(src, &v); err != nil {
		return nil, fmt.Errorf("couldn't read value for input: %w", err)
	}

	return createData(ii.inputName, values.NewVariable(v))
}

// read inplements input interface for string input
func (ii *stringInput) read(src io.Reader) (data.Data, error) {
	v := ""

	fmt.Print(ii.prompt)

	if _, err := fmt.Fscanln(src, &v); err != nil {
		return nil, fmt.Errorf("couldn't read value for input: %w", err)
	}

	return createData(ii.inputName, values.NewVariable(v))
}

// read implements input interface for messager prompt presentation.
func (ii *messager) read(src io.Reader) (data.Data, error) {
	fmt.Println(ii.prompt)

	return nil, nil
}

// ----------------------------------------------------------------------------
