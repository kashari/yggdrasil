package yggdrasil

import (
	"context"
)

// StateType defines the behavior of the node.
type StateType int

const (
	Atomic StateType = iota
	Composite
	RegionRoot
	History // Target resolves to last active child
	Choice  // Target resolves dynamically based on Guards
	End
)

// Action is a function executed during transitions.
type Action[S comparable, E comparable] func(ctx context.Context, m *Machine[S, E]) error

// Guard is a boolean predicate to validate if a transition is allowed.
type Guard[S comparable, E comparable] func(ctx context.Context, m *Machine[S, E]) bool

// StateDef represents the immutable definition of a state.
type StateDef[S comparable, E comparable] struct {
	ID      S
	Type    StateType
	Parent  S
	Initial S

	EntryActions []Action[S, E]
	ExitActions  []Action[S, E]

	// Phase 2: Orthogonal Regions
	// A generic State can host multiple child Machines running concurrently.
	Regions []*MachineDef[S, E]
}

// TransitionDef defines a directed edge in the graph.
type TransitionDef[S comparable, E comparable] struct {
	Source S
	Target S
	Event  E
	Guard  Guard[S, E]
	Action Action[S, E]
}

// MachineDef is the complete static graph.
type MachineDef[S comparable, E comparable] struct {
	States       map[S]*StateDef[S, E]
	Transitions  map[S]map[E]*TransitionDef[S, E]
	InitialState S
}
