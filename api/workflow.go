package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/kashari/yggdrasil/engine"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Server struct {
	DB *gorm.DB
}

func (s *Server) CreateDefinitions(w http.ResponseWriter, r *http.Request) {
	var defs []model.WorkflowDefinition
	if err := json.NewDecoder(r.Body).Decode(&defs); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	tx := s.DB.Begin()
	for _, d := range defs {
		if err := tx.Save(&d).Error; err != nil {
			tx.Rollback()
			http.Error(w, err.Error(), 500)
			return
		}
	}

	tx.Commit()
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) StartInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowID string                 `json:"workflowId"`
		Variables  map[string]interface{} `json:"variables"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var def model.WorkflowDefinition
	if err := s.DB.First(&def, "id = ?", req.WorkflowID).Error; err != nil {
		http.Error(w, "Definition not found", 404)
		return
	}

	vJson, _ := json.Marshal(req.Variables)
	inst := model.WorkflowInstance{
		WorkflowDefID: req.WorkflowID,
		CurrentState:  def.InitialState,
		Status:        model.StatusActive,
		Variables:     datatypes.JSON(vJson),
	}

	s.DB.Create(&inst)
	engine.Spawn(s.DB, inst.ID)

	json.NewEncoder(w).Encode(map[string]string{"id": inst.ID.String()})
}

func (s *Server) SendEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstanceID string `json:"instanceId"`
		Event      string `json:"event"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	uid, err := uuid.Parse(req.InstanceID)
	if err != nil {
		http.Error(w, "Invalid UUID", 400)
		return
	}

	m := engine.Spawn(s.DB, uid)
	if m == nil {
		http.Error(w, "Instance not found", 404)
		return
	}

	ack := make(chan bool)
	m.Inbox() <- engine.Event{Name: req.Event, Ack: ack}

	if <-ack {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusConflict)
	}
}
