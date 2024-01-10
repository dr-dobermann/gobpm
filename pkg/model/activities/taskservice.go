package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

type ServiceTask struct {
	activity

	operation *service.Operation // invoked operation
}
