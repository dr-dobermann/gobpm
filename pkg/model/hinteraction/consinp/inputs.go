package consinp

import (
	"fmt"
	"io"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

type (
	inputBase struct {
		inputName, prompt string
	}

	intInput struct {
		inputBase
	}

	stringInput struct {
		inputBase
	}

	messager struct {
		inputBase
	}
)

func (ib *inputBase) name() string {
	return ib.inputName
}

// ------------------ Input interface -----------------------------------------

// Read implements Read for integer input.
func (ii *intInput) read(src io.Reader) (data.Data, error) {
	v := 0

	fmt.Print(ii.prompt)

	if _, err := fmt.Fscanln(src, &v); err != nil {
		return nil, fmt.Errorf("couldn't read value for input: %w", err)
	}

	return createData(ii.inputName, values.NewVariable(v))
}

func (ii *stringInput) read(src io.Reader) (data.Data, error) {
	v := ""

	fmt.Print(ii.prompt)

	if _, err := fmt.Fscanln(src, &v); err != nil {
		return nil, fmt.Errorf("couldn't read value for input: %w", err)
	}

	return createData(ii.inputName, values.NewVariable(v))
}

func (ii *messager) read(src io.Reader) (data.Data, error) {
	fmt.Println(ii.prompt)

	return nil, nil
}

// ----------------------------------------------------------------------------
