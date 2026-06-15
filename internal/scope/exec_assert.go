package scope

import "github.com/dr-dobermann/gobpm/pkg/exec"

// The concrete data-plane Frame satisfies the public pkg/exec.Frame contract
// the model's data-binding (LoadData/UploadData) operates on (ADR-012 v.1).
var _ exec.Frame = (*Frame)(nil)
