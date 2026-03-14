package yggdrasil

import "errors"

var (
	// ErrNilDatabase is returned when attempting to create Yggdrasil with a nil database
	ErrNilDatabase = errors.New("yggdrasil: database connection is required")

	// ErrInstanceNotFound is returned when a workflow instance cannot be found
	ErrInstanceNotFound = errors.New("yggdrasil: workflow instance not found")

	// ErrDefinitionNotFound is returned when a workflow definition cannot be found
	ErrDefinitionNotFound = errors.New("yggdrasil: workflow definition not found")

	// ErrMachineTerminated is returned when attempting to resume a machine that is in a terminal state
	ErrMachineTerminated = errors.New("yggdrasil: machine is in a terminal state and cannot be resumed")
)
