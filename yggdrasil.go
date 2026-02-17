package yggdrasil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kashari/yggdrasil/engine"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type (
	WorkflowDefinition   = model.WorkflowDefinition
	WorkflowInstance     = model.WorkflowInstance
	StateDefinition      = model.StateDefinition
	TransitionDefinition = model.TransitionDefinition
	ActionDefinition     = model.ActionDefinition
)

const (
	StatusActive    = model.StatusActive
	StatusCompleted = model.StatusCompleted
	StatusFailed    = model.StatusFailed
	StatusWaiting   = model.StatusWaiting
)

const (
	ActionTypeHttp       = model.ActionTypeHttp
	ActionTypeStartChild = model.ActionTypeStartChild
)

type Config struct {
	DB          *gorm.DB
	HTTPTimeout time.Duration
}

type Yggdrasil struct {
	db          *gorm.DB
	httpTimeout time.Duration
	mu          sync.RWMutex
}

func New(cfg Config) (*Yggdrasil, error) {
	if cfg.DB == nil {
		return nil, ErrNilDatabase
	}

	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &Yggdrasil{
		db:          cfg.DB,
		httpTimeout: timeout,
	}, nil
}

func (y *Yggdrasil) AutoMigrate() error {
	return y.db.AutoMigrate(
		&model.WorkflowDefinition{},
		&model.StateDefinition{},
		&model.TransitionDefinition{},
		&model.ActionDefinition{},
		&model.WorkflowInstance{},
	)
}

func (y *Yggdrasil) DB() *gorm.DB {
	return y.db
}

func (y *Yggdrasil) Shutdown(ctx context.Context) error {
	engine.Machines.Range(func(key, value any) bool {
		if m, ok := value.(*engine.Machine); ok {
			select {
			case m.Stop() <- struct{}{}:
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	return ctx.Err()
}

func (y *Yggdrasil) CreateDefinition(def *WorkflowDefinition) error {
	return y.db.Save(def).Error
}

func (y *Yggdrasil) CreateDefinitions(defs []WorkflowDefinition) error {
	return y.db.Transaction(func(tx *gorm.DB) error {
		for i := range defs {
			if err := tx.Save(&defs[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (y *Yggdrasil) GetDefinition(id string) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	err := y.db.Preload("States.EntryActions").
		Preload("States.ExitActions").
		Preload("Transitions.Actions").
		First(&def, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &def, nil
}

func (y *Yggdrasil) StartWorkflow(workflowID string, variables map[string]interface{}) (*WorkflowInstance, error) {
	var def WorkflowDefinition
	if err := y.db.First(&def, "id = ?", workflowID).Error; err != nil {
		return nil, err
	}

	vJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}

	inst := WorkflowInstance{
		WorkflowDefID: workflowID,
		CurrentState:  def.InitialState,
		Status:        StatusActive,
		Variables:     datatypes.JSON(vJSON),
	}

	if err := y.db.Create(&inst).Error; err != nil {
		return nil, err
	}

	engine.Spawn(y.db, inst.ID)
	return &inst, nil
}

func (y *Yggdrasil) SendEvent(instanceID uuid.UUID, event string) (bool, error) {
	return y.SendEventWithPayload(instanceID, event, nil)
}

func (y *Yggdrasil) SendEventWithPayload(instanceID uuid.UUID, event string, payload map[string]interface{}) (bool, error) {
	m := engine.Spawn(y.db, instanceID)
	if m == nil {
		return false, ErrInstanceNotFound
	}

	ack := make(chan bool)
	m.Inbox() <- engine.Event{Name: event, Payload: payload, Ack: ack}
	return <-ack, nil
}

func (y *Yggdrasil) GetInstance(instanceID uuid.UUID) (*WorkflowInstance, error) {
	var inst WorkflowInstance
	if err := y.db.First(&inst, "id = ?", instanceID).Error; err != nil {
		return nil, err
	}
	return &inst, nil
}

func (y *Yggdrasil) ListInstances(workflowID string, status string, limit int) ([]WorkflowInstance, error) {
	query := y.db.Model(&WorkflowInstance{})

	if workflowID != "" {
		query = query.Where("workflow_def_id = ?", workflowID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}

	var instances []WorkflowInstance
	if err := query.Order("created_at DESC").Find(&instances).Error; err != nil {
		return nil, err
	}
	return instances, nil
}

func (y *Yggdrasil) HandleCreateDefinitions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var defs []WorkflowDefinition
		if err := json.NewDecoder(r.Body).Decode(&defs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := y.CreateDefinitions(defs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}

func (y *Yggdrasil) HandleStartInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			WorkflowID string                 `json:"workflowId"`
			Variables  map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		inst, err := y.StartWorkflow(req.WorkflowID, req.Variables)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				http.Error(w, "Definition not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": inst.ID.String()})
	}
}

func (y *Yggdrasil) HandleSendEvent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InstanceID string                 `json:"instanceId"`
			Event      string                 `json:"event"`
			Payload    map[string]interface{} `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		uid, err := uuid.Parse(req.InstanceID)
		if err != nil {
			http.Error(w, "Invalid UUID", http.StatusBadRequest)
			return
		}

		handled, err := y.SendEventWithPayload(uid, req.Event, req.Payload)
		if err != nil {
			if err == ErrInstanceNotFound {
				http.Error(w, "Instance not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if handled {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
	}
}

func (y *Yggdrasil) HandleGetInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			http.Error(w, "Missing id parameter", http.StatusBadRequest)
			return
		}

		uid, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "Invalid UUID", http.StatusBadRequest)
			return
		}

		inst, err := y.GetInstance(uid)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				http.Error(w, "Instance not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(inst)
	}
}

func (y *Yggdrasil) HandleListInstances() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.URL.Query().Get("workflowId")
		status := r.URL.Query().Get("status")
		limitStr := r.URL.Query().Get("limit")

		limit := 0
		if limitStr != "" {
			fmt.Sscanf(limitStr, "%d", &limit)
		}

		instances, err := y.ListInstances(workflowID, status, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
	}
}
