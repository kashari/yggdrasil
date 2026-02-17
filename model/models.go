package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	StatusActive    = "ACTIVE"
	StatusCompleted = "COMPLETED"
	StatusFailed    = "FAILED"
	StatusWaiting   = "WAITING_FOR_CHILD"
)

const (
	ActionTypeHttp       = "HTTP"
	ActionTypeStartChild = "START_CHILD"
)

type WorkflowDefinition struct {
	ID           string `gorm:"primaryKey"`
	InitialState string
	States       []StateDefinition      `gorm:"foreignKey:WorkflowID;constraint:OnDelete:CASCADE"`
	Transitions  []TransitionDefinition `gorm:"foreignKey:WorkflowID;constraint:OnDelete:CASCADE"`
}

type StateDefinition struct {
	gorm.Model
	WorkflowID   string `gorm:"index"`
	StateID      string
	IsEndState   bool
	EntryActions []ActionDefinition `gorm:"foreignKey:StateID;constraint:OnDelete:CASCADE"`
	ExitActions  []ActionDefinition `gorm:"foreignKey:StateID;constraint:OnDelete:CASCADE"`
}

type TransitionDefinition struct {
	gorm.Model
	WorkflowID string `gorm:"index"`
	Source     string
	Target     string
	Event      string
	IsCommon   bool
	Actions    []ActionDefinition `gorm:"foreignKey:TransitionID;constraint:OnDelete:CASCADE"`
}

type ActionDefinition struct {
	gorm.Model
	StateID      *uint
	TransitionID *uint
	Type         string

	Method string
	URL    string
	Body   string

	ProductId       string
	Delegate        bool
	CompletionEvent string
}

type WorkflowInstance struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`

	WorkflowDefID string
	CurrentState  string
	Status        string

	ParentInstanceID *uuid.UUID `gorm:"type:uuid;index"`

	Variables datatypes.JSON
}

func (w *WorkflowInstance) BeforeCreate(tx *gorm.DB) (err error) {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	return
}
