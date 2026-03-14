package analyzer

import (
	"fmt"
	"strings"

	"github.com/kashari/yggdrasil/model"
)

// DefinitionJourney produces a rich textual walk-through of a workflow
// definition. For every state it describes:
//   - the state role (initial / intermediate / terminal)
//   - entry and exit actions
//   - a plain-English summary of what happens in this state
//   - all transitions out (event → target state)
func DefinitionJourney(def *model.WorkflowDefinition) string {
	var sb strings.Builder

	sb.WriteString(banner("═", 72))
	sb.WriteString(fmt.Sprintf("  WORKFLOW JOURNEY  —  %s\n", def.ID))
	sb.WriteString(banner("═", 72))
	sb.WriteString(fmt.Sprintf("  Initial state : %s\n", def.InitialState))
	sb.WriteString(fmt.Sprintf("  Total states  : %d\n", len(def.States)))
	sb.WriteString(fmt.Sprintf("  Total transitions: %d\n", len(def.Transitions)))
	sb.WriteString(banner("─", 72))
	sb.WriteString("\n")

	transitionsFrom := make(map[string][]model.TransitionDefinition)
	transitionsTo := make(map[string][]model.TransitionDefinition)
	for _, t := range def.Transitions {
		transitionsFrom[t.Source] = append(transitionsFrom[t.Source], t)
		transitionsTo[t.Target] = append(transitionsTo[t.Target], t)
	}

	var commonTransitions []model.TransitionDefinition
	for _, t := range def.Transitions {
		if t.IsCommon && t.Source == "*" {
			commonTransitions = append(commonTransitions, t)
		}
	}

	ordered := bfsOrder(def.InitialState, def.States, transitionsFrom)

	for i, state := range ordered {
		role := stateRole(state, def.InitialState)

		fmt.Fprintf(&sb, "  [%d] STATE: %s", i+1, state.StateID)
		if role != "" {
			fmt.Fprintf(&sb, "  (%s)", role)
		}
		sb.WriteString("\n")
		sb.WriteString(banner("·", 64))

		fmt.Fprintf(&sb, "  Summary  : %s\n", stateSummary(state, def, transitionsFrom, transitionsTo))

		if len(state.EntryActions) > 0 {
			sb.WriteString("  On Entry :\n")
			for _, a := range state.EntryActions {
				sb.WriteString(fmt.Sprintf("    %s\n", describeAction(a)))
			}
		}

		if len(state.ExitActions) > 0 {
			sb.WriteString("  On Exit  :\n")
			for _, a := range state.ExitActions {
				fmt.Fprintf(&sb, "    %s\n", describeAction(a))
			}
		}

		outbound := transitionsFrom[state.StateID]
		if len(outbound) > 0 {
			sb.WriteString("  Transitions out:\n")
			for _, t := range outbound {
				actionSuffix := ""
				if len(t.Actions) > 0 {
					descs := make([]string, len(t.Actions))
					for j, a := range t.Actions {
						descs[j] = describeAction(a)
					}
					actionSuffix = fmt.Sprintf("  [triggers: %s]", strings.Join(descs, "; "))
				}
				fmt.Fprintf(&sb, "    ▶  event %-30s  →  %s%s\n",
					quote(t.Event), t.Target, actionSuffix)
			}
		} else if state.IsEndState {
			sb.WriteString("  Transitions out: none (terminal state)\n")
		}

		sb.WriteString("\n")
	}

	if len(commonTransitions) > 0 {
		sb.WriteString(banner("─", 72))
		sb.WriteString("  GLOBAL TRANSITIONS  (fire from any state)\n")
		sb.WriteString(banner("─", 72))
		for _, t := range commonTransitions {
			sb.WriteString(fmt.Sprintf("  ▶  event %-30s  →  %s\n", quote(t.Event), t.Target))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(banner("═", 72))
	return sb.String()
}

func banner(char string, width int) string {
	return strings.Repeat(char, width) + "\n"
}

func quote(s string) string { return fmt.Sprintf("'%s'", s) }

func stateRole(s model.StateDefinition, initialState string) string {
	if s.StateID == initialState {
		return "INITIAL"
	}
	if s.IsEndState {
		return "TERMINAL"
	}
	return "intermediate"
}

func stateSummary(
	s model.StateDefinition,
	def *model.WorkflowDefinition,
	from, to map[string][]model.TransitionDefinition,
) string {
	role := "intermediate"
	if s.StateID == def.InitialState {
		role = "starting"
	}
	if s.IsEndState {
		role = "terminal"
	}

	incoming := to[s.StateID]
	outgoing := from[s.StateID]

	inDesc := "the workflow begins here"
	if len(incoming) > 0 {
		evts := make([]string, len(incoming))
		for i, t := range incoming {
			evts[i] = quote(t.Event)
		}
		inDesc = fmt.Sprintf("reached via event(s) %s", strings.Join(evts, ", "))
	}

	outDesc := "no further transitions (workflow ends)"
	if len(outgoing) > 0 {
		targets := make([]string, len(outgoing))
		for i, t := range outgoing {
			targets[i] = t.Target
		}
		outDesc = fmt.Sprintf("can advance to: %s", strings.Join(targets, ", "))
	}

	entryDesc := ""
	if len(s.EntryActions) > 0 {
		kinds := make([]string, len(s.EntryActions))
		for i, a := range s.EntryActions {
			kinds[i] = a.Type
		}
		entryDesc = fmt.Sprintf(" Triggers %d entry action(s) (%s) on arrival.",
			len(s.EntryActions), strings.Join(kinds, ", "))
	}

	return fmt.Sprintf("A %s state — %s; %s.%s", role, inDesc, outDesc, entryDesc)
}

func describeAction(a model.ActionDefinition) string {
	switch a.Type {
	case model.ActionTypeHttp:
		return fmt.Sprintf("[HTTP] %s %s", a.Method, a.URL)
	case model.ActionTypeStartChild:
		delegateFlag := ""
		if a.Delegate {
			delegateFlag = " (parent waits)"
		}
		completionFlag := ""
		if a.CompletionEvent != "" {
			completionFlag = fmt.Sprintf(", fires '%s' on completion", a.CompletionEvent)
		}
		return fmt.Sprintf("[START_CHILD] spawns '%s'%s%s", a.ProductId, delegateFlag, completionFlag)
	default:
		return fmt.Sprintf("[%s]", a.Type)
	}
}

func bfsOrder(
	initialState string,
	states []model.StateDefinition,
	transitionsFrom map[string][]model.TransitionDefinition,
) []model.StateDefinition {
	stateMap := make(map[string]model.StateDefinition, len(states))
	for _, s := range states {
		stateMap[s.StateID] = s
	}

	visited := make(map[string]bool)
	queue := []string{initialState}
	var ordered []model.StateDefinition

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if s, ok := stateMap[cur]; ok {
			ordered = append(ordered, s)
		}
		for _, t := range transitionsFrom[cur] {
			if !visited[t.Target] {
				queue = append(queue, t.Target)
			}
		}
	}

	for _, s := range states {
		if !visited[s.StateID] {
			ordered = append(ordered, s)
		}
	}

	return ordered
}
