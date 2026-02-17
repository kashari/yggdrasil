package yggdrasil

import "errors"

var (
	// ErrNilDatabase is returned when attempting to create Yggdrasil with a nil database
	ErrNilDatabase = errors.New("yggdrasil: database connection is required")

	// ErrInstanceNotFound is returned when a workflow instance cannot be found
	ErrInstanceNotFound = errors.New("yggdrasil: workflow instance not found")

	// ErrDefinitionNotFound is returned when a workflow definition cannot be found
	ErrDefinitionNotFound = errors.New("yggdrasil: workflow definition not found")
)
