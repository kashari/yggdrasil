package analyzer

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/gorm"
)

// InstanceReport generates a self-contained HTML report for a workflow
// instance. It includes:
//   - Instance metadata (name, definition, status, current state)
//   - Full transition history table (from the history DB table)
//   - Forward-path projection: all reachable states from current state,
//     shown as a textual tree with the events needed to reach them.
func InstanceReport(db *gorm.DB, instanceID uuid.UUID, inst *model.WorkflowInstance, def *model.WorkflowDefinition) (string, error) {
	var history []model.TransitionHistory
	if err := db.Where("instance_id = ?", instanceID).
		Order("occurred_at ASC").
		Find(&history).Error; err != nil {
		return "", err
	}

	forwardPaths := computeForwardPaths(inst.CurrentState, def, 6)

	metaHTML := buildMetaSection(inst)
	historyHTML := buildHistoryTable(history)
	forwardHTML := buildForwardSection(inst.CurrentState, forwardPaths, inst.Status)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1.0"/>
  <title>Instance Report — %s</title>
  <style>
    *{box-sizing:border-box;margin:0;padding:0}
    body{background:#0f1117;color:#e2e8f0;font-family:'Segoe UI',system-ui,sans-serif;padding:32px 24px;line-height:1.6}
    h1{font-size:1.6rem;font-weight:700;color:#f8fafc;margin-bottom:4px}
    h2{font-size:1.1rem;font-weight:600;color:#cbd5e1;margin:32px 0 12px;padding-bottom:8px;border-bottom:1px solid #1e293b}
    .badge{display:inline-block;padding:2px 10px;border-radius:999px;font-size:0.75rem;font-weight:600;letter-spacing:.05em}
    .badge-active{background:#1e3a5f;color:#60a5fa}
    .badge-completed{background:#064e3b;color:#34d399}
    .badge-failed{background:#450a0a;color:#f87171}
    .badge-waiting{background:#422006;color:#fb923c}
    .meta-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:16px;margin-bottom:8px}
    .meta-card{background:#1e2433;border:1px solid #2d3748;border-radius:12px;padding:16px}
    .meta-card .label{font-size:0.72rem;color:#64748b;text-transform:uppercase;letter-spacing:.08em;margin-bottom:4px}
    .meta-card .value{font-size:0.95rem;color:#f1f5f9;font-weight:500;word-break:break-all}
    table{width:100%%;border-collapse:collapse;font-size:0.85rem}
    thead tr{background:#1e2433}
    th{text-align:left;padding:10px 14px;color:#64748b;font-weight:600;font-size:0.75rem;text-transform:uppercase;letter-spacing:.06em;border-bottom:1px solid #2d3748}
    td{padding:10px 14px;border-bottom:1px solid #1a2235;vertical-align:top}
    tr:hover td{background:#182030}
    .state-from{color:#94a3b8}
    .state-to{color:#f1f5f9;font-weight:600}
    .event-pill{display:inline-block;background:#1e3a5f;color:#93c5fd;padding:2px 8px;border-radius:6px;font-size:0.78rem;font-family:monospace}
    .payload-pre{font-size:0.75rem;color:#64748b;font-family:monospace;white-space:pre-wrap;max-width:280px}
    .ts{color:#475569;font-size:0.78rem}
    .empty-msg{color:#475569;font-style:italic;padding:20px 0}
    /* forward path tree */
    .forward-tree{background:#1e2433;border:1px solid #2d3748;border-radius:12px;padding:20px;font-family:monospace;font-size:0.85rem}
    .path-line{padding:2px 0}
    .path-current{color:#f59e0b;font-weight:700}
    .path-terminal{color:#10b981}
    .path-state{color:#a5b4fc}
    .path-event{color:#64748b}
    .path-child{color:#f59e0b}
    .terminal-note{margin-top:20px;padding:16px;background:#0c1a0e;border:1px solid #14532d;border-radius:10px;color:#86efac;font-size:0.85rem}
    .waiting-note{margin-top:20px;padding:16px;background:#1c1208;border:1px solid #78350f;border-radius:10px;color:#fcd34d;font-size:0.85rem}
  </style>
</head>
<body>
  <h1>Instance Report</h1>
  <p style="color:#64748b;font-size:0.85rem;margin-bottom:24px">Generated %s</p>

  <h2>Instance Metadata</h2>
  %s

  <h2>Transition History</h2>
  %s

  <h2>Forward Path from Current State</h2>
  %s
</body>
</html>`,
		inst.Name,
		time.Now().Format("2006-01-02 15:04:05 UTC"),
		metaHTML,
		historyHTML,
		forwardHTML,
	), nil
}

func buildMetaSection(inst *model.WorkflowInstance) string {
	statusBadge := fmt.Sprintf(`<span class="badge badge-%s">%s</span>`, strings.ToLower(inst.Status), inst.Status)
	if inst.Status == "WAITING_FOR_CHILD" {
		statusBadge = fmt.Sprintf(`<span class="badge badge-waiting">%s</span>`, inst.Status)
	}

	parentStr := "—"
	if inst.ParentInstanceID != nil {
		parentStr = inst.ParentInstanceID.String()
	}
	terminalStr := "—"
	if inst.TerminalState != "" {
		terminalStr = inst.TerminalState
	}

	cards := []struct{ label, value string }{
		{"Machine ID", inst.ID.String()},
		{"Name", inst.Name},
		{"Definition", inst.WorkflowDefID},
		{"Status", statusBadge},
		{"Current State", fmt.Sprintf("<strong style='color:#f59e0b'>%s</strong>", inst.CurrentState)},
		{"Terminal State", terminalStr},
		{"Parent Instance", parentStr},
		{"Created", inst.CreatedAt.Format("2006-01-02 15:04:05")},
		{"Last Updated", inst.UpdatedAt.Format("2006-01-02 15:04:05")},
	}

	var sb strings.Builder
	sb.WriteString(`<div class="meta-grid">`)
	for _, c := range cards {
		fmt.Fprintf(&sb, `
    <div class="meta-card">
      <div class="label">%s</div>
      <div class="value">%s</div>
    </div>`, c.label, c.value)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func buildHistoryTable(history []model.TransitionHistory) string {
	if len(history) == 0 {
		return `<p class="empty-msg">No transitions recorded yet — machine has not moved from its initial state.</p>`
	}

	var sb strings.Builder
	sb.WriteString(`<div style="overflow-x:auto"><table>
    <thead><tr>
      <th>#</th>
      <th>Timestamp</th>
      <th>From State</th>
      <th>Event</th>
      <th>To State</th>
      <th>Payload</th>
    </tr></thead>
    <tbody>`)

	for i, h := range history {
		payloadCell := `<span style="color:#475569">—</span>`
		if len(h.Payload) > 2 && string(h.Payload) != "null" {
			payloadCell = fmt.Sprintf(`<pre class="payload-pre">%s</pre>`, escapeXML(string(h.Payload)))
		}

		fmt.Fprintf(&sb, `
      <tr>
        <td class="ts">%d</td>
        <td class="ts">%s</td>
        <td class="state-from">%s</td>
        <td><span class="event-pill">%s</span></td>
        <td class="state-to">%s</td>
        <td>%s</td>
      </tr>`,
			i+1,
			h.OccurredAt.Format("2006-01-02 15:04:05"),
			escapeXML(h.FromState),
			escapeXML(h.Event),
			escapeXML(h.ToState),
			payloadCell)
	}

	sb.WriteString(`</tbody></table></div>`)
	return sb.String()
}

type pathNode struct {
	stateID  string
	event    string // event that brought us here
	depth    int
	isEnd    bool
	hasChild bool // entry action spawns a child
	children []*pathNode
}

// computeForwardPaths does a DFS from currentState up to maxDepth, building a
// tree of all reachable states. Cycles are broken by tracking visited states.
func computeForwardPaths(currentState string, def *model.WorkflowDefinition, maxDepth int) *pathNode {
	stateMap := make(map[string]model.StateDefinition)
	for _, s := range def.States {
		stateMap[s.StateID] = s
	}

	transFrom := make(map[string][]model.TransitionDefinition)
	for _, t := range def.Transitions {
		transFrom[t.Source] = append(transFrom[t.Source], t)
	}

	var commonTrans []model.TransitionDefinition
	for _, t := range def.Transitions {
		if t.IsCommon && t.Source == "*" {
			commonTrans = append(commonTrans, t)
		}
	}

	root := &pathNode{stateID: currentState, depth: 0}
	if s, ok := stateMap[currentState]; ok {
		root.isEnd = s.IsEndState
		root.hasChild = hasChildSpawn(s)
	}

	visited := map[string]bool{currentState: true}
	buildTree(root, transFrom, commonTrans, stateMap, visited, maxDepth)
	return root
}

func buildTree(
	node *pathNode,
	transFrom map[string][]model.TransitionDefinition,
	commonTrans []model.TransitionDefinition,
	stateMap map[string]model.StateDefinition,
	visited map[string]bool,
	maxDepth int,
) {
	if node.depth >= maxDepth || node.isEnd {
		return
	}

	all := append(transFrom[node.stateID], commonTrans...)
	for _, t := range all {
		if visited[t.Target] {
			continue
		}
		child := &pathNode{
			stateID: t.Target,
			event:   t.Event,
			depth:   node.depth + 1,
		}
		if s, ok := stateMap[t.Target]; ok {
			child.isEnd = s.IsEndState
			child.hasChild = hasChildSpawn(s)
		}
		node.children = append(node.children, child)

		visited[t.Target] = true
		buildTree(child, transFrom, commonTrans, stateMap, visited, maxDepth)
		delete(visited, t.Target) // allow same state via different paths
	}
}

func hasChildSpawn(s model.StateDefinition) bool {
	for _, a := range s.EntryActions {
		if a.Type == model.ActionTypeStartChild {
			return true
		}
	}
	return false
}

func buildForwardSection(currentState string, root *pathNode, status string) string {
	var sb strings.Builder

	if status == "COMPLETED" || status == "FAILED" {
		sb.WriteString(`<div class="terminal-note">`)
		sb.WriteString(fmt.Sprintf(`✓ This machine has reached a <strong>terminal state</strong> (%s). No further transitions are possible.`, currentState))
		sb.WriteString(`</div>`)
		return sb.String()
	}

	if status == "WAITING_FOR_CHILD" {
		sb.WriteString(`<div class="waiting-note">`)
		sb.WriteString(`⏳ Machine is currently <strong>WAITING_FOR_CHILD</strong>. It will only accept <code>CHILD_*</code> or <code>SYS_*</code> events until the child machine completes.`)
		sb.WriteString(`</div>`)
	}

	sb.WriteString(`<div class="forward-tree">`)
	sb.WriteString(fmt.Sprintf(`<div class="path-line path-current">● %s  ← you are here</div>`, escapeXML(currentState)))

	renderTree(&sb, root.children, "  ")

	if len(root.children) == 0 {
		sb.WriteString(`<div class="path-line" style="color:#475569;margin-top:8px">  (no further transitions available from this state)</div>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func renderTree(sb *strings.Builder, nodes []*pathNode, prefix string) {
	for i, n := range nodes {
		connector := "├─"
		childPrefix := prefix + "│  "
		if i == len(nodes)-1 {
			connector = "└─"
			childPrefix = prefix + "   "
		}

		stateClass := "path-state"
		suffix := ""
		if n.isEnd {
			stateClass = "path-terminal"
			suffix = `  <span style="color:#10b981;font-size:0.8em">✓ terminal</span>`
		}
		if n.hasChild {
			suffix += `  <span class="path-child">⚡ spawns child</span>`
		}

		sb.WriteString(fmt.Sprintf(
			`<div class="path-line"><span class="path-event">%s%s </span><span class="path-event">[%s]</span> → <span class="%s">%s</span>%s</div>`,
			escapeXML(prefix), connector,
			escapeXML(n.event),
			stateClass,
			escapeXML(n.stateID),
			suffix,
		))

		renderTree(sb, n.children, childPrefix)
	}
}
