package yggdrasil

import (
	"context"
	"errors"
	"sync"
)

// ExtendedState holds arbitrary data for the session.
type ExtendedState struct {
	data sync.Map
}

func (es *ExtendedState) Get(key any) (any, bool) {
	return es.data.Load(key)
}

func (es *ExtendedState) Set(key, value any) {
	es.data.Store(key, value)
}

// eventRequest wraps the event with a response channel.
type eventRequest[S comparable, E comparable] struct {
	event   E
	payload any
	respCh  chan error
}

// Machine is the runtime instance.
type Machine[S comparable, E comparable] struct {
	// ... (Previous fields: def, current, history, extended, etc.) ...
	def      *MachineDef[S, E]
	current  S
	history  map[S]S
	Extended *ExtendedState

	// Communication
	eventCh   chan eventRequest[S, E]
	ctx       context.Context
	cancel    context.CancelFunc
	waitGroup sync.WaitGroup

	// Phase 2: Child Machines (Orthogonal Regions)
	childrenMu sync.Mutex // Protects access to the children slice
	children   []*Machine[S, E]
}

// NewMachine initializes the machine.
func NewMachine[S comparable, E comparable](def *MachineDef[S, E], parentCtx context.Context) *Machine[S, E] {
	ctx, cancel := context.WithCancel(parentCtx)
	return &Machine[S, E]{
		def:      def,
		current:  def.InitialState,
		history:  make(map[S]S),
		Extended: &ExtendedState{},
		eventCh:  make(chan eventRequest[S, E], 10),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start boots the actor loop and enters the initial state.
func (m *Machine[S, E]) Start() error {
	m.waitGroup.Add(1)

	// Execute Initial Entry Actions for the start state
	// Note: In a full impl, we should calculate the path from Root -> Initial
	if err := m.enterStateSequence(m.def.InitialState); err != nil {
		return err
	}

	go m.loop()
	return nil
}

// Stop sends a signal to kill the loop.
func (m *Machine[S, E]) Stop() {
	// 1. Stop all children first
	m.teardownChildren()

	// 2. Stop self
	m.cancel()
	m.waitGroup.Wait()
}

// SendEvent is the non-blocking public API.
func (m *Machine[S, E]) SendEvent(event E, payload any) <-chan error {
	respCh := make(chan error, 1)
	select {
	case m.eventCh <- eventRequest[S, E]{event: event, payload: payload, respCh: respCh}:
	case <-m.ctx.Done():
		respCh <- errors.New("machine is stopped")
		close(respCh)
	}
	return respCh
}

// loop is the Reactor.
func (m *Machine[S, E]) loop() {
	defer m.waitGroup.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case req := <-m.eventCh:
			// Process Event synchronously
			err := m.processEvent(req)

			// Reply to caller
			if req.respCh != nil {
				req.respCh <- err
				close(req.respCh)
			}
		}
	}
}

// Phase 2: Fork Logic
// spawnRegions creates and starts a new Machine for every Region defined in the target state.
func (m *Machine[S, E]) spawnRegions(regions []*MachineDef[S, E]) error {
	m.childrenMu.Lock()
	defer m.childrenMu.Unlock()

	for _, regionDef := range regions {
		// Create a child machine
		// Note: We pass m.ctx to ensure the child dies if the parent dies hard.
		// However, we usually want to control child lifecycle explicitly via Stop().
		child := NewMachine(regionDef, m.ctx)

		// Share Extended State?
		// The prompt says "Extended State... accessible by Actions".
		// Usually regions share the same data context.
		child.Extended = m.Extended

		if err := child.Start(); err != nil {
			return err
		}
		m.children = append(m.children, child)
	}
	return nil
}

// Phase 2: Join/Teardown Logic
// teardownChildren stops all active regions and waits for them to exit.
func (m *Machine[S, E]) teardownChildren() {
	m.childrenMu.Lock()
	defer m.childrenMu.Unlock()

	for _, child := range m.children {
		child.Stop()
	}
	// Clear the slice
	m.children = nil
}

// IsTerminal checks if the machine has reached its End state.
// This is useful for Join conditions.
func (m *Machine[S, E]) IsTerminal() bool {
	// Assuming you have an 'End' type or specific state ID logic
	return m.def.States[m.current].Type == End
}
