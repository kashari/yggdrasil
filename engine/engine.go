package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kashari/golog"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/gorm"
)

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
		golog.Info("machine {} ({}) stopped", m.Instance.Name, m.Instance.ID)
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
			golog.Info("machine {} ({}) received stop signal", m.Instance.Name, m.Instance.ID)
			return
		}
	}
}

func (m *Machine) processEvent(evt Event) bool {
	if evt.Name == "_CHILD_DONE_TICK" {
		if m.Instance.PendingChildren > 0 {
			m.Instance.PendingChildren--
		}
		if m.Instance.PendingChildren == 0 {
			completionEvent, _ := evt.Payload["_completionEvent"].(string)
			if completionEvent == "" {
				completionEvent = "CHILD_COMPLETED"
			}

			realEvt := Event{Name: completionEvent, Payload: evt.Payload}
			return m.processEvent(realEvt)
		}

		golog.Info("machine {} still waiting for {} child(ren)", m.Instance.ID, m.Instance.PendingChildren)
		return true
	}

	if m.Instance.Status == model.StatusWaiting {
		isChildCallback := strings.HasPrefix(evt.Name, "CHILD_") || strings.HasPrefix(evt.Name, "SYS_")
		if !isChildCallback {
			golog.Info("ignored event {} on machine {}: waiting for child", evt.Name, m.Instance.ID)
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

	fromState := m.Instance.CurrentState
	m.Instance.CurrentState = selectedT.Target

	if m.Instance.Status == model.StatusWaiting {
		m.Instance.Status = model.StatusActive
	}

	isEnd := false
	for _, s := range m.Def.States {
		if s.StateID == selectedT.Target {
			if s.IsEndState {
				m.Instance.Status = model.StatusCompleted
				m.Instance.TerminalState = s.StateID
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
	var payloadJSON []byte
	if evt.Payload != nil {
		payloadJSON, _ = json.Marshal(evt.Payload)
	}
	m.DB.Create(&model.TransitionHistory{
		OccurredAt:    time.Now(),
		InstanceID:    m.Instance.ID,
		InstanceName:  m.Instance.Name,
		WorkflowDefID: m.Instance.WorkflowDefID,
		FromState:     fromState,
		Event:         evt.Name,
		ToState:       selectedT.Target,
		Payload:       payloadJSON,
	})

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
		golog.Info("failed to create child machine: {}", err)
		return
	}

	Spawn(m.DB, childInst.ID)
	golog.Info("machine {} started child {}", m.Instance.ID, childInst.ID)

	if a.Delegate {
		m.Instance.PendingChildren++
		m.Instance.Status = model.StatusWaiting
		golog.Info("machine {} is now waiting for child (pending={})", m.Instance.ID, m.Instance.PendingChildren)
	}
}

func (m *Machine) notifyParent(parentID uuid.UUID) {
	parent := Spawn(m.DB, parentID)
	if parent == nil {
		return
	}

	var parentVars, childVars map[string]any
	json.Unmarshal(parent.Instance.Variables, &parentVars)
	json.Unmarshal(m.Instance.Variables, &childVars)
	if parentVars == nil {
		parentVars = make(map[string]any)
	}
	for k, v := range childVars {
		parentVars[k] = v
	}
	if merged, err := json.Marshal(parentVars); err == nil {
		parent.Instance.Variables = merged
	}

	eventName := "CHILD_" + m.Instance.TerminalState
	for _, t := range parent.Def.Transitions {
		for _, a := range t.Actions {
			if a.Type == model.ActionTypeStartChild && a.ProductId == m.Instance.WorkflowDefID && a.CompletionEvent != "" {
				eventName = a.CompletionEvent
				break
			}
		}
	}

	if eventName == "CHILD_"+m.Instance.TerminalState {
		for _, s := range parent.Def.States {
			for _, a := range s.EntryActions {
				if a.Type == model.ActionTypeStartChild && a.ProductId == m.Instance.WorkflowDefID && a.CompletionEvent != "" {
					eventName = a.CompletionEvent
				}
			}
		}
	}

	ack := make(chan bool)
	parent.inbox <- Event{
		Name:    "_CHILD_DONE_TICK",
		Payload: map[string]any{"_completionEvent": eventName},
		Ack:     ack,
	}
	<-ack
	golog.Info("child {} notified parent {} (event={})", m.Instance.ID, parentID, eventName)
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
		golog.Info("http action: failed to build request to {}: {}", url, err)
		return
	}

	client := &http.Client{Timeout: HTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		golog.Info("http action: request to {} failed: {}", url, err)
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
