package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/gorm"
)

// HTTPTimeout controls the timeout used for HTTP actions. Set by the yggdrasil
// package from Config.HTTPTimeout at initialisation time.
var HTTPTimeout = 5 * time.Second

var Machines sync.Map

type Event struct {
	Name    string
	Payload map[string]interface{}
	Ack     chan bool
}

type Machine struct {
	Instance *model.WorkflowInstance
	Def      *model.WorkflowDefinition
	DB       *gorm.DB

	inbox chan Event
	stop  chan struct{}
}

func Spawn(db *gorm.DB, instanceID uuid.UUID) *Machine {
	// Fast path: machine already running.
	if existing, ok := Machines.Load(instanceID); ok {
		return existing.(*Machine)
	}

	var inst model.WorkflowInstance
	if err := db.First(&inst, "id = ?", instanceID).Error; err != nil {
		return nil
	}

	var def model.WorkflowDefinition
	db.Preload("States.EntryActions").
		Preload("States.ExitActions").
		Preload("Transitions.Actions").
		First(&def, "id = ?", inst.WorkflowDefID)

	m := &Machine{
		Instance: &inst,
		Def:      &def,
		DB:       db,
		inbox:    make(chan Event, 100),
		stop:     make(chan struct{}),
	}

	// LoadOrStore ensures only one Machine wins when two goroutines race here.
	actual, loaded := Machines.LoadOrStore(instanceID, m)
	if loaded {
		return actual.(*Machine)
	}

	go m.Loop()
	return m
}

func (m *Machine) Loop() {
	defer func() {
		Machines.Delete(m.Instance.ID)
		log.Printf("machine %s (%s) stopped", m.Instance.Name, m.Instance.ID)
	}()

	for {
		select {
		case evt := <-m.inbox:
			handled := m.processEvent(evt)
			if evt.Ack != nil {
				evt.Ack <- handled
			}

			m.Instance.UpdatedAt = time.Now()
			m.DB.Save(m.Instance)

			if m.Instance.Status == model.StatusCompleted || m.Instance.Status == model.StatusFailed {
				return
			}

		case <-m.stop:
			return
		}
	}
}

func (m *Machine) processEvent(evt Event) bool {
	if m.Instance.Status == model.StatusWaiting {
		isChildCallback := strings.HasPrefix(evt.Name, "CHILD_") || strings.HasPrefix(evt.Name, "SYS_")
		if !isChildCallback {
			log.Printf("ignored event %s on machine %s: waiting for child", evt.Name, m.Instance.ID)
			return false
		}
	}

	var selectedT *model.TransitionDefinition
	for i, t := range m.Def.Transitions {
		matchSource := (t.Source == m.Instance.CurrentState) || (t.IsCommon && t.Source == "*")
		if matchSource && t.Event == evt.Name {
			selectedT = &m.Def.Transitions[i]
			break
		}
	}

	if selectedT == nil {
		return false
	}

	m.runActions(m.Instance.CurrentState, "EXIT")

	for _, a := range selectedT.Actions {
		m.runAction(a)
	}

	m.Instance.CurrentState = selectedT.Target

	if m.Instance.Status == model.StatusWaiting {
		m.Instance.Status = model.StatusActive
	}

	isEnd := false
	for _, s := range m.Def.States {
		if s.StateID == selectedT.Target {
			if s.IsEndState {
				m.Instance.Status = model.StatusCompleted
				isEnd = true
			}
			for _, a := range s.EntryActions {
				m.runAction(a)
			}
		}
	}

	if isEnd && m.Instance.ParentInstanceID != nil {
		go m.notifyParent(*m.Instance.ParentInstanceID)
	}

	return true
}

func (m *Machine) runActions(stateID, kind string) {
	for _, s := range m.Def.States {
		if s.StateID == stateID {
			actions := s.EntryActions
			if kind == "EXIT" {
				actions = s.ExitActions
			}
			for _, a := range actions {
				m.runAction(a)
			}
		}
	}
}

func (m *Machine) runAction(a model.ActionDefinition) {
	switch a.Type {
	case model.ActionTypeHttp:
		go m.execHttp(a)
	case model.ActionTypeStartChild:
		m.execStartChild(a)
	}
}

func (m *Machine) execStartChild(a model.ActionDefinition) {
	childInst := model.WorkflowInstance{
		WorkflowDefID:    a.ProductId,
		CurrentState:     "INIT",
		Status:           model.StatusActive,
		ParentInstanceID: &m.Instance.ID,
		Variables:        m.Instance.Variables,
	}

	var childDef model.WorkflowDefinition
	if err := m.DB.First(&childDef, "id = ?", a.ProductId).Error; err == nil {
		childInst.CurrentState = childDef.InitialState
	}

	if err := m.DB.Create(&childInst).Error; err != nil {
		log.Printf("failed to create child machine: %v", err)
		return
	}

	Spawn(m.DB, childInst.ID)
	log.Printf("machine %s started child %s", m.Instance.ID, childInst.ID)

	if a.Delegate {
		m.Instance.Status = model.StatusWaiting
		log.Printf("machine %s is now waiting for child", m.Instance.ID)
	}
}

func (m *Machine) notifyParent(parentID uuid.UUID) {
	parent := Spawn(m.DB, parentID)
	if parent == nil {
		return
	}

	ack := make(chan bool)
	parent.inbox <- Event{Name: "CHILD_COMPLETED", Ack: ack}
	<-ack
	log.Printf("child %s notified parent %s", m.Instance.ID, parentID)
}

func (m *Machine) execHttp(a model.ActionDefinition) {
	var vars map[string]any
	json.Unmarshal(m.Instance.Variables, &vars)

	url := a.URL
	for k, v := range vars {
		url = strings.ReplaceAll(url, fmt.Sprintf("{%s}", k), fmt.Sprintf("%v", v))
	}

	req, err := http.NewRequest(a.Method, url, bytes.NewBufferString(a.Body))
	if err != nil {
		log.Printf("http action: failed to build request to %s: %v", url, err)
		return
	}

	client := &http.Client{Timeout: HTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("http action: request to %s failed: %v", url, err)
		return
	}
	resp.Body.Close()
}

func (m *Machine) Inbox() chan<- Event {
	return m.inbox
}

func (m *Machine) Stop() chan<- struct{} {
	return m.stop
}
