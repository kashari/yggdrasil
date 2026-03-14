package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// TransitionHistory records every transition that was successfully executed
// on a WorkflowInstance. It is written synchronously inside processEvent
// after a transition fires, so the history is always consistent with the
// instance's CurrentState.
type TransitionHistory struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	OccurredAt time.Time `gorm:"index"`

	InstanceID uuid.UUID `gorm:"type:uuid;index"`
	// Human-readable name of the machine (denormalised for easy querying).
	InstanceName string

	WorkflowDefID string

	FromState string
	Event     string
	ToState   string

	// Payload is the event payload that triggered the transition, stored as
	// JSON so it can be surfaced in the instance report.
	Payload datatypes.JSON
}
